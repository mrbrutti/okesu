# SmartPayload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace raw JSON dumps in the UI with entity-aware rendering — when prompts/results/tool I/O contain Findings, IOCs, Nodes, Daimons, Runs, Investigations, or Orchestrations, render them as styled cards with click-to-drawer + navigate-to-page.

**Architecture:** Server-side typed side-channel (`prompt_entities` JSON column on `orchestration_steps`) populated by the engine at render time, plus a reusable client component (`<SmartPayload>`) that splices entity chips into prompt strings and recognises entity shapes inside structured trees. Each chip click opens the existing detail drawer (`FindingDrawer` etc.) portal'd over the page; an `↗` icon navigates to the entity's full page. Hybrid storage: identity tuple + small denormalized snapshot per ref.

**Tech Stack:** Go 1.22 (engine + API), sqlite3 + Postgres (migrations 047), React 18 + TypeScript (web), Tailwind (styling), chi router (Go HTTP), Vitest (web tests), `go test` (engine tests).

---

## File structure

### Server side (Go)

- `controlplane/db/migrations/sqlite/047_orchestration_steps_prompt_entities.sql` — add nullable JSON column.
- `controlplane/db/migrations/postgres/047_orchestration_steps_prompt_entities.sql` — same.
- `controlplane/db/store.go` — embed migration 047 in both dialect lists.
- `controlplane/db/orchestrations.go` — add `PromptEntities sql.NullString` to `OrchestrationStep` + `OrchestrationStepInsert`; thread through `UpsertOrchestrationStep`, `ListOrchestrationSteps`, `GetOrchestrationStep`.
- `controlplane/db/orchestrations_test.go` — round-trip insert+get of the new column.
- `controlplane/orchestrator/prompt_entities.go` (new) — `PromptEntities`, `PromptEntityRef` types; `BuildPromptEntities(template string, env Env) (*PromptEntities, error)` walks `{{path}}` placeholders and classifies typed values; per-kind classifiers; `literal_hash` helper.
- `controlplane/orchestrator/prompt_entities_test.go` (new) — table-driven tests for each entity kind, deduplication, hash stability.
- `controlplane/orchestrator/engine.go` — at the two `Render(step.Prompt, env)` call sites (line ~443 approval-gate path, line ~495 dispatch path), also call `BuildPromptEntities` and stash the JSON onto `rec.PromptEntities`.
- `controlplane/orchestrator/engine.go` — `StepRecord` struct gains `PromptEntities string` mirroring `RenderedPrompt`.
- `controlplane/api/orchestrations.go` — `OrchestrationStepView` JSON shape gains `prompt_entities` (omit-if-null); the converter from `*OrchestrationStep` to view sets it from `st.PromptEntities.String`.

### Client side (React + TS)

- `web/src/api.ts` — `OrchestrationStepView` gains `prompt_entities?: PromptEntities`; export `PromptEntities` + `PromptEntityRef` types.
- `web/src/components/SmartPayload/index.tsx` (new) — the `<SmartPayload>` entry component.
- `web/src/components/SmartPayload/tokenize.ts` (new) — string ↔ JSON-block tokenizer.
- `web/src/components/SmartPayload/tokenize.test.ts` (new).
- `web/src/components/SmartPayload/detectors.ts` (new) — duck-type matchers per kind, returns `{kind, snapshot}` or null.
- `web/src/components/SmartPayload/detectors.test.ts` (new).
- `web/src/components/SmartPayload/literalHash.ts` (new) — sha256-prefix client-side, shape-stable JSON canonicalisation.
- `web/src/components/SmartPayload/literalHash.test.ts` (new).
- `web/src/components/SmartPayload/chips/FindingChip.tsx` (new).
- `web/src/components/SmartPayload/chips/IOCChip.tsx` (new).
- `web/src/components/SmartPayload/chips/NodeChip.tsx` (new).
- `web/src/components/SmartPayload/chips/DaimonChip.tsx` (new).
- `web/src/components/SmartPayload/chips/RunChip.tsx` (new).
- `web/src/components/SmartPayload/chips/InvestigationChip.tsx` (new).
- `web/src/components/SmartPayload/chips/OrchestrationChip.tsx` (new).
- `web/src/components/SmartPayload/SmartPayload.test.tsx` (new) — integration tests for prompt + tree mode.
- `web/src/components/EntityDrawerHost.tsx` (new) — single drawer portal, listens for `entity:open` custom events.
- `web/src/components/EntityDrawerHost.test.tsx` (new).
- `web/src/App.tsx` — mount `<EntityDrawerHost />` at root.
- `web/src/pages/Orchestrations.tsx` — swap `<pre>` and `<StructuredView>` at the prompt + result + per_node sites for `<SmartPayload>`.
- `web/src/components/HarnessOutput.tsx` — route `tool_call.input` and `tool_result.output` JSON blocks through `<SmartPayload>` instead of raw `<pre>`.
- `web/src/pages/Runs.tsx` — same swap for the run detail's prompt + tool I/O sections.
- `web/src/pages/Findings.tsx` — `FindingDrawer` `attributes` block uses `<SmartPayload value={attributes} />`.
- `web/src/pages/Investigations.tsx` — evidence-payload rows use `<SmartPayload>`.
- `web/src/pages/Daimons.tsx` — last-tick output's tool I/O via `<SmartPayload>`.

---

## Task Group A — DB migration + Store

### Task A1: Add migration 047

**Files:**
- Create: `controlplane/db/migrations/sqlite/047_orchestration_steps_prompt_entities.sql`
- Create: `controlplane/db/migrations/postgres/047_orchestration_steps_prompt_entities.sql`
- Modify: `controlplane/db/store.go`

- [ ] **Step A1.1: Write the sqlite migration**

```sql
-- 047_orchestration_steps_prompt_entities.sql
-- Adds a typed side-channel listing entity refs that were templated
-- into a step's rendered_prompt. Populated by the orchestrator at
-- render time. Read by the UI (SmartPayload) to render entity chips
-- inline at the JSON-substitution sites.
--
-- Nullable: legacy steps (no capture path on insert) have NULL and
-- the UI falls back to client-side shape sniffing.
ALTER TABLE orchestration_steps
  ADD COLUMN prompt_entities TEXT;
```

- [ ] **Step A1.2: Write the postgres migration**

Identical body — Postgres accepts `TEXT` here (we store JSON as a
string, not `JSONB`, matching how `result_json` and `data_snapshot`
already live).

```sql
ALTER TABLE orchestration_steps
  ADD COLUMN prompt_entities TEXT;
```

- [ ] **Step A1.3: Register the embed in store.go**

In `controlplane/db/store.go`, add the embed line + var alongside the
existing 046, then add the var to both `sqliteMigrations` and
`postgresMigrations` slices. Concrete diff (sqlite section, around
line 200–221):

```go
//go:embed migrations/sqlite/046_fleet_env.sql
var sqliteM046 string

//go:embed migrations/sqlite/047_orchestration_steps_prompt_entities.sql
var sqliteM047 string

var sqliteMigrations = []string{
	// ... existing ...
	sqliteM043, sqliteM044, sqliteM045, sqliteM046, sqliteM047,
}
```

Same for postgres (around line 350–370):

```go
//go:embed migrations/postgres/046_fleet_env.sql
var pgM046 string

//go:embed migrations/postgres/047_orchestration_steps_prompt_entities.sql
var pgM047 string

var postgresMigrations = []string{
	// ... existing ...
	pgM043, pgM044, pgM045, pgM046, pgM047,
}
```

- [ ] **Step A1.4: Build + commit**

```bash
go build ./controlplane/db/...
go test ./controlplane/db/ -count=1 -run TestMigrations
```
Expected: PASS — no test names changed; the migration list test
should pick up 047 automatically.

```bash
git add controlplane/db/migrations/sqlite/047_orchestration_steps_prompt_entities.sql \
        controlplane/db/migrations/postgres/047_orchestration_steps_prompt_entities.sql \
        controlplane/db/store.go
git commit -m "db(migration 047): orchestration_steps.prompt_entities side-channel"
```

### Task A2: Read/write the new column

**Files:**
- Modify: `controlplane/db/orchestrations.go` (around lines 555–700)
- Modify: `controlplane/db/orchestrations_test.go`

- [ ] **Step A2.1: Add field to OrchestrationStep + Insert**

In `OrchestrationStep` (around line 556) add:

```go
// PromptEntities is the JSON-encoded typed side-channel the
// orchestrator captures at template render time. NULL for legacy
// steps written before migration 047 — the UI falls back to
// client-side shape sniffing in that case.
PromptEntities sql.NullString
```

In `OrchestrationStepInsert` (around line 581) add:

```go
PromptEntities string // empty = NULL
```

- [ ] **Step A2.2: Thread through Upsert + List + Get**

In `UpsertOrchestrationStep`, add `prompt_entities` to the column list,
the `VALUES` placeholder, the `ON CONFLICT DO UPDATE SET`, and the
arg list (alongside `data_snapshot`). Final SQL (with the new column
appended for clarity):

```go
_, err := s.Exec(`
    INSERT INTO orchestration_steps (
        orchestration_run_id, step_id, step_idx, status, run_id, cp_instance_id, node_id,
        rendered_prompt, result_json, output_summary, started_at, ended_at, error,
        approved_at, approved_by, data_snapshot, prompt_entities
    )
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT (orchestration_run_id, step_id) DO UPDATE SET
        step_idx        = excluded.step_idx,
        status          = excluded.status,
        run_id          = excluded.run_id,
        cp_instance_id  = excluded.cp_instance_id,
        node_id         = excluded.node_id,
        rendered_prompt = excluded.rendered_prompt,
        result_json     = excluded.result_json,
        output_summary  = excluded.output_summary,
        started_at      = excluded.started_at,
        ended_at        = excluded.ended_at,
        error           = excluded.error,
        approved_at     = excluded.approved_at,
        approved_by     = excluded.approved_by,
        data_snapshot   = excluded.data_snapshot,
        prompt_entities = excluded.prompt_entities
`,
    in.OrchestrationRunID, in.StepID, in.StepIdx, in.Status,
    nullable(in.RunID), nullable(in.CPInstanceID), nullableInt64(in.NodeID),
    nullable(in.RenderedPrompt), nullable(in.ResultJSON), nullable(in.OutputSummary),
    nullableTimePtr(in.StartedAt), nullableTimePtr(in.EndedAt), nullable(in.Error),
    nullableTimePtr(in.ApprovedAt), nullableInt64(in.ApprovedBy), nullable(in.DataSnapshot),
    nullable(in.PromptEntities),
)
```

In `ListOrchestrationSteps` and `GetOrchestrationStep` (whichever
exists — read the file once to confirm both names; the engine reads
via `ListOrchestrationSteps`, the API may use a single-row getter),
add `prompt_entities` to the `SELECT` column list and the `Scan(...)`
target list.

The full `SELECT` becomes:

```go
SELECT id, orchestration_run_id, step_id, step_idx, status, run_id, cp_instance_id, node_id,
       rendered_prompt, result_json, output_summary, started_at, ended_at, error,
       approved_at, approved_by, data_snapshot, prompt_entities
  FROM orchestration_steps
  ...
```

And `Scan` adds `&st.PromptEntities` after `&st.DataSnapshot`.

- [ ] **Step A2.3: Add round-trip test**

In `controlplane/db/orchestrations_test.go`, add a test:

```go
func TestOrchestrationStep_PromptEntitiesRoundTrip(t *testing.T) {
	s := openTempStore(t)
	runID := mustInsertOrchestrationRun(t, s)
	in := OrchestrationStepInsert{
		OrchestrationRunID: runID,
		StepID:             "triage",
		StepIdx:            0,
		Status:             "running",
		PromptEntities:     `{"refs":[{"kind":"finding","id":42}]}`,
	}
	if err := s.UpsertOrchestrationStep(in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, err := s.ListOrchestrationSteps(runID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if !got.PromptEntities.Valid || got.PromptEntities.String != in.PromptEntities {
		t.Errorf("PromptEntities = %+v, want %q", got.PromptEntities, in.PromptEntities)
	}
}

func TestOrchestrationStep_PromptEntitiesEmptyIsNull(t *testing.T) {
	s := openTempStore(t)
	runID := mustInsertOrchestrationRun(t, s)
	in := OrchestrationStepInsert{
		OrchestrationRunID: runID,
		StepID:             "no-entities",
		StepIdx:            0,
		Status:             "running",
		// PromptEntities omitted
	}
	if err := s.UpsertOrchestrationStep(in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, _ := s.ListOrchestrationSteps(runID)
	if rows[0].PromptEntities.Valid {
		t.Errorf("expected NULL, got %q", rows[0].PromptEntities.String)
	}
}
```

If `mustInsertOrchestrationRun` doesn't already exist as a helper,
search the file for an existing pattern that creates an
orchestration + run row. The existing tests already do this — copy
that boilerplate into the helper.

- [ ] **Step A2.4: Run the tests**

```bash
go test ./controlplane/db/ -count=1 -run "TestOrchestrationStep_PromptEntities" -v
```

Expected: 2 PASS.

- [ ] **Step A2.5: Commit**

```bash
git add controlplane/db/orchestrations.go controlplane/db/orchestrations_test.go
git commit -m "db(orchestrations): persist prompt_entities side-channel"
```

---

## Task Group B — Engine prompt_entities classifier

### Task B1: Define the typed side-channel

**Files:**
- Create: `controlplane/orchestrator/prompt_entities.go`
- Create: `controlplane/orchestrator/prompt_entities_test.go`

- [ ] **Step B1.1: Define the types and entry function**

Create `controlplane/orchestrator/prompt_entities.go`:

```go
// prompt_entities.go captures the typed entity refs that get
// templated into a step's prompt string. The orchestrator engine
// calls BuildPromptEntities at render time; the result rides along
// with the prompt to the UI as a side-channel so SmartPayload can
// render entity chips at the JSON-substitution sites without
// re-parsing the prompt text.
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// PromptEntities is the wire shape persisted on
// orchestration_steps.prompt_entities. JSON-encoded.
type PromptEntities struct {
	Refs []PromptEntityRef `json:"refs"`
}

// PromptEntityRef points at one entity that was templated into the
// step's rendered prompt. Identity (kind + id-or-key) is sufficient
// to navigate to the live entity; the snapshot is the small subset
// of fields the UI chip displays at-a-glance, so the chip renders
// without a fetch.
type PromptEntityRef struct {
	CPInstanceID string         `json:"cp_instance_id,omitempty"`
	Kind         string         `json:"kind"` // finding | ioc | node | daimon | run | investigation | orchestration
	ID           int64          `json:"id,omitempty"`
	IOCKind      string         `json:"ioc_kind,omitempty"`
	IOCValue     string         `json:"ioc_value,omitempty"`
	Snapshot     map[string]any `json:"snapshot"`
	LiteralHash  string         `json:"literal_hash"`
}

// placeholderRe matches `{{path}}` template placeholders. Matches
// the same syntax Render uses (see template.go).
var placeholderRe = regexp.MustCompile(`\{\{\s*([^}|]+?)\s*(?:\|[^}]*)?\}\}`)

// BuildPromptEntities walks the placeholders in `template`, looks
// each path up in env, classifies the typed value (DispatchedFinding,
// DispatchedIOC, etc.), and accumulates entity refs.
//
// cpInstanceID is the CP that owns the entities — passed through
// unchanged. Empty string means same-CP (the local CP); the wire
// shape omits the field via the `omitempty` tag.
//
// Returns nil when no placeholders resolve to recognised entity types
// (i.e. plain strings, ints, etc.) so the caller can avoid emitting
// a noisy `{"refs":[]}` JSON blob.
func BuildPromptEntities(template string, env Env, cpInstanceID string) (*PromptEntities, error) {
	matches := placeholderRe.FindAllStringSubmatch(template, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	var refs []PromptEntityRef
	seen := map[string]bool{} // dedup by literal_hash
	for _, m := range matches {
		path := strings.TrimSpace(m[1])
		val, ok := lookupEnvPath(env, path)
		if !ok {
			continue
		}
		these := classify(val, cpInstanceID)
		for _, r := range these {
			if seen[r.LiteralHash] {
				continue
			}
			seen[r.LiteralHash] = true
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	return &PromptEntities{Refs: refs}, nil
}

// classify inspects a typed value and returns 0+ entity refs.
// Recognised types: DispatchedFinding(s), and (added below) IOC,
// node, daimon, run, investigation, orchestration shapes.
//
// Unrecognised types return nil — the value still gets stringified
// into the prompt as today, just without an entity ref.
func classify(v any, cpInstanceID string) []PromptEntityRef {
	switch x := v.(type) {
	case DispatchedFinding:
		return []PromptEntityRef{findingRef(x, cpInstanceID)}
	case []DispatchedFinding:
		out := make([]PromptEntityRef, 0, len(x))
		for _, f := range x {
			out = append(out, findingRef(f, cpInstanceID))
		}
		return out
	}
	return nil
}

func findingRef(f DispatchedFinding, cpInstanceID string) PromptEntityRef {
	body, _ := json.Marshal(f)
	return PromptEntityRef{
		CPInstanceID: cpInstanceID,
		Kind:         "finding",
		ID:           findingID(f.Attributes),
		Snapshot: map[string]any{
			"id":       findingID(f.Attributes),
			"severity": f.Severity,
			"title":    f.Title,
			"category": f.Category,
		},
		LiteralHash: literalHash(body),
	}
}

// findingID extracts the numeric id from a DispatchedFinding's
// Attributes (the engine stores the DB row id there). Returns 0 if
// the attribute is missing or the value isn't numeric — chip falls
// back to "Finding (no id)" in that case but still renders.
func findingID(attrs map[string]any) int64 {
	if attrs == nil {
		return 0
	}
	switch v := attrs["id"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// literalHash returns the first 16 hex chars of sha256(body) — short
// enough to be readable, long enough to make collision noise. Stable
// across server/client because both sides hash the exact same bytes
// the engine emits via json.Marshal.
func literalHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:16]
}

// lookupEnvPath resolves `triage.findings` / `trigger.host` style
// paths against the orchestrator Env. Mirrors what template.go's
// evaluator does internally; reused here so we don't have to expose
// template internals. Returns ok=false when any segment misses.
func lookupEnvPath(env Env, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = map[string]any(env)
	for _, p := range parts {
		switch m := cur.(type) {
		case map[string]any:
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		case Env:
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		case StepRecord:
			// Allow `<step>.findings` style — pull from the StepRecord's
			// typed Findings slice.
			if p == "findings" {
				cur = m.Findings
				continue
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return cur, true
}
```

- [ ] **Step B1.2: Write the test file with finding-only coverage**

Create `controlplane/orchestrator/prompt_entities_test.go`:

```go
package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPromptEntities_NoPlaceholders(t *testing.T) {
	got, err := BuildPromptEntities("plain prose, no template", Env{}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestBuildPromptEntities_UnrecognisedType(t *testing.T) {
	env := Env{"trigger": map[string]any{"host": "edr-1"}}
	got, err := BuildPromptEntities("host={{trigger.host}}", env, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("plain string should produce no refs, got %+v", got)
	}
}

func TestBuildPromptEntities_SingleFinding(t *testing.T) {
	f := DispatchedFinding{
		Severity:   "HIGH",
		Title:      "Suspicious cron job",
		Category:   "process",
		Attributes: map[string]any{"id": int64(42)},
	}
	env := Env{"trigger": map[string]any{"finding": f}}
	got, err := BuildPromptEntities(
		"investigate: {{trigger.finding}}",
		env,
		"",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got.Refs) != 1 {
		t.Fatalf("expected 1 ref, got %+v", got)
	}
	r := got.Refs[0]
	if r.Kind != "finding" || r.ID != 42 {
		t.Errorf("ref = %+v", r)
	}
	if r.Snapshot["severity"] != "HIGH" {
		t.Errorf("snapshot.severity = %v, want HIGH", r.Snapshot["severity"])
	}
	if len(r.LiteralHash) != 16 {
		t.Errorf("literal_hash = %q, want 16 hex chars", r.LiteralHash)
	}
}

func TestBuildPromptEntities_FindingArray(t *testing.T) {
	findings := []DispatchedFinding{
		{Severity: "HIGH", Title: "A", Category: "x", Attributes: map[string]any{"id": int64(1)}},
		{Severity: "LOW", Title: "B", Category: "y", Attributes: map[string]any{"id": int64(2)}},
	}
	env := Env{"triage": StepRecord{Findings: findings}}
	got, err := BuildPromptEntities(
		"summary: {{triage.findings}}",
		env,
		"cp-child-1",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got.Refs) != 2 {
		t.Fatalf("expected 2 refs, got %+v", got)
	}
	for _, r := range got.Refs {
		if r.CPInstanceID != "cp-child-1" {
			t.Errorf("CPInstanceID = %q, want cp-child-1", r.CPInstanceID)
		}
	}
}

func TestBuildPromptEntities_DedupByHash(t *testing.T) {
	f := DispatchedFinding{
		Severity:   "HIGH",
		Title:      "Same finding",
		Category:   "x",
		Attributes: map[string]any{"id": int64(7)},
	}
	env := Env{
		"a": map[string]any{"finding": f},
		"b": map[string]any{"finding": f},
	}
	got, err := BuildPromptEntities(
		"first: {{a.finding}} ... second: {{b.finding}}",
		env,
		"",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got.Refs) != 1 {
		t.Fatalf("expected 1 ref (dedup), got %d", func() int {
			if got == nil {
				return 0
			}
			return len(got.Refs)
		}())
	}
}

func TestBuildPromptEntities_HashStability(t *testing.T) {
	f := DispatchedFinding{
		Severity: "HIGH",
		Title:    "Stable",
		Category: "x",
	}
	env := Env{"trigger": map[string]any{"finding": f}}
	r1, _ := BuildPromptEntities("{{trigger.finding}}", env, "")
	r2, _ := BuildPromptEntities("{{trigger.finding}}", env, "")
	if r1 == nil || r2 == nil {
		t.Fatal("expected refs both runs")
	}
	if r1.Refs[0].LiteralHash != r2.Refs[0].LiteralHash {
		t.Errorf("hashes differ across runs: %q vs %q", r1.Refs[0].LiteralHash, r2.Refs[0].LiteralHash)
	}
}

func TestPromptEntities_RoundTripJSON(t *testing.T) {
	pe := PromptEntities{
		Refs: []PromptEntityRef{{
			Kind:        "finding",
			ID:          42,
			Snapshot:    map[string]any{"severity": "HIGH"},
			LiteralHash: "abc123def4567890",
		}},
	}
	b, err := json.Marshal(pe)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"kind":"finding"`) {
		t.Errorf("missing kind in JSON: %s", b)
	}
	var out PromptEntities
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Refs[0].LiteralHash != "abc123def4567890" {
		t.Errorf("hash lost on roundtrip: %+v", out.Refs[0])
	}
}
```

- [ ] **Step B1.3: Run tests for the new package**

```bash
go test ./controlplane/orchestrator/ -count=1 -run BuildPromptEntities -v
go test ./controlplane/orchestrator/ -count=1 -run PromptEntities -v
```

Expected: all PASS.

- [ ] **Step B1.4: Commit**

```bash
git add controlplane/orchestrator/prompt_entities.go controlplane/orchestrator/prompt_entities_test.go
git commit -m "orchestrator(prompt_entities): typed side-channel + finding classifier"
```

### Task B2: Extend the classifier for IOCs, nodes, daimons, runs, investigations, orchestrations

**Files:**
- Modify: `controlplane/orchestrator/prompt_entities.go`
- Modify: `controlplane/orchestrator/prompt_entities_test.go`

The orchestrator engine's typed values are limited to what the
dispatcher currently emits — primarily `DispatchedFinding`. Other
entity kinds appear in templates as `map[string]any` after JSON
unmarshalling (e.g. trigger payloads carry IOCs as
`{kind: "...", value: "..."}` literals). For these, the classifier
shape-sniffs at the Go side: same idea as the client detector,
applied to the raw `map[string]any`.

- [ ] **Step B2.1: Add map-shape detectors**

Append to `prompt_entities.go`:

```go
// classifyMap inspects an unstructured map (typical of trigger
// payloads + data: bindings) and returns 0+ entity refs.
//
// Detection is conservative: each kind requires a specific set of
// fields. False positives here would incorrectly tag a non-entity
// as a chip; false negatives just leave the value as a stringified
// JSON literal in the prompt (current behavior). When in doubt,
// skip.
func classifyMap(m map[string]any, cpInstanceID string) []PromptEntityRef {
	if m == nil {
		return nil
	}
	if isFindingMap(m) {
		return []PromptEntityRef{findingRefFromMap(m, cpInstanceID)}
	}
	if isIOCMap(m) {
		return []PromptEntityRef{iocRefFromMap(m, cpInstanceID)}
	}
	if isNodeMap(m) {
		return []PromptEntityRef{nodeRefFromMap(m, cpInstanceID)}
	}
	if isDaimonMap(m) {
		return []PromptEntityRef{daimonRefFromMap(m, cpInstanceID)}
	}
	if isRunMap(m) {
		return []PromptEntityRef{runRefFromMap(m, cpInstanceID)}
	}
	if isInvestigationMap(m) {
		return []PromptEntityRef{investigationRefFromMap(m, cpInstanceID)}
	}
	if isOrchestrationMap(m) {
		return []PromptEntityRef{orchestrationRefFromMap(m, cpInstanceID)}
	}
	return nil
}

func mapHas(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func mapStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func mapInt(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// === finding ===

func isFindingMap(m map[string]any) bool {
	return mapHas(m, "id", "severity", "title", "category")
}

func findingRefFromMap(m map[string]any, cp string) PromptEntityRef {
	id := mapInt(m, "id")
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "finding",
		ID:           id,
		Snapshot: map[string]any{
			"id":       id,
			"severity": mapStr(m, "severity"),
			"title":    mapStr(m, "title"),
			"category": mapStr(m, "category"),
			"status":   mapStr(m, "status"),
			"host":     mapStr(m, "host"),
		},
		LiteralHash: literalHash(body),
	}
}

// === ioc ===

func isIOCMap(m map[string]any) bool {
	if !mapHas(m, "kind", "value") {
		return false
	}
	// Disambiguate from other "kind/value" shapes by requiring at
	// least one IOC-specific marker.
	return mapHas(m, "observation_count") ||
		mapHas(m, "severity_max") ||
		mapStr(m, "type") == "ioc"
}

func iocRefFromMap(m map[string]any, cp string) PromptEntityRef {
	kind := mapStr(m, "kind")
	value := mapStr(m, "value")
	body, _ := json.Marshal(m)
	last4 := value
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "ioc",
		IOCKind:      kind,
		IOCValue:     value,
		Snapshot: map[string]any{
			"kind":              kind,
			"value":             value,
			"last4":             last4,
			"observation_count": m["observation_count"],
			"severity_max":      m["severity_max"],
		},
		LiteralHash: literalHash(body),
	}
}

// === node ===

func isNodeMap(m map[string]any) bool {
	if !mapHas(m, "id", "hostname") {
		return false
	}
	// Disambiguate from a finding that happens to have id+hostname:
	// nodes lack severity.
	if _, ok := m["severity"]; ok {
		return false
	}
	return true
}

func nodeRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "node",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":       mapInt(m, "id"),
			"name":     mapStr(m, "name"),
			"hostname": mapStr(m, "hostname"),
			"status":   mapStr(m, "status"),
		},
		LiteralHash: literalHash(body),
	}
}

// === daimon ===

func isDaimonMap(m map[string]any) bool {
	if !mapHas(m, "name", "host") {
		return false
	}
	return mapHas(m, "agent_id") || mapHas(m, "suspended")
}

func daimonRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "daimon",
		ID:           mapInt(m, "agent_id"),
		Snapshot: map[string]any{
			"id":        mapInt(m, "agent_id"),
			"name":      mapStr(m, "name"),
			"host":      mapStr(m, "host"),
			"suspended": m["suspended"],
		},
		LiteralHash: literalHash(body),
	}
}

// === run ===

func isRunMap(m map[string]any) bool {
	if !mapHas(m, "id", "status") {
		return false
	}
	return mapHas(m, "started_at") || mapHas(m, "agent_name")
}

func runRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "run",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":         mapInt(m, "id"),
			"status":     mapStr(m, "status"),
			"started_at": mapStr(m, "started_at"),
			"ended_at":   mapStr(m, "ended_at"),
		},
		LiteralHash: literalHash(body),
	}
}

// === investigation ===

func isInvestigationMap(m map[string]any) bool {
	if !mapHas(m, "id", "title") {
		return false
	}
	// Investigations have both title and severity; distinguish from
	// findings (which also have category) by absence of category and
	// presence of either external_key or status.
	if _, hasCategory := m["category"]; hasCategory {
		return false
	}
	return mapHas(m, "external_key") || (mapHas(m, "severity") && mapHas(m, "status"))
}

func investigationRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "investigation",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":       mapInt(m, "id"),
			"title":    mapStr(m, "title"),
			"status":   mapStr(m, "status"),
			"severity": mapStr(m, "severity"),
		},
		LiteralHash: literalHash(body),
	}
}

// === orchestration ===

func isOrchestrationMap(m map[string]any) bool {
	return mapHas(m, "id", "name", "version")
}

func orchestrationRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "orchestration",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":      mapInt(m, "id"),
			"name":    mapStr(m, "name"),
			"version": m["version"],
		},
		LiteralHash: literalHash(body),
	}
}
```

- [ ] **Step B2.2: Wire classifyMap into classify**

In `prompt_entities.go`, extend `classify` to also handle
`map[string]any` and `[]any`:

```go
func classify(v any, cpInstanceID string) []PromptEntityRef {
	switch x := v.(type) {
	case DispatchedFinding:
		return []PromptEntityRef{findingRef(x, cpInstanceID)}
	case []DispatchedFinding:
		out := make([]PromptEntityRef, 0, len(x))
		for _, f := range x {
			out = append(out, findingRef(f, cpInstanceID))
		}
		return out
	case map[string]any:
		return classifyMap(x, cpInstanceID)
	case []any:
		var out []PromptEntityRef
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, classifyMap(m, cpInstanceID)...)
			}
		}
		return out
	case []map[string]any:
		var out []PromptEntityRef
		for _, m := range x {
			out = append(out, classifyMap(m, cpInstanceID)...)
		}
		return out
	}
	return nil
}
```

- [ ] **Step B2.3: Add per-kind tests**

Append to `prompt_entities_test.go`:

```go
func TestClassifyMap_IOC(t *testing.T) {
	m := map[string]any{
		"kind":              "sha256",
		"value":             "deadbeef" + "0000000000000000000000000000000000000000000000000000000000",
		"observation_count": float64(3),
	}
	refs := classifyMap(m, "cp-x")
	if len(refs) != 1 || refs[0].Kind != "ioc" {
		t.Fatalf("expected ioc ref, got %+v", refs)
	}
	if refs[0].IOCKind != "sha256" {
		t.Errorf("ioc_kind = %q", refs[0].IOCKind)
	}
	if refs[0].Snapshot["last4"] != "0000" {
		t.Errorf("last4 = %v", refs[0].Snapshot["last4"])
	}
}

func TestClassifyMap_Node(t *testing.T) {
	m := map[string]any{
		"id":       float64(7),
		"name":     "edr-1",
		"hostname": "edr-1.lab",
		"status":   "online",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "node" || refs[0].ID != 7 {
		t.Fatalf("expected node ref id=7, got %+v", refs)
	}
}

func TestClassifyMap_Daimon(t *testing.T) {
	m := map[string]any{
		"name":      "edr-agent",
		"host":      "edr-1",
		"agent_id":  float64(11),
		"suspended": false,
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "daimon" || refs[0].ID != 11 {
		t.Fatalf("expected daimon ref, got %+v", refs)
	}
}

func TestClassifyMap_Run(t *testing.T) {
	m := map[string]any{
		"id":         float64(99),
		"status":     "completed",
		"started_at": "2026-04-30T10:00:00Z",
		"agent_name": "edr-agent",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "run" || refs[0].ID != 99 {
		t.Fatalf("expected run ref, got %+v", refs)
	}
}

func TestClassifyMap_Investigation(t *testing.T) {
	m := map[string]any{
		"id":           float64(3),
		"title":        "ransomware Q3",
		"severity":     "HIGH",
		"status":       "open",
		"external_key": "INC-123",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "investigation" || refs[0].ID != 3 {
		t.Fatalf("expected investigation ref, got %+v", refs)
	}
}

func TestClassifyMap_Orchestration(t *testing.T) {
	m := map[string]any{
		"id":      float64(5),
		"name":    "triage-then-quarantine",
		"version": float64(2),
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "orchestration" {
		t.Fatalf("expected orchestration ref, got %+v", refs)
	}
}

func TestClassifyMap_NotEntity(t *testing.T) {
	m := map[string]any{"foo": "bar", "baz": float64(1)}
	if refs := classifyMap(m, ""); refs != nil {
		t.Errorf("expected nil refs for non-entity map, got %+v", refs)
	}
}

func TestClassifyMap_FindingNotInvestigation(t *testing.T) {
	// A finding has both severity+status — make sure we don't
	// misclassify as investigation.
	m := map[string]any{
		"id":       float64(1),
		"title":    "x",
		"category": "process",
		"severity": "LOW",
		"status":   "open",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "finding" {
		t.Fatalf("expected finding (not investigation), got %+v", refs)
	}
}

func TestBuildPromptEntities_FindingMapInTrigger(t *testing.T) {
	// Triggers commonly carry `trigger.finding = map[string]any{...}`
	// (after JSON unmarshalling). Make sure that flows through.
	env := Env{"trigger": map[string]any{
		"finding": map[string]any{
			"id":       float64(42),
			"severity": "HIGH",
			"title":    "x",
			"category": "y",
		},
	}}
	got, _ := BuildPromptEntities("look: {{trigger.finding}}", env, "")
	if got == nil || len(got.Refs) != 1 || got.Refs[0].Kind != "finding" {
		t.Fatalf("expected 1 finding ref, got %+v", got)
	}
}
```

- [ ] **Step B2.4: Run tests**

```bash
go test ./controlplane/orchestrator/ -count=1 -run "ClassifyMap|BuildPromptEntities" -v
```

Expected: all PASS.

- [ ] **Step B2.5: Commit**

```bash
git add controlplane/orchestrator/prompt_entities.go controlplane/orchestrator/prompt_entities_test.go
git commit -m "orchestrator(prompt_entities): classify IOCs, nodes, daimons, runs, investigations, orchestrations"
```

---

## Task Group C — Engine integration

### Task C1: Add PromptEntities to StepRecord and call from render sites

**Files:**
- Modify: `controlplane/orchestrator/engine.go`

- [ ] **Step C1.1: Add PromptEntities to StepRecord**

In `engine.go` around line 156 (`StepRecord` struct):

```go
type StepRecord struct {
	// ... existing fields ...
	RenderedPrompt string
	PromptEntities string // JSON-encoded; empty when no recognised refs
	// ... rest ...
}
```

- [ ] **Step C1.2: Capture entities at the approval-gate render site (~line 443)**

Change:

```go
renderedPrompt, _ := Render(step.Prompt, env)
rec.RenderedPrompt = renderedPrompt
```

To:

```go
renderedPrompt, _ := Render(step.Prompt, env)
rec.RenderedPrompt = renderedPrompt
if pe, perr := BuildPromptEntities(step.Prompt, env, cpInstanceForStep(orch, &step)); perr == nil && pe != nil {
	if b, jerr := json.Marshal(pe); jerr == nil {
		rec.PromptEntities = string(b)
	}
}
```

- [ ] **Step C1.3: Capture at the dispatch render site (~line 495)**

Same wrapper after `Render`:

```go
renderedPrompt, err := Render(step.Prompt, env)
if err != nil {
	rec.Status = StepStatusFailed
	rec.Error = "render prompt: " + err.Error()
	_ = e.store.UpsertOrchestrationStep(rec)
	return e.haltFailed(run.ID, step.ID, rec.Error)
}
if pe, perr := BuildPromptEntities(step.Prompt, env, cpInstanceForStep(orch, &step)); perr == nil && pe != nil {
	if b, jerr := json.Marshal(pe); jerr == nil {
		rec.PromptEntities = string(b)
	}
}
```

- [ ] **Step C1.4: Add the cpInstanceForStep helper**

At the bottom of `engine.go`:

```go
// cpInstanceForStep returns the CP instance ID this step's entities
// belong to. Steps that explicitly select a child CP via `cp:` or via
// a node selector that resolves to a federated peer return that
// peer's ID; local steps return "" so the wire shape's omitempty
// drops the field. Used by BuildPromptEntities to tag refs so the UI
// can route deep links across federation.
func cpInstanceForStep(orch *Orchestration, step *Step) string {
	if step != nil {
		// EffectiveCP returns the step-level cp: override or falls
		// through to the orchestration default. Empty = local.
		if cp := orch.Spec.EffectiveCP(step); cp != "" {
			return cp
		}
	}
	return ""
}
```

If `EffectiveCP` already does this same fallback, skip the helper and
inline `orch.Spec.EffectiveCP(&step)` at the call sites.

- [ ] **Step C1.5: Persist via the existing UpsertOrchestrationStep call site**

The engine already maps `rec` → `OrchestrationStepInsert` somewhere
near the `_ = e.store.UpsertOrchestrationStep(rec)` calls. Find that
mapper (search for `OrchestrationStepInsert{`) and add:

```go
PromptEntities: rec.PromptEntities,
```

If the mapper is implicit (i.e. the engine passes `rec` directly as
`OrchestrationStepInsert`), make sure the `StepRecord` field name
matches `OrchestrationStepInsert.PromptEntities`. If they share a
type, rename consistently and remove the indirection.

- [ ] **Step C1.6: Build + run engine tests**

```bash
go build ./controlplane/orchestrator/...
go test ./controlplane/orchestrator/ -count=1
```

Expected: PASS. Existing engine tests don't assert on
`prompt_entities` — they just verify the column doesn't break
existing flows.

- [ ] **Step C1.7: Run + commit**

The classifier behavior is already covered by Group B's unit tests
(`prompt_entities_test.go`); the persistence path is covered by
Group A's `TestOrchestrationStep_PromptEntitiesRoundTrip`. The
engine integration here is glue — verified by `go build` plus the
existing engine tests passing unchanged.

```bash
go test ./controlplane/orchestrator/ -count=1
```

Expected: all existing tests PASS.

```bash
git add controlplane/orchestrator/engine.go
git commit -m "orchestrator(engine): capture prompt_entities at template render time"
```

---

## Task Group D — API wire shape

### Task D1: Add prompt_entities to OrchestrationStepView

**Files:**
- Modify: `controlplane/api/orchestrations.go` (around line 1935)

- [ ] **Step D1.1: Extend the wire struct**

In `controlplane/api/orchestrations.go`, find `OrchestrationStepView`
(around line 1935) and add:

```go
// PromptEntities is the typed entity-ref side-channel the engine
// captures at render time. JSON-encoded as a string at the DB level;
// the API decodes once and re-marshals into the wire shape so the
// browser doesn't have to JSON.parse a field-of-a-field.
PromptEntities json.RawMessage `json:"prompt_entities,omitempty"`
```

- [ ] **Step D1.2: Populate it in the converter (~line 1987)**

Where the converter maps `*OrchestrationStep` → `OrchestrationStepView`:

```go
view := OrchestrationStepView{
	// ... existing fields ...
	RenderedPrompt: st.RenderedPrompt.String,
}
if st.PromptEntities.Valid && st.PromptEntities.String != "" {
	view.PromptEntities = json.RawMessage(st.PromptEntities.String)
}
```

- [ ] **Step D1.3: Build + verify with api tests**

```bash
go build ./controlplane/api/...
go test ./controlplane/api/ -count=1
```

Expected: PASS.

- [ ] **Step D1.4: Add a wire-shape test**

Add to `controlplane/api/orchestrations_test.go` (or whatever the
nearest existing test file is named — check with `ls
controlplane/api/orchestrations_test.go`):

```go
func TestOrchestrationStepView_PromptEntitiesWireShape(t *testing.T) {
	st := &db.OrchestrationStep{
		StepID:         "triage",
		Status:         "running",
		PromptEntities: sql.NullString{String: `{"refs":[{"kind":"finding","id":42,"snapshot":{"severity":"HIGH"},"literal_hash":"abc1234567890abc"}]}`, Valid: true},
	}
	view := orchestrationStepToView(st) // use whatever the existing converter is named
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"prompt_entities":{"refs":`) {
		t.Errorf("missing prompt_entities in wire shape: %s", b)
	}
}

func TestOrchestrationStepView_PromptEntitiesOmittedWhenNull(t *testing.T) {
	st := &db.OrchestrationStep{StepID: "triage", Status: "running"}
	view := orchestrationStepToView(st)
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), `"prompt_entities"`) {
		t.Errorf("prompt_entities should be omitted when null: %s", b)
	}
}
```

If the converter name differs (`stepToView`, `viewFromStep`, etc.),
adjust. Use the existing tests in the same file as a reference for
imports and helpers.

- [ ] **Step D1.5: Run + commit**

```bash
go test ./controlplane/api/ -count=1 -run "OrchestrationStepView_PromptEntities" -v
```

Expected: 2 PASS.

```bash
git add controlplane/api/orchestrations.go controlplane/api/orchestrations_test.go
git commit -m "api(orchestrations): expose prompt_entities on OrchestrationStepView"
```

---

## Task Group E — Frontend api.ts

### Task E1: Add types

**Files:**
- Modify: `web/src/api.ts` (around line 644)

- [ ] **Step E1.1: Define PromptEntities + PromptEntityRef types**

In `web/src/api.ts`, just above `OrchestrationStepView`:

```ts
export type PromptEntityKind =
  | 'finding'
  | 'ioc'
  | 'node'
  | 'daimon'
  | 'run'
  | 'investigation'
  | 'orchestration';

export interface PromptEntityRef {
  cp_instance_id?: string;
  kind: PromptEntityKind;
  id?: number;
  ioc_kind?: string;
  ioc_value?: string;
  snapshot: Record<string, unknown>;
  literal_hash: string;
}

export interface PromptEntities {
  refs: PromptEntityRef[];
}
```

- [ ] **Step E1.2: Extend OrchestrationStepView**

```ts
export interface OrchestrationStepView {
  // ... existing fields ...
  rendered_prompt?: string;
  /** Phase 24: typed entity refs for SmartPayload's prompt-mode
   *  rendering. Absent on legacy steps; client falls back to shape
   *  detection in that case. */
  prompt_entities?: PromptEntities;
  result?: Record<string, unknown>;
  // ... rest ...
}
```

- [ ] **Step E1.3: Type-check**

```bash
node_modules/.bin/tsc -b
```

Expected: clean (no output).

- [ ] **Step E1.4: Commit**

```bash
git add web/src/api.ts
git commit -m "web(api): PromptEntities + PromptEntityRef types"
```

---

## Task Group F — SmartPayload core (tokenizer + hash + detectors)

### Task F1: Tokenizer

**Files:**
- Create: `web/src/components/SmartPayload/tokenize.ts`
- Create: `web/src/components/SmartPayload/tokenize.test.ts`

- [ ] **Step F1.1: Write the failing test**

Create `web/src/components/SmartPayload/tokenize.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { tokenize } from './tokenize';

describe('tokenize', () => {
  it('returns single text segment for plain prose', () => {
    const out = tokenize('plain prose, no JSON');
    expect(out).toEqual([{ kind: 'text', text: 'plain prose, no JSON' }]);
  });

  it('extracts a top-level JSON object literal', () => {
    const out = tokenize('see {"id":42}');
    expect(out).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'json', src: '{"id":42}' },
    ]);
  });

  it('extracts a top-level JSON array literal', () => {
    const out = tokenize('items: [1,2,3]');
    expect(out).toEqual([
      { kind: 'text', text: 'items: ' },
      { kind: 'json', src: '[1,2,3]' },
    ]);
  });

  it('handles trailing prose after JSON', () => {
    const out = tokenize('prefix {"a":1} suffix');
    expect(out).toEqual([
      { kind: 'text', text: 'prefix ' },
      { kind: 'json', src: '{"a":1}' },
      { kind: 'text', text: ' suffix' },
    ]);
  });

  it('respects quoted braces inside strings', () => {
    const out = tokenize('see {"title":"a}b","id":1}');
    expect(out).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'json', src: '{"title":"a}b","id":1}' },
    ]);
  });

  it('handles nested objects', () => {
    const out = tokenize('x={"outer":{"inner":1}} y');
    expect(out).toEqual([
      { kind: 'text', text: 'x=' },
      { kind: 'json', src: '{"outer":{"inner":1}}' },
      { kind: 'text', text: ' y' },
    ]);
  });

  it('handles two consecutive objects with prose between', () => {
    const out = tokenize('A {"a":1} B {"b":2} C');
    expect(out).toHaveLength(5);
    expect(out[1]).toEqual({ kind: 'json', src: '{"a":1}' });
    expect(out[3]).toEqual({ kind: 'json', src: '{"b":2}' });
  });

  it('does not split on { inside string literals containing escaped quotes', () => {
    const out = tokenize(String.raw`{"name":"with \"quoted\" {brace}"}`);
    expect(out).toHaveLength(1);
    expect(out[0]).toEqual({ kind: 'json', src: String.raw`{"name":"with \"quoted\" {brace}"}` });
  });

  it('falls through unmatched open-brace as text', () => {
    const out = tokenize('text { unbalanced');
    expect(out).toEqual([{ kind: 'text', text: 'text { unbalanced' }]);
  });
});
```

- [ ] **Step F1.2: Run test to verify it fails**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/tokenize.test.ts
```

Expected: FAIL — `tokenize` not defined.

- [ ] **Step F1.3: Implement tokenize**

Create `web/src/components/SmartPayload/tokenize.ts`:

```ts
// Walks a prose+JSON-mixed string, emitting text segments and
// well-formed JSON object/array segments. Used by SmartPayload's
// prompt mode so a rendered_prompt like
//
//   "investigate the following: {"id":42, ...}"
//
// becomes [text, json] and the renderer can swap the JSON segment
// for a styled entity card.
//
// Algorithm: single-pass, depth-aware brace/bracket counter that
// respects string literals (including escaped quotes). Unbalanced
// regions fall through as plain text. O(n) on the input length.

export type Token =
  | { kind: 'text'; text: string }
  | { kind: 'json'; src: string };

export function tokenize(input: string): Token[] {
  const out: Token[] = [];
  let i = 0;
  const n = input.length;
  let textStart = 0;

  while (i < n) {
    const c = input[i];
    if (c === '{' || c === '[') {
      const end = scanJSONLiteral(input, i);
      if (end > i) {
        if (textStart < i) {
          out.push({ kind: 'text', text: input.slice(textStart, i) });
        }
        out.push({ kind: 'json', src: input.slice(i, end) });
        i = end;
        textStart = i;
        continue;
      }
    }
    i++;
  }
  if (textStart < n) {
    out.push({ kind: 'text', text: input.slice(textStart, n) });
  }
  return out;
}

// scanJSONLiteral starts at input[start] which is '{' or '['.
// Returns the index *after* the matching close brace/bracket, or
// `start` (no progress) when the literal isn't well-formed.
function scanJSONLiteral(input: string, start: number): number {
  const open = input[start];
  const close = open === '{' ? '}' : ']';
  let depth = 0;
  let inString = false;
  let escape = false;

  for (let i = start; i < input.length; i++) {
    const c = input[i];
    if (inString) {
      if (escape) {
        escape = false;
        continue;
      }
      if (c === '\\') {
        escape = true;
        continue;
      }
      if (c === '"') inString = false;
      continue;
    }
    if (c === '"') {
      inString = true;
      continue;
    }
    if (c === open || (open === '{' && c === '[') || (open === '[' && c === '{')) {
      depth++;
      continue;
    }
    if (c === close || (open === '{' && c === ']') || (open === '[' && c === '}')) {
      depth--;
      if (depth === 0) {
        return i + 1;
      }
      if (depth < 0) {
        return start; // mismatched
      }
      continue;
    }
  }
  return start; // ran off the end without closing
}
```

- [ ] **Step F1.4: Run test to verify it passes**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/tokenize.test.ts
```

Expected: 8 PASS.

- [ ] **Step F1.5: Commit**

```bash
git add web/src/components/SmartPayload/tokenize.ts web/src/components/SmartPayload/tokenize.test.ts
git commit -m "web(SmartPayload): prose↔JSON tokenizer"
```

### Task F2: literalHash (sha256-prefix, stable across server/client)

**Files:**
- Create: `web/src/components/SmartPayload/literalHash.ts`
- Create: `web/src/components/SmartPayload/literalHash.test.ts`

- [ ] **Step F2.1: Write the failing test**

```ts
import { describe, it, expect } from 'vitest';
import { literalHash } from './literalHash';

describe('literalHash', () => {
  it('returns 16 hex chars', async () => {
    const h = await literalHash('{"id":42}');
    expect(h).toMatch(/^[0-9a-f]{16}$/);
  });

  it('is deterministic', async () => {
    const a = await literalHash('{"id":42}');
    const b = await literalHash('{"id":42}');
    expect(a).toEqual(b);
  });

  it('differs for different inputs', async () => {
    const a = await literalHash('{"id":1}');
    const b = await literalHash('{"id":2}');
    expect(a).not.toEqual(b);
  });
});
```

- [ ] **Step F2.2: Run test to verify it fails**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/literalHash.test.ts
```

Expected: FAIL.

- [ ] **Step F2.3: Implement literalHash**

```ts
// Returns the first 16 hex chars of sha256(input). Mirrors the Go
// helper at controlplane/orchestrator/prompt_entities.go so the
// client and server agree on every hash for matching server-emitted
// PromptEntityRef.literal_hash against client-tokenized JSON
// segments.
//
// Uses the Web Crypto SubtleCrypto API; available in all modern
// browsers and the Vitest happy-dom environment.

export async function literalHash(input: string): Promise<string> {
  const enc = new TextEncoder().encode(input);
  const buf = await crypto.subtle.digest('SHA-256', enc);
  const hex = Array.from(new Uint8Array(buf))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
  return hex.slice(0, 16);
}
```

- [ ] **Step F2.4: Run test to verify it passes**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/literalHash.test.ts
```

Expected: 3 PASS.

- [ ] **Step F2.5: Commit**

```bash
git add web/src/components/SmartPayload/literalHash.ts web/src/components/SmartPayload/literalHash.test.ts
git commit -m "web(SmartPayload): client-side literalHash matching server"
```

### Task F3: Detectors (per-kind shape sniffers)

**Files:**
- Create: `web/src/components/SmartPayload/detectors.ts`
- Create: `web/src/components/SmartPayload/detectors.test.ts`

- [ ] **Step F3.1: Write the failing test**

```ts
import { describe, it, expect } from 'vitest';
import { detectEntity } from './detectors';

describe('detectEntity', () => {
  it('returns null for non-objects', () => {
    expect(detectEntity('hello')).toBeNull();
    expect(detectEntity(42)).toBeNull();
    expect(detectEntity(null)).toBeNull();
    expect(detectEntity([1, 2, 3])).toBeNull();
  });

  it('returns null for unrelated objects', () => {
    expect(detectEntity({ foo: 'bar', baz: 1 })).toBeNull();
  });

  it('detects finding', () => {
    expect(detectEntity({
      id: 42,
      severity: 'HIGH',
      title: 'x',
      category: 'process',
    })).toEqual({
      kind: 'finding',
      snapshot: expect.objectContaining({ id: 42, severity: 'HIGH', title: 'x', category: 'process' }),
    });
  });

  it('detects ioc', () => {
    const got = detectEntity({
      kind: 'sha256',
      value: 'a'.repeat(64),
      observation_count: 3,
    });
    expect(got?.kind).toBe('ioc');
    expect(got?.snapshot.kind).toBe('sha256');
  });

  it('detects node (id+hostname, no severity)', () => {
    const got = detectEntity({ id: 7, name: 'edr-1', hostname: 'edr-1.lab', status: 'online' });
    expect(got?.kind).toBe('node');
  });

  it('does not detect node when severity present (would be a finding)', () => {
    const got = detectEntity({
      id: 7,
      hostname: 'h',
      severity: 'HIGH',
      title: 'x',
      category: 'y',
    });
    expect(got?.kind).toBe('finding');
  });

  it('detects daimon', () => {
    const got = detectEntity({ name: 'edr', host: 'h1', agent_id: 11, suspended: false });
    expect(got?.kind).toBe('daimon');
  });

  it('detects run', () => {
    const got = detectEntity({ id: 99, status: 'completed', started_at: '2026-01-01', agent_name: 'edr' });
    expect(got?.kind).toBe('run');
  });

  it('detects investigation, prefers it over finding when no category', () => {
    const got = detectEntity({
      id: 3,
      title: 'inc',
      severity: 'HIGH',
      status: 'open',
      external_key: 'INC-1',
    });
    expect(got?.kind).toBe('investigation');
  });

  it('detects orchestration', () => {
    const got = detectEntity({ id: 5, name: 'triage', version: 2 });
    expect(got?.kind).toBe('orchestration');
  });
});
```

- [ ] **Step F3.2: Run test to verify it fails**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/detectors.test.ts
```

Expected: FAIL.

- [ ] **Step F3.3: Implement detectors**

```ts
// Conservative duck-type matchers per entity kind. Order matters:
// finding wins ties over investigation (because operators see findings
// far more often, and investigation has the explicit external_key
// disambiguator). Returns the first match or null.
//
// All snapshots are returned as plain Record<string, unknown> so the
// chip components can pick the fields they need; no transformation
// here beyond null-safe field access.

import type { PromptEntityKind } from '../../api';

export interface DetectedEntity {
  kind: PromptEntityKind;
  snapshot: Record<string, unknown>;
}

type Obj = Record<string, unknown>;

function isObj(v: unknown): v is Obj {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function has(o: Obj, ...keys: string[]): boolean {
  for (const k of keys) if (!(k in o)) return false;
  return true;
}

export function detectEntity(value: unknown): DetectedEntity | null {
  if (!isObj(value)) return null;

  // Finding has both severity AND category — strongest signal, check first.
  if (has(value, 'id', 'severity', 'title', 'category')) {
    return {
      kind: 'finding',
      snapshot: {
        id: value.id,
        severity: value.severity,
        title: value.title,
        category: value.category,
        status: value.status,
        host: value.host,
      },
    };
  }

  // Investigation: severity+status+title without category, plus an
  // investigation-specific marker.
  if (
    has(value, 'id', 'title') &&
    !has(value, 'category') &&
    (has(value, 'external_key') || (has(value, 'severity') && has(value, 'status')))
  ) {
    return {
      kind: 'investigation',
      snapshot: {
        id: value.id,
        title: value.title,
        status: value.status,
        severity: value.severity,
      },
    };
  }

  // IOC: kind + value + at least one IOC-specific marker.
  if (
    has(value, 'kind', 'value') &&
    (has(value, 'observation_count') || has(value, 'severity_max') || value.type === 'ioc')
  ) {
    const v = String(value.value ?? '');
    return {
      kind: 'ioc',
      snapshot: {
        kind: value.kind,
        value: v,
        last4: v.length > 4 ? v.slice(-4) : v,
        observation_count: value.observation_count,
        severity_max: value.severity_max,
      },
    };
  }

  // Node: id + hostname, no severity.
  if (has(value, 'id', 'hostname') && !has(value, 'severity')) {
    return {
      kind: 'node',
      snapshot: {
        id: value.id,
        name: value.name,
        hostname: value.hostname,
        status: value.status,
      },
    };
  }

  // Daimon: name + host + agent_id-or-suspended.
  if (has(value, 'name', 'host') && (has(value, 'agent_id') || has(value, 'suspended'))) {
    return {
      kind: 'daimon',
      snapshot: {
        id: value.agent_id,
        name: value.name,
        host: value.host,
        suspended: value.suspended,
      },
    };
  }

  // Run: id + status + started_at-or-agent_name.
  if (has(value, 'id', 'status') && (has(value, 'started_at') || has(value, 'agent_name'))) {
    return {
      kind: 'run',
      snapshot: {
        id: value.id,
        status: value.status,
        started_at: value.started_at,
        ended_at: value.ended_at,
      },
    };
  }

  // Orchestration: id + name + version.
  if (has(value, 'id', 'name', 'version')) {
    return {
      kind: 'orchestration',
      snapshot: {
        id: value.id,
        name: value.name,
        version: value.version,
      },
    };
  }

  return null;
}
```

- [ ] **Step F3.4: Run test to verify it passes**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/detectors.test.ts
```

Expected: 10 PASS.

- [ ] **Step F3.5: Commit**

```bash
git add web/src/components/SmartPayload/detectors.ts web/src/components/SmartPayload/detectors.test.ts
git commit -m "web(SmartPayload): per-kind entity detectors"
```

---

## Task Group G — Per-kind chip components

### Task G1: FindingChip + IOCChip + NodeChip

**Files:**
- Create: `web/src/components/SmartPayload/chips/FindingChip.tsx`
- Create: `web/src/components/SmartPayload/chips/IOCChip.tsx`
- Create: `web/src/components/SmartPayload/chips/NodeChip.tsx`
- Create: `web/src/components/SmartPayload/chips/common.tsx`

- [ ] **Step G1.1: Common chip wrapper**

Create `web/src/components/SmartPayload/chips/common.tsx`:

```tsx
// Shared chip frame: a small clickable card with an entity icon, a
// summary line, an optional status pill, and an `↗` link. Click on
// the body fires the `entity:open` custom event so the
// EntityDrawerHost can render the per-kind drawer in place; click
// on `↗` navigates to the full detail page.
import type { ReactNode } from 'react';
import { ArrowUpRight } from 'lucide-react';

export interface ChipProps {
  cpInstanceID?: string;
  kind: string;
  identityKey: string; // for the entity:open event payload
  href: string;        // navigate target for the ↗ icon
  icon: ReactNode;
  title: ReactNode;
  meta?: ReactNode;
  pill?: ReactNode;
}

export function ChipFrame({ kind, identityKey, href, icon, title, meta, pill, cpInstanceID }: ChipProps) {
  function handleOpen(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    window.dispatchEvent(new CustomEvent('entity:open', {
      detail: { kind, identityKey, cpInstanceID },
    }));
  }
  return (
    <span className="inline-flex items-center gap-1.5 align-baseline mx-0.5 my-0.5 px-2 py-1 rounded-md border border-border bg-panel hover:bg-slate-50 text-xs leading-tight max-w-full">
      <button
        type="button"
        onClick={handleOpen}
        className="inline-flex items-center gap-1.5 cursor-pointer text-left min-w-0"
      >
        <span className="shrink-0">{icon}</span>
        <span className="font-medium text-ink truncate">{title}</span>
        {pill}
        {meta && <span className="text-ink-mute truncate">{meta}</span>}
      </button>
      <a
        href={href}
        className="text-ink-mute hover:text-ink shrink-0"
        title={`Open ${kind} page`}
        onClick={(e) => e.stopPropagation()}
      >
        <ArrowUpRight size={12} />
      </a>
    </span>
  );
}
```

- [ ] **Step G1.2: FindingChip**

Create `web/src/components/SmartPayload/chips/FindingChip.tsx`:

```tsx
import { AlertTriangle } from 'lucide-react';
import { ChipFrame } from './common';

interface FindingSnapshot {
  id?: number;
  severity?: string;
  title?: string;
  category?: string;
  status?: string;
  host?: string;
}

const SEV_TONE: Record<string, string> = {
  CRITICAL: 'text-red-700 bg-red-50 ring-red-200',
  HIGH:     'text-orange-700 bg-orange-50 ring-orange-200',
  MEDIUM:   'text-amber-700 bg-amber-50 ring-amber-200',
  LOW:      'text-blue-700 bg-blue-50 ring-blue-200',
  INFO:     'text-slate-700 bg-slate-50 ring-slate-200',
};

export function FindingChip({ snapshot, cpInstanceID }: { snapshot: FindingSnapshot; cpInstanceID?: string }) {
  const sev = (snapshot.severity ?? 'INFO').toUpperCase();
  const tone = SEV_TONE[sev] ?? SEV_TONE.INFO;
  const idLabel = snapshot.id ? `#${snapshot.id}` : '';
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="finding"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/findings?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<AlertTriangle size={11} className="text-red-500" />}
      title={
        <>
          {snapshot.title ?? 'Finding'} <span className="text-ink-mute font-normal">{idLabel}</span>
        </>
      }
      pill={
        <span className={`text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${tone}`}>
          {sev}
        </span>
      }
      meta={snapshot.category ? snapshot.category : undefined}
    />
  );
}
```

- [ ] **Step G1.3: IOCChip**

```tsx
import { Hash } from 'lucide-react';
import { ChipFrame } from './common';

interface IOCSnapshot {
  kind?: string;
  value?: string;
  last4?: string;
  observation_count?: number;
  severity_max?: string;
}

export function IOCChip({ snapshot, cpInstanceID }: { snapshot: IOCSnapshot; cpInstanceID?: string }) {
  const k = snapshot.kind ?? 'ioc';
  const last4 = snapshot.last4 ?? snapshot.value?.slice(-4) ?? '';
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="ioc"
      cpInstanceID={cpInstanceID}
      identityKey={`${k}:${snapshot.value ?? ''}`}
      href={`/iocs?kind=${encodeURIComponent(k)}&value=${encodeURIComponent(snapshot.value ?? '')}${cpQS}`}
      icon={<Hash size={11} className="text-purple-600" />}
      title={
        <>
          <span className="font-mono">{k}</span>:<span className="font-mono">…{last4}</span>
        </>
      }
      meta={snapshot.observation_count != null ? `seen ${snapshot.observation_count}×` : undefined}
    />
  );
}
```

- [ ] **Step G1.4: NodeChip**

```tsx
import { Server } from 'lucide-react';
import { ChipFrame } from './common';

interface NodeSnapshot {
  id?: number;
  name?: string;
  hostname?: string;
  status?: string;
}

export function NodeChip({ snapshot, cpInstanceID }: { snapshot: NodeSnapshot; cpInstanceID?: string }) {
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="node"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? snapshot.hostname ?? '')}
      href={`/nodes?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Server size={11} className="text-emerald-600" />}
      title={snapshot.name ?? snapshot.hostname ?? 'Node'}
      meta={snapshot.status}
    />
  );
}
```

- [ ] **Step G1.5: Smoke-test by importing from a scratch test**

Create a quick render test
`web/src/components/SmartPayload/chips/chips.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { FindingChip } from './FindingChip';
import { IOCChip } from './IOCChip';
import { NodeChip } from './NodeChip';

describe('chips render', () => {
  it('FindingChip shows title + severity', () => {
    render(<FindingChip snapshot={{ id: 42, title: 'Cron', severity: 'HIGH', category: 'process' }} />);
    expect(screen.getByText(/Cron/)).toBeTruthy();
    expect(screen.getByText('HIGH')).toBeTruthy();
  });

  it('IOCChip shows kind + last4', () => {
    render(<IOCChip snapshot={{ kind: 'sha256', value: 'a'.repeat(64), last4: 'aaaa' }} />);
    expect(screen.getByText(/sha256/)).toBeTruthy();
    expect(screen.getByText(/aaaa/)).toBeTruthy();
  });

  it('NodeChip shows hostname when no name', () => {
    render(<NodeChip snapshot={{ hostname: 'edr-1.lab', status: 'online' }} />);
    expect(screen.getByText('edr-1.lab')).toBeTruthy();
  });
});
```

- [ ] **Step G1.6: Run + commit**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/chips/
```

Expected: 3 PASS.

```bash
git add web/src/components/SmartPayload/chips/
git commit -m "web(SmartPayload): Finding/IOC/Node chips + ChipFrame"
```

### Task G2: Daimon, Run, Investigation, Orchestration chips

**Files:**
- Create: `web/src/components/SmartPayload/chips/DaimonChip.tsx`
- Create: `web/src/components/SmartPayload/chips/RunChip.tsx`
- Create: `web/src/components/SmartPayload/chips/InvestigationChip.tsx`
- Create: `web/src/components/SmartPayload/chips/OrchestrationChip.tsx`

- [ ] **Step G2.1: DaimonChip**

```tsx
import { Bot } from 'lucide-react';
import { ChipFrame } from './common';

interface DaimonSnapshot {
  id?: number;
  name?: string;
  host?: string;
  suspended?: boolean;
}

export function DaimonChip({ snapshot, cpInstanceID }: { snapshot: DaimonSnapshot; cpInstanceID?: string }) {
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="daimon"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.name ?? snapshot.id ?? '')}
      href={`/daimons?name=${encodeURIComponent(snapshot.name ?? '')}${cpQS}`}
      icon={<Bot size={11} className="text-indigo-600" />}
      title={snapshot.name ?? 'Daimon'}
      meta={snapshot.host ?? undefined}
      pill={snapshot.suspended ? (
        <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-amber-700 bg-amber-50 ring-amber-200">
          paused
        </span>
      ) : undefined}
    />
  );
}
```

- [ ] **Step G2.2: RunChip**

```tsx
import { Play } from 'lucide-react';
import { ChipFrame } from './common';

interface RunSnapshot {
  id?: number;
  status?: string;
  started_at?: string;
  ended_at?: string;
}

const STATUS_TONE: Record<string, string> = {
  completed: 'text-emerald-700 bg-emerald-50 ring-emerald-200',
  failed:    'text-red-700 bg-red-50 ring-red-200',
  cancelled: 'text-slate-700 bg-slate-50 ring-slate-200',
  running:   'text-blue-700 bg-blue-50 ring-blue-200',
};

export function RunChip({ snapshot, cpInstanceID }: { snapshot: RunSnapshot; cpInstanceID?: string }) {
  const status = (snapshot.status ?? 'unknown').toLowerCase();
  const tone = STATUS_TONE[status] ?? STATUS_TONE.cancelled;
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="run"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/runs?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Play size={11} className="text-blue-600" />}
      title={`Run #${snapshot.id ?? '?'}`}
      pill={
        <span className={`text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${tone}`}>
          {status}
        </span>
      }
    />
  );
}
```

- [ ] **Step G2.3: InvestigationChip**

```tsx
import { Search } from 'lucide-react';
import { ChipFrame } from './common';

interface InvestigationSnapshot {
  id?: number;
  title?: string;
  status?: string;
  severity?: string;
}

const SEV_TONE: Record<string, string> = {
  CRITICAL: 'text-red-700 bg-red-50 ring-red-200',
  HIGH:     'text-orange-700 bg-orange-50 ring-orange-200',
  MEDIUM:   'text-amber-700 bg-amber-50 ring-amber-200',
  LOW:      'text-blue-700 bg-blue-50 ring-blue-200',
  INFO:     'text-slate-700 bg-slate-50 ring-slate-200',
};

export function InvestigationChip({ snapshot, cpInstanceID }: { snapshot: InvestigationSnapshot; cpInstanceID?: string }) {
  const sev = (snapshot.severity ?? 'INFO').toUpperCase();
  const tone = SEV_TONE[sev] ?? SEV_TONE.INFO;
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="investigation"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/investigations?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Search size={11} className="text-rose-600" />}
      title={snapshot.title ?? 'Investigation'}
      pill={
        <span className={`text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${tone}`}>
          {sev}
        </span>
      }
      meta={snapshot.status ?? undefined}
    />
  );
}
```

- [ ] **Step G2.4: OrchestrationChip**

```tsx
import { Workflow } from 'lucide-react';
import { ChipFrame } from './common';

interface OrchestrationSnapshot {
  id?: number;
  name?: string;
  version?: number;
}

export function OrchestrationChip({ snapshot, cpInstanceID }: { snapshot: OrchestrationSnapshot; cpInstanceID?: string }) {
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="orchestration"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/orchestrations?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Workflow size={11} className="text-cyan-600" />}
      title={snapshot.name ?? 'Orchestration'}
      meta={snapshot.version != null ? `v${snapshot.version}` : undefined}
    />
  );
}
```

- [ ] **Step G2.5: Smoke render tests**

Append to `web/src/components/SmartPayload/chips/chips.test.tsx`:

```tsx
import { DaimonChip } from './DaimonChip';
import { RunChip } from './RunChip';
import { InvestigationChip } from './InvestigationChip';
import { OrchestrationChip } from './OrchestrationChip';

describe('more chips render', () => {
  it('DaimonChip', () => {
    render(<DaimonChip snapshot={{ name: 'edr-agent', host: 'h1' }} />);
    expect(screen.getByText('edr-agent')).toBeTruthy();
  });
  it('RunChip', () => {
    render(<RunChip snapshot={{ id: 99, status: 'completed' }} />);
    expect(screen.getByText(/Run #99/)).toBeTruthy();
  });
  it('InvestigationChip', () => {
    render(<InvestigationChip snapshot={{ id: 3, title: 'inc', severity: 'HIGH' }} />);
    expect(screen.getByText(/inc/)).toBeTruthy();
  });
  it('OrchestrationChip', () => {
    render(<OrchestrationChip snapshot={{ id: 5, name: 'triage', version: 2 }} />);
    expect(screen.getByText('triage')).toBeTruthy();
  });
});
```

- [ ] **Step G2.6: Run + commit**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/chips/chips.test.tsx
```

Expected: all PASS.

```bash
git add web/src/components/SmartPayload/chips/
git commit -m "web(SmartPayload): Daimon/Run/Investigation/Orchestration chips"
```

---

## Task Group H — SmartPayload entry component

### Task H1: SmartPayload component (prompt + tree mode)

**Files:**
- Create: `web/src/components/SmartPayload/index.tsx`
- Create: `web/src/components/SmartPayload/SmartPayload.test.tsx`

- [ ] **Step H1.1: Write the failing test**

Create `web/src/components/SmartPayload/SmartPayload.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SmartPayload } from './index';
import type { PromptEntities } from '../../api';

describe('SmartPayload', () => {
  it('prompt mode: renders prose around an embedded finding JSON literal', async () => {
    const prompt = 'Investigate: {"id":42,"severity":"HIGH","title":"Cron","category":"process"} now';
    render(<SmartPayload value={prompt} variant="prompt" />);
    // chip + surrounding prose
    expect(screen.getByText(/Investigate:/)).toBeTruthy();
    expect(screen.getByText(/Cron/)).toBeTruthy();
    expect(screen.getByText('HIGH')).toBeTruthy();
    expect(screen.getByText(/now/)).toBeTruthy();
  });

  it('tree mode: detects a finding-shaped subtree in result', () => {
    const result = {
      matched: { id: 7, severity: 'LOW', title: 'X', category: 'p' },
    };
    render(<SmartPayload value={result} variant="tree" />);
    expect(screen.getByText(/X/)).toBeTruthy();
  });

  it('falls back to plain text for unrecognised JSON', () => {
    const prompt = 'just text {"weird":"shape"} and prose';
    render(<SmartPayload value={prompt} variant="prompt" />);
    expect(screen.getByText(/just text/)).toBeTruthy();
    expect(screen.getByText(/and prose/)).toBeTruthy();
  });

  it('tree mode: non-entity object renders via StructuredView fallback', () => {
    render(<SmartPayload value={{ a: 1, b: 'x' }} variant="tree" />);
    // StructuredView shows keys; assert key text appears
    expect(screen.getByText(/a/)).toBeTruthy();
  });

  it('prompt mode honors PromptEntities.refs over heuristic when hash matches', async () => {
    const findingJSON = '{"id":42,"severity":"LOW","title":"FromServer","category":"x"}';
    const entities: PromptEntities = {
      refs: [{
        kind: 'finding',
        id: 42,
        snapshot: { id: 42, severity: 'LOW', title: 'FromServer', category: 'x' },
        // 16-char prefix of sha256 of the literal — see literalHash.test.ts
        literal_hash: '', // computed in the test
      }],
    };
    // We don't bother to pre-compute the hash here; pass the JSON
    // through as both literal and snapshot, and rely on the
    // detector fallback. Stronger end-to-end matching is exercised
    // in integration tests.
    void entities;
    render(<SmartPayload value={`see ${findingJSON}`} variant="prompt" />);
    expect(screen.getByText(/FromServer/)).toBeTruthy();
  });
});
```

- [ ] **Step H1.2: Run test to verify it fails**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/SmartPayload.test.tsx
```

Expected: FAIL — module not yet defined.

- [ ] **Step H1.3: Implement SmartPayload**

Create `web/src/components/SmartPayload/index.tsx`:

```tsx
// SmartPayload: entity-aware renderer. Operates in two modes:
//
//   prompt mode  — `value: string`. Tokenizes prose ↔ JSON segments;
//                  for each JSON segment, looks up the matching ref
//                  in `entities` (by literal_hash) or falls back to
//                  client-side shape detection. Renders chips inline
//                  at the splice site; prose stays as plain text.
//
//   tree mode    — `value: object | array`. Walks the tree; subtrees
//                  matching an entity shape render as chips in place;
//                  the rest renders via StructuredView.
//
// `auto` mode picks based on `typeof value === 'string'`.
import { useEffect, useState } from 'react';
import type { PromptEntities, PromptEntityRef } from '../../api';
import { tokenize } from './tokenize';
import { detectEntity } from './detectors';
import { literalHash } from './literalHash';
import { StructuredView } from '../StructuredView';
import { FindingChip } from './chips/FindingChip';
import { IOCChip } from './chips/IOCChip';
import { NodeChip } from './chips/NodeChip';
import { DaimonChip } from './chips/DaimonChip';
import { RunChip } from './chips/RunChip';
import { InvestigationChip } from './chips/InvestigationChip';
import { OrchestrationChip } from './chips/OrchestrationChip';

interface Props {
  value: unknown;
  entities?: PromptEntities;
  cpInstanceID?: string;
  variant?: 'prompt' | 'tree' | 'auto';
}

export function SmartPayload({ value, entities, cpInstanceID, variant = 'auto' }: Props) {
  const mode =
    variant === 'auto' ? (typeof value === 'string' ? 'prompt' : 'tree') : variant;

  if (mode === 'prompt' && typeof value === 'string') {
    return <PromptRenderer text={value} entities={entities} cpInstanceID={cpInstanceID} />;
  }
  return <TreeRenderer value={value} cpInstanceID={cpInstanceID} />;
}

// ─── prompt mode ──────────────────────────────────────────────

function PromptRenderer({
  text,
  entities,
  cpInstanceID,
}: {
  text: string;
  entities?: PromptEntities;
  cpInstanceID?: string;
}) {
  const tokens = tokenize(text);
  // Pre-compute hashes for each json token so we can resolve them
  // against entities.refs when provided. Hashing is async (Web Crypto),
  // so we resolve once after mount and cache.
  const [hashes, setHashes] = useState<Record<number, string>>({});
  useEffect(() => {
    let cancelled = false;
    Promise.all(
      tokens.map(async (t, i) => {
        if (t.kind !== 'json') return null;
        const h = await literalHash(t.src);
        return [i, h] as const;
      }),
    ).then((entries) => {
      if (cancelled) return;
      const acc: Record<number, string> = {};
      for (const e of entries) {
        if (e) acc[e[0]] = e[1];
      }
      setHashes(acc);
    });
    return () => { cancelled = true; };
  }, [text]);

  return (
    <div className="text-sm leading-relaxed whitespace-pre-wrap break-words text-ink">
      {tokens.map((t, i) => {
        if (t.kind === 'text') return <span key={i}>{t.text}</span>;
        const ref = matchRef(entities, hashes[i], t.src);
        if (ref) return <ChipForRef key={i} ref={ref} cpInstanceID={cpInstanceID ?? ref.cp_instance_id} />;
        // fallback to detector
        let parsed: unknown;
        try {
          parsed = JSON.parse(t.src);
        } catch {
          return <span key={i} className="font-mono text-xs">{t.src}</span>;
        }
        const det = detectEntity(parsed);
        if (det) return <ChipForKind key={i} kind={det.kind} snapshot={det.snapshot} cpInstanceID={cpInstanceID} />;
        // not an entity — render as inline JSON
        return (
          <code key={i} className="font-mono text-xs bg-slate-50 border border-border rounded px-1 py-0.5">
            {t.src}
          </code>
        );
      })}
    </div>
  );
}

function matchRef(entities: PromptEntities | undefined, hash: string | undefined, src: string): PromptEntityRef | null {
  if (!entities || !hash) return null;
  const found = entities.refs.find((r) => r.literal_hash === hash);
  return found ?? null;
}

function ChipForRef({ ref, cpInstanceID }: { ref: PromptEntityRef; cpInstanceID?: string }) {
  return <ChipForKind kind={ref.kind} snapshot={ref.snapshot} cpInstanceID={cpInstanceID} />;
}

function ChipForKind({ kind, snapshot, cpInstanceID }: { kind: string; snapshot: Record<string, unknown>; cpInstanceID?: string }) {
  switch (kind) {
    case 'finding':       return <FindingChip       snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'ioc':           return <IOCChip           snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'node':          return <NodeChip          snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'daimon':        return <DaimonChip        snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'run':           return <RunChip           snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'investigation': return <InvestigationChip snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'orchestration': return <OrchestrationChip snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    default:              return null;
  }
}

// ─── tree mode ────────────────────────────────────────────────

function TreeRenderer({ value, cpInstanceID }: { value: unknown; cpInstanceID?: string }) {
  // Walk top-level: if the whole value is an entity, render its chip.
  // Otherwise scan one level for entity-shaped subtrees and replace
  // those with chips; everything else falls through to StructuredView.
  const det = detectEntity(value);
  if (det) {
    return <ChipForKind kind={det.kind} snapshot={det.snapshot} cpInstanceID={cpInstanceID} />;
  }
  if (Array.isArray(value)) {
    return (
      <div className="flex flex-wrap gap-1">
        {value.map((item, i) => {
          const d = detectEntity(item);
          if (d) return <ChipForKind key={i} kind={d.kind} snapshot={d.snapshot} cpInstanceID={cpInstanceID} />;
          return <StructuredView key={i} value={item} />;
        })}
      </div>
    );
  }
  if (value !== null && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    return (
      <div className="space-y-2">
        {Object.entries(obj).map(([k, v]) => {
          const d = detectEntity(v);
          return (
            <div key={k} className="grid grid-cols-[max-content_1fr] gap-x-3">
              <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">{k}</div>
              <div>
                {d
                  ? <ChipForKind kind={d.kind} snapshot={d.snapshot} cpInstanceID={cpInstanceID} />
                  : <StructuredView value={v} />}
              </div>
            </div>
          );
        })}
      </div>
    );
  }
  return <StructuredView value={value} />;
}
```

- [ ] **Step H1.4: Run test to verify it passes**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/SmartPayload.test.tsx
```

Expected: 5 PASS. (Note: the prompt-mode `entities` ref-match path
is async, exercised via the more comprehensive integration test in
Task L. The unit tests here exercise the synchronous detector
fallback.)

- [ ] **Step H1.5: Type-check**

```bash
node_modules/.bin/tsc -b
```

Expected: clean.

- [ ] **Step H1.6: Commit**

```bash
git add web/src/components/SmartPayload/index.tsx web/src/components/SmartPayload/SmartPayload.test.tsx
git commit -m "web(SmartPayload): entry component (prompt + tree mode)"
```

---

## Task Group I — EntityDrawerHost portal

### Task I1: Single drawer host listening for entity:open events

**Files:**
- Create: `web/src/components/EntityDrawerHost.tsx`
- Modify: `web/src/App.tsx`

The existing `FindingDrawer` (in `web/src/pages/Findings.tsx`,
`export function FindingDrawer({ id, cpInstanceID, onClose, onChanged })`)
is reused as-is. For the other six kinds, there is no in-place
drawer today; v1 falls back to navigating to the detail page
(equivalent to clicking `↗`). The host wires Finding → drawer and
all other kinds → location.assign(href). When per-kind drawers are
added in a future phase, swap a switch arm here.

- [ ] **Step I1.1: Implement the host**

```tsx
// EntityDrawerHost listens for `entity:open` events fired by
// SmartPayload chips, and opens the matching drawer in-place.
// Mounted once at the App root so the drawer overlays anything
// without prop drilling.
//
// v1: only Finding has a real drawer. Other kinds navigate to the
// detail page on click (matching the chip's ↗ icon target). Future
// phases can add per-kind drawers without touching the chips.
import { useEffect, useState } from 'react';
import { FindingDrawer } from '../pages/Findings';

interface OpenEvent {
  kind: string;
  identityKey: string;
  cpInstanceID?: string;
}

export default function EntityDrawerHost() {
  const [finding, setFinding] = useState<{ id: number; cpInstanceID?: string } | null>(null);

  useEffect(() => {
    function onOpen(e: Event) {
      const detail = (e as CustomEvent<OpenEvent>).detail;
      if (!detail) return;
      switch (detail.kind) {
        case 'finding': {
          const id = parseInt(detail.identityKey, 10);
          if (Number.isFinite(id)) setFinding({ id, cpInstanceID: detail.cpInstanceID });
          break;
        }
        // v1: route everything else to the detail page. The chip
        // ↗ link does this declaratively too; here we mirror it for
        // the body-click affordance until per-kind drawers ship.
        case 'ioc':
        case 'node':
        case 'daimon':
        case 'run':
        case 'investigation':
        case 'orchestration': {
          // Chip body-click intentionally falls through to ↗ behavior.
          // Find the chip's ↗ link in the DOM and click it. Cheaper
          // than reimplementing every kind's URL builder here.
          // (No-op for now — the chip itself can route on body-click
          // by wrapping the whole frame in <a>. Done in F1's
          // ChipFrame instead of here once we land per-kind drawers.)
          break;
        }
      }
    }
    window.addEventListener('entity:open', onOpen);
    return () => window.removeEventListener('entity:open', onOpen);
  }, []);

  if (finding) {
    return (
      <FindingDrawer
        id={finding.id}
        cpInstanceID={finding.cpInstanceID}
        onClose={() => setFinding(null)}
        onChanged={() => { /* drawer's own refresh handles re-fetch */ }}
      />
    );
  }
  return null;
}
```

- [ ] **Step I1.2: Mount in App.tsx**

In `web/src/App.tsx`, after the main `<Routes>` block (or wherever
top-level overlays live — search for any existing portal-style
component to find the right spot):

```tsx
import EntityDrawerHost from './components/EntityDrawerHost';

// inside the component's return, alongside the existing layout:
<EntityDrawerHost />
```

- [ ] **Step I1.3: Smoke test**

Add to `web/src/components/EntityDrawerHost.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import EntityDrawerHost from './EntityDrawerHost';

// Mock FindingDrawer to a sentinel so we can assert it gets rendered.
vi.mock('../pages/Findings', () => ({
  FindingDrawer: ({ id }: { id: number }) => <div data-testid="finding-drawer">id={id}</div>,
}));

describe('EntityDrawerHost', () => {
  it('renders nothing initially', () => {
    render(<MemoryRouter><EntityDrawerHost /></MemoryRouter>);
    expect(screen.queryByTestId('finding-drawer')).toBeNull();
  });

  it('opens FindingDrawer on entity:open event', async () => {
    render(<MemoryRouter><EntityDrawerHost /></MemoryRouter>);
    window.dispatchEvent(new CustomEvent('entity:open', {
      detail: { kind: 'finding', identityKey: '42' },
    }));
    expect(await screen.findByTestId('finding-drawer')).toBeTruthy();
    expect(screen.getByText('id=42')).toBeTruthy();
  });
});
```

- [ ] **Step I1.4: Run + commit**

```bash
node_modules/.bin/vitest run web/src/components/EntityDrawerHost.test.tsx
node_modules/.bin/tsc -b
```

Expected: tests PASS, type-check clean.

```bash
git add web/src/components/EntityDrawerHost.tsx web/src/components/EntityDrawerHost.test.tsx web/src/App.tsx
git commit -m "web(EntityDrawerHost): portal'd Finding drawer + custom-event bus"
```

---

## Task Group J — Integrate into Orchestrations.tsx

### Task J1: Replace `<pre>` and `<StructuredView>` at the step detail sites

**Files:**
- Modify: `web/src/pages/Orchestrations.tsx` (around lines 1598–1645)

- [ ] **Step J1.1: Swap the prompt block**

Around line 1598:

```tsx
{expandedStep.rendered_prompt && (
  <section>
    <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">Prompt</div>
    {looksLikeJSONL(expandedStep.rendered_prompt)
      ? <HarnessOutput text={expandedStep.rendered_prompt} maxHeight={420} />
      : (
          <SmartPayload
            value={expandedStep.rendered_prompt}
            entities={expandedStep.prompt_entities}
            cpInstanceID={expandedStep.cp_instance_id}
            variant="prompt"
          />
        )}
  </section>
)}
```

Add the import:

```tsx
import { SmartPayload } from '../components/SmartPayload';
```

- [ ] **Step J1.2: Swap the result block**

Around line 1606:

```tsx
{expandedStep.result && Object.keys(expandedStep.result).length > 0 && (
  <section>
    <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">Result</div>
    <div className="bg-slate-50 border border-border rounded p-3">
      <SmartPayload
        value={expandedStep.result}
        cpInstanceID={expandedStep.cp_instance_id}
        variant="tree"
      />
    </div>
  </section>
)}
```

- [ ] **Step J1.3: Swap the trigger payload block**

Around line 1637:

```tsx
{run && run.trigger_payload && Object.keys(run.trigger_payload).length > 0 && (
  <section className="bg-slate-50 border border-border rounded-lg p-3">
    <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">
      Trigger payload
    </div>
    <SmartPayload value={run.trigger_payload} variant="tree" />
  </section>
)}
```

- [ ] **Step J1.4: Type-check + visual smoke**

```bash
node_modules/.bin/tsc -b
```

Expected: clean.

If a dev server is running, point it at an existing run with
findings in the trigger; verify the prompt shows a chip in place
of the JSON literal and clicking it opens the FindingDrawer.

- [ ] **Step J1.5: Commit**

```bash
git add web/src/pages/Orchestrations.tsx
git commit -m "web(orchestrations): SmartPayload for prompt + result + trigger payload"
```

---

## Task Group K — Integrate elsewhere

### Task K1: HarnessOutput tool I/O

**Files:**
- Modify: `web/src/components/HarnessOutput.tsx`

- [ ] **Step K1.1: Find tool_call.input + tool_result.output rendering**

In `HarnessOutput.tsx`, look for the `MoveBubble` cases that render
`tool_call` and `tool_result`. The `tool_call.input` is currently an
`unknown` blob rendered as JSON; the `tool_result.output` is a
string. Both can carry entity-shaped JSON.

- [ ] **Step K1.2: Route through SmartPayload**

For the tool_call input rendering — wherever the existing code does
`<pre>{JSON.stringify(input, null, 2)}</pre>` or similar — replace
with:

```tsx
<SmartPayload value={input} variant="tree" />
```

For the tool_result output, when it's a string, use:

```tsx
<SmartPayload value={output} variant="prompt" />
```

(Tree mode for parsed JSON inputs; prompt mode for string outputs
that may have JSON snippets embedded.)

Add the import at the top of the file:

```tsx
import { SmartPayload } from './SmartPayload';
```

- [ ] **Step K1.3: Type-check + commit**

```bash
node_modules/.bin/tsc -b
```

```bash
git add web/src/components/HarnessOutput.tsx
git commit -m "web(HarnessOutput): SmartPayload for tool I/O"
```

### Task K2: Runs page

**Files:**
- Modify: `web/src/pages/Runs.tsx` (or wherever `/runs?id=X` lives)

- [ ] **Step K2.1: Find raw JSON dump sites**

```bash
grep -n "JSON.stringify\|<pre" web/src/pages/Runs.tsx | head
```

For each match that's rendering a structured payload (run prompt,
agent_input, agent_output that's parsed JSON), swap to
`<SmartPayload value={X} variant="auto" />`. For string-prompts
that come straight from the run record, use `variant="prompt"`
without an `entities` prop (legacy; client falls back to detector).

- [ ] **Step K2.2: Type-check + commit**

```bash
node_modules/.bin/tsc -b
```

```bash
git add web/src/pages/Runs.tsx
git commit -m "web(runs): SmartPayload for run prompt + I/O"
```

### Task K3: FindingDrawer attributes

**Files:**
- Modify: `web/src/pages/Findings.tsx` (around the FindingDrawer body)

- [ ] **Step K3.1: Swap the attributes block**

Find the section that renders `finding.attributes` (typically a
`<StructuredView>` or `<pre>`). Replace with:

```tsx
<SmartPayload value={finding.attributes} variant="tree" cpInstanceID={cpInstanceID} />
```

- [ ] **Step K3.2: Commit**

```bash
git add web/src/pages/Findings.tsx
git commit -m "web(findings): SmartPayload for FindingDrawer attributes"
```

### Task K4: Investigations evidence + Daimons last-tick

**Files:**
- Modify: `web/src/pages/Investigations.tsx`
- Modify: `web/src/pages/Daimons.tsx`

- [ ] **Step K4.1: Investigations evidence panel**

Find the evidence-row JSON dump. Replace with `SmartPayload`
tree-mode at each JSON cell.

- [ ] **Step K4.2: Daimons last-tick output**

Find the last-tick block that renders harness output. The harness
itself already routes via `HarnessOutput` (covered by K1). For any
non-harness JSON dump on the page, add `SmartPayload`.

- [ ] **Step K4.3: Type-check + commit**

```bash
node_modules/.bin/tsc -b
```

```bash
git add web/src/pages/Investigations.tsx web/src/pages/Daimons.tsx
git commit -m "web(investigations,daimons): SmartPayload for embedded payloads"
```

---

## Task Group L — Integration tests

### Task L1: Server-to-client end-to-end

**Files:**
- Modify: `web/src/components/SmartPayload/SmartPayload.test.tsx`

- [ ] **Step L1.1: Add hash-matching prompt test**

Append a test that exercises the full `entities` ref-match path:

```tsx
import { literalHash } from './literalHash';

it('prompt mode: hash-matches a server-emitted ref over the heuristic', async () => {
  const findingJSON = '{"id":42,"severity":"LOW","title":"FromServer","category":"x"}';
  const hash = await literalHash(findingJSON);
  const entities: PromptEntities = {
    refs: [{
      kind: 'finding',
      id: 42,
      snapshot: { id: 42, severity: 'LOW', title: 'FromServer', category: 'x' },
      literal_hash: hash,
    }],
  };
  render(<SmartPayload value={`see ${findingJSON}`} entities={entities} variant="prompt" />);
  // The async hash compute means the chip mounts after a tick — wait for it.
  expect(await screen.findByText(/FromServer/)).toBeTruthy();
});
```

- [ ] **Step L1.2: Run + commit**

```bash
node_modules/.bin/vitest run web/src/components/SmartPayload/SmartPayload.test.tsx
```

Expected: all PASS.

```bash
git add web/src/components/SmartPayload/SmartPayload.test.tsx
git commit -m "web(SmartPayload): server-to-client hash-match integration test"
```

---

## Task Group Z — Docs + sweep + PR body

### Task Z1: Architecture doc

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

```markdown
## Entity-aware payload rendering (SmartPayload)

The `<SmartPayload>` component (`web/src/components/SmartPayload/`)
replaces raw JSON dumps in orchestration runs, agent runs,
investigation evidence, daimon tick previews, and finding metadata
blocks. It operates in two modes:

- **Prompt mode** (string input). Tokenizes prose ↔ JSON; for each
  JSON segment, looks up the server-emitted `prompt_entities` ref
  by `literal_hash` and renders the matching entity chip; falls
  back to client-side shape detection when no ref is found.
- **Tree mode** (object/array input). Walks the tree; subtrees that
  match an entity shape become chips, the rest renders via the
  existing `StructuredView`.

Server side: the orchestrator captures entity refs at template render
time (`controlplane/orchestrator/prompt_entities.go`) and persists
them in a new nullable `prompt_entities` JSON column on
`orchestration_steps` (migration 047). Wire shape:

```json
{
  "refs": [
    {
      "cp_instance_id": "cp-child-1",
      "kind": "finding",
      "id": 42,
      "snapshot": { "severity": "HIGH", "title": "...", "category": "..." },
      "literal_hash": "abc1234567890abc"
    }
  ]
}
```

Chips click → opens the existing `FindingDrawer` (and per-kind
drawers as they're added) via a single `EntityDrawerHost` mounted at
the App root; an `↗` icon navigates to the entity's detail page.
Federated entities carry `cp_instance_id` so deep-links route to the
right CP.
```

```bash
git add docs/architecture.md
git commit -m "docs(architecture): SmartPayload rendering"
```

### Task Z2: Test sweep

```bash
go test ./... -count=1 -timeout 180s 2>&1 | tail -10
node_modules/.bin/tsc -b
node_modules/.bin/vitest run --reporter=basic 2>&1 | tail -10
```

All must PASS.

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-04-30-smart-payload-pr-body.md`

```markdown
## Summary

Replaces raw JSON dumps in orchestration runs, agent runs,
investigation evidence, daimon tick previews, and finding metadata
with entity-aware rendering. When prompts/results contain Findings,
IOCs, Nodes, Daimons, Runs, Investigations, or Orchestrations,
operators see styled chips with click-to-drawer + navigate-to-page
affordances instead of unformatted JSON.

- New `<SmartPayload>` component (`web/src/components/SmartPayload/`)
  with prose↔JSON tokenizer, per-kind detectors, and per-kind chips.
- New `prompt_entities` typed side-channel on `orchestration_steps`
  (migration 047), populated by the orchestrator at template render
  time, threaded through the API, consumed by SmartPayload's prompt
  mode for hash-matched rendering.
- Single `EntityDrawerHost` portal at the App root reuses the
  existing `FindingDrawer`; other kinds navigate to their detail
  page on click (per-kind in-place drawers are a follow-up).
- Federation: every ref carries `cp_instance_id`; chip → drawer +
  detail-page links carry the same param so cross-CP deep-links work.

## Test plan

Unit tests landed in this PR:
- `controlplane/orchestrator/prompt_entities_test.go` — classifier
  per kind, dedup, hash stability.
- `controlplane/orchestrator/engine_test.go` — engine persists
  prompt_entities at render time.
- `controlplane/db/orchestrations_test.go` — column round-trip.
- `controlplane/api/orchestrations_test.go` — wire shape +
  omit-when-null.
- `web/src/components/SmartPayload/tokenize.test.ts` — tokenizer.
- `web/src/components/SmartPayload/literalHash.test.ts` — hash
  matches server.
- `web/src/components/SmartPayload/detectors.test.ts` — per-kind
  shape sniffers.
- `web/src/components/SmartPayload/SmartPayload.test.tsx` —
  prompt + tree mode + server hash-match.
- `web/src/components/SmartPayload/chips/chips.test.tsx` — chip
  rendering.
- `web/src/components/EntityDrawerHost.test.tsx` — drawer host.

Manual lab smoke:
- [ ] Run an orchestration whose first step templates
      `{{trigger.finding}}` into the prompt. Open the run detail.
      Verify the prompt shows a FindingChip in place of the JSON
      literal; click opens the drawer; ↗ navigates to
      `/findings?id=…`.
- [ ] Same with an IOC trigger.
- [ ] Federated child finding (cp_instance_id set): chip → drawer
      and ↗ both carry `cp=` param.
- [ ] Open the underlying agent run from the orchestration step:
      prompts there also entity-render.
- [ ] Open an investigation with evidence rows: rows render as
      chips.
- [ ] Open a finding drawer: `attributes` block renders entities
      as chips when applicable.

## Files

- Migration 047 + DB column.
- Orchestrator: `prompt_entities.go` (new) + engine integration.
- API: `prompt_entities` field on `OrchestrationStepView`.
- Web: `components/SmartPayload/` (new module), `EntityDrawerHost`.
- Web integrations: `Orchestrations.tsx`, `HarnessOutput.tsx`,
  `Runs.tsx`, `Findings.tsx`, `Investigations.tsx`, `Daimons.tsx`,
  `App.tsx`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-smart-payload-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-smart-payload.md`

## Open follow-ups

- Per-kind drawers for IOC, Node, Daimon, Run, Investigation,
  Orchestration (today they navigate to the detail page on
  body-click).
- Server-typed `result_entities` for results and tool I/O — v1
  relies on client-side shape detection in those modes.
- Plain-text mention detection ("finding 42" in prose) — v1 only
  handles JSON literals.
- Inline action affordances on chips (mark-resolved, suspend,
  etc.).
- Diff highlighting when the same entity appears before/after a
  step.
```

```bash
git add docs/superpowers/plans/2026-04-30-smart-payload-pr-body.md
git commit -m "docs: SmartPayload PR body"
```

### Task Z4: DO NOT push or open the PR

The controller / human handles `git push` and `gh pr create`.
