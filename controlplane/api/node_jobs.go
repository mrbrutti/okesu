// Pull-mode jobs queue HTTP handlers.
//
// Three endpoints, all under the mgmt-plane mTLS router (so they
// inherit ClientAuth: RequireAndVerifyClientCert and authenticate the
// caller via the cert CN):
//
//   GET  /api/v1/agents/jobs               daemon polls; returns claimed jobs
//   POST /api/v1/agents/jobs/{id}/output   daemon streams stdout chunks
//   POST /api/v1/agents/jobs/{id}/exit     daemon reports terminal status
//
// The cert CN identifies the node — the same identity the daimon uses
// for /heartbeat, /register, etc. We do not invent a separate trust
// path for the jobs runtime.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/db"
)

// MgmtJobsPoll handles GET /api/v1/agents/jobs.
//
// The CN identifies the polling daemon. We resolve it to a node id
// (via the existing nodes table — daimon names and node names share
// the same registration path), then atomically claim the oldest
// pending jobs for that node.
//
// Side-effect: bumps `nodes.jobs_runtime_seen_at` so the orchestrator
// dispatcher knows the runtime is live without waiting for a separate
// heartbeat. This makes the pickup loop self-announcing.
func MgmtJobsPoll(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certName := agentNameFromCert(r)
		if certName == "" {
			http.Error(w, "client cert required", http.StatusUnauthorized)
			return
		}
		nodeID, err := resolveNodeIDByName(store, certName)
		if err != nil {
			http.Error(w, "no node registered for cert CN: "+certName, http.StatusForbidden)
			return
		}

		// Bump liveness — also reports current tunnel state via the
		// optional ?tunnel=running|stopped query param. The daemon's
		// next call carries the right value; for the first call we
		// fall back to whatever the column currently has.
		tunnelRunning := r.URL.Query().Get("tunnel") == "running"
		_ = store.MarkJobsRuntimeSeen(nodeID, tunnelRunning)

		claimed, err := store.ClaimPendingJobs(nodeID, 10)
		if err != nil {
			http.Error(w, "claim: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Translate db rows → wire shape. We re-encode the payload
		// (it's stored as a string in the db); errors surface as a
		// failed job rather than blocking the whole poll.
		out := agent.JobsPollResponse{NextPollMs: nextPollInterval(len(claimed))}
		for _, j := range claimed {
			var payload agent.JobPayload
			if err := json.Unmarshal([]byte(j.PayloadJSON), &payload); err != nil {
				_ = store.FinishNodeJob(j.ID, "failed", -1, "bad payload: "+err.Error(), false)
				continue
			}
			out.Jobs = append(out.Jobs, agent.Job{
				ID:        j.ID,
				Kind:      agent.JobKind(j.Kind),
				RunID:     j.RunID.String,
				Payload:   payload,
				CreatedAt: j.CreatedAt.UnixMilli(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// MgmtJobOutput handles POST /api/v1/agents/jobs/{id}/output.
// Each call appends one chunk to the underlying run's run_lines so
// the existing SSE stream sees them. Cert CN must match the node
// that claimed the job (no cross-node spoofing).
func MgmtJobOutput(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, ok := jobIDFromURL(r)
		if !ok {
			http.Error(w, "bad job id", http.StatusBadRequest)
			return
		}
		job, err := store.GetNodeJob(jobID)
		if err != nil {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		if !certOwnsJob(r, store, job) {
			http.Error(w, "cert does not own this job", http.StatusForbidden)
			return
		}
		// Tunnel-management jobs don't stream output — they're
		// child-process supervision, not agent runs.
		if job.Kind != string(agent.JobKindAgentRun) {
			http.Error(w, "output not allowed for job kind: "+job.Kind, http.StatusBadRequest)
			return
		}
		// Append each chunk to the linked Run's lines so the SSE
		// stream picks it up. run_id should always be set for
		// agent_run kind; we tolerate missing run rows quietly so a
		// stale post-cancel chunk doesn't 500.
		if !job.RunID.Valid || job.RunID.String == "" {
			http.Error(w, "job has no linked run", http.StatusConflict)
			return
		}
		var chunk agent.JobOutputChunk
		if err := json.NewDecoder(r.Body).Decode(&chunk); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		stream := chunk.Stream
		if stream == "" {
			stream = "stdout"
		}
		if err := store.AppendRunLine(job.RunID.String, stream, chunk.Data); err != nil {
			http.Error(w, "append: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Bump the runtime's liveness on every chunk so the
		// orchestrator's freshness check stays green across long-running
		// jobs that block the runtime's poll loop. Without this, a job
		// that takes longer than the freshness threshold makes the next
		// step on the same node fail with "no dispatch runtime
		// available". We don't have a tunnel-state signal on this
		// endpoint, so we preserve whatever the column currently holds.
		_ = store.MarkJobsRuntimeSeen(job.NodeID, currentTunnelRunning(store, job.NodeID))
		w.WriteHeader(http.StatusNoContent)
	}
}

// currentTunnelRunning returns the current tunnel_running flag for
// a node — a small cheap read so MarkJobsRuntimeSeen calls from
// non-poll endpoints don't accidentally clobber the column.
func currentTunnelRunning(store *db.Store, nodeID int64) bool {
	row := store.QueryRow(`SELECT tunnel_running FROM nodes WHERE id = ?`, nodeID)
	var b bool
	if err := row.Scan(&b); err != nil {
		return false
	}
	return b
}

// MgmtJobExit handles POST /api/v1/agents/jobs/{id}/exit.
//
// Idempotent: a duplicate post (network retry) hits the WHERE clause's
// status='claimed' filter and is a no-op. The orchestrator's
// jobsDispatcher polls GetNodeJob to see status flip to terminal —
// no signalling channel needed, which keeps the whole flow stateless
// across CP restarts.
func MgmtJobExit(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, ok := jobIDFromURL(r)
		if !ok {
			http.Error(w, "bad job id", http.StatusBadRequest)
			return
		}
		job, err := store.GetNodeJob(jobID)
		if err != nil {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		if !certOwnsJob(r, store, job) {
			http.Error(w, "cert does not own this job", http.StatusForbidden)
			return
		}
		var payload agent.JobExit
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		status := "succeeded"
		if payload.ExitCode != 0 || payload.Error != "" {
			status = "failed"
		}
		if err := store.FinishNodeJob(jobID, status, payload.ExitCode, payload.Error, payload.TunnelStarted); err != nil {
			http.Error(w, "finish: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Mirror the terminal state into the linked Run row when
		// it's an agent_run — keeps the existing /api/runs / SSE
		// flow consistent across tunnel and pull dispatch paths.
		if job.Kind == string(agent.JobKindAgentRun) && job.RunID.Valid && job.RunID.String != "" {
			runStatus := db.RunStatusSucceeded
			if status == "failed" {
				runStatus = db.RunStatusFailed
			}
			_ = store.FinishRun(job.RunID.String, runStatus, payload.ExitCode, payload.Error)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func jobIDFromURL(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// certOwnsJob enforces that the polling cert's node owns this job
// row. Without this, a daemon on host A could ack a job for host B.
func certOwnsJob(r *http.Request, store *db.Store, job *db.NodeJob) bool {
	cn := agentNameFromCert(r)
	if cn == "" {
		return false
	}
	nodeID, err := resolveNodeIDByName(store, cn)
	if err != nil {
		return false
	}
	return nodeID == job.NodeID
}

// resolveNodeIDByName looks up the nodes row whose `name` matches
// the cert CN. Cached lookup would be a future optimisation; the
// poll cadence is low enough that one extra SELECT per call is
// negligible.
func resolveNodeIDByName(store *db.Store, name string) (int64, error) {
	row := store.QueryRow(`SELECT id FROM nodes WHERE name = ?`, name)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, fmt.Errorf("nodes.id by name=%q: %w", name, err)
	}
	return id, nil
}

// nextPollInterval suggests a poll cadence to the daemon. Busy
// nodes (jobs claimed last call) poll at 2s; quiet ones back off to
// 5s. The daemon uses this advisory; the CP's actual ceiling is
// bounded by the orchestrator's per-step timeout.
func nextPollInterval(claimedThisPoll int) int {
	if claimedThisPoll > 0 {
		return 2000
	}
	return 5000
}
