// HTTP surface for orchestrations + the wiring between the engine,
// the database, and the existing run dispatch path.
//
// This file does three jobs:
//
//   1. orchestratorStoreAdapter — adapts *db.Store to the narrow
//      Store interface the orchestrator engine declares. Lives in the
//      api package so the db package stays free of orchestrator
//      imports (preserving api → orchestrator → (-) and api → db).
//
//   2. localDispatcher — Dispatcher impl that runs a step on this CP
//      via the existing tunnel runtime. Reuses the same primitives as
//      CreateRun (tunnel.Registry, RunRegistry, db.Store) and blocks
//      until the dispatched run terminates so the engine can advance.
//      Phase B will add a federatedDispatcher that proxies to a
//      child CP via the federation write proxy.
//
//   3. HTTP handlers — list / create / update / delete / run /
//      run-detail / approve / cancel. Same patterns the agent /
//      daimon library uses; federation wrappers come in Phase B.

package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/api/enrichment"
	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
	"github.com/section9labs/okesu/controlplane/orchestrator"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// ── orchestratorStoreAdapter ─────────────────────────────────────────

// orchestratorStoreAdapter wraps *db.Store and exposes the methods the
// orchestrator.Engine consumes via its Store interface. The adapter
// translates between db's row types (sql.Null* fields) and the
// engine's pointer-time records.
type orchestratorStoreAdapter struct {
	store *db.Store
}

var _ orchestrator.Store = (*orchestratorStoreAdapter)(nil)

func (a *orchestratorStoreAdapter) GetOrchestration(id int64) (*orchestrator.Orchestration, error) {
	row, err := a.store.GetOrchestration(id)
	if err != nil {
		return nil, err
	}
	return &orchestrator.Orchestration{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description.String,
		SpecYAML:    row.SpecYAML,
		Enabled:     row.Enabled,
	}, nil
}

func (a *orchestratorStoreAdapter) GetOrchestrationRun(id int64) (*orchestrator.RunRecord, error) {
	row, err := a.store.GetOrchestrationRun(id)
	if err != nil {
		return nil, err
	}
	r := &orchestrator.RunRecord{
		ID:              row.ID,
		OrchestrationID: row.OrchestrationID,
		Status:          row.Status,
		TriggerKind:     row.TriggerKind,
		TriggerPayload:  row.TriggerPayload.String,
		CurrentStepID:   row.CurrentStepID.String,
		StartedAt:       row.StartedAt,
		Error:           row.Error.String,
	}
	if row.EndedAt.Valid {
		t := row.EndedAt.Time
		r.EndedAt = &t
	}
	if row.StartedBy.Valid {
		r.StartedByUserID = row.StartedBy.Int64
	}
	return r, nil
}

func (a *orchestratorStoreAdapter) UpdateOrchestrationRunStatus(id int64, status, currentStepID, errMsg string) error {
	return a.store.UpdateOrchestrationRunStatus(id, status, currentStepID, errMsg)
}

func (a *orchestratorStoreAdapter) FinishOrchestrationRun(id int64, status, errMsg string) error {
	return a.store.FinishOrchestrationRun(id, status, errMsg)
}

func (a *orchestratorStoreAdapter) ListOrchestrationSteps(runID int64) ([]*orchestrator.StepRecord, error) {
	rows, err := a.store.ListOrchestrationSteps(runID)
	if err != nil {
		return nil, err
	}
	out := make([]*orchestrator.StepRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbStepToEngine(r))
	}
	return out, nil
}

func (a *orchestratorStoreAdapter) UpsertOrchestrationStep(rec *orchestrator.StepRecord) error {
	return a.store.UpsertOrchestrationStep(db.OrchestrationStepInsert{
		OrchestrationRunID: rec.OrchestrationRunID,
		StepID:             rec.StepID,
		StepIdx:            rec.StepIdx,
		Status:             rec.Status,
		RunID:              rec.RunID,
		CPInstanceID:       rec.CPInstanceID,
		NodeID:             rec.NodeID,
		RenderedPrompt:     rec.RenderedPrompt,
		ResultJSON:         rec.ResultJSON,
		OutputSummary:      rec.OutputSummary,
		StartedAt:          rec.StartedAt,
		EndedAt:            rec.EndedAt,
		Error:              rec.Error,
		ApprovedAt:         rec.ApprovedAt,
		ApprovedBy:         rec.ApprovedByUserID,
		DataSnapshot:       rec.DataSnapshot,
		PromptEntities:     rec.PromptEntities,
	})
}

func (a *orchestratorStoreAdapter) GetOrchestrationStep(runID int64, stepID string) (*orchestrator.StepRecord, error) {
	r, err := a.store.GetOrchestrationStep(runID, stepID)
	if err != nil {
		return nil, err
	}
	return dbStepToEngine(r), nil
}

func dbStepToEngine(r *db.OrchestrationStep) *orchestrator.StepRecord {
	rec := &orchestrator.StepRecord{
		OrchestrationRunID: r.OrchestrationRunID,
		StepID:             r.StepID,
		StepIdx:            r.StepIdx,
		Status:             r.Status,
		RunID:              r.RunID.String,
		CPInstanceID:       r.CPInstanceID.String,
		NodeID:             r.NodeID.Int64,
		RenderedPrompt:     r.RenderedPrompt.String,
		ResultJSON:         r.ResultJSON.String,
		OutputSummary:      r.OutputSummary.String,
		Error:              r.Error.String,
		ApprovedByUserID:   r.ApprovedBy.Int64,
		DataSnapshot:       r.DataSnapshot.String,
		PromptEntities:     r.PromptEntities.String,
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time
		rec.StartedAt = &t
	}
	if r.EndedAt.Valid {
		t := r.EndedAt.Time
		rec.EndedAt = &t
	}
	if r.ApprovedAt.Valid {
		t := r.ApprovedAt.Time
		rec.ApprovedAt = &t
	}
	return rec
}

// ── stepNodeProgressSinkAdapter ─────────────────────────────────────

// stepNodeProgressSinkAdapter delegates the engine's per-host fan-out
// sink calls to the db Store's Insert/Update on the
// orchestration_step_node_dispatches table.
type stepNodeProgressSinkAdapter struct {
	store *db.Store
}

var _ orchestrator.StepNodeProgressSink = (*stepNodeProgressSinkAdapter)(nil)

func (a *stepNodeProgressSinkAdapter) OnDispatchStart(runID int64, stepID, host string, startedAt time.Time) error {
	return a.store.InsertStepNodeDispatch(db.StepNodeDispatchInsert{
		RunID:     runID,
		StepID:    stepID,
		Host:      host,
		Status:    "running",
		StartedAt: &startedAt,
	})
}

func (a *stepNodeProgressSinkAdapter) OnDispatchEnd(runID int64, stepID, host string,
	status, agentRunID string, findingsCount int,
	outputTail, errorStr string, endedAt time.Time) error {
	return a.store.UpdateStepNodeDispatch(db.StepNodeDispatchUpdate{
		RunID:         runID,
		StepID:        stepID,
		Host:          host,
		Status:        status,
		AgentRunID:    agentRunID,
		FindingsCount: findingsCount,
		OutputTail:    outputTail,
		Error:         errorStr,
		EndedAt:       &endedAt,
	})
}

// ── localDispatcher ──────────────────────────────────────────────────

// localDispatcher executes a step on this CP. It mirrors CreateRun's
// dispatch path but blocks synchronously until the run terminates so
// the engine can read findings and advance.
type localDispatcher struct {
	reg       *RunRegistry
	tunReg    *tunnel.Registry
	store     *db.Store
	agentDirs []string
}

func newLocalDispatcher(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store, agentDirs []string) *localDispatcher {
	return &localDispatcher{reg: reg, tunReg: tunReg, store: store, agentDirs: agentDirs}
}

// runStepLocal is the core of localDispatcher.Dispatch, factored out
// so the federation `/api/v1/federation/runs/sync` handler can call
// it directly when a parent CP delegates a step to this CP.
//
// As of the pull-mode refactor, this function picks between:
//  1. Tunnel runtime (existing) — preferred when an `okesu node`
//     reverse-tunnel client is connected for this hostname.
//  2. Jobs runtime (new) — used when the host's `okesu jobs`
//     runtime has polled within the last 60s but no tunnel exists.
//
// Steps that match neither path get the actionable not-connected
// error pointing the operator at the install path.
func runStepLocal(
	ctx context.Context,
	reg *RunRegistry,
	tunReg *tunnel.Registry,
	store *db.Store,
	agentDirs []string,
	req orchestrator.DispatchRequest,
) (orchestrator.DispatchResult, error) {
	// Look up the agent file so the daemon doesn't need it pre-staged.
	var agentContent string
	if req.AgentName != "" {
		path, err := findAgentFile(agentDirs, req.AgentName)
		if err != nil {
			return orchestrator.DispatchResult{}, fmt.Errorf("agent %q not found in library", req.AgentName)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return orchestrator.DispatchResult{}, fmt.Errorf("read agent file: %w", err)
		}
		agentContent = string(b)
	}

	if req.NodeSelector == "" {
		return orchestrator.DispatchResult{}, errors.New(
			"step has no node target. Set `node:` (single host) or `nodes:` (fan-out) on the step. " +
				"Local-only steps that just touch CP state are not supported in v1.")
	}
	conn := tunReg.Get(req.NodeSelector)
	if conn == nil {
		// No tunnel — try the jobs runtime. If a recent poll exists
		// for this node, we delegate to jobsDispatcher which queues
		// the work and waits for the daemon's /exit POST.
		if jobsRuntimeFreshOnThisCP(store, req.NodeSelector) {
			jd := newJobsDispatcher(store, agentDirs)
			return jd.Dispatch(ctx, req)
		}
		return orchestrator.DispatchResult{}, fmt.Errorf(
			"node %q has no dispatch runtime available — no tunnel connected and no recent jobs-runtime poll. "+
				"Install the jobs runtime (start `okesu-jobs.service` on the host) or open a tunnel with `okesu node`. "+
				"Auto-deploy is on the roadmap.", req.NodeSelector)
	}

	runID, _ := randomID()
	if err := store.CreateRun(db.RunInsert{
		ID:        runID,
		NodeName:  req.NodeSelector,
		AgentName: req.AgentName,
		Prompt:    req.Prompt,
	}); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("persist run: %w", err)
	}

	run := &Run{
		ID:       runID,
		NodeName: req.NodeSelector,
		Status:   db.RunStatusRunning,
		subs:     map[chan string]struct{}{},
		finish:   make(chan struct{}),
	}
	reg.put(run)

	lines, exit, cleanup, err := conn.SendRun(tunnel.RunPayload{
		RunID:        runID,
		Agent:        req.AgentName,
		AgentContent: agentContent,
		Prompt:       req.Prompt,
	})
	if err != nil {
		_ = store.FinishRun(runID, db.RunStatusFailed, -1, err.Error())
		run.completeError(err.Error())
		reg.remove(runID)
		return orchestrator.DispatchResult{}, fmt.Errorf("tunnel send: %w", err)
	}

	go run.consume(store, reg, lines, exit, cleanup)
	select {
	case <-run.finish:
	case <-ctx.Done():
		<-run.finish
	}

	dbRun, err := store.GetRun(runID)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("re-read run: %w", err)
	}
	rawLines, err := store.RunLines(runID, 5000)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("read run lines: %w", err)
	}

	result := orchestrator.DispatchResult{
		RunID:        runID,
		Findings:     parseFindingsFromLines(rawLines),
		OutputTail:   joinLineTails(rawLines, 4096),
		CPInstanceID: "local",
		HostResolved: req.NodeSelector,
	}
	switch dbRun.Status {
	case db.RunStatusSucceeded:
		result.Status = orchestrator.StepStatusCompleted
	case db.RunStatusFailed:
		result.Status = orchestrator.StepStatusFailed
		result.Error = dbRun.Error.String
	case db.RunStatusCancelled:
		result.Status = orchestrator.StepStatusFailed
		result.Error = "run cancelled"
	default:
		result.Status = orchestrator.StepStatusFailed
		result.Error = "run ended in unexpected status: " + dbRun.Status
	}
	return result, nil
}

func (d *localDispatcher) Dispatch(ctx context.Context, req orchestrator.DispatchRequest) (orchestrator.DispatchResult, error) {
	return runStepLocal(ctx, d.reg, d.tunReg, d.store, d.agentDirs, req)
}

// ── federatedDispatcher ─────────────────────────────────────────────

// federatedDispatcher dispatches a step to a child CP. It uses the
// existing federation token + `/api/v1/federation/runs/sync` endpoint
// — which is the runStepLocal function exposed over HTTP. Each call
// is a single blocking POST: the child holds the request open while
// the run executes, then returns the structured DispatchResult.
//
// Long-running steps: the parent's HTTP client carries the engine's
// per-step timeout via context, so a hung child run is cancelled at
// the engine's discretion (not the network's default).
type federatedDispatcher struct {
	agg    *federation.Aggregator
	client *http.Client
}

func newFederatedDispatcher(agg *federation.Aggregator) *federatedDispatcher {
	return &federatedDispatcher{
		agg: agg,
		client: &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
}

func (d *federatedDispatcher) Dispatch(ctx context.Context, req orchestrator.DispatchRequest) (orchestrator.DispatchResult, error) {
	if d.agg == nil {
		return orchestrator.DispatchResult{}, errors.New("federation not configured: cp selector requires a federated parent CP")
	}
	peers, _ := d.agg.HealthyPeers()
	var target *federation.Peer
	for i := range peers {
		if peers[i].Snapshot.InstanceID == req.CPInstanceID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("federation peer %q not found or unhealthy", req.CPInstanceID)
	}

	body, _ := json.Marshal(map[string]any{
		"agent":           req.AgentName,
		"node":            req.NodeSelector,
		"prompt":          req.Prompt,
		"timeout_seconds": int(req.Timeout.Seconds()),
	})

	url := strings.TrimRight(target.Row.URL, "/") + "/api/v1/federation/runs/sync"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return orchestrator.DispatchResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Okesu-Federation-Token", target.Row.Token)

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("federated dispatch to %s: %w", target.Snapshot.DisplayName, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return orchestrator.DispatchResult{}, fmt.Errorf("federated dispatch returned %d: %s", resp.StatusCode, string(respBody))
	}
	var out orchestrator.DispatchResult
	if err := json.Unmarshal(respBody, &out); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("federated dispatch: bad response: %w", err)
	}
	// Stamp the CP id even if the child's response forgot it — this is
	// what shows up on the step card's CP-source chip in the UI.
	if out.CPInstanceID == "" || out.CPInstanceID == "local" {
		out.CPInstanceID = target.Snapshot.InstanceID
	}
	return out, nil
}

// ── jobsDispatcher ──────────────────────────────────────────────────

// jobsDispatcher is the pull-mode dispatcher: it INSERTs a row into
// node_jobs, then polls the row's status until it reaches a terminal
// state. The host-side `okesu jobs` runtime claims, executes, and
// reports back via the mgmt-plane HTTP endpoints.
//
// No persistent connection is held — both sides use stateless HTTP,
// which is the whole point of pull mode. This dispatcher returns
// when the daemon's /exit POST flips the row, when the engine's ctx
// cancels, or when our liveness probe says the daemon hasn't polled
// recently enough to claim the job.
type jobsDispatcher struct {
	store      *db.Store
	agentDirs  []string
	pollEvery  time.Duration
	staleAfter time.Duration
}

func newJobsDispatcher(store *db.Store, agentDirs []string) *jobsDispatcher {
	return &jobsDispatcher{
		store:      store,
		agentDirs:  agentDirs,
		pollEvery:  500 * time.Millisecond,
		staleAfter: 30 * time.Second,
	}
}

func (d *jobsDispatcher) Dispatch(ctx context.Context, req orchestrator.DispatchRequest) (orchestrator.DispatchResult, error) {
	if req.NodeSelector == "" {
		return orchestrator.DispatchResult{}, errors.New("jobs dispatcher: node selector required")
	}

	// Look up the node id by name. The jobs runtime on that node is
	// expected to have the matching mTLS cert; we'll fail at claim
	// time if it never polls.
	row := d.store.QueryRow(`SELECT id FROM nodes WHERE name = ?`, req.NodeSelector)
	var nodeID int64
	if err := row.Scan(&nodeID); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("jobs dispatcher: no nodes row for %q: %w", req.NodeSelector, err)
	}

	// Build the underlying Run record so the existing /api/runs UI
	// + finding linkage continue to work uniformly across dispatch
	// paths.
	runID, _ := randomID()
	if err := d.store.CreateRun(db.RunInsert{
		ID:        runID,
		NodeName:  req.NodeSelector,
		AgentName: req.AgentName,
		Prompt:    req.Prompt,
	}); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("persist run: %w", err)
	}

	// Look up agent content the same way the local dispatcher does,
	// so the daemon doesn't need the file pre-staged. The orchestrator
	// uses the same agent search dirs as ad-hoc Runs.
	var agentContent string
	if req.AgentName != "" && len(d.agentDirs) > 0 {
		if path, ferr := findAgentFile(d.agentDirs, req.AgentName); ferr == nil {
			if b, rerr := os.ReadFile(path); rerr == nil {
				agentContent = string(b)
			}
		}
	}
	payload := agent.JobPayload{
		Agent:        req.AgentName,
		AgentContent: agentContent,
		Prompt:       req.Prompt,
	}
	payloadJSON, _ := json.Marshal(payload)
	jobID, err := d.store.CreateNodeJob(runID, nodeID, string(agent.JobKindAgentRun), string(payloadJSON))
	if err != nil {
		_ = d.store.FinishRun(runID, db.RunStatusFailed, -1, err.Error())
		return orchestrator.DispatchResult{}, fmt.Errorf("enqueue job: %w", err)
	}

	// Block on terminal status. We use a short polling cadence so
	// the engine sees state flips quickly without us holding a DB
	// transaction open. ctx cancellation flips the job to cancelled
	// so the daemon stops streaming.
	t := time.NewTicker(d.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = d.store.CancelPendingNodeJobs(runID)
			return orchestrator.DispatchResult{}, ctx.Err()
		case <-t.C:
		}
		j, err := d.store.GetNodeJob(jobID)
		if err != nil {
			return orchestrator.DispatchResult{}, fmt.Errorf("read job: %w", err)
		}
		switch j.Status {
		case "succeeded", "failed", "cancelled", "timeout":
			return d.assembleResult(runID, j)
		}
	}
}

// assembleResult reads the underlying Run's final state + run_lines
// to produce the same DispatchResult shape the tunnel dispatcher
// returns. This is what makes the orchestrator's downstream env
// bindings (`{{stepN.findings}}`, `{{stepN.output}}`) work the same
// regardless of which runtime executed the step.
func (d *jobsDispatcher) assembleResult(runID string, j *db.NodeJob) (orchestrator.DispatchResult, error) {
	dbRun, err := d.store.GetRun(runID)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("read run: %w", err)
	}
	rawLines, err := d.store.RunLines(runID, 5000)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("read run lines: %w", err)
	}
	result := orchestrator.DispatchResult{
		RunID:        runID,
		Findings:     parseFindingsFromLines(rawLines),
		OutputTail:   joinLineTails(rawLines, 4096),
		CPInstanceID: "local",
		HostResolved: dbRun.NodeName,
	}
	switch j.Status {
	case "succeeded":
		result.Status = orchestrator.StepStatusCompleted
	case "failed":
		result.Status = orchestrator.StepStatusFailed
		result.Error = j.Error.String
	case "cancelled":
		result.Status = orchestrator.StepStatusFailed
		result.Error = "cancelled"
	case "timeout":
		result.Status = orchestrator.StepStatusFailed
		result.Error = "timeout"
	}
	return result, nil
}

// ── routingDispatcher ───────────────────────────────────────────────

// routingDispatcher inspects req.CPInstanceID and routes to the local
// or federated impl. When the spec didn't pin a CP, the dispatcher
// auto-discovers which CP owns the requested node by checking the
// local tunnel registry first, then querying each healthy federation
// peer. This means an orchestration authored on the Global CP can
// target a node on a child CP just by hostname — operators don't have
// to know which CP each node is registered with.
type routingDispatcher struct {
	local     *localDispatcher
	federated *federatedDispatcher
	jobs      *jobsDispatcher
	cpLocal   *cpLocalDispatcher // nil when no okesu binary was resolvable
	tunReg    *tunnel.Registry
	agg       *federation.Aggregator
	store     *db.Store

	// autoDeployer (optional) installs the jobs runtime on a node
	// when no dispatch path is available. Nil disables auto-deploy
	// entirely — the engine fails dispatch with a "manual install
	// required" error pointing at the Nodes UI. Wired by
	// NewOrchestrationCoordinator only when the CP has a
	// fleet-ssh-key configured.
	autoDeployer AutoDeployer
}

// AutoDeployer abstracts the SSH-based jobs-runtime install so the
// engine can trigger it without dragging the sshdeploy package into
// the orchestrator's interface surface. The api layer wires a
// concrete implementation that uses the CP's fleet SSH key.
type AutoDeployer interface {
	// AutoDeploy installs the jobs runtime on the named node and
	// returns once `okesu-jobs.service` is active. Returns an error
	// if SSH credentials aren't available, the install fails, or
	// the runtime didn't poll within the timeout.
	AutoDeploy(ctx context.Context, nodeName string) error
}

// jobsRuntimeFresh reports whether a node's jobs runtime has polled
// recently enough that we trust it to claim a new job. The 60s
// threshold is roughly 12 poll intervals — beyond that we consider
// the runtime stale and fall through to other paths (or auto-deploy).
func (d *routingDispatcher) jobsRuntimeFresh(nodeName string) bool {
	return jobsRuntimeFreshOnThisCP(d.store, nodeName)
}

// nodeRowExists guards auto-deploy: we only attempt it for nodes the
// CP knows about (and has SSH metadata for via the existing nodes
// table). Without this, an arbitrary `node:` value in a spec would
// cause the engine to attempt an SSH dial against a phantom host.
func (d *routingDispatcher) nodeRowExists(nodeName string) bool {
	row := d.store.QueryRow(`SELECT 1 FROM nodes WHERE name = ?`, nodeName)
	var v int
	return row.Scan(&v) == nil
}

// nodePreferredDispatch reads the per-node `preferred_dispatch`
// column. Layered under the spec's effective dispatch when the
// operator left the spec on "auto" — i.e. when the orchestration
// itself doesn't care, the node's own preference wins.
func (d *routingDispatcher) nodePreferredDispatch(nodeName string) string {
	row := d.store.QueryRow(`SELECT preferred_dispatch FROM nodes WHERE name = ?`, nodeName)
	var pref string
	if err := row.Scan(&pref); err != nil {
		return ""
	}
	return pref
}

// startTunnelAndWait enqueues a start_tunnel job for the host's
// jobs runtime, then waits up to 30s for the resulting `okesu node`
// child to register with the CP's tunnel registry. Used when a step
// hard-requested dispatch=tunnel but no tunnel is currently up.
//
// The job's exit comes back via the jobs runtime's normal /exit
// path; we don't read the exit, we just watch the tunnel registry —
// that's the source of truth for whether the tunnel is *useable*,
// not just whether the spawn succeeded.
func (d *routingDispatcher) startTunnelAndWait(ctx context.Context, req orchestrator.DispatchRequest) error {
	row := d.store.QueryRow(`SELECT id FROM nodes WHERE name = ?`, req.NodeSelector)
	var nodeID int64
	if err := row.Scan(&nodeID); err != nil {
		return fmt.Errorf("start_tunnel: no nodes row for %q: %w", req.NodeSelector, err)
	}
	payload, _ := json.Marshal(agent.JobPayload{
		// The daemon side defaults to its own configured CPMgmtURL
		// + CertDir + NodeName when these are empty — operators
		// don't need to thread them through the spec.
		NodeName: req.NodeSelector,
	})
	if _, err := d.store.CreateNodeJob("", nodeID, string(agent.JobKindStartTunnel), string(payload)); err != nil {
		return fmt.Errorf("enqueue start_tunnel: %w", err)
	}

	// Poll the tunnel registry — the registration happens when the
	// `okesu node` child completes its handshake. 30s is generous
	// for slow-network restarts; orchestrators tighten this via
	// per-step `timeout:` if they need to fail faster.
	deadline := time.Now().Add(30 * time.Second)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if d.tunReg.Get(req.NodeSelector) != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("start_tunnel: tunnel for %q did not register within 30s", req.NodeSelector)
		}
	}
}

// jobsRuntimeFreshOnThisCP is the package-level helper that runStepLocal
// also uses (it doesn't have a dispatcher receiver, so we share via this
// free function instead of duplicating the SQL).
func jobsRuntimeFreshOnThisCP(store *db.Store, nodeName string) bool {
	// 120s window: the runtime's poll interval throttles up while it
	// has work in flight, and the synchronous dispatchJob blocks the
	// loop until the agent_run finishes. Output chunks bump the
	// column too (see MgmtJobOutput) so a chatty `okesu claude` keeps
	// the column fresh even mid-job; the wider window covers the rare
	// long silent stretch.
	row := store.QueryRow(`
		SELECT jobs_runtime_seen_at
		  FROM nodes
		 WHERE name = ? AND jobs_runtime_seen_at IS NOT NULL
		   AND jobs_runtime_seen_at > datetime('now', '-120 seconds')
	`, nodeName)
	var seen sql.NullTime
	if err := row.Scan(&seen); err != nil {
		return false
	}
	return seen.Valid
}

func (d *routingDispatcher) Dispatch(ctx context.Context, req orchestrator.DispatchRequest) (orchestrator.DispatchResult, error) {
	cpHint := strings.TrimSpace(req.CPInstanceID)

	// Operator pinned a specific CP — honour it without auto-discovery.
	if cpHint != "" && cpHint != "local" {
		return d.federated.Dispatch(ctx, req)
	}

	// CP-only step (no node target). Cron-triggered orchestrations
	// that just read/write the CP's own API run here — see
	// cpLocalDispatcher for why this exists.
	if req.NodeSelector == "" {
		if d.cpLocal == nil {
			return orchestrator.DispatchResult{}, errors.New(
				"step has no node target. Set `node:` (single host) or `nodes:` (fan-out) on the step. " +
					"CP-local execution is unavailable: no okesu binary resolved on the CP host.")
		}
		return d.cpLocal.Dispatch(ctx, req)
	}

	mode := strings.TrimSpace(req.DispatchMode)
	if mode == "" || mode == "auto" {
		// Layer node-level preference under auto. The orchestration
		// engine doesn't see the node row at parse time, so we fold
		// in `nodes.preferred_dispatch` here.
		mode = d.nodePreferredDispatch(req.NodeSelector)
	}

	switch mode {
	case "tunnel":
		// Hard requirement: tunnel must be available. If not, send a
		// start_tunnel job to the host's jobs runtime, wait for the
		// tunnel registry to register, then dispatch via tunnel. If
		// the jobs runtime isn't there either, we can't auto-start —
		// fall through to the clear-error path.
		if d.tunReg.Get(req.NodeSelector) != nil {
			return d.local.Dispatch(ctx, req)
		}
		if d.jobs != nil && d.jobsRuntimeFresh(req.NodeSelector) {
			if err := d.startTunnelAndWait(ctx, req); err != nil {
				return orchestrator.DispatchResult{}, err
			}
			return d.local.Dispatch(ctx, req)
		}
		return orchestrator.DispatchResult{}, fmt.Errorf(
			"step requested dispatch=tunnel but no tunnel is connected for node %q and no jobs runtime is available to start one. "+
				"Run `okesu jobs` on the host (or open a tunnel manually with `okesu node`).", req.NodeSelector)

	case "jobs":
		// Hard requirement: jobs runtime must be available.
		if d.jobs != nil && d.jobsRuntimeFresh(req.NodeSelector) {
			return d.jobs.Dispatch(ctx, req)
		}
		return orchestrator.DispatchResult{}, fmt.Errorf(
			"step requested dispatch=jobs but no recent jobs-runtime poll for node %q. Start `okesu-jobs.service` on the host.",
			req.NodeSelector)
	}

	// Auto path — prefer tunnel if up, then jobs if polled, then
	// federation auto-discovery, then auto-deploy, then the clear
	// not-connected error.
	if d.tunReg.Get(req.NodeSelector) != nil {
		return d.local.Dispatch(ctx, req)
	}
	if d.jobs != nil && d.jobsRuntimeFresh(req.NodeSelector) {
		return d.jobs.Dispatch(ctx, req)
	}

	// No runtime currently available locally — try auto-deploy
	// before falling through to federation auto-discovery. The
	// AutoDeployer is nil when --fleet-ssh-key-path isn't set, in
	// which case we skip and let the operator-facing error surface.
	if d.autoDeployer != nil && d.nodeRowExists(req.NodeSelector) {
		if err := d.autoDeployer.AutoDeploy(ctx, req.NodeSelector); err == nil {
			// Install succeeded; the runtime should have polled by
			// now. Re-check freshness and dispatch via jobs.
			if d.jobs != nil && d.jobsRuntimeFresh(req.NodeSelector) {
				return d.jobs.Dispatch(ctx, req)
			}
		} else {
			// Surface the auto-deploy error directly — operators
			// reading the step's failure message want to see "ssh
			// auth failed: ..." not "node not connected".
			return orchestrator.DispatchResult{}, fmt.Errorf("auto-deploy failed: %w", err)
		}
	}

	// Look across federated peers for a connected node with this name.
	// If exactly one matches, route there; if multiple, error so the
	// operator can disambiguate with an explicit `cp:` selector.
	if d.agg != nil {
		matches := d.findNodeOnFederation(ctx, req.NodeSelector)
		switch len(matches) {
		case 1:
			req.CPInstanceID = matches[0]
			return d.federated.Dispatch(ctx, req)
		case 0:
			// Not found anywhere — fall through; the local dispatcher
			// produces a clear "node X is not connected" error.
		default:
			return orchestrator.DispatchResult{}, fmt.Errorf(
				"node %q is connected on multiple CPs (%v); set `cp: <instance_id>` on this step to disambiguate",
				req.NodeSelector, matches)
		}
	}
	return d.local.Dispatch(ctx, req)
}

// findNodeOnFederation queries each healthy peer's /api/v1/federation/nodes
// for a connected node with the given name. Returns the matching CP
// instance_ids — usually 0 or 1, but a multi-match means the operator
// has identically-named nodes on different CPs and we let them pick.
//
// Per-call federation cost: one HTTP request per peer. Acceptable since
// orchestration step dispatches are minute-scale, not millisecond-scale.
// A small TTL cache could be added if probing becomes a hotspot, but
// for v1 the freshness is more valuable than the savings.
func (d *routingDispatcher) findNodeOnFederation(ctx context.Context, name string) []string {
	peers, _ := d.agg.HealthyPeers()
	if len(peers) == 0 {
		return nil
	}
	type nodeRow struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	var matches []string
	for i := range peers {
		var nodes []nodeRow
		if err := d.agg.FetchJSON(ctx, peers[i], "/api/v1/federation/nodes", &nodes); err != nil {
			continue
		}
		for _, n := range nodes {
			if n.Name == name && n.Status == "ready" {
				matches = append(matches, peers[i].Snapshot.InstanceID)
				break
			}
		}
	}
	return matches
}

// normalizeFindingTags accepts the agent's tags field as either a
// comma-separated string or a JSON array of strings, and returns the
// canonical comma-separated form used everywhere else (db column,
// dashboard query, etc.).
func normalizeFindingTags(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Then array of strings.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, ",")
	}
	return ""
}

// parseFindingsFromLines pulls JSONL `type=finding` events out of a
// run transcript. The orchestrator only cares about the few finding
// fields that drive templating + result distillation; everything
// else stays in the original Run for the FindingDrawer to render.
func parseFindingsFromLines(lines []db.RunLine) []orchestrator.DispatchedFinding {
	var out []orchestrator.DispatchedFinding
	for _, ln := range lines {
		// Cheap pre-filter so we don't json-decode every stdout line.
		if !strings.Contains(ln.Data, `"type":"finding"`) && !strings.Contains(ln.Data, `"type": "finding"`) {
			continue
		}
		var f struct {
			Type       string         `json:"type"`
			Severity   string         `json:"severity"`
			Title      string         `json:"title"`
			Category   string         `json:"category"`
			Resource   string         `json:"resource"`
			DedupKey   string         `json:"dedup_key"`
			Attributes map[string]any `json:"attributes"`
			// Phase 22.3 — finding subtype + tags. Carried forward to
			// DispatchedFinding so downstream steps (and the meeting
			// executor) can read what the agent emitted, e.g. `subtype:
			// "meeting_minutes"` or `tags: ["war-bridge"]`.
			Subtype string `json:"subtype"`
			// Tags can arrive as a comma-separated string or a JSON
			// array. Decode the raw value flexibly below.
			Tags json.RawMessage `json:"tags"`
		}
		if err := json.Unmarshal([]byte(ln.Data), &f); err != nil {
			continue
		}
		if f.Type != "finding" {
			continue
		}
		out = append(out, orchestrator.DispatchedFinding{
			Severity:   f.Severity,
			Title:      f.Title,
			Category:   f.Category,
			Resource:   f.Resource,
			DedupKey:   f.DedupKey,
			Attributes: f.Attributes,
			Subtype:    f.Subtype,
			Tags:       normalizeFindingTags(f.Tags),
		})
	}
	if len(out) == 0 {
		// Fallback: agents (LLMs) frequently write the orchestration
		// result as JSON inside a markdown code block in their text
		// stream rather than as a typed JSONL `finding` event. Scan
		// the assistant text for a JSON object with the expected
		// shape (verdict / actions / etc.) and synthesise an
		// orchestration_result so downstream actions still apply.
		if synth := synthesizeOrchestrationResult(lines); synth != nil {
			out = append(out, *synth)
		}
	}
	return out
}

// synthesizeOrchestrationResult is the fallback path. It walks the
// agent's text deltas (the harness emits each assistant token as a
// `{"type":"text","text":"…"}` event), reassembles the full text,
// and looks for the LAST JSON object containing top-level `verdict`
// or `actions`. That object becomes the orchestration_result's
// attributes. This is the LLM-tolerant cousin of the strict-finding
// path: when an agent outputs the right *content* in the wrong
// *form*, the engine still routes the actions correctly.
func synthesizeOrchestrationResult(lines []db.RunLine) *orchestrator.DispatchedFinding {
	var b strings.Builder
	for _, ln := range lines {
		// Quick filter: only text deltas matter for this fallback.
		if !strings.Contains(ln.Data, `"type":"text"`) {
			continue
		}
		var t struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(ln.Data), &t); err != nil || t.Type != "text" {
			continue
		}
		b.WriteString(t.Text)
	}
	full := b.String()
	if full == "" {
		return nil
	}
	obj := lastResultObject(full)
	if obj == nil {
		return nil
	}
	// Heuristic: the result must mention either an `actions` array
	// or a `verdict` field — otherwise we'd treat unrelated JSON
	// (e.g. a tool-output sample) as the result.
	if _, hasActions := obj["actions"]; !hasActions {
		if _, hasVerdict := obj["verdict"]; !hasVerdict {
			return nil
		}
	}
	title, _ := obj["title"].(string)
	if title == "" {
		title = "orchestration_result (synthesised)"
	}
	return &orchestrator.DispatchedFinding{
		Severity:   "INFO",
		Title:      title,
		Category:   orchestrator.OrchestrationResultCategory,
		Attributes: obj,
	}
}

// lastResultObject scans `s` for the latest JSON object that contains
// `"actions"` or `"verdict"` at the top level. It walks the string
// byte-by-byte, tracks balanced braces while respecting quoted
// strings + escapes, and tries to parse each candidate. Returns the
// last successful parse — agents often write a working draft and a
// final block, and the LAST one is the one to honor.
func lastResultObject(s string) map[string]any {
	var found map[string]any
	for start := 0; start < len(s); start++ {
		if s[start] != '{' {
			continue
		}
		depth := 0
		inStr := false
		esc := false
		for i := start; i < len(s); i++ {
			c := s[i]
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = !inStr
				continue
			}
			if inStr {
				continue
			}
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					candidate := s[start : i+1]
					if !looksLikeResult(candidate) {
						break
					}
					var parsed map[string]any
					if err := json.Unmarshal([]byte(candidate), &parsed); err == nil {
						found = parsed
					}
					break
				}
			}
		}
	}
	return found
}

func looksLikeResult(s string) bool {
	return strings.Contains(s, `"actions"`) || strings.Contains(s, `"verdict"`)
}

func joinLineTails(lines []db.RunLine, max int) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln.Data)
		b.WriteByte('\n')
	}
	s := b.String()
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

// ── HTTP handlers ────────────────────────────────────────────────────

// orchestrationJSON is the wire shape returned by the list / detail
// endpoints. Matches the YAML spec's frontmatter at a high level so
// the UI can show metadata without re-parsing.
type orchestrationJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	SpecYAML      string `json:"spec_yaml"`
	TriggerKind   string `json:"trigger_kind"`
	TriggerFilter string `json:"trigger_filter,omitempty"`
	TriggerCron   string `json:"trigger_cron,omitempty"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func toOrchestrationJSON(o *db.Orchestration) orchestrationJSON {
	return orchestrationJSON{
		ID:            o.ID,
		Name:          o.Name,
		Description:   o.Description.String,
		SpecYAML:      o.SpecYAML,
		TriggerKind:   o.TriggerKind,
		TriggerFilter: o.TriggerFilter.String,
		TriggerCron:   o.TriggerCron.String,
		Enabled:       o.Enabled,
		CreatedAt:     o.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     o.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// OrchestrationsList — GET /api/orchestrations
func OrchestrationsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListOrchestrations()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]orchestrationJSON, 0, len(rows))
		for _, o := range rows {
			out = append(out, toOrchestrationJSON(o))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// OrchestrationDetail — GET /api/orchestrations/{id}
func OrchestrationDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		o, err := store.GetOrchestration(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toOrchestrationJSON(o))
	}
}

type orchestrationCreateReq struct {
	SpecYAML string `json:"spec_yaml"`
	// TargetCPInstanceID is the federation forward field — when set
	// on the parent CP, the request is proxied to that child instead
	// of being persisted locally. Same convention as Add Node.
	TargetCPInstanceID string `json:"target_cp_instance_id,omitempty"`
}

// OrchestrationCreate — POST /api/orchestrations
func OrchestrationCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req orchestrationCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		spec, err := orchestrator.Parse(req.SpecYAML)
		if err != nil {
			http.Error(w, "spec: "+err.Error(), http.StatusBadRequest)
			return
		}

		var startedBy int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			startedBy = u.ID
		}
		id, err := store.CreateOrchestration(
			spec.Name, spec.Description, req.SpecYAML,
			spec.Trigger.On, spec.Trigger.Filter, spec.Trigger.Cron,
			startedBy,
		)
		if err != nil {
			http.Error(w, "persist: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action:   "orchestration.create",
			Target:   fmt.Sprintf("orchestration:%d", id),
			Metadata: map[string]any{"name": spec.Name, "trigger": spec.Trigger.On},
		})
		o, _ := store.GetOrchestration(id)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toOrchestrationJSON(o))
	}
}

// OrchestrationUpdate — PUT /api/orchestrations/{id}
func OrchestrationUpdate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var req orchestrationCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		spec, err := orchestrator.Parse(req.SpecYAML)
		if err != nil {
			http.Error(w, "spec: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.UpdateOrchestration(id, spec.Description, req.SpecYAML, spec.Trigger.On, spec.Trigger.Filter, spec.Trigger.Cron); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "orchestration.update",
			Target: fmt.Sprintf("orchestration:%d", id),
		})
		o, _ := store.GetOrchestration(id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toOrchestrationJSON(o))
	}
}

// OrchestrationDelete — DELETE /api/orchestrations/{id}
func OrchestrationDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteOrchestration(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "orchestration.delete",
			Target: fmt.Sprintf("orchestration:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── orchestration runs ──────────────────────────────────────────────

// OrchestrationCoordinator owns the engine + dispatcher and
// schedules background goroutines to drive runs to completion. One
// instance per CP, constructed at server boot.
type OrchestrationCoordinator struct {
	mu       sync.Mutex
	inflight map[int64]struct{}
	engine   *orchestrator.Engine
	store    *db.Store

	// runSlots is a buffered channel acting as a global semaphore that
	// caps the number of concurrently executing engine.Run goroutines.
	// A burst of finding triggers (we saw 700+ in 12h) saturated the
	// shared Anthropic key and produced cascading "context deadline
	// exceeded" failures; the cap shapes the load before it reaches
	// the API. Run rows still exist in the DB while waiting — only
	// the engine.Run call is gated.
	runSlots chan struct{}

	// triggerStop closes when StopTriggers is called — the cron tick
	// goroutine selects on it to exit cleanly on CP shutdown.
	triggerStop chan struct{}
}

// MaxConcurrentRuns is the global ceiling on simultaneously running
// orchestration runs. Sized empirically: the lab's single Anthropic
// key starts queueing past ~30 in-flight requests, and most runs
// have 2–3 steps each.
const MaxConcurrentRuns = 20

// CoordinatorOpts bundles the pluggable deps the coordinator needs
// beyond the core stores. Built up in server.go from the CP config
// — the orchestrator package itself doesn't depend on any of these.
type CoordinatorOpts struct {
	AutoDeployer AutoDeployer // nil disables auto-deploy

	// CPLocalOkesuBinary is an explicit path to the okesu CLI used by
	// the cp-local dispatcher (for cron orchestrations with no node:).
	// Empty means "auto-resolve" (PATH lookup, then dir-of-CP-binary).
	CPLocalOkesuBinary string

	// CPLocalEnvExtras returns env vars merged into the cp-local
	// subprocess's environment — typically API keys for the agent
	// provider. Format: "KEY=value". Inherited env from the CP
	// process is used as the base. Called on each dispatch —
	// return value not cached, so key rotations land without a
	// CP restart.
	CPLocalEnvExtras func() []string

	// ActionPolicy carries the operator-set per-class auto-approve
	// toggle — the orchestrator engine consults it at the
	// step-approval gate. Zero value (empty AutoApprove) preserves
	// today's gate-everything behaviour. See
	// controlplane/orchestrator/action_class.go.
	ActionPolicy orchestrator.Policy

	// EnrichmentService backs the enrich_ioc action. Nil disables the
	// action — the applier returns "enrichment service not configured"
	// when an agent emits enrich_ioc on a CP without any vendor keys
	// wired up. Threaded in from server.go after the per-vendor
	// adapters + cache adapter are constructed from cfg.Enrichment.
	EnrichmentService *enrichment.Service
}

func NewOrchestrationCoordinator(
	store *db.Store, reg *RunRegistry, tunReg *tunnel.Registry,
	agentDirs []string, agg *federation.Aggregator,
	opts CoordinatorOpts,
) *OrchestrationCoordinator {
	adapter := &orchestratorStoreAdapter{store: store}
	local := newLocalDispatcher(reg, tunReg, store, agentDirs)
	fed := newFederatedDispatcher(agg)
	jobs := newJobsDispatcher(store, agentDirs)
	// CP-local dispatcher — only constructed when an okesu binary is
	// resolvable. Without one, the routing layer returns a clear
	// "no okesu binary" error on cp-local dispatch attempts rather
	// than silently failing the run.
	var cpLocal *cpLocalDispatcher
	if okesuBin := resolveOkesuBinary(opts.CPLocalOkesuBinary); okesuBin != "" {
		cpLocal = newCPLocalDispatcher(reg, store, agentDirs, okesuBin, opts.CPLocalEnvExtras)
		log.Printf("orchestrator: cp-local dispatcher enabled (okesu binary: %s)", okesuBin)
	} else {
		log.Printf("orchestrator: cp-local dispatcher disabled — no okesu binary on PATH or next to the CP binary")
	}
	disp := &routingDispatcher{
		local:        local,
		federated:    fed,
		jobs:         jobs,
		cpLocal:      cpLocal,
		tunReg:       tunReg,
		agg:          agg,
		store:        store,
		autoDeployer: opts.AutoDeployer,
	}
	engine := orchestrator.NewEngine(adapter, disp)
	engine.SetProgressSink(&stepNodeProgressSinkAdapter{store: store})
	// Wire the action applier so agents' orchestration_result.actions
	// produce real CP mutations (status / tags / severity / run links).
	applier := NewFindingActionApplier(store)
	if opts.EnrichmentService != nil {
		applier.SetEnrichmentService(opts.EnrichmentService)
	}
	engine.SetActionApplier(applier)
	// Wire the data resolver so steps with `data:` blocks get their
	// queries resolved against the CP store and bound into the
	// prompt template — replaces the old "have the agent curl
	// /api/findings" pattern. See data_resolver.go for the registry.
	engine.SetDataResolver(NewDataResolver(store))
	// Action-class auto-approve policy. Zero value = today's
	// gate-everything behaviour, so passing through unchanged is safe
	// even when the operator hasn't configured `policy:` in the YAML.
	engine.SetActionPolicy(opts.ActionPolicy)
	return &OrchestrationCoordinator{
		inflight:    map[int64]struct{}{},
		engine:      engine,
		store:       store,
		runSlots:    make(chan struct{}, MaxConcurrentRuns),
		triggerStop: make(chan struct{}),
	}
}

// SpawnRun creates an orchestration_run row, seeds pending step
// records, and kicks the engine. Used by both manual triggers (the
// HTTP handler) and the auto-trigger probes (finding/cron). Returns
// the run id or an error.
func (c *OrchestrationCoordinator) SpawnRun(orchID int64, triggerKind, triggerPayload string, startedBy int64) (int64, error) {
	o, err := c.store.GetOrchestration(orchID)
	if err != nil {
		return 0, fmt.Errorf("orchestration %d: %w", orchID, err)
	}
	if !o.Enabled {
		return 0, fmt.Errorf("orchestration %q is disabled", o.Name)
	}
	spec, err := orchestrator.Parse(o.SpecYAML)
	if err != nil {
		return 0, fmt.Errorf("spec: %w", err)
	}
	runID, err := c.store.CreateOrchestrationRun(orchID, triggerKind, triggerPayload, startedBy)
	if err != nil {
		return 0, err
	}
	for i, step := range spec.Steps {
		_ = c.store.UpsertOrchestrationStep(db.OrchestrationStepInsert{
			OrchestrationRunID: runID,
			StepID:             step.ID,
			StepIdx:            i,
			Status:             orchestrator.StepStatusPending,
		})
	}
	c.kick(runID)
	return runID, nil
}

// OnEnrichment probes every enabled `ioc_enriched` trigger and
// fires matching runs. Called once per fresh cache write by the
// enrichment service's hook. Cache hits don't fire the hook, so
// this path doesn't see the same IOC twice within a TTL window.
//
// Loop guard: a run triggered by enrichment that itself calls the
// `enrich_ioc` action on the SAME IOC would short-circuit at the
// cache (no fresh write → no re-fire), so an explicit visited-set
// here is unnecessary. Cross-IOC enrichment chains are still
// possible but require the operator to author them deliberately.
func (c *OrchestrationCoordinator) OnEnrichment(payload orchestrator.EnrichmentPayload) {
	rows, err := c.store.ListOrchestrations()
	if err != nil {
		return
	}
	for _, o := range rows {
		if !o.Enabled || o.TriggerKind != "ioc_enriched" {
			continue
		}
		filter := o.TriggerFilter.String
		ok, ferr := orchestrator.EvaluateEnrichmentFilter(filter, payload)
		if ferr != nil || !ok {
			continue
		}
		_, _ = c.SpawnRun(
			o.ID,
			"ioc_enriched",
			orchestrator.EnrichmentTriggerPayload(payload),
			0, // system actor
		)
		_ = c.store.UpdateOrchestrationLastFired(o.ID, fmt.Sprintf("ioc_enriched:%d:%s", payload.IOCID, payload.Adapter))
	}
}

// OnFinding probes every enabled finding-trigger orchestration and
// fires runs that match. Called by the eventpipeline once a finding
// has been projected. Errors per-orchestration are logged and don't
// stop other matches.
func (c *OrchestrationCoordinator) OnFinding(payload orchestrator.FindingPayload) {
	rows, err := c.store.ListOrchestrations()
	if err != nil {
		return
	}
	for _, o := range rows {
		if !o.Enabled || o.TriggerKind != "finding" {
			continue
		}
		filter := o.TriggerFilter.String
		ok, ferr := orchestrator.EvaluateFindingFilter(filter, payload)
		if ferr != nil || !ok {
			continue
		}
		_, _ = c.SpawnRun(
			o.ID,
			"finding",
			orchestrator.FindingTriggerPayload(payload),
			0, // system actor
		)
		_ = c.store.UpdateOrchestrationLastFired(o.ID, fmt.Sprintf("finding:%d", payload.ID))
	}
}

// StartCronScheduler runs a tick loop that fires cron-triggered
// orchestrations as their schedules come due. Started in server.Run
// after migrations have applied. Stops when StopTriggers is called.
func (c *OrchestrationCoordinator) StartCronScheduler(ctx context.Context) {
	go func() {
		// Tick every minute — cron's smallest unit is the minute, and
		// drift up to 30s either side is acceptable for the kinds of
		// schedules orchestrations use (every-Nth-hour, daily, etc.).
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		// Probe immediately on start so a CP restart doesn't miss a
		// run that should have fired in the past minute.
		c.probeCron(time.Now().UTC())
		for {
			select {
			case <-c.triggerStop:
				return
			case <-ctx.Done():
				return
			case now := <-t.C:
				c.probeCron(now.UTC())
			}
		}
	}()
}

func (c *OrchestrationCoordinator) StopTriggers() {
	select {
	case <-c.triggerStop:
		// already closed
	default:
		close(c.triggerStop)
	}
}

func (c *OrchestrationCoordinator) probeCron(now time.Time) {
	rows, err := c.store.ListOrchestrations()
	if err != nil {
		return
	}
	for _, o := range rows {
		if !o.Enabled || o.TriggerKind != "cron" {
			continue
		}
		expr := o.TriggerCron.String
		if expr == "" {
			continue
		}
		// Use last_fired_at as the lower bound. If the orchestration
		// has never fired, use one tick ago — that way an immediate
		// match (e.g. `* * * * *`) doesn't fire twice on first start.
		since := now.Add(-60 * time.Second)
		if o.LastFiredAt.Valid {
			since = o.LastFiredAt.Time
		}
		next, err := nextCronFireExposed(expr, since)
		if err != nil {
			continue
		}
		if next.After(now) {
			continue // not due yet
		}
		_, _ = c.SpawnRun(
			o.ID,
			"cron",
			orchestrator.CronTriggerPayload(now),
			0,
		)
		_ = c.store.UpdateOrchestrationLastFired(o.ID, "cron")
	}
}

// nextCronFireExposed wraps the package-private cron evaluator so
// the coordinator (which lives in the api package) can reach it
// without exporting parser internals from the orchestrator package.
// This stays here to keep orchestrator/triggers.go's surface tight.
func nextCronFireExposed(expr string, from time.Time) (time.Time, error) {
	return orchestrator.NextCronFire(expr, from)
}

// kick spawns a background goroutine that drives the run forward.
// Idempotent: if a goroutine is already in flight for this run, the
// call is a no-op (the in-flight one will pick up any new state on
// its next loop). The goroutine runs with a long-lived context so
// CP shutdown will eventually cancel it via process exit; per-step
// timeouts are honoured inside the engine.
func (c *OrchestrationCoordinator) kick(runID int64) {
	c.mu.Lock()
	if _, busy := c.inflight[runID]; busy {
		c.mu.Unlock()
		return
	}
	c.inflight[runID] = struct{}{}
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, runID)
			c.mu.Unlock()
		}()
		// Acquire a global run slot before invoking the engine. When
		// the cap is reached, this send blocks until a running goroutine
		// releases its slot. Run rows stay queued in the DB; engine.Run
		// honours per-step timeouts only after we hand control to it.
		waitStart := time.Now()
		c.runSlots <- struct{}{}
		if waited := time.Since(waitStart); waited > 5*time.Second {
			log.Printf("orchestration run %d waited %s for a concurrency slot (cap=%d)", runID, waited.Round(time.Millisecond), MaxConcurrentRuns)
		}
		defer func() { <-c.runSlots }()
		_ = c.engine.Run(context.Background(), runID)
	}()
}

type orchestrationRunReq struct {
	Inputs map[string]any `json:"inputs,omitempty"`
}

// OrchestrationRunCreate — POST /api/orchestrations/{id}/run
func OrchestrationRunCreate(store *db.Store, coord *OrchestrationCoordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		o, err := store.GetOrchestration(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// Parse the spec early — we need the step list to seed pending
		// step records so the run-detail canvas can render the full
		// DAG from second one, even if step 1 fails before persisting.
		spec, err := orchestrator.Parse(o.SpecYAML)
		if err != nil {
			http.Error(w, "spec: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var req orchestrationRunReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Trigger payload always carries `kind: manual` plus whatever
		// inputs the operator supplied. Inputs are flattened into the
		// trigger map directly so a spec input named `host` is
		// available as `{{trigger.host}}`.
		payload := map[string]any{"kind": "manual"}
		for k, v := range req.Inputs {
			payload[k] = v
		}
		payloadJSON, _ := json.Marshal(payload)

		var startedBy int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			startedBy = u.ID
		}
		runID, err := store.CreateOrchestrationRun(o.ID, "manual", string(payloadJSON), startedBy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Seed pending step records — the engine upserts these as it
		// transitions them through running/completed/failed/skipped,
		// but having them present from the start means the operator
		// sees the full graph layout immediately after clicking Run.
		for i, step := range spec.Steps {
			_ = store.UpsertOrchestrationStep(db.OrchestrationStepInsert{
				OrchestrationRunID: runID,
				StepID:             step.ID,
				StepIdx:            i,
				Status:             orchestrator.StepStatusPending,
			})
		}
		audit.Emit(r, store, db.AuditEntry{
			Action:   "orchestration.run",
			Target:   fmt.Sprintf("orchestration_run:%d", runID),
			Metadata: map[string]any{"orchestration_id": o.ID, "name": o.Name},
		})
		coord.kick(runID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": runID})
	}
}

// OrchestrationRunsList — GET /api/orchestration-runs
//
// Query string filters (any combination):
//
//	status=running,failed         CSV — multi-select pill
//	orchestration_id=3,7          CSV
//	trigger_kind=manual,finding   CSV
//	since=30m | 1h | 24h | 7d     relative-time alias (or unix-ms)
//	q=needle                      free-text against id/step/host/finding_id
//	limit=N                       page size; clamped to 1000, default 100
//	offset=M                      page offset
//	counts=1                      return wrapper with {rows, counts_by_status}
//	                              so the runs-tab pills don't need a separate
//	                              round-trip.
func OrchestrationRunsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := db.OrchestrationRunFilter{
			Status:           splitCSV(r.URL.Query().Get("status")),
			OrchestrationIDs: parseInt64CSV(r.URL.Query().Get("orchestration_id")),
			TriggerKinds:     splitCSV(r.URL.Query().Get("trigger_kind")),
			SinceMs:          parseSinceMs(r.URL.Query().Get("since")),
			Search:           r.URL.Query().Get("q"),
		}
		f.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
		f.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

		rows, err := store.ListOrchestrationRunsFiltered(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonRows := make([]orchestrationRunJSON, 0, len(rows))
		for _, run := range rows {
			jsonRows = append(jsonRows, toOrchestrationRunJSON(run, nil, nil))
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("counts") == "1" {
			counts, _ := store.CountOrchestrationRunsByStatus(f)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rows":             jsonRows,
				"counts_by_status": counts,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(jsonRows)
	}
}

// splitCSV trims and drops empty entries.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseInt64CSV is splitCSV + ParseInt; bad entries are silently
// dropped so a stray ?orchestration_id=,7,abc still works.
func parseInt64CSV(s string) []int64 {
	parts := splitCSV(s)
	if len(parts) == 0 {
		return nil
	}
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// parseSinceMs accepts either a relative alias ("30m", "1h", "24h",
// "7d") or a raw unix-ms timestamp. Returns 0 ⇒ no filter.
func parseSinceMs(s string) int64 {
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d).UnixMilli()
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return n
	}
	return 0
}

// OrchestrationRunDetail — GET /api/orchestration-runs/{id}
func OrchestrationRunDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		run, err := store.GetOrchestrationRun(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		steps, _ := store.ListOrchestrationSteps(id)
		// Group per-host dispatches by step_id so the serializer can
		// drop the matching slice into each step's PerNode field.
		perNode, _ := store.ListStepNodeDispatchesByRun(id)
		perNodeByStep := map[string][]*db.StepNodeDispatch{}
		for _, p := range perNode {
			perNodeByStep[p.StepID] = append(perNodeByStep[p.StepID], p)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toOrchestrationRunJSON(run, steps, perNodeByStep))
	}
}

// OrchestrationStepApprove — POST /api/orchestration-runs/{id}/steps/{stepID}/approve
func OrchestrationStepApprove(store *db.Store, coord *OrchestrationCoordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		stepID := chi.URLParam(r, "stepID")
		var userID int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			userID = u.ID
		}
		if err := coord.engine.Approve(r.Context(), id, stepID, userID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "orchestration.approve",
			Target: fmt.Sprintf("orchestration_run:%d/step:%s", id, stepID),
		})
		coord.kick(id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// OrchestrationRunCancel — POST /api/orchestration-runs/{id}/cancel
func OrchestrationRunCancel(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		// Best-effort: mark cancelled. The engine respects the new
		// status on its next iteration. In-flight step Runs continue
		// — operators cancel those individually if needed.
		if err := store.FinishOrchestrationRun(id, "cancelled", "operator cancelled"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "orchestration.cancel",
			Target: fmt.Sprintf("orchestration_run:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// bulkOpRequest is the wire body for both /bulk-cancel and /bulk-retry.
// Run ids are scoped per-CP — the federated wrapper splits the list
// by `cp_instance_id` (passed alongside each id) and forwards the
// per-CP slices to the right peer. The local handler ignores the
// cp field; it only sees ids that already belong to it.
type bulkOpRequest struct {
	IDs []int64 `json:"ids"`
}

// bulkOpResponse summarises which ids the operator's request affected.
// Failures per-id are included so the UI can show "12 cancelled, 1
// already finished, 1 not found" instead of an opaque success/fail.
type bulkOpResponse struct {
	Affected []int64          `json:"affected"`
	Skipped  map[int64]string `json:"skipped,omitempty"` // id → reason
}

// OrchestrationRunsBulkCancel — POST /api/orchestration-runs/bulk-cancel
//
// Accepts `{"ids": [...]}` and marks each `running` / `pending` /
// `approval_required` run as cancelled. Already-terminal runs are
// silently skipped (with a "not in cancellable state" reason in the
// response so operators see why a row didn't move). Each transition
// emits one audit row.
func OrchestrationRunsBulkCancel(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bulkOpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
			http.Error(w, "request body must be {ids: [...]}", http.StatusBadRequest)
			return
		}
		resp := bulkOpResponse{Skipped: map[int64]string{}}
		for _, id := range req.IDs {
			run, err := store.GetOrchestrationRun(id)
			if err != nil {
				resp.Skipped[id] = "not found"
				continue
			}
			switch run.Status {
			case "completed", "failed", "cancelled":
				resp.Skipped[id] = "already " + run.Status
				continue
			}
			if err := store.FinishOrchestrationRun(id, "cancelled", "operator bulk-cancel"); err != nil {
				resp.Skipped[id] = err.Error()
				continue
			}
			audit.Emit(r, store, db.AuditEntry{
				Action: "orchestration.bulk_cancel",
				Target: fmt.Sprintf("orchestration_run:%d", id),
			})
			resp.Affected = append(resp.Affected, id)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// OrchestrationRunsBulkRetry — POST /api/orchestration-runs/bulk-retry
//
// Spawns a fresh run for each input run id, reusing the original
// trigger kind + payload + orchestration id. The new runs are NOT
// linked back to the originals — they appear as new rows. This is
// the right semantics for the lab pattern of "the deploy hiccupped,
// re-fire that batch of T1 autotriages": each new run audits cleanly
// and operators can compare side-by-side.
//
// Skips: original run not found, original orchestration disabled
// (would fire and immediately quietly do nothing, surface clearly),
// original orchestration deleted.
func OrchestrationRunsBulkRetry(store *db.Store, coord *OrchestrationCoordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bulkOpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
			http.Error(w, "request body must be {ids: [...]}", http.StatusBadRequest)
			return
		}
		var startedBy int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			startedBy = u.ID
		}
		resp := bulkOpResponse{Skipped: map[int64]string{}}
		for _, id := range req.IDs {
			run, err := store.GetOrchestrationRun(id)
			if err != nil {
				resp.Skipped[id] = "not found"
				continue
			}
			orch, err := store.GetOrchestration(run.OrchestrationID)
			if err != nil {
				resp.Skipped[id] = "orchestration deleted"
				continue
			}
			if !orch.Enabled {
				resp.Skipped[id] = "orchestration disabled"
				continue
			}
			newID, err := coord.SpawnRun(orch.ID, run.TriggerKind, run.TriggerPayload.String, startedBy)
			if err != nil {
				resp.Skipped[id] = err.Error()
				continue
			}
			audit.Emit(r, store, db.AuditEntry{
				Action:   "orchestration.bulk_retry",
				Target:   fmt.Sprintf("orchestration_run:%d", id),
				Metadata: map[string]any{"new_run_id": newID},
			})
			resp.Affected = append(resp.Affected, newID)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

type orchestrationRunJSON struct {
	ID              int64                   `json:"id"`
	OrchestrationID int64                   `json:"orchestration_id"`
	Status          string                  `json:"status"`
	TriggerKind     string                  `json:"trigger_kind"`
	TriggerPayload  map[string]any          `json:"trigger_payload,omitempty"`
	CurrentStepID   string                  `json:"current_step_id,omitempty"`
	StartedAt       string                  `json:"started_at"`
	EndedAt         string                  `json:"ended_at,omitempty"`
	Error           string                  `json:"error,omitempty"`
	Steps           []orchestrationStepJSON `json:"steps,omitempty"`
}

type orchestrationStepJSON struct {
	StepID         string         `json:"step_id"`
	StepIdx        int            `json:"step_idx"`
	Status         string         `json:"status"`
	RunID          string         `json:"run_id,omitempty"`
	CPInstanceID   string         `json:"cp_instance_id,omitempty"`
	RenderedPrompt string         `json:"rendered_prompt,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	OutputSummary  string         `json:"output_summary,omitempty"`
	StartedAt      string         `json:"started_at,omitempty"`
	EndedAt        string         `json:"ended_at,omitempty"`
	Error          string         `json:"error,omitempty"`
	ApprovedAt     string         `json:"approved_at,omitempty"`
	// Data is the resolved snapshot of the step's `data:` block,
	// captured at dispatch. The UI surfaces this on the step-detail
	// panel so operators can see exactly what input the agent saw.
	Data    any                    `json:"data,omitempty"`
	PerNode []stepNodeDispatchJSON `json:"per_node,omitempty"`
}

type stepNodeDispatchJSON struct {
	Host          string `json:"host"`
	Status        string `json:"status"`
	AgentRunID    string `json:"agent_run_id,omitempty"`
	FindingsCount int    `json:"findings_count"`
	OutputTail    string `json:"output_tail,omitempty"`
	Error         string `json:"error,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	EndedAt       string `json:"ended_at,omitempty"`
}

func toOrchestrationRunJSON(
	r *db.OrchestrationRun,
	steps []*db.OrchestrationStep,
	perNodeByStep map[string][]*db.StepNodeDispatch,
) orchestrationRunJSON {
	out := orchestrationRunJSON{
		ID:              r.ID,
		OrchestrationID: r.OrchestrationID,
		Status:          r.Status,
		TriggerKind:     r.TriggerKind,
		CurrentStepID:   r.CurrentStepID.String,
		StartedAt:       r.StartedAt.UTC().Format(time.RFC3339),
		Error:           r.Error.String,
	}
	if r.EndedAt.Valid {
		out.EndedAt = r.EndedAt.Time.UTC().Format(time.RFC3339)
	}
	if r.TriggerPayload.Valid {
		_ = json.Unmarshal([]byte(r.TriggerPayload.String), &out.TriggerPayload)
	}
	for _, st := range steps {
		s := orchestrationStepJSON{
			StepID:         st.StepID,
			StepIdx:        st.StepIdx,
			Status:         st.Status,
			RunID:          st.RunID.String,
			CPInstanceID:   st.CPInstanceID.String,
			RenderedPrompt: st.RenderedPrompt.String,
			OutputSummary:  st.OutputSummary.String,
			Error:          st.Error.String,
		}
		if st.StartedAt.Valid {
			s.StartedAt = st.StartedAt.Time.UTC().Format(time.RFC3339)
		}
		if st.EndedAt.Valid {
			s.EndedAt = st.EndedAt.Time.UTC().Format(time.RFC3339)
		}
		if st.ApprovedAt.Valid {
			s.ApprovedAt = st.ApprovedAt.Time.UTC().Format(time.RFC3339)
		}
		if st.ResultJSON.Valid {
			_ = json.Unmarshal([]byte(st.ResultJSON.String), &s.Result)
		}
		if st.DataSnapshot.Valid && st.DataSnapshot.String != "" {
			var data any
			if err := json.Unmarshal([]byte(st.DataSnapshot.String), &data); err == nil {
				s.Data = data
			}
		}
		// Per-host dispatch hydration. Prefer the dedicated table;
		// fall back to result_json.byNode for legacy runs (pre-table).
		if rows, ok := perNodeByStep[st.StepID]; ok && len(rows) > 0 {
			s.PerNode = make([]stepNodeDispatchJSON, 0, len(rows))
			for _, p := range rows {
				s.PerNode = append(s.PerNode, stepNodeDispatchJSON{
					Host:          p.Host,
					Status:        p.Status,
					AgentRunID:    p.AgentRunID.String,
					FindingsCount: p.FindingsCount,
					OutputTail:    p.OutputTail.String,
					Error:         p.Error.String,
					StartedAt:     formatNullableTime(p.StartedAt),
					EndedAt:       formatNullableTime(p.EndedAt),
				})
			}
		} else if byNode := readByNodeFromResult(s.Result); len(byNode) > 0 {
			// Legacy run: hydrate read-only from the byNode JSON map.
			// Status / agent_run_id / output_tail / error / findings_count
			// are present; timestamps stay empty.
			s.PerNode = byNode
		}
		out.Steps = append(out.Steps, s)
	}
	return out
}

func formatNullableTime(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}

// readByNodeFromResult decodes orchestration_steps.result_json for a
// fan-out step and projects its byNode map into the same shape as the
// new table. Used to render legacy runs (pre-migration 038) with the
// new fan-out card. Returns nil for non-fan-out steps.
func readByNodeFromResult(result map[string]any) []stepNodeDispatchJSON {
	raw, ok := result["byNode"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]stepNodeDispatchJSON, 0, len(raw))
	hosts := make([]string, 0, len(raw))
	for h := range raw {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		entry, _ := raw[h].(map[string]any)
		if entry == nil {
			continue
		}
		j := stepNodeDispatchJSON{Host: h}
		if v, ok := entry["status"].(string); ok {
			j.Status = v
		}
		if v, ok := entry["run_id"].(string); ok {
			j.AgentRunID = v
		}
		if v, ok := entry["output_tail"].(string); ok {
			j.OutputTail = v
		}
		if v, ok := entry["error"].(string); ok {
			j.Error = v
		}
		if findings, ok := entry["findings"].([]any); ok {
			j.FindingsCount = len(findings)
		}
		out = append(out, j)
	}
	return out
}
