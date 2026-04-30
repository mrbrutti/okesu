# Orchestration fan-out visualization

**Status:** approved
**Date:** 2026-04-29

## Goal

When an orchestration step uses `nodes: [...]` to fan out to multiple
hosts, the run-detail canvas should clearly show the parallelism — per-host
status, failures, and progress — instead of rendering it as a single
opaque card with one hostname.

The data the engine already collects (`PerNode` slice in `DispatchResult`,
serialized to `result_json["byNode"]`) is sufficient for retrospective
display. To support live progress while a fan-out runs, the engine will
also persist per-host state incrementally to a new table.

## Non-goals

- Per-host approval gates. Approval stays at the step level.
- Streaming live `output_tail` per host (only the final tail is shown).
- Cross-run analytics on per-host failures. The schema enables it; the
  pages are a follow-up.
- Scaling above ~10 000 hosts/step. The design works correctly past that
  point but isn't tuned for it; revisit when a real user gets there.

## Decisions

| # | Decision |
|---|---|
| 1 | Adaptive layout — fan-out cardinality is mixed across runs. |
| 2 | Collapsed view shows aggregate progress, status histogram, failure surfacing. Expanded view adds per-host findings count, duration, drill-down. |
| 3 | Auto-expand rule: ≤ 4 always expanded; 5–20 expanded while running or failed, collapsed when settled-and-green; > 20 never expands per-host rows on the canvas (drill-in via the host-list drawer). |
| 4 | Visual idiom: single canvas card per step (matches DB model). Card body toggles between collapsed and expanded; no synthetic per-host react-flow nodes. |
| 5 | Engine plumbs live per-host progress (writes incrementally as each host returns), not just the aggregated end-state. |
| 6 | Per-host state lives in a new `orchestration_step_node_dispatches` table. The aggregated `byNode` map continues to be written to `result_json` at completion for template back-compat. |
| 7 | Clicking a host row opens a host-scoped drawer (rendered prompt, output tail, findings, agent run link). |
| 8 | **Above 20 hosts/step** the canvas card never shows per-host rows or a dot strip. It shows the histogram bar with an `XXX / YYYY` count. Drill-in goes through a host-list drawer reached from a button on the card. |

## Architecture

### Layers touched

```
controlplane/db/migrations/037_*.sql           (new table)
controlplane/db/store.go                       (wire migration)
controlplane/db/sqlite/step_node_dispatches.go (new slice)
controlplane/db/postgres/step_node_dispatches.go (new slice)
controlplane/orchestrator/engine.go            (write progress in fanOut)
controlplane/api/orchestrations.go             (hydrate PerNode in view)
web/src/api.ts                                 (new types on view)
web/src/components/OrchestrationFanoutCard.tsx (new)
web/src/components/OrchestrationRunCanvas.tsx  (delegate to fanout card)
web/src/components/FanoutHostDrawer.tsx        (new)
web/src/components/FanoutHostListDrawer.tsx    (new — used at >20)
```

### Data flow

```
fanOut() goroutine:
  on dispatch-start  → InsertStepNodeDispatch(run_id, step_id, host, status='running', started_at)
  on dispatch-return → UpdateStepNodeDispatch(run_id, step_id, host, status, agent_run_id,
                                               findings_count, output_tail, error, ended_at)

After wg.Wait():
  result_json["byNode"] is written as today (template back-compat).

Run-detail handler:
  Build OrchestrationStepView, then for each step join orchestration_step_node_dispatches
  by (run_id, step_id) and attach PerNode []StepNodeDispatchView.

Frontend run page:
  Polls api.orchestrationRunDetail every 2.5 s (existing).
  OrchestrationRunCanvas renders OrchestrationFanoutCard when step.per_node.length > 0,
  else today's RunStepNodeView.
```

## Data model

### Migration 037 — `orchestration_step_node_dispatches`

```sql
CREATE TABLE orchestration_step_node_dispatches (
  run_id          INTEGER NOT NULL,
  step_id         TEXT    NOT NULL,
  host            TEXT    NOT NULL,
  status          TEXT    NOT NULL,         -- pending | running | completed | failed
  agent_run_id    TEXT,
  findings_count  INTEGER NOT NULL DEFAULT 0,
  output_tail     TEXT,
  error           TEXT,
  started_at      TIMESTAMP,
  ended_at        TIMESTAMP,
  PRIMARY KEY (run_id, step_id, host),
  FOREIGN KEY (run_id) REFERENCES orchestration_runs(id) ON DELETE CASCADE
);

CREATE INDEX idx_osnd_run_step ON orchestration_step_node_dispatches(run_id, step_id);
```

Postgres equivalent uses `BIGINT` for `run_id` and `TIMESTAMPTZ` for the
two timestamp columns; otherwise identical.

### `StepNodeDispatchView` (Go + TS)

```go
type StepNodeDispatchView struct {
    Host          string  `json:"host"`
    Status        string  `json:"status"`
    AgentRunID    string  `json:"agent_run_id,omitempty"`
    FindingsCount int     `json:"findings_count"`
    OutputTail    string  `json:"output_tail,omitempty"`
    Error         string  `json:"error,omitempty"`
    StartedAt     *string `json:"started_at,omitempty"`
    EndedAt       *string `json:"ended_at,omitempty"`
}
```

Added to `OrchestrationStepView` as `PerNode []StepNodeDispatchView \`json:"per_node,omitempty"\``.
Empty for non-fan-out steps and legacy runs.

## Engine changes

`fanOut()` in `controlplane/orchestrator/engine.go` accepts a small
sink interface so it can be unit-tested without the store:

```go
type StepNodeProgressSink interface {
    OnDispatchStart(runID int64, stepID, host string, startedAt time.Time) error
    OnDispatchEnd(runID int64, stepID, host string,
                  status string, agentRunID string,
                  findingsCount int, outputTail, errorStr string,
                  endedAt time.Time) error
}
```

Each goroutine calls `OnDispatchStart` immediately before `disp.Dispatch`
and `OnDispatchEnd` after it returns. The sink is passed in by the
engine wiring; in tests it's an in-memory recorder.

### Engine restart mid-fanout

When the engine restarts, in-flight rows are left at `running` with no
`ended_at`. The run-status-update path (already present) reconciles by
running `UPDATE orchestration_step_node_dispatches SET status='failed',
error='run cancelled', ended_at=now() WHERE run_id=? AND status='running'`
when the run transitions to a terminal state. This is the only place
that touches another goroutine's row — safe because the run is no
longer dispatching.

## Frontend behavior

### Auto-expand rule (state of `expanded` boolean)

```
n = effective_nodes.length

if n <= 4:        expanded = true  (always)
if 5 <= n <= 20:  expanded = (step.status in {running, failed}
                              || any host.status in {running, failed})
if n > 20:        expanded = false (rows never rendered on canvas)
```

User clicks toggle the local override and bypass the rule.

### Card content by mode

**Collapsed (any cardinality):**
- Agent badge.
- Step status pill.
- Headline `"N hosts (a ok • b fail • c running)"`.
- Histogram bar (segments by status).
- Count `done / total` next to the bar.
- Dot strip (one dot per host) — hidden when `n > 20`.
- Inline failed-host lines `✕ host — error` for up to 3 failed hosts
  (only when `n ≤ 20`). When more than 3 hosts failed, show
  `✕ N hosts failed — open to inspect` instead.

**Expanded (only at `n ≤ 20`):**
- Everything from collapsed.
- Stacked host rows: status dot, hostname (mono), findings count badge
  (only when > 0), elapsed/duration, chevron.
- Click a row → `FanoutHostDrawer` for that host.

**At `n > 20`:**
- Card stays in collapsed mode permanently.
- Add a `View all N hosts →` button that opens
  `FanoutHostListDrawer` — a virtualized list of host rows, with a
  text filter and a status filter. Clicking a row in the list drawer
  opens `FanoutHostDrawer`.

### `FanoutHostDrawer`

Slides in from the right over the run page. Header shows
`step-N / hostname` breadcrumb. Sections:

1. Status, started_at, ended_at, duration, error.
2. Rendered prompt — pulled from the parent step (same on every host).
3. Output tail — that host's `output_tail`.
4. Findings — read from `result_json.byNode[host].findings` for
   completed runs (the engine already keeps per-host findings under
   the byNode map). For runs that haven't yet aggregated byNode
   (mid-flight), the drawer shows `findings_count` from the new
   table and "(populated when run completes)".
5. Footer: `Open agent run →` link, only when `agent_run_id` is set.

### `FanoutHostListDrawer` (new, only used at `n > 20`)

Same drawer chrome. Header: `step-N · N hosts`. Body: a virtualized list
of `[dot] hostname [findings] [duration] [chev]` rows, with a status
filter (`all / failed / running / completed`) and a hostname text
filter at the top. Click a row → `FanoutHostDrawer` for that host. Two
drawers may be visible at once (list behind, detail on top); ESC closes
the topmost.

## Edge cases

- **Legacy runs without per-host rows.** When the new table is empty
  for a step but `result_json.byNode` exists, the API hydrates
  `per_node` from it (status, agent_run_id, output_tail, error,
  findings_count) so old completed runs render with the new card.
  Timestamps stay null. The engine never back-fills the new table —
  the hydration is read-only at view time. Steps with neither table
  rows nor `byNode` (single-node steps) render as today.
- **Single-host fan-out (`nodes: [foo]`).** Renders as the fan-out card
  with one host. No special case.
- **`agent_run_id` missing.** "Open agent run" link in the host drawer
  is conditional on the field being set.
- **Concurrent writes per `(run, step, host)`.** Impossible by
  construction (one goroutine owns each tuple). PRIMARY KEY enforces.
- **Templated `nodes:` resolving to empty.** Already an engine-level
  error; UI not involved.

## Testing

### DB

`controlplane/db/sqlite/step_node_dispatches_test.go` and the postgres
counterpart, mirroring the existing slice-test shape:
- Insert + read back.
- Update transitions `running → completed` and `running → failed`.
- ON DELETE CASCADE removes rows when the run is deleted.

### Engine

`controlplane/orchestrator/engine_test.go` adds:
- `TestFanOut_PersistsPerHostProgress` — fake dispatcher returns mixed
  results; assert (a) one row per host with `status='running'` after
  start, (b) one row per host with the right terminal status / findings
  count / error after return, (c) `byNode` still lands in `result_json`.
- `TestFanOut_RestartReconciliation` — set up rows in `running` with
  no `ended_at`, transition the run to failed, assert all stale rows
  are reconciled to `failed`.

### API

`controlplane/api/orchestrations_test.go` adds a handler test asserting
`OrchestrationStepView.PerNode` is populated from the table when rows
exist, empty otherwise.

### Frontend

RTL tests on `OrchestrationFanoutCard`:
- `n ≤ 4` always expanded.
- `n` between 5 and 20 expands during running, collapses on settled-green.
- `n > 20` never shows host rows; renders the `View all hosts` button.
- Inline failed-host line shows when exactly one failure at `n ≤ 20`.
- Host row click invokes the drawer-open callback.

## Deferred work (revisit when needed)

1. **Cross-run host failure analytics.** New table makes it possible;
   page TBD when an operator asks for it.
2. **Streaming per-host output_tail.** Currently shown only at terminal.
3. **Tuning above ~10 000 hosts/step.** Histogram-only mode handles the
   canvas, but `FanoutHostListDrawer` would need server-side pagination
   instead of fetching the full list. Address when a real user crosses
   the threshold.
