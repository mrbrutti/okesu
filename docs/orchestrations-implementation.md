# Orchestrations — Implementation Guide

This is the builder's companion to [`orchestrations.md`](./orchestrations.md). The
user-facing doc explains the YAML DSL — what an operator types. This doc
explains what an implementer has to build to make that DSL work. If you are
porting orchestrations into a new project from scratch, follow this end to
end; if you are extending the existing system, jump to the relevant section.

The spec language itself (trigger forms, step fields, templating, action
protocol) is canonical in `orchestrations.md`. This doc references it but does
not duplicate it.

---

## 1. Scope and design constraints

**What an orchestration is.** A YAML spec describing a chain of agent
invocations ("steps") plus a trigger (manual, cron, finding, enrichment).
Each step renders a templated prompt against prior steps' outputs and runs an
agent — locally on the control plane (CP), on a remote node via a reverse-mTLS
tunnel, or on a federated child CP. Steps can request CP-side mutations
(status changes, tags, severity overrides, lesson capture, IOC enrichment) via
a structured `actions:` allowlist; the engine validates and applies them.

**Constraints the engine must honor.**

1. **Sequential execution within a run.** Phase A–D ship single-thread
   sequential steps. Phase E parallel DAG is on the roadmap but not built.
   Fan-out (`nodes:`) is the only parallelism — it parallelizes one step
   across N hosts but blocks until all hosts terminate.
2. **Idempotent triggers.** Cron must not double-fire across CP restarts; the
   `last_fired_at` column dedupes. Finding triggers fire once per finding
   event; replay must not re-trigger.
3. **Default-deny actions.** A step that doesn't list an action kind in its
   `actions:` allowlist cannot apply that mutation, even if the agent
   requests it. The applier silently drops + logs unauthorized requests.
4. **Audit trail.** Every applied action writes a `finding_edits` row tied
   to the run + step. Step output is persisted (`output_summary`, capped 4KB
   for replay; full transcript ≤100KB lives on the step row). Resolved
   `data:` blocks are snapshot to `data_snapshot` for replay.
5. **Tunnel-or-fail for remote dispatch.** A step targeting a node with no
   active reverse-mTLS tunnel fails fast with a specific error message —
   never silently skipped, never queued indefinitely. Pull-mode (jobs
   runtime) is the alternative; the routing dispatcher picks based on the
   node's `preferred_dispatch` and live state.
6. **Federation is opt-in per step.** Default `cp: local` runs on the
   authoring CP. `cp: <child_id>` proxies via the federation aggregator.
   Trigger evaluation is local to each CP — to react fleet-wide, install
   the orchestration on every CP that should react.

**What this doc covers.** Data model, engine internals, trigger plumbing,
dispatcher routing, templating + expression evaluator, data resolver,
action applier, approval gates, meeting steps, federation, visual editor
round-trip, API surface, validation, test strategy, recommended build order.

---

## 2. System architecture (text view)

```
┌──────────────────────────────────────────────────────────────────────┐
│                          Control Plane (CP)                          │
│                                                                      │
│  ┌─────────────┐   ┌──────────────┐   ┌──────────────────────────┐   │
│  │ HTTP API    │   │ Cron sched.  │   │ Event pipeline           │   │
│  │  /api/orch* │   │  60s tick    │   │  finding ingest          │   │
│  │  /api/runs* │   │              │   │  enrichment ingest       │   │
│  └──────┬──────┘   └──────┬───────┘   └────────────┬─────────────┘   │
│         │                 │                        │                 │
│         ▼                 ▼                        ▼                 │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                Orchestration Coordinator                    │     │
│  │   - Trigger evaluation (filter expr / cron parse)           │     │
│  │   - Run row creation                                        │     │
│  │   - Dispatch to engine workers                              │     │
│  └────────────────────────────┬────────────────────────────────┘     │
│                               ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │                     Engine.Run(run)                         │     │
│  │   for each step:                                            │     │
│  │     1. evaluate `when:` skip                                │     │
│  │     2. resolve `data:` block (CP-side queries)              │     │
│  │     3. render `prompt` template                             │     │
│  │     4. dispatch via routingDispatcher                       │     │
│  │     5. parse findings + result from output                  │     │
│  │     6. apply actions (allowlist-checked)                    │     │
│  │     7. persist step state                                   │     │
│  │     8. halt or skip on failure (per continue_on_error)      │     │
│  └────────────────────────────┬────────────────────────────────┘     │
│                               ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │  routingDispatcher                                          │     │
│  │   ├── localDispatcher        (CP-internal step, no node)    │     │
│  │   ├── tunnelDispatcher       (mTLS reverse tunnel)          │     │
│  │   ├── jobsRuntimeDispatcher  (pull-mode)                    │     │
│  │   └── federatedDispatcher    (cp:<child> proxy)             │     │
│  └─────────────────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────────────────┘
                                 │
       ┌─────────────────────────┼─────────────────────────┐
       ▼                         ▼                         ▼
  ┌─────────┐              ┌──────────┐             ┌─────────────┐
  │ Node A  │              │ Node B   │             │ Child CP    │
  │ (mTLS   │              │ (jobs    │             │ (federated) │
  │  tunnel)│              │  pull)   │             │             │
  └─────────┘              └──────────┘             └─────────────┘
```

The CP is single-process Go. The orchestrator package is in
`controlplane/orchestrator/`; the HTTP-facing wiring is in
`controlplane/api/orchestrations.go`. Persistence is SQLite or Postgres
(dual-dialect migrations under `controlplane/db/migrations/{sqlite,postgres}/`).

---

## 3. Data model

Every column and index. Migrations are dual-dialect — keep the SQLite and
Postgres files in lock-step. Type names below are SQLite; Postgres uses
`TEXT`, `BIGINT`, `BOOLEAN`, `TIMESTAMP WITH TIME ZONE` as appropriate.

### `orchestrations` (migration 023, extended by 024)

```sql
CREATE TABLE orchestrations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,            -- slug; [a-z][a-z0-9-]*
    description     TEXT    NOT NULL,
    spec_yaml       TEXT    NOT NULL,                   -- the canonical spec
    trigger_kind    TEXT    NOT NULL,                   -- manual|cron|finding|ioc_enriched
    trigger_filter  TEXT,                               -- expression for finding/enrichment
    trigger_cron    TEXT,                               -- 5-field cron for kind=cron
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_fired_at   TIMESTAMP,                          -- migration 024
    last_fired_by   TEXT,                               -- migration 024 — "cron"|"finding:<id>"|"manual:<userid>"
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by      TEXT,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_orchestrations_name           ON orchestrations(name);
CREATE INDEX idx_orchestrations_trigger_kind   ON orchestrations(trigger_kind, enabled);
-- Partial index for cron probes (Postgres):
--   CREATE INDEX idx_orch_cron_enabled ON orchestrations(trigger_kind, enabled)
--     WHERE trigger_kind = 'cron' AND enabled = TRUE;
```

`trigger_kind`, `trigger_filter`, `trigger_cron` are denormalized from the
YAML for indexed probes — the canonical source of truth is `spec_yaml`.
Update both fields together when the spec changes.

### `orchestration_runs`

```sql
CREATE TABLE orchestration_runs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id  INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
    status            TEXT    NOT NULL,                 -- pending|running|approval_required|completed|failed|cancelled
    trigger_kind      TEXT    NOT NULL,                 -- copy of orch.trigger_kind at fire time
    trigger_payload   TEXT,                             -- JSON: trigger context (finding fields, cron tick, manual inputs)
    current_step_id   TEXT,                             -- step id last attempted
    started_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at          TIMESTAMP,
    started_by        TEXT,                             -- email or "cron"/"finding:<id>"
    error             TEXT
);
CREATE INDEX idx_orch_runs_orch_started ON orchestration_runs(orchestration_id, started_at DESC);
CREATE INDEX idx_orch_runs_status       ON orchestration_runs(status, started_at DESC);
```

Status transitions: `pending → running → (approval_required ⇄ running)* →
(completed | failed | cancelled)`. The engine never moves directly from
`pending` to `completed` — it always passes through `running`.

### `orchestration_steps` (migration 023, extended by 028 + 049)

```sql
CREATE TABLE orchestration_steps (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_run_id     INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    step_id                  TEXT    NOT NULL,          -- as authored in YAML
    step_idx                 INTEGER NOT NULL,          -- 0-based position
    status                   TEXT    NOT NULL,          -- pending|running|completed|failed|skipped
    run_id                   INTEGER,                   -- FK to runs(id) — the underlying agent run on a node
    cp_instance_id           TEXT,                      -- which CP the step ran on (federation)
    node_id                  INTEGER,                   -- nodes(id) for single-node steps; NULL for fan-out / CP-local
    rendered_prompt          TEXT,                      -- post-template prompt text
    result_json              TEXT,                      -- attributes from orchestration_result finding
    output_summary           TEXT,                      -- last ~4KB of transcript for the run-detail UI
    started_at               TIMESTAMP,
    ended_at                 TIMESTAMP,
    error                    TEXT,
    approved_at              TIMESTAMP,                 -- approval gate
    approved_by              TEXT,
    data_snapshot            TEXT,                      -- migration 028 — JSON of resolved data: block
    prompt_entities          TEXT,                      -- migration 049 — JSON list of entity refs templated in
    UNIQUE(orchestration_run_id, step_id)
);
CREATE INDEX idx_orch_steps_run ON orchestration_steps(orchestration_run_id);
```

`output_summary` is bounded by `tail4K` (engine.go:905). The full transcript
is capped at 100KB at parse time — anything past 100KB is truncated and a
sentinel line `... [truncated] ...` is appended. This protects template
expansion from blowing up the prompt budget.

### Auxiliary tables driven by orchestrations

```sql
-- migration 027 — every applied action writes one row here.
CREATE TABLE finding_edits (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    finding_id               INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    field                    TEXT    NOT NULL,          -- status|severity_override|tag_add|tag_remove|linked_run
    old_value                TEXT,
    new_value                TEXT,
    reason                   TEXT,
    edited_by_user_id        INTEGER,
    orchestration_run_id     INTEGER REFERENCES orchestration_runs(id) ON DELETE SET NULL,
    orchestration_step_id    TEXT,                      -- step_id within the run
    edited_at                TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_fe_finding ON finding_edits(finding_id, edited_at DESC);
CREATE INDEX idx_fe_run     ON finding_edits(orchestration_run_id);

-- The "auto-handled by run #N" link surfaced in finding-detail UI.
CREATE TABLE finding_run_links (
    finding_id               INTEGER NOT NULL,
    orchestration_run_id     INTEGER NOT NULL,
    step_id                  TEXT    NOT NULL,
    linked_at                TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(finding_id, orchestration_run_id, step_id)
);
CREATE INDEX idx_frl_run ON finding_run_links(orchestration_run_id);

-- migration 035 — captured by reflect_with_lessons action; injected into
-- the daimon's system prompt on the next tick.
CREATE TABLE agent_lessons (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_name               TEXT    NOT NULL,
    lesson_text              TEXT    NOT NULL,         -- ≤200 chars
    orchestration_run_id     INTEGER REFERENCES orchestration_runs(id) ON DELETE SET NULL,
    orchestration_step_id    TEXT,
    created_at               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_al_agent ON agent_lessons(agent_name, created_at DESC);

-- migration 038 — driven by enrich_ioc action.
CREATE TABLE ioc_enrichments (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ioc_id       INTEGER NOT NULL REFERENCES iocs(id) ON DELETE CASCADE,
    adapter      TEXT    NOT NULL,                      -- virustotal|abuseipdb|shodan
    verdict      TEXT,
    score        REAL,
    raw_json     TEXT,
    fetched_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   TIMESTAMP NOT NULL,                    -- cache TTL (default 24h)
    UNIQUE(ioc_id, adapter)
);
CREATE INDEX idx_ie_expires ON ioc_enrichments(expires_at);
CREATE INDEX idx_ie_adapter ON ioc_enrichments(adapter);
```

### Schema-evolution rules

- New columns: append-only, NULL-safe, no defaults in SQLite (Postgres ok).
- New tables: same migration number + dialect on both sides.
- Backfills: idempotent; the migration runner re-applies on schema bumps.
- Indexes: name them `idx_<table>_<cols>` so duplicates are easy to spot.

---

## 4. Spec language — quick reference

The full spec is in [`orchestrations.md`](./orchestrations.md). Here is a
single page of every field shape the validator must accept:

```yaml
---
name: my-orch                  # required, [a-z][a-z0-9-]*
description: One-liner.        # required
version: 1                     # optional, defaults to 1

trigger:                       # optional, defaults to {on: manual}
  on: manual | cron | finding | ioc_enriched
  cron:   "0 */6 * * *"        # required iff on==cron
  filter: "<expr>"             # required iff on==finding | ioc_enriched

inputs:                        # optional; map<name, schema>
  finding_id:
    type: int | string | bool
    required: true | false
    default: <literal>
    enum: [<literal>, ...]

defaults:                      # optional; per-step defaults
  timeout: <duration>
  continue_on_error: bool
  cp: local | <instance_id>

steps:                         # required; ordered list
  - id: <step_id>              # required, unique within orch, [a-z][a-z0-9_-]*
    kind: agent | meeting      # default agent

    # agent step
    agent: <agent_name>        # required
    prompt: |                  # required, templated
      ...{{ trigger.host }}...
    node: <selector>           # mutually exclusive with nodes
    nodes:                     # fan-out
      - <selector>
    cp: local | <instance_id>
    inputs: {<key>: <value>}
    timeout: <duration>
    continue_on_error: bool
    when: "<expr>"             # skip if expr is falsy
    approval: required         # pause before dispatching
    actions:                   # allowlist of CP-side mutations the step may apply
      - update_finding_status
      - add_finding_tag
      - remove_finding_tag
      - set_finding_severity_override
      - link_run_to_finding
      - escalate
      - reflect_with_lessons
      - enrich_ioc
    data:                      # CP-side queries resolved before dispatch
      <bind_name>:
        query: <namespace>.<method>
        params: {<key>: <value>}

    # meeting step (kind: meeting)
    meeting:
      participants: [<agent>, ...]
      synthesizer: <agent>
    war_bridge: bool

# (anything below the closing --- is operator notes; ignored by the engine)
---
# Notes
Free-form markdown.
```

**Validation invariants** (`controlplane/orchestrator/spec.go:Validate`):

1. `name`, `description`, ≥1 step are required.
2. Step IDs are unique and slug-shaped.
3. `trigger.on` is one of the four kinds; `cron`/`filter` presence
   matches the kind.
4. `node` and `nodes` are mutually exclusive.
5. `actions:` entries must be in the registered allowlist
   (`AllowedActionKinds`). Unknown kinds reject the spec.
6. `data:` query names match the registered handler set
   (`SupportedQueries`).
7. Forward refs in templates are caught — a step cannot reference a
   later step's output. Self-refs are rejected.
8. `approval` accepts only `required` (or omitted).
9. `version` must equal `SpecVersion` (currently 1). Future bumps
   are reserved for breaking-change releases.

---

## 5. The execution engine

Single file: `controlplane/orchestrator/engine.go` (~1200 lines). Companion
files: `actions.go`, `meeting.go`, `template.go`, `triggers.go`, `spec.go`.

### 5.1 Run lifecycle (state machine)

```
   ┌──────────┐      Engine.Run() picks up a pending row
   │ pending  │
   └────┬─────┘
        ▼
   ┌──────────┐      first step dispatches
   │ running  │
   └────┬─────┘
        │
        │   step has approval: required
        ▼
   ┌──────────────────┐    Approve(runID, stepID) called
   │ approval_required│ ───────────────────────────────────►  back to running
   └────┬─────────────┘
        │ no approve, operator cancels:
        ▼
   ┌──────────┐
   │ cancelled│
   └──────────┘

   running ──► (all steps finished cleanly) ──► completed
   running ──► (a step failed and continue_on_error=false) ──► failed
```

### 5.2 Step state machine

```
pending ──► running ──► completed
                    └─► failed   (continue_on_error=false halts run)
                    └─► failed   (continue_on_error=true; later steps still run)
pending ──► skipped  (when: expr was falsy, or earlier failure halted run)
pending ──► running ──► (approval required) ──► running ──► completed/failed
```

### 5.3 The Run() main loop (pseudocode)

```go
func (e *Engine) Run(ctx context.Context, runID int64) error {
    run, err := e.store.LoadRun(runID)
    if err != nil { return err }

    spec, err := ParseSpec(run.SpecYAML)
    if err != nil { return e.markFailed(run, err) }

    e.store.UpdateRunStatus(runID, "running", "")

    env := newEnv()
    env["trigger"] = run.TriggerPayload   // {kind, host, finding_id, ...}

    for idx, step := range spec.Steps {
        // 1. evaluate `when:`
        if step.When != "" {
            ok, err := EvalBool(step.When, env)
            if err != nil { return e.markFailed(run, err) }
            if !ok {
                e.persistStep(run, step, "skipped", "")
                env[step.ID] = stepStub{Status: "skipped"}
                continue
            }
        }

        // 2. approval gate
        if step.Approval == "required" && step.ApprovedAt == nil {
            e.store.UpdateRunStatus(runID, "approval_required", "")
            e.persistStep(run, step, "pending", "")
            return nil   // engine yields; resumed by Approve()
        }

        // 3. resolve data: block (CP-side queries)
        dataBindings, snapshot, err := e.resolveData(ctx, step.Data, env)
        if err != nil { return e.haltFailed(run, step, err) }
        env["data"] = dataBindings
        e.persistStepDataSnapshot(run, step, snapshot)

        // 4. render prompt
        prompt, entities, err := RenderPrompt(step.Prompt, env)
        if err != nil { return e.haltFailed(run, step, err) }

        // 5. dispatch (single node, fan-out, federated, or local)
        result, err := e.dispatcher.Dispatch(ctx, DispatchRequest{
            Step:           step,
            RenderedPrompt: prompt,
            CP:             step.CP,
            Node:           step.Node,
            Nodes:          step.Nodes,
            Timeout:        step.Timeout,
        })
        if err != nil {
            if step.ContinueOnError {
                env[step.ID] = stepStub{Status: "failed", Error: err.Error()}
                e.persistStep(run, step, "failed", err.Error())
                continue
            }
            return e.haltFailed(run, step, err)
        }

        // 6. parse findings + structured result
        findings, structured := parseFindings(result.OutputLines)
        env[step.ID] = stepBindings(result, findings, structured)

        // 7. apply actions (allowlist-checked)
        actions := extractActionsFrom(structured)
        applied, rejected := e.applyActions(run, step, actions)

        // 8. persist
        e.persistStep(run, step, "completed", "")
    }

    e.store.FinishRun(runID, "completed", "")
    return nil
}
```

Key types: `Engine` (engine.go:263), `RunRecord` (138), `StepRecord` (155),
`DispatchRequest` (195), `DispatchResult` (212).

### 5.4 Persistence contract

The engine persists step state on every transition:

| Event | Method (Store interface) |
|---|---|
| Step starts | `UpsertOrchestrationStep(runID, step, "running", started_at=now)` |
| Step finishes | `UpsertOrchestrationStep(runID, step, status, ended_at=now, output_summary, result_json, run_id, cp_instance_id, node_id)` |
| Run status change | `UpdateOrchestrationRunStatus(runID, status, current_step_id)` |
| Run finishes | `FinishOrchestrationRun(runID, status, ended_at, error)` |
| Data block resolved | `UpdateOrchestrationStepDataSnapshot(runID, stepID, json)` |

The Store interface (engine.go:71) is the seam between the engine and SQL.
Implementing it on a different RDBMS is a single adapter file.

### 5.5 Output truncation

```go
const stepOutputCap = 100_000   // 100KB
const stepSummaryCap = 4_000    // 4KB tail in output_summary

func tail4K(s string) string {
    if len(s) <= stepSummaryCap { return s }
    return s[len(s)-stepSummaryCap:]
}
```

The transcript is truncated *before* it reaches `env[stepID].output`. This
caps template expansion cost — a 50-step orchestration that piped raw output
forward could otherwise explode the prompt.

### 5.6 Cancellation

`POST /api/orchestration-runs/{id}/cancel` writes `status='cancelled'` on the
run row and signals the engine via context cancellation. In-flight dispatches
get a context-deadline error; the engine catches it, marks the step `failed`
with `cancelled`, and persists. Steps already running on remote nodes continue
to completion on the node side — cancellation is best-effort at the CP layer.
The node's run row records its own outcome but is not stitched into the
cancelled orchestration run.

---

## 6. Triggers

Four kinds: `manual`, `cron`, `finding`, `ioc_enriched`. All four end the same
way — they create an `orchestration_runs` row with `status='pending'` and
hand it to the coordinator. The differences are in *when* and *with what
trigger payload* the row is created.

### 6.1 Manual

`POST /api/orchestrations/{id}/run` with body:

```json
{ "inputs": { "finding_id": 42, "scope": "fleet" } }
```

Handler: `OrchestrationRunCreate` (api/orchestrations.go:1618). Steps:

1. Load orchestration row, parse spec.
2. Validate inputs against `inputs:` schema (types, required, enum).
3. Build trigger payload: `{kind: "manual", inputs: <validated>}`.
4. Insert run row with `started_by = "<email>"`, `trigger_payload = JSON`.
5. Kick the coordinator (channel send).
6. Return `{run_id}`.

### 6.2 Cron

The cron scheduler is a 60-second tick loop (`StartCronScheduler` at
api/orchestrations.go:1502). Each tick:

1. `SELECT id, name, trigger_cron, last_fired_at FROM orchestrations
    WHERE trigger_kind='cron' AND enabled=1`
2. For each row: `next := NextCronFire(trigger_cron, max(last_fired_at, now-60s))`.
3. If `next ≤ now`: insert run row, set `last_fired_at = now`, set
   `last_fired_by = "cron"`, set trigger payload `{kind: "cron", tick:
   <unix>, at: <rfc3339>}`. Kick coordinator.
4. Sleep until next tick.

`last_fired_at` is the dedup key. The math `max(last_fired_at, now-60s)`
prevents catch-up storms after long pauses (a 7-day downtime won't fire 7×24
intervals at startup) while still firing missed schedules within the last
minute window. Tune that window to taste; 60s matches the tick rate.

#### Cron parser

`NextCronFire(expr, after)` (triggers.go:179). Accepts standard 5-field
cron — minute, hour, day-of-month, month, day-of-week (0=Sunday). Each
field supports:

| Form | Example | Meaning |
|---|---|---|
| Wildcard | `*` | every value |
| Single | `30` | exactly 30 |
| List | `1,3,5` | any of the listed values |
| Range | `1-5` | inclusive range |
| Step | `*/15` | every 15th value starting at 0 |
| Step on range | `9-17/2` | every 2nd value within 9–17 |

Not supported: `?`, `L`, `W`, `#`, named months/days. Reject these at spec
validation time so operators get a clear error rather than a silent never-fire.

```go
// Parse one field; returns the set of valid values for that position.
func parseCronField(field string, min, max int) (map[int]bool, error)

// Find the next time after `after` that matches all five fields.
func NextCronFire(expr string, after time.Time) (time.Time, error)
```

The implementation iterates minute-by-minute (max 366 days ahead) and returns
the first match. This is bounded and trivially correct — don't optimize
unless you have a reason; cron expressions almost always match within an hour.

### 6.3 Finding-trigger

The event pipeline calls `OnFinding(finding)` (api/orchestrations.go:1475) for
every finding projected on the local CP. Steps:

1. `SELECT id, trigger_filter FROM orchestrations
    WHERE trigger_kind='finding' AND enabled=1`
2. For each row, build a `FindingPayload` (triggers.go:27) — flatten finding
   fields into the env binding `finding`.
3. Eval `trigger_filter` against `{finding: payload}`. Empty filter ⇒ never
   fires (opt-in design).
4. If true, insert run row with trigger_payload =
   `{kind: "finding", finding_id, severity, host, ...all finding fields}`.
   Kick coordinator.

`FindingPayload` exposes: `id`, `severity`, `status`, `title`, `agent`,
`host`, `category`, `dedup_key`, `resource`, `attributes` (map),
`tags` (list), `cluster_id`, `ioc_*` (when finding carries an IOC).

The expression evaluator is the same one used for `when:` and templates
(see §8). Operators: `==`, `!=`, `<`, `<=`, `>`, `>=`, `&&`, `||`, `!`,
`in [a, b, c]`, `contains`. Numeric coercion: when both sides parse as a
number, comparison is numeric.

### 6.4 Enrichment trigger

Same pattern as finding-trigger but driven by IOC enrichment events.
`OnEnrichment(ioc, adapter, verdict)` (api/orchestrations.go:1447) iterates
`trigger_kind='ioc_enriched'` rows; `EvaluateEnrichmentFilter(filter, payload)`
binds an `enrichment` map: `{ioc_id, kind, value, adapter, verdict, score}`.

```yaml
trigger:
  on: ioc_enriched
  filter: "enrichment.verdict == 'malicious' && enrichment.score > 80"
```

### 6.5 Webhook ingestion — clarification

The CP's `/api/webhooks/events` endpoint is the **daemon webhook ingest**:
deployed daimons POST findings + raw events here. It is *not* a generic
inbound-webhook trigger source. Findings ingested through that endpoint do
flow through the finding-projection pipeline and therefore can fire
`finding`-triggered orchestrations — that's the indirection.

If you want a true inbound webhook trigger ("an outside SaaS posts JSON, an
orchestration fires"), build a thin shim: an HTTP handler that maps the
incoming payload into the trigger contract. Two design options:

1. **Synthetic finding.** Translate the webhook into a finding row
   (severity, agent="webhook", host=<derivable from payload>). Existing
   finding triggers consume it. Cleanest if your webhooks describe events
   you'd treat as findings anyway (PagerDuty incident, Slack alert).
2. **Direct trigger kind.** Add `trigger.on: webhook` + a handler at
   `/api/webhooks/orchestration/{name}` that POSTs straight to
   `OrchestrationRunCreate` with `inputs = <payload>`. Simpler wire format
   but a new trigger kind to schema-evolve.

The current codebase implements neither — webhooks reach orchestrations via
option 1 implicitly, through the finding pipeline. If your project needs
option 2, add the route + insert into the trigger-kind enum + extend
`Spec.Validate`.

### 6.6 Trigger payload contract

`orchestration_runs.trigger_payload` is the canonical record of what fired
the run. The engine binds it as `env["trigger"]`. Schema by kind:

```jsonc
// manual
{ "kind": "manual", "inputs": {<schema-validated key/value>} }

// cron
{ "kind": "cron", "tick": <unix-seconds>, "at": "<rfc3339>" }

// finding
{ "kind": "finding", "finding_id": 42, "severity": "HIGH",
  "title": "...", "agent": "edr", "host": "web-01",
  "category": "process", "dedup_key": "...", "resource": "pid:1337",
  "attributes": {<finding attrs>}, "tags": [...] }

// ioc_enriched
{ "kind": "ioc_enriched", "ioc_id": 17, "kind_field": "sha256",
  "value": "abc...", "adapter": "virustotal",
  "verdict": "malicious", "score": 85.2 }
```

Persist this exact shape. Templates rely on it: `{{trigger.host}}`,
`{{trigger.tick}}`, `{{trigger.kind}}`, `{{trigger.attributes.process_pid}}`.

---

## 7. The dispatcher

`routingDispatcher` (controlplane/api/orchestrations.go:772) picks one of
four sub-dispatchers based on the step's CP/node and the node's live state.
Decision tree:

```
                      ┌─────────────────┐
                      │ DispatchRequest │
                      └────────┬────────┘
                               │
            ┌──────────────────┼──────────────────┐
       step.CP != "local"      │       step.CP == "local"
                               │
                               ▼
                       node selector?
                               │
                  ┌────────────┴──────────┐
              none/CP-only             node|nodes
                  │                         │
                  ▼                         ▼
           localDispatcher        node has live tunnel?
            (CP-internal)              │
                                       ├─ yes → tunnelDispatcher
                                       ├─ no, jobs runtime active → jobsRuntimeDispatcher
                                       └─ neither → fail step
```

Federated dispatch (left branch above):

```
federatedDispatcher
   ├── parent CP looks up child's CP record
   ├── proxies POST to child's federation runs endpoint
   ├── streams logs back via federation aggregator
   └── stitches result into parent's orchestration_steps row
```

### 7.1 Local dispatcher (CP-internal step)

For steps with no `node:` and no `cp:`, the prompt runs in-process on the
CP. Used for steps that mutate CP state (write a finding, post a webhook)
without needing a remote host. Handler: `localDispatcher.Dispatch`.

### 7.2 Tunnel dispatcher (mTLS reverse tunnel)

The node has called `okesu node` with mTLS certs against the CP's tunnel
listener (`controlplane/tunnel/server.go:Server`). The tunnel registry holds
active connections keyed by node id (`controlplane/tunnel/registry.go`).
Dispatch flow:

1. Resolve node by selector (hostname → node row).
2. Look up tunnel conn in registry. If absent → fail with the canonical
   "no reverse-tunnel client" message (visible to operators in the
   run-detail card).
3. Build a `RunCreateRequest` with the rendered prompt + agent name.
4. POST it through the tunnel to the node's local agent runner.
5. Stream `RunLine` events back; persist them to the underlying `runs`
   row; parse findings out of structured-output lines.
6. On run completion, return `DispatchResult{OutputLines, FindingsJSON,
   StructuredResult}`.

The mTLS handshake is the security boundary. Tunnel certs are rotated per
deployment via the install package (`controlplane/packaging/packaging.go`).
A node without a valid cert pair cannot complete the handshake.

### 7.3 Jobs runtime dispatcher (pull-mode)

Some nodes don't have an inbound-tunnel path (deep NAT, captive air-gap).
They run `okesu-jobs` which long-polls the CP for work. Dispatch flow:

1. Resolve node, check `nodes.preferred_dispatch == 'jobs'` or tunnel
   absent + jobs heartbeat fresh.
2. Insert a `runs` row with status `pending` and `dispatch_kind='jobs'`.
3. Insert into `jobs_queue` with the rendered prompt.
4. The node's jobs runtime polls, claims, executes, posts back outputs.
5. The dispatcher waits on a channel signalled by the jobs ingest path.

Failure mode: jobs runtime not heartbeating recently → fail same as
no-tunnel.

### 7.4 Federated dispatcher (`cp: <child_id>`)

Steps that target a federated child CP. Parent calls the child's federation
write proxy (`controlplane/federation/aggregator/aggregator.go`) which
forwards the run-create as if locally launched. The child executes, the
parent stitches results back into its orchestration_steps row. Wire format:

```
POST  /api/v1/federation/proxy/runs
Body: { agent, prompt, node, cp_run_context: {parent_run_id, parent_step_id} }
```

The child's response carries the run_id local to the child. The parent
records `cp_instance_id = <child>` on the step row so the run-detail UI can
tag the step card with a CP-source chip.

For S3 dead-drop child CPs (no inbound reachability), the wire format is
the same payload but written to a `cp/<child>/inbox/<key>.json` object;
the child's S3 reader picks it up and runs locally. Results travel back via
`cp/<child>/outbox/<key>.json`. See `docs/s3-transport.md` for the full
S3 transport contract.

### 7.5 Cross-CP node auto-discovery

When a step pins `node:` but omits `cp:`, the engine tries local first,
then queries every healthy federated peer for a connected node with that
name. If exactly one peer has it, the step dispatches there automatically.
Multiple peers with the same name → the engine refuses to guess; operator
must pin `cp:` explicitly. This makes operator-authored YAML portable across
federated topologies without hardcoding instance ids.

### 7.6 Fan-out (`nodes:` list)

`nodes:` parallelizes one step across N hosts. The dispatcher launches one
underlying `runs` row per host, waits for all to terminate, then merges:

- `step.findings` is the flat union across all hosts.
- `step.byNode["<host>"]` is the per-host slice with the same shape as a
  single-node binding.
- `step.output` carries a `── <hostname> ──` divider before each host's
  section so `tail` filters still produce readable text.
- Step status is `completed` only if every node completed; `failed` if
  any failed. `continue_on_error` applies to the whole step, not per host
  (a per-host opt-out is reserved for Phase E).

Fan-out limits: today the engine launches all N dispatches concurrently
without a cap. If you need bounded concurrency for a 10k-host hunt, add a
worker-pool wrapper around `dispatcher.Dispatch`.

---

## 8. Templating engine and expression evaluator

Single file: `controlplane/orchestrator/template.go` (~960 lines). It is
intentionally not a third-party template library — pure Go, no eval, no
sandbox holes.

### 8.1 What renders templates

Three call sites:

1. **Step prompt.** `RenderPrompt(step.Prompt, env)` (template.go:48,
   exposed as `Render`). Substitutes `{{...}}` with `Eval` results.
   Result is plain text.
2. **`when:` expressions.** `EvalBool(step.When, env)` — same evaluator,
   coerced to bool.
3. **`trigger.filter` expressions.** `EvalBool(filter, env)` against
   `FindingPayload` or `EnrichmentPayload` env.

`data:` block param values templated: any string param ending in `{{...}}`
is rendered against the current env before the data resolver dispatches.

### 8.2 The Env

`Env` is `map[string]any`. The engine populates:

```go
env["trigger"]  = TriggerPayload    // see §6.6
env["data"]     = DataBindings      // resolved data: block (§9)
env["<step_id>"] = StepBindings{    // populated as steps complete
    Status:   "completed" | "failed" | "skipped",
    Output:   string,                // last 100KB
    Findings: []map[string]any,      // parsed from JSONL output
    Result:   map[string]any,        // attributes from orchestration_result finding
    Host:     string,
    CP:       string,
    Nodes:    []string,              // fan-out only
    ByNode:   map[string]StepBindings, // fan-out only
}
```

Path access uses dot notation: `step.findings.first.path` walks the
binding. Numeric indexes work: `step.findings.0.path`.

### 8.3 Expression grammar

```
expr        = orExpr
orExpr      = andExpr ( '||' andExpr )*
andExpr     = notExpr ( '&&' notExpr )*
notExpr     = '!'? cmpExpr
cmpExpr     = sumExpr ( ( '==' | '!=' | '<' | '<=' | '>' | '>=' | 'in' | 'contains' ) sumExpr )?
sumExpr     = primary
primary     = literal | path | '(' expr ')'
literal     = number | string | bool | nil | listLiteral
path        = ident ( '.' ident | '[' index ']' )*
index       = string | number
listLiteral = '[' (literal (',' literal)*)? ']'
pipe        = primary ('|' filter)*
filter      = ident ( '(' arglist ')' )?
```

Parser: `nPath` (template.go:281), `nPipe` (284), the rest in `Parse()` /
`parseExpr()`. Don't ship a full grammar tool — recursive descent in 200
lines is plenty.

### 8.4 Filters

Implemented in `applyFilter` (template.go:648):

| Filter | Args | Behaviour |
|---|---|---|
| `first` | none | first element of array |
| `last` | none | last element |
| `length` | none | `len(arr)` or `len(str)` |
| `head(N)` | int | first N elements / chars / lines |
| `tail(N)` | int | last N |
| `json` | none | JSON-encode (use sparingly) |
| `where(<key>=<value>)` | k=v | filter list by field equality |
| `any(<key>=<value>)` | k=v | true if any element matches |

Path-style sugar: `first`, `last`, `length`, numeric index work as path
segments — `step.findings.first.path` instead of
`(step.findings | first).path`.

### 8.5 Equality semantics

Equality is *zeroish-tolerant*: `nil == ''`, `nil == 0`, `nil == false`,
`nil == []` all return true. This makes
`finding.attributes.sha256 != ''` correctly false for findings that don't
carry the attribute, instead of always-true. Implement this in `eqlVals`
(template.go) — every comparison must coerce nil to its type's zero before
equality.

Numeric comparison: when both sides parse as numbers (via `strconv.Atoi` /
`ParseFloat`), compare numerically. Otherwise fall back to string compare.
This makes `finding.attributes.process_pid > 0` work whether the attribute
is JSON-encoded as a number or a digit string.

### 8.6 `contains`

Polymorphic on the LHS:

- string LHS: `strings.Contains(lhs, rhs)`
- array LHS: element membership

Both directions are useful: `finding.tags contains 'production'` and
`finding.title contains 'mimikatz'`.

---

## 9. Data resolver

A `data:` block on a step lets the spec author pull CP-side state into the
prompt without baking a curl into the agent. The engine resolves each entry
*before* dispatch, binds the result to `env["data"][bind_name]`, and snapshots
the resolved JSON to `orchestration_steps.data_snapshot` for replay.

### 9.1 Registered queries

| Query | Params | Returns |
|---|---|---|
| `findings.list` | `state`, `severity`, `agent`, `host`, `category`, `tag`, `since_ms`, `until_ms`, `limit`, `offset` | array of finding rows |
| `findings.summary` | none | per-CP rollup with 24h trend |
| `findings.history` | `finding_id` (req) | edit history for one finding |
| `orchestration-runs.list` | `status`, `trigger_kind`, `since`, `since_ms`, `limit`, `offset` | array of run rows |
| `nodes.list` | `limit`, `offset` | array of node rows |
| `agents.list` | `limit`, `offset` | array of (daemon agent, host) rows |
| `iocs.lookup` | `kind` (req), `value` (req) | normalized IOC + attribution |

Implementation: `controlplane/api/data_resolver.go`. Each handler is a typed
function registered at construction:

```go
func NewDataResolver(store *db.Store) *DataResolver {
    r := &DataResolver{store: store, handlers: map[string]Handler{}}
    r.registerHandler("findings.list", findingsListQuery)
    r.registerHandler("findings.summary", findingsSummaryQuery)
    // ...
    return r
}

type Handler func(ctx context.Context, store *db.Store, params map[string]any) (any, error)

func (r *DataResolver) Resolve(ctx context.Context, query string, params map[string]any) (any, error) {
    h, ok := r.handlers[query]
    if !ok { return nil, fmt.Errorf("unknown query %q", query) }
    return h(ctx, r.store, params)
}
```

### 9.2 Adding a new query

1. Write the handler. Validate its own params (return `fmt.Errorf` on bad
   input — the engine surfaces it as the step's error).
2. Register at `NewDataResolver`.
3. Add the query name to `SupportedQueries()` so spec validation accepts
   it in `data.<bind>.query`.
4. Document in the table above + in `orchestrations.md`.

### 9.3 Replay invariant

`data_snapshot` is the source of truth for "what did the agent see at run
time". The run-detail UI renders it verbatim under each step's collapse
section. The engine never re-resolves data on read — even if findings have
moved on, the captured snapshot is the canonical record. This matters for
audit ("what input made the agent decide to suppress this?") and for replay.

---

## 10. Engine-applied actions

The action protocol is the second-most important piece of the orchestrator
after the engine itself. It's how T1 chains *actually resolve* findings
instead of just emitting briefs.

### 10.1 Wire format

The agent emits a final JSONL line:

```jsonl
{"type":"finding","category":"orchestration_result","title":"...","severity":"INFO","attributes":{
  "verdict":"noise",
  "actions":[
    {"kind":"update_finding_status","finding_id":245,"status":"false_positive","reason":"..."},
    {"kind":"add_finding_tag","finding_id":245,"tag":"auto-triaged"}
  ]
}}
```

The engine parses `attributes.actions[]`, validates each action against the
step's `actions:` allowlist, and applies allowed entries via DB methods.

### 10.2 Action kinds

| Kind | Class | Effect |
|---|---|---|
| `update_finding_status` | modify | Set triage state. Allowed: `open`, `acknowledged`, `investigating`, `resolved`, `false_positive`, `wontfix`, `suppressed`. |
| `add_finding_tag` | modify | Append a tag. Idempotent. |
| `remove_finding_tag` | modify | Drop a tag. No-op when absent. |
| `set_finding_severity_override` | modify | Set operator-severity (CRITICAL/HIGH/MEDIUM/LOW/INFO). |
| `link_run_to_finding` | create | Insert finding_run_links row. |
| `escalate` | modify (default class for unknown) | Soft signal — operator review requested. |
| `reflect_with_lessons` | create | Append entries to `agent_lessons`. |
| `enrich_ioc` | enrich | Trigger vendor adapters, cache in `ioc_enrichments`. |

### 10.3 Action classes and auto-approve

Each kind belongs to a class:

| Class | Examples |
|---|---|
| read | (none yet — reserved for future "fetch a row" actions) |
| enrich | `enrich_ioc` |
| fetch | (reserved for inbound content fetch) |
| create | `link_run_to_finding`, `reflect_with_lessons` |
| modify | `update_finding_status`, `add_finding_tag`, `remove_finding_tag`, `set_finding_severity_override` |

CP settings carry a per-class auto-approve toggle:

```yaml
policy:
  auto_approve:
    read:   true
    enrich: true
    create: false
    modify: false
```

When *every* action kind in a step's allowlist belongs to an auto-approved
class, the engine bypasses the approval gate for that step. A mixed
allowlist keeps the gate. Default: nothing is auto-approved (every gate
fires). Unknown kinds map to `modify` (most-restrictive).

### 10.4 Persistence — every action audits itself

Every applied action writes:

1. A `finding_edits` row with `field`, `old_value`, `new_value`, `reason`,
   `orchestration_run_id`, `orchestration_step_id`. The finding-detail
   History panel reads this — "auto-resolved by run #42 / step:verify".
2. A `finding_run_links` row when `link_run_to_finding` runs (or
   automatically alongside any modify action — make this explicit in the
   spec; current code links only when the kind is requested).

Lessons add a row in `agent_lessons`. The daimon prepends the 10 newest
(200 chars each) to its system prompt on the next tick — implementation in
the agent runtime, not the engine.

### 10.5 Implementation skeleton

```go
type FindingActionApplier struct {
    store  *db.Store
    policy AutoApprovePolicy
}

func (a *FindingActionApplier) Apply(ctx context.Context, run *RunRecord, step Step, action Action) error {
    if !IsAllowedActionKind(action.Kind) {
        return fmt.Errorf("unknown action kind %q", action.Kind)
    }
    if !step.AllowsAction(action.Kind) {
        log.Printf("rejected: step %s does not allow %s", step.ID, action.Kind)
        return nil  // silent drop, never abort the step
    }

    switch action.Kind {
    case "update_finding_status":
        return a.UpdateFindingStatus(action.FindingID, action.Status, action.Reason, run, step)
    case "add_finding_tag":
        return a.AddFindingTag(action.FindingID, action.Tag, run, step)
    // ... one case per kind
    }
    return nil
}
```

Failure to apply one action does not abort the step; log and continue. The
engine's contract is "applied or audited as rejected" — never silent loss.

### 10.6 Adding a new action kind

1. Add the kind to `AllowedActionKinds` in `orchestrator/actions.go`.
2. Classify it (read/enrich/fetch/create/modify) in `action_class.go`.
3. Implement the applier method on `FindingActionApplier` (or whatever
   target the action mutates). Always write a `finding_edits` row (or the
   relevant audit table).
4. Document the wire format in `orchestrations.md` and the agent-author
   protocol in `agents/_orchestration-actions.md`.
5. Add a unit test that exercises both the allowlist gate and the
   auto-approve class behaviour.

---

## 11. Approval gates

A step with `approval: required` pauses the run before dispatching. State:

- Engine sets `orchestration_runs.status = 'approval_required'` and
  `current_step_id = <step_id>`.
- The step row exists in status `pending` (no run_id yet — nothing
  dispatched).
- The engine returns from `Run()`. The coordinator does not retry.
- Operator clicks Approve in the UI →
  `POST /api/orchestration-runs/{id}/steps/{stepID}/approve`.
- Handler stamps `orchestration_steps.approved_at = now`,
  `approved_by = <email>`, sets run status back to `running`, and re-kicks
  the engine via the coordinator.
- Engine resumes from the approved step.

Approvals are scoped per step. Approving step N does not auto-approve any
later gated step — each one stops separately. The auto-approve policy
(§10.3) bypasses the gate when the step's actions are all in
auto-approved classes; this is the only escape hatch.

The same gate code is reused on standalone agent Runs: a Run can be marked
`approval_required` from the Run dialog before destructive actions
(containment, deploy rollback). The shared mechanism lives in `runs.go`
and is referenced by both the orchestration engine and the run handler.

---

## 12. Meeting steps

`controlplane/orchestrator/meeting.go` (~210 lines).

### 12.1 Spec

```yaml
- id: discuss
  kind: meeting
  meeting:
    participants: [investigator, threat-hunter, malware-analyst]
    synthesizer: incident-responder
  prompt: |
    A critical finding has surfaced on `{{trigger.host}}`.
    Discuss the incident from your perspective.
  war_bridge: true     # optional
```

### 12.2 Execution

```go
func runMeetingStep(ctx context.Context, env Env, step Step, dispatch DispatchFn) (*StepResult, error) {
    convo := []string{}                    // accumulated outputs
    for _, agent := range step.Meeting.Participants {
        prompt := buildParticipantPrompt(step.Prompt, agent, convo)
        out, err := dispatch(ctx, agent, prompt)
        if err != nil { return nil, err }
        convo = append(convo, fmt.Sprintf("## %s\n\n%s", agent, out))
    }

    synthPrompt := buildSynthesizerPrompt(step.Prompt, convo, step.WarBridge)
    out, err := dispatch(ctx, step.Meeting.Synthesizer, synthPrompt)
    if err != nil { return nil, err }

    // Synthesizer emits a finding with subtype: meeting_minutes.
    // war_bridge=true tags the finding "war-bridge".
    return parseStepResult(out), nil
}
```

Each participant sees prior participants' outputs as a "## Conversation so
far" block. The synthesizer sees the full conversation and emits a
structured `meeting_minutes` finding (agenda, positions, action_items,
decision).

### 12.3 War bridges

`war_bridge: true` instructs the synthesizer to tag the resulting finding
`war-bridge`. The dashboard renders a red banner listing active war-bridge
findings — operators can drill in immediately. The query that drives the
banner is `GET /api/findings/war-bridge`.

Pair with a `severity == 'CRITICAL'` trigger filter so war bridges fire
only on alarms that warrant the all-hands response.

### 12.4 Timeout semantics

The step's `timeout` is the budget for the *whole* conversation, not per
turn. Implement it as a single context with deadline encompassing all
participants + synthesizer. If participant 1 takes 18m of a 20m budget,
the synthesizer has 2m left.

---

## 13. Federation

Orchestrations live on the CP that authored them. From the Global CP's
create dialog, the operator picks where the orchestration should live —
Global itself, or any healthy federated child CP. Once saved, the Global
library lists orchestrations from every CP in the federation, the same
way Daimons and Findings already federate.

### 13.1 List federation

`GET /api/orchestrations` on the parent fans out to every healthy child
via the federation aggregator (`controlplane/federation/aggregator/`),
merges results, tags each row with `cp_source` (instance_id +
display_name). Same pattern for `GET /api/orchestration-runs` and detail
endpoints (which accept `?cp=<instance_id>` to pin one child).

### 13.2 Cross-CP step dispatch

Steps with `cp: <child_id>` route via `federatedDispatcher` (§7.4). The
parent posts the run-create payload through the federation write proxy;
the child runs the step as if locally launched; the parent stitches the
result back into its `orchestration_steps` row. The run-detail UI shows
a single timeline with each step card carrying a CP-source chip.

### 13.3 Trigger evaluation is local

Each CP evaluates its own trigger filters against its own event stream.
A finding-triggered orchestration on east only fires for findings
projected on east. To react across the whole fleet, install the
orchestration on every CP that should react (or use a parent-side
supervisor daimon that watches federated event projections — see the
`cross-cp-ioc-pattern-supervisor` agent for the pattern).

A planned task (#155) adds federation-forwarded triggers — install the
orchestration once on the parent, the parent forwards trigger events
from children. Not built yet.

### 13.4 S3 dead-drop transport

For child CPs with no inbound reachability (offline, deep NAT), the same
federation write proxy serializes the step dispatch to an S3 object under
`cp/<child>/inbox/<key>.json`. The child's S3 reader picks it up at the
next poll. Results flow back via `cp/<child>/outbox/<key>.json`.

This adds 30-60s of latency per step round-trip (poll cadence) but
preserves the operator's single-pane-of-glass run view. See
`docs/s3-transport.md` for the full transport contract and key/object
layout.

---

## 14. Visual editor round-trip

`web/src/components/OrchestrationEditorCanvas.tsx`. Two modes — visual
canvas (React Flow) and YAML mode. Operators can switch freely; both edit
the same source of truth.

### 14.1 YAML → canvas

`parseSpecYAML(yaml)` (≈line 193):
1. Split frontmatter from operator-notes body at the closing `---`.
2. YAML.parse the frontmatter into `Spec`.
3. For each step, build a `StepNode` (React Flow node) with x/y derived
   from index (sequential layout) or from a `// pos: x,y` comment if
   present.
4. Build edges between consecutive steps; insert branch edges on `when`
   conditions.

### 14.2 Canvas → YAML

`serializeToYAML(canvasState)` (≈line 269):
1. Walk the node graph in execution order.
2. Reconstruct the `Spec` object from node fields.
3. YAML.stringify with stable key order so diffs stay clean.
4. Append the operator-notes body verbatim from canvas state.

Round-trip invariant: parse → serialize → parse must yield byte-identical
canvas state for any spec the validator accepts. Test this with golden
fixtures — spec.yaml → spec.json → spec.yaml.

### 14.3 Inspector panel

The right-side inspector edits the focused step's fields:
- Common: id, kind, agent, prompt, node/nodes, cp, timeout,
  continue_on_error, when, approval, actions, data.
- Meeting-only: meeting.participants, meeting.synthesizer, war_bridge.

Each field change updates canvas state, which immediately re-serializes
the YAML mode buffer so the operator can switch tabs and see the change.

### 14.4 Palette

The left palette lists agents (loaded from `GET /api/agents`) and step
templates. Drag-drop creates a new step node with the selected agent
pre-filled.

---

## 15. API surface (HTTP)

Every route the orchestrator exposes. Auth: all admin endpoints require
the `admin` role; read endpoints accept both `admin` and `operator`.

| Method | Path | Handler | Notes |
|---|---|---|---|
| GET | `/api/orchestrations` | `FederatedOrchestrationsList` | Federates by default on parent. Pass `?cp=<id>` to pin. |
| GET | `/api/orchestrations/{id}` | `FederatedOrchestrationDetail` | |
| POST | `/api/orchestrations` | `FederatedOrchestrationCreate` | Body: `{spec_yaml, target_cp_instance_id?}`. |
| PUT | `/api/orchestrations/{id}` | `FederatedOrchestrationUpdate` | Body: `{spec_yaml}`. Accepts `?cp=` for federated edit. |
| DELETE | `/api/orchestrations/{id}` | `FederatedOrchestrationDelete` | |
| POST | `/api/orchestrations/{id}/run` | `FederatedOrchestrationRunCreate` | Body: `{inputs: {...}}`. Returns `{run_id}`. |
| GET | `/api/orchestration-runs` | `FederatedOrchestrationRunsList` | Federates. Filters: `status`, `orchestration_id`, `since_ms`, `limit`, `offset`. |
| GET | `/api/orchestration-runs/{id}` | `FederatedOrchestrationRunDetail` | Returns run + steps + each step's `data_snapshot`. |
| POST | `/api/orchestration-runs/{id}/cancel` | `FederatedOrchestrationRunCancel` | |
| POST | `/api/orchestration-runs/bulk-cancel` | `FederatedOrchestrationRunsBulkCancel` | Body: `{run_ids: [...]}`. |
| POST | `/api/orchestration-runs/bulk-retry` | `FederatedOrchestrationRunsBulkRetry` | Re-runs failed runs. |
| POST | `/api/orchestration-runs/{id}/steps/{stepID}/approve` | `FederatedOrchestrationStepApprove` | Releases approval gate. |

All routes registered in `controlplane/server.go` around line 1259.

### 15.1 Run-detail response shape

```jsonc
{
  "run": {
    "id": 42,
    "orchestration_id": 7,
    "status": "completed",
    "trigger_kind": "finding",
    "trigger_payload": { ... },
    "started_at": "...", "ended_at": "...",
    "started_by": "...", "error": null,
    "cp_source": { "instance_id": "global", "display_name": "Global CP" }
  },
  "steps": [
    {
      "step_id": "triage", "step_idx": 0,
      "status": "completed",
      "agent": "investigator", "node": "web-01",
      "rendered_prompt": "...",
      "result_json": { ... },
      "output_summary": "... last 4KB ...",
      "data_snapshot": { ... },           // resolved data: block
      "actions": [                         // applied + rejected
        { "kind": "update_finding_status", "applied": true, "reason": "..." },
        { "kind": "set_severity_override", "applied": false, "rejected_reason": "not in allowlist" }
      ],
      "started_at": "...", "ended_at": "...",
      "approved_at": null, "approved_by": null,
      "cp_instance_id": "global", "node_id": 17, "run_id": 1234
    }
  ]
}
```

---

## 16. Spec validation

`controlplane/orchestrator/spec.go:Spec.Validate()`. Run on every create
and update — reject malformed specs at the boundary so the engine never
sees them. Validation errors are 400 with a precise message naming the
field and the allowed values.

| Check | Rejected if |
|---|---|
| `name` | empty, fails regex `^[a-z][a-z0-9-]*$`, length > 64 |
| `description` | empty |
| `version` | not 1 |
| `trigger.on` | not in {manual, cron, finding, ioc_enriched} |
| `trigger.cron` | empty when `on==cron`; fails parser; uses unsupported chars |
| `trigger.filter` | empty when `on==finding|ioc_enriched`; fails parser |
| `inputs[i].type` | not in {string, int, bool} |
| `inputs[i].enum` | non-list, contains non-literal |
| `steps` | empty list |
| `steps[i].id` | empty, duplicate, fails regex `^[a-z][a-z0-9_-]*$` |
| `steps[i].kind` | not in {agent, meeting} |
| `steps[i].agent` | empty (when kind==agent) |
| `steps[i].meeting` | empty when kind==meeting; participants empty; synthesizer empty |
| `steps[i].node` | both `node` and `nodes` set |
| `steps[i].nodes` | not a list |
| `steps[i].approval` | set to anything other than `required` |
| `steps[i].actions[k]` | not in `AllowedActionKinds` |
| `steps[i].data[bind].query` | not in `SupportedQueries()` |
| `steps[i].when` | fails expression parser |
| `steps[i].prompt` | references a later step's binding (forward ref) |

Two-pass validation: pass 1 builds the step ID set; pass 2 walks templates
looking up `{{stepN.something}}` references — N must be a step *before*
the current one. This catches typos and copy-paste mistakes early.

---

## 17. Examples library

`examples/orchestrations/` ships a curated set of T1 (autonomous) and
T2 (approval-gated) chains. Each is a `.md` file with the YAML frontmatter
+ operator notes. README indexes them.

T1 conventions:
- Default trigger: `finding` with a tight filter (severity, agent, host).
- Default-deny actions; every state-mutating step lists its `actions:`
  allowlist.
- Whitelist-only auto-actions (no `rm -rf`, no unconditional restarts).
- Tag-based escape hatch: hosts opt out via `/etc/okesu/labels`
  (`noremediate=disk`, `noisolate=yes`, `production=true`).
- Silent on success — only emit findings when something needs operator
  attention.

T2 conventions:
- Default trigger: `finding` with a `severity in ['HIGH','CRITICAL']`
  filter or a cron schedule for sweeps.
- `approval: required` on the final destructive step.
- Always emit a one-page operator brief at run end.

### 17.1 Installing

```bash
for f in examples/orchestrations/t1-*.md examples/orchestrations/t2-*.md; do
  body=$(python3 -c "import json,sys; print(json.dumps({'spec_yaml': open('$f').read()}))")
  curl -sk -b "$COOKIES" -X POST "$CP_URL/api/orchestrations" \
    -H 'Content-Type: application/json' -d "$body"
done
```

Or paste a YAML into the visual editor's YAML mode. The editor
round-trips cleanly back to canvas.

### 17.2 Authoring a new orchestration

1. Pick a trigger. Manual for ops-driven workflows; finding for autotriage;
   cron for sweeps; ioc_enriched for vendor-driven escalation.
2. Sketch the chain on paper. Each step is one agent doing one thing.
3. Decide where state mutates — those steps need `actions:`.
4. Write the YAML. Validate with the visual editor or
   `okesu orchestration validate <file>` (CLI command if your project
   ships one — current code wires this only in the API).
5. Test on a stub finding via manual run + canned `inputs:`.
6. Wire up the trigger filter against real findings.
7. Submit via the API or paste into the editor.

---

## 18. Testing strategy

Three test layers. All in `controlplane/orchestrator/*_test.go` and
`controlplane/api/*_test.go`.

### 18.1 Engine-level (no HTTP)

`engine_test.go`:
- `TestEngine_HappyPath` — sequential 3-step run, all complete.
- `TestEngine_HaltOnError` — failing step halts run, later steps skipped.
- `TestEngine_ContinueOnError` — failing step doesn't halt; downstream
  branches on `step.status == 'failed'`.
- `TestEngine_ApprovalGate` — step with `approval: required` parks run;
  Approve releases.
- `TestEngine_TriggerPayloadInTemplate` — `{{trigger.host}}` resolves.
- `TestEngine_DataParamsRendered` — `data:` block params with templates
  resolve before query dispatch.
- `TestFanOut_PersistsPerHostProgress` — `nodes:` step writes per-host
  state, merges into byNode binding.

Stub the dispatcher with a fake that returns canned `DispatchResult`
payloads. Stub the data resolver with a fake handler that records calls.
This isolates the engine from network and DB.

### 18.2 Trigger-level

`triggers_test.go`:
- `TestCronParse` — every form (`*`, `30`, `1,3,5`, `1-5`, `*/15`,
  `9-17/2`); reject `?`, `L`, `W`, `#`.
- `TestNextCronFire` — golden table of (expr, after, expected_next).
- `TestEvaluateFindingFilter` — precedence (`&&` over `||`), in-list,
  contains, numeric coercion.

`triggers_enrichment_test.go`:
- `TestEnrichmentFilter` — same eval path with the enrichment payload.

### 18.3 Action applier

`actions_test.go`:
- `TestParseActions` — well-formed and malformed payloads.
- `TestActionApplier_AllowlistGate` — kind not in step's allowlist
  silently dropped + logged; gate-gates fire.

`action_class_test.go`:
- `TestAutoApprovePolicy` — every-kind-auto-approved bypasses gate;
  mixed allowlist still gates.

### 18.4 Meeting

`meeting_test.go`:
- `TestMeetingStepSequence` — N participants run in declared order;
  each sees prior outputs; synthesizer sees full conversation.

### 18.5 End-to-end (HTTP)

`controlplane/api/orchestrations_test.go`:
- POST create + validation rejection.
- POST run + run-detail response shape.
- Approval flow over HTTP.
- Cancel mid-run.
- Federated proxy (with a fake child CP).

### 18.6 Golden YAMLs

Keep a `testdata/specs/` directory of canonical specs. Each is a roundtrip
test: parse → validate → serialize → parse → byte-identical. This catches
regressions in the YAML serializer or the visual editor.

---

## 19. Recommended build order

If you are porting orchestrations into a fresh project, build in this
order. Each step ships a working slice that the next builds on.

1. **Migrations + schema.** Land all of §3 first. Don't try to evolve
   the schema midway — get the columns right up front. (3 days)
2. **Spec parser + validator.** YAML → `Spec` struct. The validator from
   §16. Round-trip tests. (2 days)
3. **Templating engine.** §8. Filters, expression evaluator, `Eval`,
   `EvalBool`, `Render`. Hundreds of unit tests, no engine wiring yet.
   (3 days)
4. **Engine skeleton — manual trigger, local dispatch only.** §5 + the
   localDispatcher half of §7. No remote nodes, no federation, no
   actions. Just sequential CP-internal step execution end-to-end with
   persistence. (4 days)
5. **API surface — create/list/run/detail.** §15 minus federation, minus
   approve. Operator can submit a YAML and watch it run. (2 days)
6. **Approval gates.** §11. Park run, approve endpoint, resume. (1 day)
7. **Action applier.** §10. Start with `update_finding_status` and
   `add_finding_tag`. Wire `finding_edits`. (3 days)
8. **Tunnel dispatcher.** §7.2. mTLS reverse tunnel, registry, dispatch
   to remote node. This is the biggest single chunk — do it after the
   engine works locally. (1-2 weeks depending on existing tunnel infra)
9. **Triggers — cron.** §6.2. Scheduler tick loop, parser, dedup. (2 days)
10. **Triggers — finding.** §6.3. Hook into the event pipeline, evaluate
    filters, fire runs. (2 days)
11. **Data resolver block.** §9. Start with `findings.list` and
    `findings.summary`. Add `data_snapshot` persistence. (3 days)
12. **Visual editor.** §14. YAML ↔ canvas round-trip. Inspector. Save +
    load. (1-2 weeks)
13. **Fan-out (`nodes:`).** §7.6. (3 days)
14. **Federation.** §13. Proxy dispatch, list federation, federated
    detail. (1 week — assumes federation aggregator already exists)
15. **Meeting steps.** §12. (3 days)
16. **More action kinds.** Roll out the rest of the allowlist —
    `set_severity_override`, `link_run_to_finding`, `escalate`,
    `reflect_with_lessons`, `enrich_ioc`. Each is a couple of days.
17. **Auto-approve policy.** §10.3. (2 days)
18. **War bridges.** §12.3 + dashboard banner. (2 days)
19. **More data queries.** Whatever your domain needs.
20. **Examples library.** Author 5–10 T1/T2 chains for your domain.

A 2-engineer team can ship steps 1–8 in roughly 4 weeks, the full Phase
A–D feature set in 8–10 weeks. Phase E (true parallel DAG, conditional
branches with multiple downstream paths, role/os selectors, `cp: "*"`
fan-out) is roadmap, not built.

---

## 20. Migration / rollout considerations

**Schema rollout.** Migrations are idempotent; the runner re-applies on
schema bumps. Roll forward in CI before deploying. Postgres needs the
WHERE-partial-index variant; SQLite gets the same index without WHERE.

**Trigger backfill.** When you first enable cron triggers on existing
orchestrations, set `last_fired_at = now` on every cron-triggered row to
prevent a startup catch-up storm. Same for finding triggers — they're
idempotent (each finding fires once), but if you turn on a high-volume
filter against a multi-month finding backlog, you'll create runs for the
full backlog. Best practice: enable triggers on new orchestrations, not
on retroactive enables.

**Action allowlist evolution.** Adding a new kind is forward-compatible:
old specs don't list it, so it's never requested. Removing a kind
breaks specs that reference it — bump the spec version and reject old
specs at the validator. Unknown kinds always map to class `modify` so
they don't auto-apply.

**Visual editor versions.** The YAML round-trip is a contract. Pin the
serializer to stable key order, pin the parser to ignore unknown fields
(or warn but not fail) so old editor versions can read newer specs.
Strict-validate at the API boundary.

**Federation upgrade.** A child CP running an older orchestrator version
must still accept federation step proxy calls. Keep the proxy wire format
backwards-compatible: never remove fields, always add. Bump the API
version path (`/api/v1/...` → `/api/v2/...`) for breaking changes.

**Tunnel cert rotation.** Tunnel certs are pinned per-deployment. When you
rotate, distribute new certs via the install package, then ratchet the CP
to require the new CA after a grace period. Failed handshakes during the
grace are logged but not fatal.

---

## 21. Pointers

| Concern | Canonical file |
|---|---|
| Spec language reference | [`docs/orchestrations.md`](./orchestrations.md) |
| Spec parser + validator | `controlplane/orchestrator/spec.go` |
| Engine | `controlplane/orchestrator/engine.go` |
| Templating | `controlplane/orchestrator/template.go` |
| Triggers | `controlplane/orchestrator/triggers.go` + `controlplane/api/orchestrations.go` |
| Data resolver | `controlplane/api/data_resolver.go` |
| Action applier | `controlplane/api/orchestration_actions.go` + `controlplane/orchestrator/actions.go` |
| Meeting steps | `controlplane/orchestrator/meeting.go` |
| Tunnel server | `controlplane/tunnel/server.go` |
| Federation aggregator | `controlplane/federation/aggregator/aggregator.go` |
| HTTP routes | `controlplane/server.go` (search "orchestration") |
| Visual editor | `web/src/components/OrchestrationEditorCanvas.tsx` |
| Examples | `examples/orchestrations/` |
| Migrations | `controlplane/db/migrations/{sqlite,postgres}/0{23,24,27,28,35,38,49}_*.sql` |

For the agent-side protocol (how an agent emits findings and actions), see
`agents/_orchestration-data.md` and `agents/_orchestration-actions.md`.
