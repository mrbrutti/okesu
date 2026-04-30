package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// Run is the in-memory companion of a row in the `runs` table. We use it to
// fan out incoming tunnel lines to live SSE subscribers without paying a DB
// round-trip per line. The DB row remains the source of truth for status,
// history, and replay after the run is finished.
type Run struct {
	ID       string
	NodeName string
	Status   string

	mu       sync.Mutex
	lines    []string // bounded buffer for late subscribers
	subs     map[chan string]struct{}
	finish   chan struct{}
	finished sync.Once // guards close(finish) so multiple paths can call markFinished
}

// RunLogCap caps the in-memory replay buffer per live run.
const RunLogCap = 2000

// RunRegistry tracks the currently-live runs (registered when started, removed
// once finished). Live runs source their SSE stream from here; finished runs
// replay from the DB.
type RunRegistry struct {
	mu   sync.RWMutex
	runs map[string]*Run
}

// NewRunRegistry builds an empty registry.
func NewRunRegistry() *RunRegistry { return &RunRegistry{runs: map[string]*Run{}} }

// Get returns the live record for id, or nil if none.
func (r *RunRegistry) Get(id string) *Run {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runs[id]
}

func (r *RunRegistry) put(run *Run) {
	r.mu.Lock()
	r.runs[run.ID] = run
	r.mu.Unlock()
}

func (r *RunRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.runs, id)
	r.mu.Unlock()
}

// runRequest is the JSON body for POST /api/runs.
type runRequest struct {
	Node     string `json:"node"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	MaxTurns int    `json:"max_turns,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Prompt   string `json:"prompt"`
	// FindingID, when > 0, links this run to a finding so the FindingDrawer
	// can show the resulting transcript as part of its "Investigations"
	// section. Set by the "Investigate this finding" button.
	FindingID int64 `json:"finding_id,omitempty"`
}

// CreateRun handles POST /api/runs — start a new ad-hoc agent run on a node.
//
// agentSearchDirs is the list of directories on the CP host that hold
// short-form agent definitions (Claude/Codex format). When the request
// names an agent, we ship the matching file's content to the node so the
// run uses the CP-managed definition even if the node doesn't have a
// local copy. Daimons (long-form, scheduled) are not selectable here —
// trigger them ad-hoc from the daimon detail page instead.
func CreateRun(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store, agentSearchDirs []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Node == "" || req.Prompt == "" {
			http.Error(w, "node and prompt are required", http.StatusBadRequest)
			return
		}

		// Look up agent content from the library so the node doesn't need
		// the file pre-staged. If the lookup fails (e.g. agent removed
		// between picker fetch and submit), surface a clear 400 — the
		// daemon would otherwise get an obscure file-not-found error.
		var agentContent string
		if req.Agent != "" {
			path, err := findAgentFile(agentSearchDirs, req.Agent)
			if err == nil {
				if content, rerr := os.ReadFile(path); rerr == nil {
					agentContent = string(content)
				}
			}
			if agentContent == "" {
				http.Error(w, fmt.Sprintf("agent %q not found in library", req.Agent), http.StatusBadRequest)
				return
			}
		}
		runID, _ := randomID()
		var startedByID int64
		var startedByEmail string
		if u := auth.UserFromContext(r.Context()); u != nil {
			startedByID = u.ID
			startedByEmail = u.Email
		}

		// Decide dispatch: tunnel preferred (real-time stream), pull-
		// mode fallback (write a node_jobs row; the okesu-jobs runtime
		// claims it on its next poll). The orchestration step path
		// already does this; ad-hoc runs now match that behaviour so
		// nodes without a tunnel are dispatchable from the Run-Agent
		// dialog. Future S3-transport nodes will route through the
		// same node_jobs pipeline once the bucket-side writer lands
		// (PR B in this design).
		conn := tunReg.Get(req.Node)
		if conn == nil && !jobsRuntimeFreshOnThisCP(store, req.Node) {
			http.Error(w, fmt.Sprintf(
				"node %q has no dispatch runtime: no tunnel attached and no recent jobs-runtime poll. "+
					"Start `okesu node` (tunnel) or `okesu-jobs.service` (pull) on the host.", req.Node,
			), http.StatusBadRequest)
			return
		}

		if err := store.CreateRun(db.RunInsert{
			ID:              runID,
			NodeName:        req.Node,
			Provider:        req.Provider,
			Model:           req.Model,
			Effort:          req.Effort,
			AgentName:       req.Agent,
			Prompt:          req.Prompt,
			StartedByUserID: startedByID,
			StartedByEmail:  startedByEmail,
			FindingID:       req.FindingID,
		}); err != nil {
			http.Error(w, "persist run: "+err.Error(), http.StatusInternalServerError)
			return
		}

		run := &Run{
			ID:       runID,
			NodeName: req.Node,
			Status:   db.RunStatusRunning,
			subs:     map[chan string]struct{}{},
			finish:   make(chan struct{}),
		}
		reg.put(run)

		dispatchMethod := "tunnel"
		if conn != nil {
			lines, exit, cleanup, err := conn.SendRun(tunnel.RunPayload{
				RunID:        runID,
				Provider:     req.Provider,
				Model:        req.Model,
				Effort:       req.Effort,
				MaxTurns:     req.MaxTurns,
				Agent:        req.Agent,
				AgentContent: agentContent,
				Prompt:       req.Prompt,
			})
			if err != nil {
				_ = store.FinishRun(runID, db.RunStatusFailed, -1, err.Error())
				run.completeError(err.Error())
				reg.remove(runID)
				http.Error(w, "tunnel send: "+err.Error(), http.StatusBadGateway)
				return
			}
			go run.consume(store, reg, lines, exit, cleanup)
		} else {
			// Pull-mode path. Look up node id, write a node_jobs row;
			// MgmtJobOutput / MgmtJobExit (with reg threaded in) will
			// notify the in-memory Run as the daemon streams output and
			// fires its terminal exit. No persistent connection held.
			dispatchMethod = "pull"
			row := store.QueryRow(`SELECT id FROM nodes WHERE name = ?`, req.Node)
			var nodeID int64
			if err := row.Scan(&nodeID); err != nil {
				_ = store.FinishRun(runID, db.RunStatusFailed, -1, "no nodes row: "+err.Error())
				run.completeError(err.Error())
				reg.remove(runID)
				http.Error(w, "no nodes row for "+req.Node, http.StatusBadRequest)
				return
			}
			payload := agent.JobPayload{
				Agent:        req.Agent,
				AgentContent: agentContent,
				Prompt:       req.Prompt,
				// Provider/Model/Effort/MaxTurns aren't currently part of
				// agent.JobPayload — pull-mode adopts the runtime's
				// defaults. If/when we extend the pull-mode wire, plumb
				// them through here too.
			}
			payloadJSON, _ := json.Marshal(payload)
			if _, err := store.CreateNodeJob(runID, nodeID, string(agent.JobKindAgentRun), string(payloadJSON)); err != nil {
				_ = store.FinishRun(runID, db.RunStatusFailed, -1, "enqueue job: "+err.Error())
				run.completeError(err.Error())
				reg.remove(runID)
				http.Error(w, "enqueue job: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		audit.Emit(r, store, db.AuditEntry{
			Action:   "run.start",
			Target:   fmt.Sprintf("run:%s", runID),
			Metadata: map[string]any{
				"node":     req.Node,
				"provider": req.Provider,
				"agent":    req.Agent,
				"dispatch": dispatchMethod,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run_id":   runID,
			"node":     req.Node,
			"dispatch": dispatchMethod,
		})
	}
}

// consume fans line/exit events into the DB and live subscribers.
func (run *Run) consume(
	store *db.Store, reg *RunRegistry,
	lines <-chan tunnel.LinePayload, exit <-chan tunnel.ExitPayload, cleanup func(),
) {
	defer cleanup()
	defer reg.remove(run.ID)
	defer run.markFinishClosed()

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			run.appendLine(line.Data)
			_ = store.AppendRunLine(run.ID, line.Stream, line.Data)
		case ex, ok := <-exit:
			if !ok {
				run.completeError("tunnel closed before exit")
				_ = store.FinishRun(run.ID, db.RunStatusFailed, -1, "tunnel closed before exit")
				return
			}
			// Drain any remaining buffered lines first so the DB reflects them.
			for {
				select {
				case line, ok := <-lines:
					if !ok {
						lines = nil
						goto finish
					}
					run.appendLine(line.Data)
					_ = store.AppendRunLine(run.ID, line.Stream, line.Data)
				default:
					goto finish
				}
			}
		finish:
			status := db.RunStatusSucceeded
			if ex.Error != "" {
				status = db.RunStatusFailed
			} else if ex.Code != 0 {
				status = db.RunStatusFailed
			}
			run.complete(status)
			_ = store.FinishRun(run.ID, status, ex.Code, ex.Error)
			return
		}
	}
}

func (run *Run) appendLine(line string) {
	run.mu.Lock()
	if len(run.lines) >= RunLogCap {
		run.lines = run.lines[len(run.lines)-RunLogCap+1:]
	}
	run.lines = append(run.lines, line)
	subs := make([]chan string, 0, len(run.subs))
	for ch := range run.subs {
		subs = append(subs, ch)
	}
	run.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- line:
		default:
			// drop lines for slow subscribers
		}
	}
}

func (run *Run) complete(status string) {
	run.mu.Lock()
	run.Status = status
	for ch := range run.subs {
		close(ch)
	}
	run.subs = nil
	run.mu.Unlock()
}

func (run *Run) completeError(msg string) {
	run.mu.Lock()
	run.Status = db.RunStatusFailed
	for ch := range run.subs {
		close(ch)
	}
	run.subs = nil
	run.mu.Unlock()
	_ = msg // recorded via FinishRun by the caller
}

// markFinishClosed closes run.finish at most once. Multiple paths can
// race to declare a run done — the tunnel consume loop's exit branch,
// the pull-mode exit POST handler, and a CP-side cancel — so the
// close-once guard keeps the channel safe to use as the SSE handler's
// "the run is over" signal regardless of which path got there first.
func (run *Run) markFinishClosed() {
	run.finished.Do(func() { close(run.finish) })
}

// AppendLine is the public entry-point used by handlers that ingest
// run output produced outside the tunnel path (currently: the pull-
// mode jobs runtime's MgmtJobOutput webhook). The DB write happens at
// the call site (store.AppendRunLine); this just notifies the live
// in-memory subscribers so the SSE stream sees the chunk in real time.
//
// Safe to call when nobody is subscribed — slow/missing subscribers
// drop lines silently per appendLine's contract.
func (run *Run) AppendLine(line string) { run.appendLine(line) }

// Complete is the public entry-point that marks a run done from a
// path other than the tunnel consume loop (currently: MgmtJobExit for
// pull-mode and S3 transports). Closes subscriber channels and the
// finish signal; idempotent via sync.Once on `finish`. The Status is
// updated under the same lock as the in-memory line buffer, so a
// subscriber reading Lines() after Complete returns sees the final
// status atomically.
func (run *Run) Complete(status string) {
	run.complete(status)
	run.markFinishClosed()
}

// MarkLive registers a Run in this registry. Used by the pull-mode
// CreateRun path which doesn't go through consume() (no tunnel
// goroutine to own the lifecycle). Tunnel path keeps using `put`
// directly so the contract matches its existing semantics.
func (r *RunRegistry) MarkLive(run *Run) { r.put(run) }

// Forget removes a run from the registry. Pull-mode path calls this
// when MgmtJobExit fires the terminal state, mirroring the deferred
// `reg.remove` in consume(). Idempotent.
func (r *RunRegistry) Forget(id string) { r.remove(id) }

// Lines snapshots the live in-memory tail for replay to a new SSE subscriber.
func (run *Run) Lines() []string {
	run.mu.Lock()
	defer run.mu.Unlock()
	out := make([]string, len(run.lines))
	copy(out, run.lines)
	return out
}

// Subscribe registers a channel for new lines on a live run.
func (run *Run) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 64)
	run.mu.Lock()
	if run.subs == nil {
		run.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	run.subs[ch] = struct{}{}
	run.mu.Unlock()
	return ch, func() {
		run.mu.Lock()
		if _, ok := run.subs[ch]; ok {
			delete(run.subs, ch)
			close(ch)
		}
		run.mu.Unlock()
	}
}

// Finished returns a channel closed once the run terminates (live registry).
func (run *Run) Finished() <-chan struct{} { return run.finish }

// CancelRun handles POST /api/runs/{id}/cancel — sends a Cancel frame to the
// node and marks the run cancelled. No-op (404) if the run isn't live.
func CancelRun(reg *RunRegistry, tunReg *tunnel.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := chi.URLParam(r, "id")
		row, err := store.GetRun(runID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if row.Status != db.RunStatusRunning {
			http.Error(w, "run is not running (status="+row.Status+")", http.StatusConflict)
			return
		}
		conn := tunReg.Get(row.NodeName)
		if conn == nil {
			// Node disconnected — mark cancelled in DB anyway so the row clears.
			_ = store.FinishRun(runID, db.RunStatusCancelled, -1, "node disconnected")
			audit.Emit(r, store, db.AuditEntry{
				Action: "run.cancel", Target: "run:" + runID,
				Metadata: map[string]any{"node_disconnected": true},
			})
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if err := conn.Cancel(runID); err != nil {
			http.Error(w, "cancel: "+err.Error(), http.StatusBadGateway)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "run.cancel", Target: "run:" + runID,
		})
		// Mark cancelled now so the row reflects operator intent even if the
		// node's exit frame is delayed or lost. FinishRun is guarded by
		// status='running', so the eventual exit-frame FinishRun(succeeded|failed)
		// becomes a no-op and the cancelled state sticks.
		_ = store.FinishRun(runID, db.RunStatusCancelled, -1, "cancelled by operator")
		w.WriteHeader(http.StatusAccepted)
	}
}

// runListItem is the wire shape for /api/runs.
type runListItem struct {
	ID         string `json:"id"`
	Node       string `json:"node"`
	Provider   string `json:"provider,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Prompt     string `json:"prompt"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	StartedBy  string `json:"started_by,omitempty"`
	FindingID  int64  `json:"finding_id,omitempty"`
}

// RunsList handles GET /api/runs?limit=&offset= — DB-backed history.
func RunsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		rows, err := store.ListRuns(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]runListItem, 0, len(rows))
		for _, row := range rows {
			out = append(out, projectRun(row, true))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// runDetail extends runListItem with full prompt + buffered log lines + error.
type runDetail struct {
	runListItem
	Lines []string `json:"lines"`
	Error string   `json:"error,omitempty"`
}

// RunStatus handles GET /api/runs/{id}.
func RunStatus(reg *RunRegistry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := chi.URLParam(r, "id")
		row, err := store.GetRun(runID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := runDetail{
			runListItem: projectRun(row, false),
		}
		if row.Error.Valid {
			out.Error = row.Error.String
		}
		// Prefer live in-memory tail (fresher) when the run is still streaming.
		if live := reg.Get(runID); live != nil {
			out.Lines = live.Lines()
		} else {
			db, err := store.RunLines(runID, 0)
			if err == nil {
				for _, l := range db {
					out.Lines = append(out.Lines, l.Data)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// RunLogStream handles GET /api/runs/{id}/log — SSE.
//
// Live runs: replay the in-memory buffer, then tail subscribers; emit `done`
// when the run finishes.
//
// Finished runs: replay the persisted log from the DB once and immediately
// emit `done`. This makes the same URL work whether the run is in flight or
// from yesterday.
func RunLogStream(reg *RunRegistry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := chi.URLParam(r, "id")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")

		live := reg.Get(runID)
		if live == nil {
			row, err := store.GetRun(runID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			lines, err := store.RunLines(runID, 0)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, l := range lines {
				fmt.Fprintf(w, "data: %s\n\n", l.Data)
			}
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", row.Status)
			flusher.Flush()
			return
		}

		// Live run: snapshot buffer, then tail.
		for _, line := range live.Lines() {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		flusher.Flush()
		ch, cancel := live.Subscribe()
		defer cancel()
		finished := live.Finished()
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					fmt.Fprintf(w, "event: done\ndata: %s\n\n", live.Status)
					flusher.Flush()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			case <-finished:
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", live.Status)
				flusher.Flush()
				return
			}
		}
	}
}

// projectRun maps a db.Run to the wire shape, optionally truncating the prompt
// for list views (where the full prompt is rarely needed).
func projectRun(row *db.Run, truncatePrompt bool) runListItem {
	prompt := row.Prompt
	if truncatePrompt {
		prompt = truncate(prompt, 120)
	}
	item := runListItem{
		ID:        row.ID,
		Node:      row.NodeName,
		Provider:  row.Provider.String,
		Agent:     row.AgentName.String,
		Prompt:    prompt,
		StartedAt: row.StartedAt.UTC().Format(time.RFC3339),
		Status:    row.Status,
		ExitCode:  row.ExitCode,
		StartedBy: row.StartedByEmail.String,
	}
	if row.FinishedAt.Valid {
		item.FinishedAt = row.FinishedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.FindingID.Valid {
		item.FindingID = row.FindingID.Int64
	}
	return item
}

// RunsForFinding handles GET /api/findings/{id}/runs — every run linked
// to the finding, newest first. Drives the "Investigations" section on
// the FindingDrawer.
func RunsForFinding(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		rows, err := store.ListRunsForFinding(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]runListItem, 0, len(rows))
		for _, row := range rows {
			out = append(out, projectRun(row, true))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// ConnectedNodes handles GET /api/nodes/connected.
func ConnectedNodes(tunReg *tunnel.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tunReg.Names())
	}
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
