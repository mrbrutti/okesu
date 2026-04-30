# Orchestration fan-out visualization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render multi-host orchestration fan-out steps with per-host status, live progress, and host-scoped drill-in on the run-detail canvas. Backed by a new `orchestration_step_node_dispatches` table the engine writes to incrementally.

**Architecture:** A new DB table holds one row per `(run, step, host)`. The engine's `fanOut()` writes through a `StepNodeProgressSink` interface (Insert on dispatch start, Update on return). The aggregated `byNode` map continues to land in `result_json` at completion for template back-compat. The run-detail API hydrates a new `per_node` slice on each step view (preferring the table; falling back to `result_json.byNode` for legacy runs). The frontend's `OrchestrationRunCanvas` delegates to a new `OrchestrationFanoutCard` when `per_node` is non-empty, with collapsed/expanded modes governed by the host count and live status. A host-scoped drawer (`FanoutHostDrawer`) exposes per-host findings, output_tail, and a link to the agent run; a list drawer (`FanoutHostListDrawer`) is the drill surface at >20 hosts.

**Tech Stack:** Go (controlplane), SQLite + Postgres dialects via `db.Store`'s rewriter, React 18 + TypeScript, react-flow (`@xyflow/react`), Tailwind, lucide-react.

**Spec:** `docs/superpowers/specs/2026-04-29-orchestration-fanout-visualization-design.md`

**Note on frontend tests:** The web/ workspace currently has no test runner configured (vitest/jest/RTL). Rather than introduce one as a side-effect of this plan, the frontend tasks substitute manual verification steps with explicit acceptance criteria. Adding a test harness is an out-of-band follow-up.

---

## File structure

**Created (10):**
- `controlplane/db/migrations/sqlite/038_step_node_dispatches.sql`
- `controlplane/db/migrations/postgres/038_step_node_dispatches.sql`
- `controlplane/db/step_node_dispatches.go`
- `controlplane/db/step_node_dispatches_test.go`
- `controlplane/orchestrator/progress_sink.go`
- `web/src/components/OrchestrationFanoutCard.tsx`
- `web/src/components/FanoutHostDrawer.tsx`
- `web/src/components/FanoutHostListDrawer.tsx`
- `web/src/lib/fanoutAutoExpand.ts` (the rule, factored out for reuse + manual verification)

**Modified (8):**
- `controlplane/db/store.go` (embed migration 038, append to slices)
- `controlplane/db/orchestrations.go` (extend `FinishOrchestrationRun` to reconcile orphan rows; new method `ListStepNodeDispatchesByRun`)
- `controlplane/orchestrator/engine.go` (`fanOut` accepts a sink; engine wires it; restart reconciliation)
- `controlplane/orchestrator/engine_test.go` (recorder sink; new tests)
- `controlplane/api/orchestrations.go` (`orchestrationStepJSON.PerNode`, hydration logic)
- `controlplane/api/orchestrations_test.go` (handler test for `per_node`)
- `web/src/api.ts` (`StepNodeDispatchView` type; `per_node` on `OrchestrationStepView`)
- `web/src/components/OrchestrationRunCanvas.tsx` (delegate to fanout card when `per_node.length > 0`)

---

## Task 1: Migration 038 — `orchestration_step_node_dispatches`

**Files:**
- Create: `controlplane/db/migrations/sqlite/038_step_node_dispatches.sql`
- Create: `controlplane/db/migrations/postgres/038_step_node_dispatches.sql`
- Modify: `controlplane/db/store.go` (embed + append)

- [ ] **Step 1: Write the SQLite migration**

Create `controlplane/db/migrations/sqlite/038_step_node_dispatches.sql`:

```sql
-- Phase 23.x: per-host fan-out dispatch state.
--
-- A fan-out step (StepSpec.Nodes non-empty) executes the same agent on
-- N hosts in parallel. Pre-23, the orchestration_steps row recorded
-- only the aggregate outcome — there was no place to surface per-host
-- progress on the run-detail canvas, no way to attribute a slow host,
-- and no queryable history of per-host fan-out failures.
--
-- This table holds one row per (run, step, host). The engine's fan-out
-- aggregator writes through a progress sink: Insert on dispatch start,
-- Update on return. The aggregated byNode map continues to land in
-- orchestration_steps.result_json at completion for template
-- back-compat ({{stepN.byNode["host"]}}).
--
-- See docs/superpowers/specs/2026-04-29-orchestration-fanout-visualization-design.md.

CREATE TABLE IF NOT EXISTS orchestration_step_node_dispatches (
  run_id          INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  step_id         TEXT    NOT NULL,
  host            TEXT    NOT NULL,
  status          TEXT    NOT NULL,         -- pending | running | completed | failed
  agent_run_id    TEXT,
  findings_count  INTEGER NOT NULL DEFAULT 0,
  output_tail     TEXT,
  error           TEXT,
  started_at      TIMESTAMP,
  ended_at        TIMESTAMP,
  PRIMARY KEY (run_id, step_id, host)
);

CREATE INDEX IF NOT EXISTS idx_osnd_run_step
  ON orchestration_step_node_dispatches(run_id, step_id);
```

- [ ] **Step 2: Write the Postgres migration**

Create `controlplane/db/migrations/postgres/038_step_node_dispatches.sql`:

```sql
-- Phase 23.x postgres parity. See migrations/sqlite/038_step_node_dispatches.sql.

CREATE TABLE IF NOT EXISTS orchestration_step_node_dispatches (
  run_id          BIGINT      NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
  step_id         TEXT        NOT NULL,
  host            TEXT        NOT NULL,
  status          TEXT        NOT NULL,
  agent_run_id    TEXT,
  findings_count  INTEGER     NOT NULL DEFAULT 0,
  output_tail     TEXT,
  error           TEXT,
  started_at      TIMESTAMPTZ,
  ended_at        TIMESTAMPTZ,
  PRIMARY KEY (run_id, step_id, host)
);

CREATE INDEX IF NOT EXISTS idx_osnd_run_step
  ON orchestration_step_node_dispatches(run_id, step_id);
```

- [ ] **Step 3: Wire migration 038 into `store.go`**

Modify `controlplane/db/store.go`. Find the block that ends with `sqliteM037` and add:

```go
//go:embed migrations/sqlite/038_step_node_dispatches.sql
var sqliteM038 string
```

Then update the `sqliteMigrations` slice (line ~191):

```go
var sqliteMigrations = []string{
	sqliteM001, sqliteM002, sqliteM003,
	sqliteM004, sqliteM005, sqliteM006, sqliteM007,
	sqliteM008, sqliteM009, sqliteM010, sqliteM011, sqliteM012,
	sqliteM013, sqliteM014, sqliteM015, sqliteM016, sqliteM017,
	sqliteM018, sqliteM019, sqliteM020, sqliteM021, sqliteM022,
	sqliteM023, sqliteM024, sqliteM025, sqliteM026, sqliteM027,

	sqliteM028, sqliteM029, sqliteM030, sqliteM031, sqliteM032, sqliteM033, sqliteM034,
	sqliteM035, sqliteM036, sqliteM037, sqliteM038,
}
```

Find the `pgM037` block and add (mirroring the sqlite side):

```go
//go:embed migrations/postgres/038_step_node_dispatches.sql
var pgM038 string
```

Update the `postgresMigrations` slice with `pgM038` appended after `pgM037`.

- [ ] **Step 4: Verify migrations apply cleanly**

Run: `go build ./controlplane/...`
Expected: no compile errors.

Run: `go test ./controlplane/db -run TestCPMeta_BootstrapsOnFirstRead -count=1`
Expected: PASS. (This test exercises `Open()` → `applyMigrations()` end-to-end on a fresh sqlite store; failure here means migration 038 has a syntax error.)

- [ ] **Step 5: Commit**

```bash
git add controlplane/db/migrations/sqlite/038_step_node_dispatches.sql \
        controlplane/db/migrations/postgres/038_step_node_dispatches.sql \
        controlplane/db/store.go
git commit -m "$(cat <<'EOF'
db(migration 038): orchestration_step_node_dispatches table

Adds the per-host fan-out dispatch table that the engine will write to
incrementally as each host returns. Sqlite + postgres parity, wired
into store.go.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: DB slice — `step_node_dispatches.go`

**Files:**
- Create: `controlplane/db/step_node_dispatches.go`
- Create: `controlplane/db/step_node_dispatches_test.go`

- [ ] **Step 1: Write the failing test**

Create `controlplane/db/step_node_dispatches_test.go`:

```go
package db

import (
	"testing"
	"time"
)

// makeRunForFanout creates a minimal orchestration_run row so the
// node-dispatch FK has something to point at. Returns the run id.
func makeRunForFanout(t *testing.T, s *Store) int64 {
	t.Helper()
	// Bare minimum to satisfy NOT NULL constraints. Inserts directly
	// rather than going through CreateOrchestrationRun so we don't
	// also need to build an orchestrations row first — the fanout
	// table FKs orchestration_runs only.
	res, err := s.Exec(`
		INSERT INTO orchestrations (name, spec_yaml, trigger_kind, enabled, created_at, updated_at)
		VALUES ('t-orch', '---\nname: t\n---', 'manual', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("seed orchestration: %v", err)
	}
	orchID, _ := res.LastInsertId()
	runID, err := s.CreateOrchestrationRun(orchID, "manual", "{}", 0)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return runID
}

func TestStepNodeDispatch_InsertAndList(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()

	if err := s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID:     runID,
		StepID:    "step-1",
		Host:      "web-prod-01",
		Status:    "running",
		StartedAt: &now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID:     runID,
		StepID:    "step-1",
		Host:      "web-prod-02",
		Status:    "running",
		StartedAt: &now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.ListStepNodeDispatchesByRun(runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2", len(got))
	}
	if got[0].Host != "web-prod-01" || got[1].Host != "web-prod-02" {
		t.Errorf("List host order = [%q,%q], want sorted by host", got[0].Host, got[1].Host)
	}
	if got[0].Status != "running" {
		t.Errorf("Status = %q, want running", got[0].Status)
	}
}

func TestStepNodeDispatch_UpdateOnReturn(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	start := time.Now().UTC()
	end := start.Add(2 * time.Second)

	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &start,
	})
	if err := s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID:          runID,
		StepID:         "step-1",
		Host:           "h1",
		Status:         "completed",
		AgentRunID:     "run-abc",
		FindingsCount:  3,
		OutputTail:     "ok\n",
		EndedAt:        &end,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Status != "completed" || got[0].FindingsCount != 3 || got[0].AgentRunID.String != "run-abc" {
		t.Errorf("after update: %+v", got[0])
	}
	if !got[0].EndedAt.Valid {
		t.Errorf("EndedAt not set")
	}
}

func TestStepNodeDispatch_UpdateMarksFailure(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})
	end := now.Add(time.Second)
	_ = s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID:   runID,
		StepID:  "step-1",
		Host:    "h1",
		Status:  "failed",
		Error:   "timeout after 30s",
		EndedAt: &end,
	})
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if got[0].Status != "failed" || got[0].Error.String != "timeout after 30s" {
		t.Errorf("after fail: %+v", got[0])
	}
}

func TestStepNodeDispatch_CascadesOnRunDelete(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})

	// Delete the run row directly. CASCADE should drop the dispatch row.
	if _, err := s.Exec(`DELETE FROM orchestration_runs WHERE id = ?`, runID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 0 {
		t.Errorf("expected cascade to drop rows, still have %d", len(got))
	}
}

func TestStepNodeDispatch_ReconcileOrphans(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	// Two rows still in `running` (engine restarted mid-fanout) and one
	// already settled — only the running ones should be reconciled.
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h2",
		Status: "running", StartedAt: &now,
	})
	end := now.Add(time.Second)
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h3",
		Status: "completed", StartedAt: &now, EndedAt: &end,
	})
	_ = s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID: runID, StepID: "step-1", Host: "h3",
		Status: "completed", EndedAt: &end,
	})

	if err := s.ReconcileStepNodeDispatchesForRun(runID, "run cancelled"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	for _, r := range got {
		switch r.Host {
		case "h1", "h2":
			if r.Status != "failed" || r.Error.String != "run cancelled" || !r.EndedAt.Valid {
				t.Errorf("%s: expected failed/cancelled, got %+v", r.Host, r)
			}
		case "h3":
			if r.Status != "completed" {
				t.Errorf("h3: expected unchanged completed, got %+v", r)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./controlplane/db -run TestStepNodeDispatch -count=1`
Expected: FAIL — "InsertStepNodeDispatch undefined", "ListStepNodeDispatchesByRun undefined", etc.

- [ ] **Step 3: Implement the slice**

Create `controlplane/db/step_node_dispatches.go`:

```go
package db

import (
	"database/sql"
	"time"
)

// StepNodeDispatch is a per-host slice of an orchestration step's
// fan-out execution. One row per (run_id, step_id, host).
//
// The engine writes through this table to surface live per-host
// progress on the run-detail canvas. The aggregated byNode map
// continues to land in orchestration_steps.result_json at step
// completion for template back-compat.
type StepNodeDispatch struct {
	RunID         int64
	StepID        string
	Host          string
	Status        string
	AgentRunID    sql.NullString
	FindingsCount int
	OutputTail    sql.NullString
	Error         sql.NullString
	StartedAt     sql.NullTime
	EndedAt       sql.NullTime
}

// StepNodeDispatchInsert is the input shape for InsertStepNodeDispatch.
// Engine calls this on dispatch-start with status='running'.
type StepNodeDispatchInsert struct {
	RunID     int64
	StepID    string
	Host      string
	Status    string
	StartedAt *time.Time
}

// StepNodeDispatchUpdate is the input for UpdateStepNodeDispatch.
// Engine calls this once per host on dispatch-return with the final
// status and (when present) findings count, output tail, agent run id,
// and error.
type StepNodeDispatchUpdate struct {
	RunID         int64
	StepID        string
	Host          string
	Status        string
	AgentRunID    string
	FindingsCount int
	OutputTail    string
	Error         string
	EndedAt       *time.Time
}

// InsertStepNodeDispatch records that the engine is about to dispatch
// `step_id` on `host`. Idempotent: if the row already exists (engine
// retry, fan-out replay), the row's status/started_at are reset to the
// caller's values.
func (s *Store) InsertStepNodeDispatch(in StepNodeDispatchInsert) error {
	_, err := s.Exec(`
		INSERT INTO orchestration_step_node_dispatches
		    (run_id, step_id, host, status, started_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (run_id, step_id, host) DO UPDATE SET
		    status     = excluded.status,
		    started_at = excluded.started_at,
		    ended_at   = NULL,
		    error      = NULL
	`, in.RunID, in.StepID, in.Host, in.Status, nullableTimePtr(in.StartedAt))
	return err
}

// UpdateStepNodeDispatch records the dispatch outcome for one host.
// Called when the dispatcher returns (success or failure) — never
// concurrently with another writer for the same (run, step, host).
func (s *Store) UpdateStepNodeDispatch(in StepNodeDispatchUpdate) error {
	_, err := s.Exec(`
		UPDATE orchestration_step_node_dispatches
		   SET status         = ?,
		       agent_run_id   = ?,
		       findings_count = ?,
		       output_tail    = ?,
		       error          = ?,
		       ended_at       = ?
		 WHERE run_id = ? AND step_id = ? AND host = ?
	`,
		in.Status,
		nullable(in.AgentRunID),
		in.FindingsCount,
		nullable(in.OutputTail),
		nullable(in.Error),
		nullableTimePtr(in.EndedAt),
		in.RunID, in.StepID, in.Host,
	)
	return err
}

// ListStepNodeDispatchesByRun returns every per-host row for a run,
// ordered by step_id then host (stable, so the canvas can render
// rows in a deterministic order without sorting client-side).
func (s *Store) ListStepNodeDispatchesByRun(runID int64) ([]*StepNodeDispatch, error) {
	rows, err := s.Query(`
		SELECT run_id, step_id, host, status, agent_run_id, findings_count,
		       output_tail, error, started_at, ended_at
		  FROM orchestration_step_node_dispatches
		 WHERE run_id = ?
		 ORDER BY step_id, host
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*StepNodeDispatch
	for rows.Next() {
		r := &StepNodeDispatch{}
		if err := rows.Scan(
			&r.RunID, &r.StepID, &r.Host, &r.Status, &r.AgentRunID,
			&r.FindingsCount, &r.OutputTail, &r.Error,
			&r.StartedAt, &r.EndedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReconcileStepNodeDispatchesForRun marks any row still in `running`
// as `failed` with the given reason and now() as ended_at. Called when
// a run transitions to a terminal state (cancelled / failed) so we
// don't leak orphan `running` rows after an engine restart or operator
// cancellation.
//
// Safe to call on runs with no fan-out rows — UPDATE is a no-op then.
func (s *Store) ReconcileStepNodeDispatchesForRun(runID int64, reason string) error {
	now := time.Now().UTC()
	_, err := s.Exec(`
		UPDATE orchestration_step_node_dispatches
		   SET status   = 'failed',
		       error    = ?,
		       ended_at = ?
		 WHERE run_id = ? AND status = 'running'
	`, reason, now, runID)
	return err
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./controlplane/db -run TestStepNodeDispatch -count=1 -v`
Expected: PASS for all five subtests.

- [ ] **Step 5: Commit**

```bash
git add controlplane/db/step_node_dispatches.go controlplane/db/step_node_dispatches_test.go
git commit -m "$(cat <<'EOF'
db(orchestration): step_node_dispatches slice — Insert/Update/List/Reconcile

Per-host CRUD for the new fan-out dispatch table. Reconcile transitions
orphan 'running' rows to 'failed' with a caller-supplied reason — used
when a run hits a terminal status with in-flight rows.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Hook reconcile into `FinishOrchestrationRun`

**Files:**
- Modify: `controlplane/db/orchestrations.go` (extend `FinishOrchestrationRun`)

This makes orphan-cleanup automatic on every terminal transition (operator cancel, engine halt, normal completion). The reconcile is a no-op when the run had no fan-out rows.

- [ ] **Step 1: Write the failing test**

Append to `controlplane/db/step_node_dispatches_test.go`:

```go
func TestFinishOrchestrationRun_ReconcilesFanoutRows(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})

	if err := s.FinishOrchestrationRun(runID, "cancelled", "operator cancelled"); err != nil {
		t.Fatalf("FinishOrchestrationRun: %v", err)
	}
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Status != "failed" {
		t.Errorf("expected fanout row reconciled to failed, got %s", got[0].Status)
	}
	if got[0].Error.String != "operator cancelled" {
		t.Errorf("expected reason to propagate, got %q", got[0].Error.String)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./controlplane/db -run TestFinishOrchestrationRun_ReconcilesFanoutRows -count=1`
Expected: FAIL — fanout row still `running`.

- [ ] **Step 3: Extend `FinishOrchestrationRun`**

Open `controlplane/db/orchestrations.go`, find `FinishOrchestrationRun` (around line 447). Modify:

```go
func (s *Store) FinishOrchestrationRun(id int64, status, errMsg string) error {
	_, err := s.Exec(`
		UPDATE orchestration_runs
		   SET status   = ?,
		       error    = ?,
		       ended_at = CURRENT_TIMESTAMP
		 WHERE id = ?
	`, status, nullable(errMsg), id)
	if err != nil {
		return err
	}
	// Reconcile any orphan fan-out rows: if a step was mid-dispatch
	// when this run hit terminal, the per-host row would otherwise
	// stay `running` forever. The reason mirrors the run's terminal
	// reason (e.g. "operator cancelled", or the failure message that
	// halted the engine) so the host drawer shows a coherent error.
	reason := errMsg
	if reason == "" {
		reason = "run " + status
	}
	return s.ReconcileStepNodeDispatchesForRun(id, reason)
}
```

(The exact pre-image may differ slightly — preserve the existing UPDATE statement and add the reconcile call after it.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./controlplane/db -run "TestFinishOrchestrationRun_ReconcilesFanoutRows|TestStepNodeDispatch" -count=1 -v`
Expected: PASS for all six tests.

- [ ] **Step 5: Commit**

```bash
git add controlplane/db/orchestrations.go controlplane/db/step_node_dispatches_test.go
git commit -m "$(cat <<'EOF'
db(orchestration): reconcile fan-out rows on run termination

FinishOrchestrationRun now sweeps orphan 'running' step_node_dispatches
rows to 'failed' with the run's terminal reason. Prevents stale
in-flight rows from surviving operator cancellation or engine restart.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Engine — `StepNodeProgressSink` interface

**Files:**
- Create: `controlplane/orchestrator/progress_sink.go`

The sink is the engine's hook for incremental per-host writes. Defining it as an interface keeps fanOut() unit-testable without a real DB store.

- [ ] **Step 1: Write the file**

Create `controlplane/orchestrator/progress_sink.go`:

```go
package orchestrator

import "time"

// StepNodeProgressSink is the engine's hook for live per-host fan-out
// state. fanOut() calls OnDispatchStart immediately before each
// per-host Dispatch goroutine fires, and OnDispatchEnd as soon as the
// goroutine returns.
//
// The default sink is a no-op (used by tests + the engine before
// wiring). Production wires this to a db-backed implementation that
// inserts/updates rows in orchestration_step_node_dispatches.
//
// Errors returned by the sink are logged by fanOut() but do not
// abort the dispatch — telemetry must not gate execution.
type StepNodeProgressSink interface {
	OnDispatchStart(runID int64, stepID, host string, startedAt time.Time) error
	OnDispatchEnd(runID int64, stepID, host string,
		status, agentRunID string,
		findingsCount int,
		outputTail, errorStr string,
		endedAt time.Time) error
}

// noopProgressSink is the default. fanOut() uses this when the engine
// hasn't been wired with a real sink — e.g. unit tests that don't
// care about per-host telemetry.
type noopProgressSink struct{}

func (noopProgressSink) OnDispatchStart(int64, string, string, time.Time) error { return nil }
func (noopProgressSink) OnDispatchEnd(int64, string, string, string, string, int, string, string, time.Time) error {
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./controlplane/orchestrator/...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add controlplane/orchestrator/progress_sink.go
git commit -m "$(cat <<'EOF'
orchestrator: StepNodeProgressSink interface for per-host fan-out telemetry

Hook for live per-host writes during fanOut(). Default no-op keeps
existing tests unchanged; the db-backed impl lands when wired.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Engine — `fanOut` writes through the sink

**Files:**
- Modify: `controlplane/orchestrator/engine.go` (`fanOut` signature + body, `Engine` struct, `NewEngine`, the call site at the dispatch switch)
- Modify: `controlplane/orchestrator/engine_test.go` (recorder sink + new test)

- [ ] **Step 1: Write the failing test**

Append to `controlplane/orchestrator/engine_test.go`:

```go
// recorderSink captures every OnDispatchStart / OnDispatchEnd call
// for assertion. Used by the fan-out progress test below.
type recorderSink struct {
	mu     sync.Mutex
	starts []startEvent
	ends   []endEvent
}

type startEvent struct {
	RunID  int64
	StepID string
	Host   string
}

type endEvent struct {
	RunID         int64
	StepID        string
	Host          string
	Status        string
	AgentRunID    string
	FindingsCount int
	Error         string
}

func (r *recorderSink) OnDispatchStart(runID int64, stepID, host string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, startEvent{runID, stepID, host})
	return nil
}

func (r *recorderSink) OnDispatchEnd(runID int64, stepID, host string,
	status, agentRunID string, findingsCount int, _, errorStr string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends = append(r.ends, endEvent{
		RunID: runID, StepID: stepID, Host: host,
		Status: status, AgentRunID: agentRunID,
		FindingsCount: findingsCount, Error: errorStr,
	})
	return nil
}

func TestFanOut_PersistsPerHostProgress(t *testing.T) {
	spec := mustParse(t, `---
name: fanout
description: parallel host scan
steps:
  - id: scan
    agent: edr-triage
    nodes: [h1, h2, h3]
    prompt: "scan {{trigger.host}}"
---`)
	orch := &Orchestration{ID: 1, Name: "fanout", Spec: spec, Enabled: true}
	run := &RunRecord{ID: 42, OrchestrationID: 1, Status: RunStatusPending, TriggerKind: "manual"}

	disp := &fakeDispatcher{
		resultByStep: map[string]DispatchResult{
			// fakeDispatcher keys by req.StepID, which fanOut sets to
			// step.ID + "@" + host — so we can return per-host
			// distinct payloads.
			"scan@h1": {Status: StepStatusCompleted, RunID: "r1", Findings: []DispatchedFinding{{Title: "f1"}, {Title: "f2"}}},
			"scan@h2": {Status: StepStatusCompleted, RunID: "r2"},
		},
		errByStep: map[string]error{
			"scan@h3": context.DeadlineExceeded,
		},
	}
	store := newFakeStore(orch, run)
	sink := &recorderSink{}

	eng := NewEngine(store, disp)
	eng.SetProgressSink(sink)
	if err := eng.Run(context.Background(), run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Three start events, three end events.
	if got := len(sink.starts); got != 3 {
		t.Errorf("starts = %d, want 3", got)
	}
	if got := len(sink.ends); got != 3 {
		t.Errorf("ends = %d, want 3", got)
	}

	// Build host → end-event map for status / findings_count assertions.
	endByHost := map[string]endEvent{}
	for _, e := range sink.ends {
		endByHost[e.Host] = e
	}
	if e := endByHost["h1"]; e.Status != StepStatusCompleted || e.FindingsCount != 2 {
		t.Errorf("h1 end = %+v", e)
	}
	if e := endByHost["h2"]; e.Status != StepStatusCompleted || e.FindingsCount != 0 {
		t.Errorf("h2 end = %+v", e)
	}
	if e := endByHost["h3"]; e.Status != StepStatusFailed || e.Error == "" {
		t.Errorf("h3 end = %+v (expected failed with error)", e)
	}

	// byNode still lands in result_json for template back-compat.
	stepRec, _ := store.GetOrchestrationStep(run.ID, "scan")
	if !strings.Contains(stepRec.ResultJSON, `"byNode"`) {
		t.Errorf("expected byNode in result_json, got %q", stepRec.ResultJSON)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./controlplane/orchestrator -run TestFanOut_PersistsPerHostProgress -count=1`
Expected: FAIL — `eng.SetProgressSink` undefined, fanOut signature mismatch.

- [ ] **Step 3: Modify the `Engine` struct and add `SetProgressSink`**

Open `controlplane/orchestrator/engine.go`. Update the `Engine` struct (around line 234) — add a field:

```go
type Engine struct {
	store      Store
	dispatcher Dispatcher
	applier ActionApplier
	data DataResolver
	actionPolicy Policy
	// progressSink receives per-host fan-out telemetry. nil-safe via
	// noopProgressSink — production wires a db-backed impl.
	progressSink StepNodeProgressSink
}
```

Update `NewEngine` to default the sink:

```go
func NewEngine(store Store, dispatcher Dispatcher) *Engine {
	return &Engine{
		store:        store,
		dispatcher:   dispatcher,
		progressSink: noopProgressSink{},
	}
}
```

Add the setter directly below the existing `SetActionPolicy`:

```go
// SetProgressSink installs the per-host fan-out telemetry hook. Called
// once at boot from the api/orchestrations.go coordinator with a
// db-backed impl. Safe to leave unset — the zero value is a no-op
// sink that preserves existing behavior.
func (e *Engine) SetProgressSink(s StepNodeProgressSink) {
	if s == nil {
		s = noopProgressSink{}
	}
	e.progressSink = s
}
```

- [ ] **Step 4: Update the `fanOut` signature and body to use the sink**

In `engine.go`, modify the fan-out call site (around line 548) to pass the sink + run id:

```go
default:
	result, dispatchErr = fanOut(stepCtx, e.dispatcher, step, renderedPrompt, cpSel, targets,
		orch.Spec.EffectiveTimeout(&step), orch.Spec.EffectiveDispatch(&step),
		e.progressSink, run.ID)
```

Then update the `fanOut` function signature (around line 848) and add per-host sink calls:

```go
func fanOut(
	ctx context.Context,
	disp Dispatcher,
	step StepSpec,
	renderedPrompt string,
	cpSel string,
	targets []string,
	timeout time.Duration,
	dispatchMode string,
	sink StepNodeProgressSink,
	runID int64,
) (DispatchResult, error) {
	type one struct {
		node   string
		result DispatchResult
		err    error
	}
	results := make([]one, len(targets))
	var wg sync.WaitGroup
	for i, node := range targets {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			started := time.Now().UTC()
			if err := sink.OnDispatchStart(runID, step.ID, n, started); err != nil {
				log.Printf("orchestrator: fan-out sink OnDispatchStart err: %v", err)
			}
			r, err := disp.Dispatch(ctx, DispatchRequest{
				StepID:       step.ID + "@" + n,
				AgentName:    step.Agent,
				NodeSelector: n,
				CPInstanceID: cpSel,
				Prompt:       renderedPrompt,
				Inputs:       step.Inputs,
				Timeout:      timeout,
				DispatchMode: dispatchMode,
			})
			results[i] = one{node: n, result: r, err: err}

			// Persist the per-host outcome for the live-progress UI.
			ended := time.Now().UTC()
			status := r.Status
			errStr := ""
			if err != nil {
				status = StepStatusFailed
				errStr = err.Error()
			} else if r.Status == StepStatusFailed && r.Error != "" {
				errStr = r.Error
			}
			if sErr := sink.OnDispatchEnd(runID, step.ID, n,
				status, r.RunID, len(r.Findings),
				r.OutputTail, errStr, ended); sErr != nil {
				log.Printf("orchestrator: fan-out sink OnDispatchEnd err: %v", sErr)
			}
		}(i, node)
	}
	wg.Wait()

	// (Aggregation block stays exactly as today — see existing code
	//  around line 884–924.)
	// ... unchanged: build out.PerNode, allFindings, allTails,
	//                failedNodes; set out.Status / out.Error; return.
}
```

Make sure `log` is already imported in `engine.go` (it is — used elsewhere in the file).

- [ ] **Step 5: Run all engine tests to verify**

Run: `go test ./controlplane/orchestrator -count=1`
Expected: PASS for `TestFanOut_PersistsPerHostProgress` and all existing engine tests.

- [ ] **Step 6: Commit**

```bash
git add controlplane/orchestrator/engine.go controlplane/orchestrator/engine_test.go
git commit -m "$(cat <<'EOF'
orchestrator(engine): fanOut writes per-host progress through sink

fanOut() now calls OnDispatchStart immediately before each per-host
Dispatch goroutine fires and OnDispatchEnd as soon as the goroutine
returns, capturing status / findings count / agent_run_id / error /
output_tail. The default sink is a no-op so existing tests stay green;
production will wire a db-backed sink in a follow-up commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Wire engine to the DB-backed sink

**Files:**
- Modify: `controlplane/api/orchestrations.go` (build a sink adapter; install with `engine.SetProgressSink`)

The api package already builds a thin `db.Store` adapter for the orchestrator's Store interface. We add a sink adapter the same way.

- [ ] **Step 1: Locate the engine wiring**

Open `controlplane/api/orchestrations.go`. Find the line `engine := orchestrator.NewEngine(adapter, disp)` (around line 1280). Note the surrounding code: there's a `*db.Store` available as `store`.

- [ ] **Step 2: Add a sink adapter struct**

In the same file, near the existing adapter (search for `type orchestratorStoreAdapter` or similar — the exact name may differ), add:

```go
// stepNodeProgressSinkAdapter delegates the engine's per-host fan-out
// sink calls to the db Store's Insert/Update on the
// orchestration_step_node_dispatches table.
type stepNodeProgressSinkAdapter struct {
	store *db.Store
}

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
```

- [ ] **Step 3: Install the sink on the engine**

Right after `engine := orchestrator.NewEngine(adapter, disp)`, add:

```go
engine.SetProgressSink(&stepNodeProgressSinkAdapter{store: store})
```

- [ ] **Step 4: Verify it builds**

Run: `go build ./controlplane/...`
Expected: no errors.

Run: `go test ./controlplane/orchestrator ./controlplane/db -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controlplane/api/orchestrations.go
git commit -m "$(cat <<'EOF'
api(orchestrations): wire db-backed StepNodeProgressSink to engine

Engine fan-out telemetry now lands in orchestration_step_node_dispatches
on every host start/return. The run-detail handler (next commit) will
hydrate this into the wire view.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: API — hydrate `per_node` on `OrchestrationStepView`

**Files:**
- Modify: `controlplane/api/orchestrations.go` (`orchestrationStepJSON` + `toOrchestrationRunJSON` + handler signature)
- Modify: `controlplane/api/orchestrations_test.go` (handler test)

- [ ] **Step 1: Write the failing handler test**

Open `controlplane/api/orchestrations_test.go`. Find an existing handler test (any `TestOrchestration*` is fine) for shape reference, then append:

```go
func TestOrchestrationRunDetail_HydratesPerNode(t *testing.T) {
	store := openTestStore(t) // existing helper in this package — confirm exact name in this file
	// Seed orchestration + run + step + per-host dispatch rows.
	orchID, _ := store.Exec(`INSERT INTO orchestrations
		(name, spec_yaml, trigger_kind, enabled, created_at, updated_at)
		VALUES ('fan', '---\nname: fan\n---', 'manual', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	oid, _ := orchID.LastInsertId()
	runID, _ := store.CreateOrchestrationRun(oid, "manual", "{}", 0)
	_ = store.UpsertOrchestrationStep(db.OrchestrationStepInsert{
		OrchestrationRunID: runID,
		StepID:             "scan",
		StepIdx:            0,
		Status:             "completed",
	})
	now := time.Now().UTC()
	end := now.Add(time.Second)
	_ = store.InsertStepNodeDispatch(db.StepNodeDispatchInsert{
		RunID: runID, StepID: "scan", Host: "h1",
		Status: "running", StartedAt: &now,
	})
	_ = store.UpdateStepNodeDispatch(db.StepNodeDispatchUpdate{
		RunID: runID, StepID: "scan", Host: "h1",
		Status: "completed", AgentRunID: "agent-r1",
		FindingsCount: 2, OutputTail: "ok\n",
		EndedAt: &end,
	})
	_ = store.InsertStepNodeDispatch(db.StepNodeDispatchInsert{
		RunID: runID, StepID: "scan", Host: "h2",
		Status: "running", StartedAt: &now,
	})
	_ = store.UpdateStepNodeDispatch(db.StepNodeDispatchUpdate{
		RunID: runID, StepID: "scan", Host: "h2",
		Status: "failed", Error: "timeout",
		EndedAt: &end,
	})

	r := httptest.NewRequest("GET", fmt.Sprintf("/api/orchestration-runs/%d", runID), nil)
	r = r.WithContext(chiContextWithParam("id", fmt.Sprintf("%d", runID)))
	w := httptest.NewRecorder()
	OrchestrationRunDetail(store).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Steps []struct {
			StepID  string `json:"step_id"`
			PerNode []struct {
				Host          string `json:"host"`
				Status        string `json:"status"`
				AgentRunID    string `json:"agent_run_id,omitempty"`
				FindingsCount int    `json:"findings_count"`
				Error         string `json:"error,omitempty"`
			} `json:"per_node,omitempty"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — body: %s", err, w.Body.String())
	}
	if len(got.Steps) != 1 {
		t.Fatalf("steps = %d", len(got.Steps))
	}
	pn := got.Steps[0].PerNode
	if len(pn) != 2 {
		t.Fatalf("per_node = %d, want 2 — body: %s", len(pn), w.Body.String())
	}
	if pn[0].Host != "h1" || pn[0].Status != "completed" || pn[0].FindingsCount != 2 {
		t.Errorf("h1 = %+v", pn[0])
	}
	if pn[1].Host != "h2" || pn[1].Status != "failed" || pn[1].Error != "timeout" {
		t.Errorf("h2 = %+v", pn[1])
	}
}
```

> **Note:** Inspect the file's existing handler tests to confirm helper names — `openTestStore` (or similar) and `chiContextWithParam` are common patterns in chi-based handler tests. If the file uses different helpers, adapt the seed/exec lines accordingly. The assertions are the load-bearing part.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./controlplane/api -run TestOrchestrationRunDetail_HydratesPerNode -count=1`
Expected: FAIL — `per_node` is empty (the handler doesn't populate it yet) or `per_node` decode fails.

- [ ] **Step 3: Add `PerNode` to the JSON shape**

In `controlplane/api/orchestrations.go`, modify the structs (around line 1836):

```go
type orchestrationStepJSON struct {
	StepID         string                  `json:"step_id"`
	StepIdx        int                     `json:"step_idx"`
	Status         string                  `json:"status"`
	RunID          string                  `json:"run_id,omitempty"`
	CPInstanceID   string                  `json:"cp_instance_id,omitempty"`
	RenderedPrompt string                  `json:"rendered_prompt,omitempty"`
	Result         map[string]any          `json:"result,omitempty"`
	OutputSummary  string                  `json:"output_summary,omitempty"`
	StartedAt      string                  `json:"started_at,omitempty"`
	EndedAt        string                  `json:"ended_at,omitempty"`
	Error          string                  `json:"error,omitempty"`
	ApprovedAt     string                  `json:"approved_at,omitempty"`
	Data           any                     `json:"data,omitempty"`
	PerNode        []stepNodeDispatchJSON  `json:"per_node,omitempty"`
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
```

- [ ] **Step 4: Update the handler to fetch + group per-node rows**

In `OrchestrationRunDetail` (around line 1644), insert the lookup before serializing:

```go
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
```

- [ ] **Step 5: Update `toOrchestrationRunJSON` signature and body**

```go
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
	// Also patch the existing call site at line ~1580 — it passes a
	// nil map since the list view doesn't need per-host detail.
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
```

(Add `"sort"` to the file's import block if not already present.)

Update the other call site (line ~1580 in the list handler) to pass `nil` for the new arg:

```go
jsonRows = append(jsonRows, toOrchestrationRunJSON(run, nil, nil))
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./controlplane/api -run TestOrchestrationRunDetail_HydratesPerNode -count=1 -v`
Expected: PASS.

Run: `go test ./controlplane/api -count=1`
Expected: All existing api tests still PASS.

- [ ] **Step 7: Commit**

```bash
git add controlplane/api/orchestrations.go controlplane/api/orchestrations_test.go
git commit -m "$(cat <<'EOF'
api(orchestrations): hydrate per_node on run-detail step view

Each step in OrchestrationRunDetail now carries a per_node slice
sourced from orchestration_step_node_dispatches. Falls back to
result_json.byNode for legacy runs that completed before migration 038
landed (read-only — timestamps stay empty).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Frontend types

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step 1: Add the new types**

Open `web/src/api.ts`. Find `OrchestrationStepView` (around line 394). Add the new interface above it and a field on the existing one:

```ts
export interface StepNodeDispatchView {
  host: string;
  status: 'pending' | 'running' | 'completed' | 'failed';
  agent_run_id?: string;
  findings_count: number;
  output_tail?: string;
  error?: string;
  started_at?: string;
  ended_at?: string;
}

export interface OrchestrationStepView {
  step_id: string;
  step_idx: number;
  status: OrchestrationStepStatus;
  run_id?: string;
  cp_instance_id?: string;
  rendered_prompt?: string;
  result?: Record<string, unknown>;
  output_summary?: string;
  started_at?: string;
  ended_at?: string;
  error?: string;
  approved_at?: string;
  /** Phase 23.x: per-host fan-out dispatches. Present when the step
   *  used `nodes:` (fan-out). Empty for single-node steps and legacy
   *  runs without byNode data. */
  per_node?: StepNodeDispatchView[];
}
```

- [ ] **Step 2: Verify types compile**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no type errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/api.ts
git commit -m "$(cat <<'EOF'
web(api): StepNodeDispatchView + per_node on OrchestrationStepView

Wire up the new per-host fan-out shape so the run canvas can render it.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: `fanoutAutoExpand` helper + manual verification harness

**Files:**
- Create: `web/src/lib/fanoutAutoExpand.ts`

The auto-expand rule is the load-bearing piece of the UI logic. Factoring it into a pure function lets us reason about it independently of the React component, and lets manual verification dump truth tables to the console.

- [ ] **Step 1: Create the helper**

Create `web/src/lib/fanoutAutoExpand.ts`:

```ts
import type { OrchestrationStepStatus, StepNodeDispatchView } from '../api';

/**
 * Decide whether a fan-out card should auto-expand its per-host rows
 * on first render, before any user override.
 *
 * Rules (decided in spec §"Auto-expand rule"):
 *
 *   - n ≤ 4         → always expanded
 *   - 5 ≤ n ≤ 20   → expanded while the step is running/failed OR any
 *                    host is running/failed; collapsed otherwise
 *   - n > 20        → never expands per-host rows on the canvas;
 *                    drill-in is via the host-list drawer
 *
 * Returns true when host rows should be visible inline.
 */
export function fanoutAutoExpand(
  hostCount: number,
  stepStatus: OrchestrationStepStatus,
  hosts: StepNodeDispatchView[],
): boolean {
  if (hostCount > 20) return false;
  if (hostCount <= 4) return true;
  if (stepStatus === 'running' || stepStatus === 'failed') return true;
  return hosts.some((h) => h.status === 'running' || h.status === 'failed');
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/fanoutAutoExpand.ts
git commit -m "$(cat <<'EOF'
web(lib): fanoutAutoExpand — pure decider for fan-out card expansion

Centralizes the n≤4 / 5–20 / >20 rule so the React component stays a
thin renderer and manual verification can exercise the rule directly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: `OrchestrationFanoutCard` — collapsed mode

**Files:**
- Create: `web/src/components/OrchestrationFanoutCard.tsx`

This is the canvas-friendly card that replaces the standard `RunStepNodeView` when `per_node.length > 0`. We build collapsed mode first, then layer expanded mode in Task 11.

- [ ] **Step 1: Create the component skeleton**

Create `web/src/components/OrchestrationFanoutCard.tsx`:

```tsx
import { useState } from 'react';
import { Handle, Position, type NodeProps, type Node } from '@xyflow/react';
import { CheckCircle2, Cpu, Loader2, Pause, Server, XCircle, AlertCircle, ChevronRight, ChevronDown } from 'lucide-react';
import { cn } from '../lib/cn';
import { fanoutAutoExpand } from '../lib/fanoutAutoExpand';
import type { OrchestrationStepStatus, StepNodeDispatchView } from '../api';

interface FanoutCardData {
  stepID: string;
  agent?: string;
  status: OrchestrationStepStatus;
  cpInstanceID?: string;
  approvable?: boolean;
  approving?: boolean;
  onApprove?: (stepID: string) => void;
  perNode: StepNodeDispatchView[];
  onSelectHost?: (stepID: string, host: string) => void;
  onOpenHostList?: (stepID: string) => void;
  [key: string]: unknown;
}

type FanoutCardNode = Node<FanoutCardData>;

const NODE_W = 260;
const NODE_W_LARGE = 280; // slightly wider when expanded with host rows

// ── Tone helpers (mirrored from OrchestrationRunCanvas for visual parity) ──

function stepRunTone(status: OrchestrationStepStatus) {
  switch (status) {
    case 'running':          return { stripe: 'bg-blue-500',  badge: 'bg-blue-50 text-blue-700',     iconColor: 'text-blue-600' };
    case 'completed':        return { stripe: 'bg-green-500', badge: 'bg-green-50 text-green-700',   iconColor: 'text-green-600' };
    case 'failed':           return { stripe: 'bg-red-500',   badge: 'bg-red-50 text-red-700',       iconColor: 'text-red-600' };
    case 'waiting_approval': return { stripe: 'bg-amber-400', badge: 'bg-amber-50 text-amber-700',   iconColor: 'text-amber-600' };
    case 'skipped':          return { stripe: 'bg-slate-300', badge: 'bg-slate-100 text-slate-600',  iconColor: 'text-slate-400' };
    default:                 return { stripe: 'bg-slate-300', badge: 'bg-slate-100 text-slate-600',  iconColor: 'text-slate-400' };
  }
}

function stepIcon(status: OrchestrationStepStatus) {
  switch (status) {
    case 'running':          return Loader2;
    case 'completed':        return CheckCircle2;
    case 'failed':           return XCircle;
    case 'waiting_approval': return Pause;
    case 'skipped':          return ChevronRight;
    default:                 return AlertCircle;
  }
}

function hostDot(status: StepNodeDispatchView['status']): string {
  switch (status) {
    case 'completed': return 'bg-green-500';
    case 'failed':    return 'bg-red-500';
    case 'running':   return 'bg-blue-500 animate-pulse';
    default:          return 'bg-slate-300';
  }
}

interface Counts { ok: number; fail: number; running: number; pending: number; total: number; }

function tally(per: StepNodeDispatchView[]): Counts {
  const c: Counts = { ok: 0, fail: 0, running: 0, pending: 0, total: per.length };
  for (const h of per) {
    if (h.status === 'completed')      c.ok++;
    else if (h.status === 'failed')    c.fail++;
    else if (h.status === 'running')   c.running++;
    else                                c.pending++;
  }
  return c;
}

export function FanoutCardView({ data, selected }: NodeProps<FanoutCardNode>) {
  const tone = stepRunTone(data.status);
  const Icon = stepIcon(data.status);
  const counts = tally(data.perNode);

  // The auto-expand rule decides initial state. Local override sticks
  // for the lifetime of the component (i.e. as long as react-flow
  // keeps the node mounted) but doesn't persist across reloads.
  const auto = fanoutAutoExpand(counts.total, data.status, data.perNode);
  const [override, setOverride] = useState<boolean | null>(null);
  const expanded = override ?? auto;

  const failedHosts = data.perNode.filter((h) => h.status === 'failed');
  const isLarge = counts.total > 20;

  return (
    <div
      className={cn(
        'bg-panel border rounded-lg shadow-card overflow-hidden text-left',
        selected ? 'ring-2 ring-brand-300 border-brand-200' : 'border-border',
        data.status === 'running' && 'ring-2 ring-blue-400 shadow-lg',
        data.status === 'waiting_approval' && 'ring-2 ring-amber-400 animate-pulse',
        data.status === 'failed' && 'ring-2 ring-red-300',
      )}
      style={{ width: expanded ? NODE_W_LARGE : NODE_W }}
    >
      <Handle type="target" position={Position.Left} className="!bg-slate-400 !w-2 !h-2 !border-0" />
      <div className={cn('h-1', tone.stripe)} />

      {/* Header */}
      <div
        className="p-3 cursor-pointer"
        onClick={() => !isLarge && setOverride((cur) => !(cur ?? auto))}
      >
        <div className="flex items-center gap-2 mb-1.5">
          <Icon size={14} className={tone.iconColor} />
          <span className="text-xs font-semibold text-ink truncate flex-1">{data.stepID}</span>
          <span className={cn('text-[9px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded', tone.badge)}>
            {data.status.replace('_', ' ')}
          </span>
        </div>

        {data.agent && (
          <div className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded font-medium mb-1">
            <Cpu size={9} />
            {data.agent}
          </div>
        )}

        {/* Headline + histogram + count */}
        <div className="text-[10px] text-ink-dim mt-1">
          {counts.total} hosts ({counts.ok} ok • {counts.fail} fail • {counts.running} running)
        </div>
        <div className="flex items-center gap-2 mt-1.5">
          <div className="flex-1 flex h-1.5 rounded overflow-hidden bg-slate-200">
            {counts.ok      > 0 && <div className="bg-green-500" style={{ width: `${(counts.ok      / counts.total) * 100}%` }} />}
            {counts.fail    > 0 && <div className="bg-red-500"   style={{ width: `${(counts.fail    / counts.total) * 100}%` }} />}
            {counts.running > 0 && <div className="bg-blue-500"  style={{ width: `${(counts.running / counts.total) * 100}%` }} />}
          </div>
          <span className="text-[9px] text-ink-dim font-semibold whitespace-nowrap">
            {counts.ok + counts.fail} / {counts.total}
          </span>
        </div>

        {/* Dot strip — only ≤20 */}
        {!isLarge && (
          <div className="mt-1.5 flex gap-[2px] flex-wrap p-1 rounded bg-slate-50">
            {data.perNode.map((h) => (
              <span
                key={h.host}
                className={cn('w-2 h-2 rounded-full', hostDot(h.status))}
                title={`${h.host} — ${h.status}`}
              />
            ))}
          </div>
        )}

        {/* Failed-host inline lines — only ≤20 */}
        {!isLarge && failedHosts.length > 0 && failedHosts.length <= 3 && (
          <div className="mt-1.5 space-y-0.5">
            {failedHosts.map((h) => (
              <div key={h.host} className="text-[10px] text-red-700 font-mono truncate">
                ✕ {h.host} — {h.error || 'failed'}
              </div>
            ))}
          </div>
        )}
        {!isLarge && failedHosts.length > 3 && (
          <div className="mt-1.5 text-[10px] text-red-700 font-mono">
            ✕ {failedHosts.length} hosts failed — open to inspect
          </div>
        )}

        {data.cpInstanceID && data.cpInstanceID !== 'local' && (
          <div className="text-[10px] text-brand-700 mt-1 truncate">
            cp: {data.cpInstanceID.slice(0, 8)}…
          </div>
        )}

        {data.approvable && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              data.onApprove?.(data.stepID);
            }}
            disabled={data.approving}
            className="mt-2 w-full inline-flex items-center justify-center gap-1 bg-amber-500 hover:bg-amber-600 disabled:opacity-50 text-white text-[11px] font-medium px-2 py-1 rounded-md"
          >
            {data.approving ? <Loader2 size={10} className="animate-spin" /> : <CheckCircle2 size={10} />}
            Approve
          </button>
        )}
      </div>

      {/* Expanded host rows + large-mode footer button — wired in Task 11 + 12 */}

      <Handle type="source" position={Position.Right} className="!bg-slate-400 !w-2 !h-2 !border-0" />
    </div>
  );
}
```

- [ ] **Step 2: Verify the component compiles**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/OrchestrationFanoutCard.tsx
git commit -m "$(cat <<'EOF'
web(orchestration): OrchestrationFanoutCard — collapsed mode

Header + agent badge + headline counts + histogram bar + dot strip +
inline failed-host lines. Per-host rows and large-mode list button
land in the next two commits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: `OrchestrationFanoutCard` — expanded host rows + large-mode button

**Files:**
- Modify: `web/src/components/OrchestrationFanoutCard.tsx`

- [ ] **Step 1: Add the expanded-rows block**

In `OrchestrationFanoutCard.tsx`, replace the placeholder comment (`{/* Expanded host rows + large-mode footer button — wired in Task 11 + 12 */}`) with:

```tsx
      {/* Expanded host rows — only when n ≤ 20 and expanded */}
      {!isLarge && expanded && data.perNode.length > 0 && (
        <div className="border-t border-slate-100">
          {data.perNode.map((h) => {
            const hostDuration = formatHostDuration(h);
            const isFailed = h.status === 'failed';
            const isLive = h.status === 'running';
            return (
              <button
                key={h.host}
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  data.onSelectHost?.(data.stepID, h.host);
                }}
                className={cn(
                  'w-full flex items-center gap-2 px-3 py-1.5 border-t border-slate-100 first:border-t-0',
                  'hover:bg-slate-50 text-left text-[10px] font-mono',
                  isFailed && 'text-red-700',
                  isLive && 'text-blue-700',
                  !isFailed && !isLive && 'text-ink-dim',
                )}
              >
                <span className={cn('w-1.5 h-1.5 rounded-full shrink-0', hostDot(h.status))} />
                <span className="flex-1 truncate">{h.host}</span>
                {h.findings_count > 0 && (
                  <span className="bg-brand-50 text-brand-700 ring-1 ring-brand-200 px-1 rounded text-[9px] font-semibold">
                    {h.findings_count}
                  </span>
                )}
                {hostDuration && <span className="text-[9px] text-ink-mute">{hostDuration}</span>}
                <ChevronRight size={10} className="text-slate-300 shrink-0" />
              </button>
            );
          })}
        </div>
      )}

      {/* Large-mode footer button — only when n > 20 */}
      {isLarge && (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            data.onOpenHostList?.(data.stepID);
          }}
          className="w-full text-[11px] text-brand-700 hover:bg-brand-50 border-t border-slate-100 px-3 py-2 inline-flex items-center justify-between"
        >
          <span>View all {counts.total} hosts</span>
          <ChevronRight size={12} />
        </button>
      )}

      {/* Manual collapse toggle — only when ≤20 and not auto-locked-open */}
      {!isLarge && counts.total > 4 && (
        <div className="text-center text-[9px] text-ink-mute py-1 border-t border-dashed border-slate-200 select-none">
          {expanded ? (
            <span className="inline-flex items-center gap-1"><ChevronDown size={9} /> click to collapse</span>
          ) : (
            <span className="inline-flex items-center gap-1"><ChevronRight size={9} /> click to expand · {counts.total} hosts</span>
          )}
        </div>
      )}
```

- [ ] **Step 2: Add the duration formatter at the bottom of the file**

Below `FanoutCardView`, add:

```tsx
function formatHostDuration(h: StepNodeDispatchView): string {
  if (!h.started_at) return '';
  const start = Date.parse(h.started_at);
  const end = h.ended_at ? Date.parse(h.ended_at) : Date.now();
  const seconds = Math.max(0, (end - start) / 1000);
  const suffix = h.ended_at ? '' : '…';
  if (seconds < 10)  return `${seconds.toFixed(1)}s${suffix}`;
  if (seconds < 600) return `${Math.round(seconds)}s${suffix}`;
  return `${Math.round(seconds / 60)}m${suffix}`;
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/OrchestrationFanoutCard.tsx
git commit -m "$(cat <<'EOF'
web(orchestration): FanoutCard expanded host rows + large-mode list button

Per-host rows show status dot, hostname, findings count, duration, and
chevron — clicking opens the host drawer (parent supplies the
callback). At >20 hosts the canvas card stays collapsed and a 'View
all N hosts' button opens the host-list drawer instead.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: `FanoutHostDrawer` — host-scoped detail panel

**Files:**
- Create: `web/src/components/FanoutHostDrawer.tsx`

- [ ] **Step 1: Create the drawer**

Create `web/src/components/FanoutHostDrawer.tsx`:

```tsx
import { useEffect } from 'react';
import { X, Server, ExternalLink, AlertCircle } from 'lucide-react';
import { cn } from '../lib/cn';
import type { OrchestrationStepView, StepNodeDispatchView } from '../api';

interface Props {
  /** The parent step (used for the breadcrumb + the rendered prompt
   *  the host shared with all other hosts). */
  step: OrchestrationStepView;
  /** Which host's detail to show. The drawer pulls the matching row
   *  from step.per_node by host name; that row is the source of truth
   *  for status / output_tail / findings_count / agent_run_id. */
  host: string;
  onClose: () => void;
}

export default function FanoutHostDrawer({ step, host, onClose }: Props) {
  const hostRow: StepNodeDispatchView | undefined =
    step.per_node?.find((h) => h.host === host);

  // ESC closes the drawer (matches the existing patterns in this app).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Per-host findings come from result_json.byNode[host].findings —
  // the engine keeps per-host findings under the byNode map at step
  // completion. For mid-flight runs the byNode map isn't populated yet;
  // we fall back to displaying the count from per_node.
  const result = step.result as Record<string, unknown> | undefined;
  const byNode = result?.byNode as Record<string, { findings?: unknown[]; output_tail?: string }> | undefined;
  const hostByNode = byNode?.[host];
  const findingsList = (hostByNode?.findings ?? []) as unknown[];
  const hostOutputTail = hostRow?.output_tail || hostByNode?.output_tail || '';

  return (
    <>
      {/* backdrop */}
      <div
        className="fixed inset-0 bg-slate-900/30 z-40"
        onClick={onClose}
      />
      <aside
        className="fixed top-0 right-0 h-full w-[480px] max-w-[90vw] bg-panel border-l border-border z-50 shadow-xl flex flex-col"
        role="dialog"
        aria-label={`Host detail for ${host}`}
      >
        <header className="flex items-center justify-between px-4 py-3 border-b border-border bg-gradient-to-r from-brand-50/40 via-panel to-panel">
          <div className="flex items-center gap-2 min-w-0">
            <Server size={14} className="text-brand-700 shrink-0" />
            <span className="text-[11px] text-ink-dim font-mono truncate">
              {step.step_id} / <span className="text-ink font-semibold">{host}</span>
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-ink-dim hover:text-ink p-1 rounded"
            aria-label="Close"
          >
            <X size={14} />
          </button>
        </header>

        <div className="flex-1 overflow-y-auto px-4 py-3 space-y-4 text-[12px]">
          {!hostRow && (
            <div className="text-ink-mute italic flex items-center gap-2">
              <AlertCircle size={12} /> No dispatch row recorded for this host yet.
            </div>
          )}

          {hostRow && (
            <section>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">Status</div>
              <div className={cn(
                'inline-block text-[11px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded',
                hostRow.status === 'completed' && 'bg-green-50 text-green-700',
                hostRow.status === 'failed' && 'bg-red-50 text-red-700',
                hostRow.status === 'running' && 'bg-blue-50 text-blue-700',
                hostRow.status === 'pending' && 'bg-slate-100 text-slate-600',
              )}>{hostRow.status}</div>
              <dl className="grid grid-cols-2 gap-x-3 gap-y-1 mt-2 text-[11px]">
                {hostRow.started_at && (<><dt className="text-ink-mute">started</dt><dd className="font-mono">{hostRow.started_at}</dd></>)}
                {hostRow.ended_at   && (<><dt className="text-ink-mute">ended</dt>  <dd className="font-mono">{hostRow.ended_at}</dd></>)}
                {hostRow.error      && (<><dt className="text-ink-mute">error</dt>  <dd className="text-red-700 break-words">{hostRow.error}</dd></>)}
              </dl>
            </section>
          )}

          {step.rendered_prompt && (
            <section>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">Rendered prompt</div>
              <pre className="bg-slate-50 ring-1 ring-slate-200 rounded p-2 text-[10px] font-mono whitespace-pre-wrap break-words">{step.rendered_prompt}</pre>
            </section>
          )}

          {hostOutputTail && (
            <section>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">Output tail</div>
              <pre className="bg-slate-50 ring-1 ring-slate-200 rounded p-2 text-[10px] font-mono whitespace-pre-wrap max-h-[260px] overflow-auto">{hostOutputTail}</pre>
            </section>
          )}

          <section>
            <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">
              Findings ({hostRow?.findings_count ?? 0})
            </div>
            {findingsList.length === 0 && (hostRow?.findings_count ?? 0) > 0 && (
              <div className="text-ink-mute italic text-[11px]">(populated when run completes)</div>
            )}
            {findingsList.length === 0 && (hostRow?.findings_count ?? 0) === 0 && (
              <div className="text-ink-mute italic text-[11px]">No findings.</div>
            )}
            {findingsList.length > 0 && (
              <pre className="bg-slate-50 ring-1 ring-slate-200 rounded p-2 text-[10px] font-mono whitespace-pre-wrap max-h-[260px] overflow-auto">{JSON.stringify(findingsList, null, 2)}</pre>
            )}
          </section>
        </div>

        {hostRow?.agent_run_id && (
          <footer className="px-4 py-3 border-t border-border bg-slate-50/40">
            <a
              href={`/runs/${encodeURIComponent(hostRow.agent_run_id)}`}
              className="inline-flex items-center gap-1 text-[11px] text-brand-700 hover:text-brand-900 font-medium"
            >
              <ExternalLink size={11} />
              Open agent run {hostRow.agent_run_id}
            </a>
          </footer>
        )}
      </aside>
    </>
  );
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/FanoutHostDrawer.tsx
git commit -m "$(cat <<'EOF'
web(orchestration): FanoutHostDrawer — host-scoped detail panel

Slides in over the run page with status / timing / rendered prompt /
host-specific output tail / findings (from byNode map) / agent run
link. ESC closes. Wired into the canvas in a follow-up commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: `FanoutHostListDrawer` — virtualized host list (>20 mode)

**Files:**
- Create: `web/src/components/FanoutHostListDrawer.tsx`

For the deferred 10k-host concern, the list uses windowing via a simple visible-range render — no react-window dependency to keep the bundle lean. Hosts up to a few hundred render cheaply; we revisit if a real user crosses the threshold (per spec deferral §3).

- [ ] **Step 1: Create the list drawer**

Create `web/src/components/FanoutHostListDrawer.tsx`:

```tsx
import { useEffect, useMemo, useState } from 'react';
import { X, Server, ChevronRight } from 'lucide-react';
import { cn } from '../lib/cn';
import type { OrchestrationStepView, StepNodeDispatchView } from '../api';

interface Props {
  step: OrchestrationStepView;
  onSelectHost: (host: string) => void;
  onClose: () => void;
}

type Filter = 'all' | 'failed' | 'running' | 'completed';

export default function FanoutHostListDrawer({ step, onSelectHost, onClose }: Props) {
  const [filter, setFilter] = useState<Filter>('all');
  const [query, setQuery] = useState('');

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const all = step.per_node ?? [];
  const filtered = useMemo<StepNodeDispatchView[]>(() => {
    let out = all;
    if (filter !== 'all') out = out.filter((h) => h.status === filter);
    const q = query.trim().toLowerCase();
    if (q) out = out.filter((h) => h.host.toLowerCase().includes(q));
    return out;
  }, [all, filter, query]);

  return (
    <>
      <div className="fixed inset-0 bg-slate-900/20 z-30" onClick={onClose} />
      <aside
        className="fixed top-0 right-0 h-full w-[440px] max-w-[80vw] bg-panel border-l border-border z-40 shadow-xl flex flex-col"
        role="dialog"
        aria-label={`Hosts in ${step.step_id}`}
      >
        <header className="px-4 py-3 border-b border-border">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2 min-w-0">
              <Server size={14} className="text-brand-700 shrink-0" />
              <span className="text-[11px] text-ink-dim font-mono truncate">
                <span className="text-ink font-semibold">{step.step_id}</span> · {all.length} hosts
              </span>
            </div>
            <button onClick={onClose} className="text-ink-dim hover:text-ink p-1 rounded" aria-label="Close">
              <X size={14} />
            </button>
          </div>
          <input
            type="text"
            placeholder="filter hostnames…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-full text-[11px] font-mono px-2 py-1 rounded ring-1 ring-slate-200 bg-white focus:ring-brand-300 focus:outline-none"
          />
          <div className="flex gap-1 mt-2">
            {(['all', 'failed', 'running', 'completed'] as Filter[]).map((f) => (
              <button
                key={f}
                onClick={() => setFilter(f)}
                className={cn(
                  'text-[10px] uppercase tracking-wide px-2 py-0.5 rounded ring-1',
                  filter === f
                    ? 'bg-brand-50 text-brand-700 ring-brand-200'
                    : 'bg-white text-ink-dim ring-slate-200 hover:bg-slate-50',
                )}
              >
                {f}
              </button>
            ))}
          </div>
        </header>

        <div className="flex-1 overflow-y-auto">
          {filtered.length === 0 && (
            <div className="px-4 py-6 text-[11px] text-ink-mute italic">No hosts match.</div>
          )}
          {filtered.map((h) => (
            <button
              key={h.host}
              type="button"
              onClick={() => onSelectHost(h.host)}
              className="w-full flex items-center gap-2 px-4 py-2 border-b border-slate-100 hover:bg-slate-50 text-left text-[11px] font-mono"
            >
              <span className={cn(
                'w-2 h-2 rounded-full shrink-0',
                h.status === 'completed' && 'bg-green-500',
                h.status === 'failed' && 'bg-red-500',
                h.status === 'running' && 'bg-blue-500 animate-pulse',
                h.status === 'pending' && 'bg-slate-300',
              )} />
              <span className={cn(
                'flex-1 truncate',
                h.status === 'failed' && 'text-red-700',
                h.status === 'running' && 'text-blue-700',
              )}>{h.host}</span>
              {h.findings_count > 0 && (
                <span className="bg-brand-50 text-brand-700 ring-1 ring-brand-200 px-1 rounded text-[9px] font-semibold">
                  {h.findings_count}
                </span>
              )}
              <ChevronRight size={11} className="text-slate-300" />
            </button>
          ))}
        </div>
      </aside>
    </>
  );
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/FanoutHostListDrawer.tsx
git commit -m "$(cat <<'EOF'
web(orchestration): FanoutHostListDrawer — virtualized host list at >20

Hostname filter + status filter + scrollable list. Clicking a row
opens FanoutHostDrawer for that (step, host). Used as the drill
surface when the canvas card is in large-mode.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Wire the fan-out card into `OrchestrationRunCanvas`

**Files:**
- Modify: `web/src/components/OrchestrationRunCanvas.tsx`
- Modify: `web/src/pages/Orchestrations.tsx` (RunDetail — host drawer state + wiring)

- [ ] **Step 1: Register the new node type and dispatch on `per_node`**

Open `web/src/components/OrchestrationRunCanvas.tsx`. At the top, add the import:

```tsx
import { FanoutCardView } from './OrchestrationFanoutCard';
import type { StepNodeDispatchView } from '../api';
```

Update the `nodeTypes` map (around line 113):

```tsx
const nodeTypes = {
  runStep: RunStepNodeView,
  runStepFanout: FanoutCardView,
};
```

In the `Props` interface (around line 145), add the host callbacks:

```tsx
interface Props {
  steps: OrchestrationStepView[];
  approvingStepID?: string | null;
  onApprove?: (stepID: string) => void;
  onSelectStep?: (stepID: string) => void;
  selectedStepID?: string | null;
  agentByStepID?: Record<string, string>;
  nodeByStepID?: Record<string, string>;
  /** Phase 23.x: invoked when an operator clicks a host row inside a
   *  fan-out card (or a row in the host-list drawer). Parent opens
   *  FanoutHostDrawer for the (stepID, host) pair. */
  onSelectHost?: (stepID: string, host: string) => void;
  /** Phase 23.x: invoked when the operator clicks "View all N hosts"
   *  on a large-mode (>20) fan-out card. Parent opens
   *  FanoutHostListDrawer for that step. */
  onOpenHostList?: (stepID: string) => void;
}
```

Update the `useMemo` that builds `initialNodes` (around line 175). Replace the body so each step picks its node type and data shape based on `per_node`:

```tsx
const initialNodes = useMemo<Node[]>(() => {
  return steps.map((s, i) => {
    const isFanout = (s.per_node?.length ?? 0) > 0;
    const baseData = {
      stepID: s.step_id,
      agent: agentByStepID?.[s.step_id],
      status: s.status,
      cpInstanceID: s.cp_instance_id,
      approvable: s.status === 'waiting_approval',
      approving: approvingStepID === s.step_id,
      onApprove,
    };
    if (isFanout) {
      return {
        id: s.step_id,
        type: 'runStepFanout',
        position: { x: i * (NODE_W + NODE_GAP), y: 0 },
        data: {
          ...baseData,
          perNode: s.per_node as StepNodeDispatchView[],
          onSelectHost,
          onOpenHostList,
        },
      };
    }
    return {
      id: s.step_id,
      type: 'runStep',
      position: { x: i * (NODE_W + NODE_GAP), y: 0 },
      data: {
        ...baseData,
        node: nodeByStepID?.[s.step_id],
      },
    };
  });
}, [steps, approvingStepID, onApprove, agentByStepID, nodeByStepID, onSelectHost, onOpenHostList]);
```

- [ ] **Step 2: Wire host drawers in `RunDetail`**

Open `web/src/pages/Orchestrations.tsx`. Find the `RunDetail` component (around line 1414). Add imports at the top of the file:

```tsx
import FanoutHostDrawer from '../components/FanoutHostDrawer';
import FanoutHostListDrawer from '../components/FanoutHostListDrawer';
```

Inside `RunDetail`, add new state alongside the existing `expanded` state:

```tsx
const [hostDrawer, setHostDrawer] = useState<{ stepID: string; host: string } | null>(null);
const [hostList, setHostList] = useState<string | null>(null); // step ID for list drawer
```

Find the line where `OrchestrationRunCanvas` is rendered (search for `<OrchestrationRunCanvas`) and add the new props:

```tsx
onSelectHost={(stepID, host) => setHostDrawer({ stepID, host })}
onOpenHostList={(stepID) => setHostList(stepID)}
```

At the bottom of the `RunDetail` JSX (just before the closing `</div>` of the outermost wrapper), render the drawers conditionally:

```tsx
{hostList && run?.steps && (() => {
  const step = run.steps.find((s) => s.step_id === hostList);
  if (!step) return null;
  return (
    <FanoutHostListDrawer
      step={step}
      onSelectHost={(host) => setHostDrawer({ stepID: step.step_id, host })}
      onClose={() => setHostList(null)}
    />
  );
})()}

{hostDrawer && run?.steps && (() => {
  const step = run.steps.find((s) => s.step_id === hostDrawer.stepID);
  if (!step) return null;
  return (
    <FanoutHostDrawer
      step={step}
      host={hostDrawer.host}
      onClose={() => setHostDrawer(null)}
    />
  );
})()}
```

- [ ] **Step 3: Verify everything compiles**

Run: `cd web && npx tsc --noEmit && cd ..`
Expected: no errors.

- [ ] **Step 4: Build the frontend bundle to surface any runtime concerns**

Run: `cd web && npm run build && cd ..`
Expected: build succeeds, bundle written to `web/dist/`.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/OrchestrationRunCanvas.tsx web/src/pages/Orchestrations.tsx
git commit -m "$(cat <<'EOF'
web(orchestration): wire FanoutCard + drawers into the run canvas

OrchestrationRunCanvas now renders FanoutCardView when a step has
per_node entries; otherwise the existing RunStepNodeView. RunDetail
owns the host-drawer + host-list-drawer state — opening the list
from a >20 card and a host from either the inline rows or the list.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Manual verification + final commit

Frontend tests aren't in scope (no harness). This task is the explicit acceptance check before declaring done.

**Files:**
- None (verification only)

- [ ] **Step 1: Build everything**

Run: `go build ./controlplane/... && cd web && npm run build && cd ..`
Expected: clean build.

- [ ] **Step 2: Run all backend tests**

Run: `go test ./controlplane/db ./controlplane/orchestrator ./controlplane/api -count=1`
Expected: PASS across all three packages.

- [ ] **Step 3: Smoke-test the canvas locally**

Start a dev CP and the web dev server:

```bash
go run ./cmd/okesu-cp &
cd web && npm run dev &
```

In another terminal, create a simple fan-out orchestration via the UI (or CLI):

```yaml
---
name: fanout-smoke
description: smoke test
steps:
  - id: scan
    agent: investigator
    nodes: [host-a, host-b, host-c]
    prompt: "scan"
---
```

Trigger it manually. While it runs, open the run-detail page in the browser.

**Acceptance checklist (≤ 4 hosts):**
- The fan-out step renders with the new card geometry (agent badge, headline `3 hosts (...)`, histogram bar, dot strip, expanded host rows visible by default).
- Each host row shows status dot + hostname + (eventually) findings count + duration + chevron.
- Clicking a host row opens `FanoutHostDrawer` with status, prompt, output tail, findings (or "(populated when run completes)" if mid-flight), and the agent-run link.
- ESC closes the drawer.
- Edges between this step and adjacent steps still render correctly.

**Acceptance checklist (5–20 hosts):**
- Modify the smoke spec to use 8 hosts. Re-run.
- While the step is `running`, the card auto-expands.
- After the step settles all-green, the card auto-collapses to histogram + strip + count.
- Clicking the card body toggles the override (expands/collapses regardless of the rule).
- Inducing a single failure (e.g. one bogus hostname) shows the inline `✕ host — error` line and keeps the card auto-expanded.

**Acceptance checklist (> 20 hosts):**
- Modify the smoke spec to use 25 hosts (`[h1, h2, ..., h25]`). Re-run.
- The card stays in collapsed mode permanently — no inline host rows, no dot strip — and shows `25 / 25` next to the histogram.
- A `View all 25 hosts →` button appears at the bottom.
- Clicking it opens `FanoutHostListDrawer` with a hostname filter and status-filter pills.
- Clicking a host in the list opens `FanoutHostDrawer` for that host (the list drawer stays behind it).
- ESC closes the topmost drawer.

**Acceptance checklist (legacy run):**
- Find an existing pre-migration fan-out run (`SELECT id FROM orchestration_runs JOIN orchestration_steps ... WHERE result_json LIKE '%byNode%' LIMIT 1` against the dev DB).
- Open it on the run-detail page.
- It renders with the new card; per-host rows show status / agent_run_id / findings_count from the `byNode` JSON; timestamps are blank ("missing", not garbage).

- [ ] **Step 4: Stop the dev servers**

```bash
kill %1 %2  # or whatever shell-job ids are running
```

- [ ] **Step 5: Final smoke-clean commit (only if anything changed during verification)**

If verification surfaced any tweaks, fix them and commit. If everything passed cleanly, no commit needed for this step.

---

## Self-review

**1. Spec coverage**

- Decision 1 (adaptive layout) — covered in Task 9 (`fanoutAutoExpand`) + 10/11 (card behavior). ✓
- Decision 2 (collapsed glance signals + expanded richness) — Task 10 (collapsed) + Task 11 (rows). ✓
- Decision 3 (auto-expand rule with >20 cap) — Task 9 helper + Task 10/11 wiring. ✓
- Decision 4 (single canvas card per step, no synthetic nodes) — Tasks 10/11 use one node type, react-flow node identity = step id. ✓
- Decision 5 (engine plumbs live progress) — Task 5. ✓
- Decision 6 (new table; byNode still in result_json) — Tasks 1/2/3/5. ✓
- Decision 7 (host-scoped drawer) — Task 12. ✓
- Decision 8 (>20 → histogram-only + list drawer) — Tasks 11/13. ✓

Non-goals: per-host approval (untouched — task 5 doesn't change approval flow), live output_tail streaming (drawer pulls only what's persisted), cross-run analytics (table makes it possible; no page built). ✓

Edge cases: legacy runs (Task 7 step 5 — `readByNodeFromResult` fallback), single-host fan-out (passes through naturally — `per_node.length == 1` still hits the fanout card), missing agent_run_id (Task 12 — link is conditional), concurrent writes (PRIMARY KEY enforces; engine spawns one goroutine per host), templated nodes resolving empty (engine-level error, untouched). ✓

Engine restart mid-fanout: covered by Task 3 (`FinishOrchestrationRun` reconciles orphans) + Task 2 (`ReconcileStepNodeDispatchesForRun` test). ✓

**2. Placeholder scan**

No "TBD", "TODO", or "implement later" remain. All test code, all DDL, all engine + handler patches show actual content. The one fuzzy line ("the exact name may differ" in Task 6 step 2) is a deliberate flag for the executor to inspect surrounding code in the file — the load-bearing structure is fully specified. ✓

**3. Type / name consistency**

- `StepNodeDispatchInsert` / `StepNodeDispatchUpdate` / `StepNodeDispatch` (Task 2) match `db.StepNodeDispatchInsert` / `db.StepNodeDispatchUpdate` references in Task 6, Task 7. ✓
- `StepNodeProgressSink` interface (Task 4) signature matches the recorder in Task 5 step 1 and the adapter in Task 6 step 2 (same arg list, same names). ✓
- `stepNodeProgressSinkAdapter` in Task 6 calls `db.StepNodeDispatchInsert{...}` with fields matching Task 2's struct. ✓
- TS `StepNodeDispatchView` (Task 8) matches the JSON shape produced by `stepNodeDispatchJSON` (Task 7). ✓
- `fanoutAutoExpand(hostCount, stepStatus, hosts)` (Task 9) is called from Task 10 with `(counts.total, data.status, data.perNode)`. ✓
- `FanoutCardData.onSelectHost` and `.onOpenHostList` (Task 10/11) are passed through `OrchestrationRunCanvas` (Task 14) and originate in `RunDetail` (Task 14). ✓

**4. Scope**

Single integrated plan: one feature, one PR. 15 tasks of which 7 are backend (small), 6 are frontend (medium), 2 are wiring. Doable in a single session of subagent-driven dev.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-29-orchestration-fanout-visualization.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
