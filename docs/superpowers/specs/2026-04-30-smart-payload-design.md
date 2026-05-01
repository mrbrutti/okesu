# SmartPayload — entity-aware rendering of Okesu JSON in the UI

## Goal

Replace today's raw JSON dumps in orchestration runs, agent runs,
investigation evidence, daimon tick previews, and finding metadata
blocks with a renderer that recognises Okesu entity payloads
(Findings, IOCs, Nodes, Daimons, Runs, Investigations, Orchestrations)
and renders them as styled cards with click-to-drawer + navigate-to-page
affordances. Operators stop reading JSON to triage; they see entities.

## Non-goals (cut)

- Plain-text mention detection ("look at finding #42" in prose) — out
  of scope; we render only literal JSON snippets / structured trees.
- Per-entity rich previews beyond what the existing drawers / cards
  already render — we reuse `FindingDrawer` etc., not build new ones.
- Editing entities from the orchestration view — read-only display
  with deep-link navigation.
- Backfill of `prompt_entities` for historical orchestration runs —
  legacy rows degrade gracefully (see fallback below).

## Architecture

One reusable React component, **`<SmartPayload>`**, lives at
`web/src/components/SmartPayload/`. It accepts:

- `value`: a `string | unknown` payload (rendered_prompt, result, tool
  I/O, finding metadata, anything previously dumped via `<pre>` or
  `StructuredView`).
- `entities` (optional): a typed side-channel (`PromptEntities` shape
  below) listing the entities the server knows are referenced. Only
  the orchestration step's prompt path passes this; everywhere else
  uses pure client-side recognition.

`SmartPayload` operates in two modes determined by the `value` type:

1. **Prompt mode** (`value: string`, `entities` provided). Walks the
   string finding balanced `{…}` / `[…]` JSON blocks. For each match,
   `JSON.parse` and identity-match against `entities` to render the
   correct entity card stack inline. Surrounding prose stays as-is.
   When `entities` isn't provided (legacy row), falls back to tree-mode
   shape sniffing on the parsed JSON block.

2. **Tree mode** (`value: object | array`). Walks the tree like
   `StructuredView` does today, but at every object node first asks
   "does this match an entity shape?" — if yes, render an entity card
   in place; if no, fall through to `StructuredView`'s normal node
   rendering.

Both modes render entity cards via the same per-kind components:
`FindingChip`, `IOCChip`, `NodeChip`, `DaimonChip`, `RunChip`,
`InvestigationChip`, `OrchestrationChip`. Each chip:

- Shows a compact summary (kind icon, key fields, status pill, etc.)
- Click-on-body opens the existing detail drawer for that kind
  in-place (portal'd over the page) — `FindingDrawer` already
  exists; we add lightweight drawers / reuse existing pages for the
  others as needed.
- A small `↗` icon link navigates to the full detail page
  (`/findings?id=42`, `/iocs?value=…`, `/nodes?id=12`, etc.) —
  carrying `cp_instance_id` for federated entities.

## Data flow

### Server side — `prompt_entities` capture

1. **DB schema.** New nullable `prompt_entities` JSON column on
   `orchestration_steps` (sqlite + postgres migrations). No backfill.

2. **Engine capture.** In `controlplane/orchestrator/engine.go`, the
   render path that calls `Render(step.Prompt, env)` is replaced by
   a thin wrapper that, before calling `stringify` on each templated
   value, classifies it as an entity / list-of-entities and records
   the typed reference into a `PromptEntities` builder. The builder
   accumulates refs across all `{{…}}` substitutions in one prompt.
   The final `prompt_entities` JSON is stored on the step record
   alongside the existing `rendered_prompt` string.

3. **Entity classification.** The engine has typed visibility into
   what gets templated — `[]DispatchedFinding`, `IOCRef`,
   `NodeSelector`-resolved hosts, etc. Classification is done by Go
   type, not shape sniffing. New types we don't recognise are
   stringified as today (no entity ref recorded) — strictly additive,
   never breaks existing prompts.

4. **API wire shape.** `OrchestrationStepView` (the JSON returned by
   `GET /api/orchestration-runs/{id}`) gains a new
   `prompt_entities` field, omitted when null. UI passes it straight
   to `SmartPayload`.

### `PromptEntities` shape

```ts
type PromptEntityRef = {
  // Identity — sufficient to navigate to live entity.
  cp_instance_id: string | null;   // null = same-CP, omit param in URL
  kind: 'finding' | 'ioc' | 'node' | 'daimon' | 'run' | 'investigation' | 'orchestration';
  id?: number;                      // findings, nodes, daimons, runs, investigations, orchestrations
  ioc_kind?: string;                // iocs use (kind, value) instead of id
  ioc_value?: string;

  // Snapshot — small, just enough to render the card without a fetch.
  // Per-kind subset — see "Snapshot fields per kind" below.
  snapshot: Record<string, unknown>;

  // Source — which JSON literal in the prompt this ref corresponds to.
  // The renderer matches `JSON.parse(literal)` against snapshot to
  // pick the right ref when the same prompt has multiple of the same
  // kind. Server emits a stable identity hash so client can match
  // without ambiguity.
  literal_hash: string;             // sha256 of the JSON literal (16 hex chars)
};

type PromptEntities = {
  refs: PromptEntityRef[];
};
```

`literal_hash` is a sha256-prefix of the exact JSON string the engine
substituted into the prompt. The client, after extracting a JSON
block from the prompt, hashes it the same way and looks up the ref.
This avoids guessing which finding-shaped object in the prompt
corresponds to which ref when there are duplicates.

### Snapshot fields per kind

```ts
finding:        { id, severity, title, category, status, host?, daimon? }
ioc:            { kind, value, last4, severity_max?, observation_count? }
node:           { id, name, hostname, status }
daimon:         { id, name, host, suspended }
run:            { id, status, started_at, ended_at? }
investigation:  { id, title, status, severity }
orchestration:  { id, name, version }
```

Total wire cost per ref: ~150–250 bytes. A prompt with 30 entity refs
adds ~5 KiB to the step record — negligible relative to existing
`rendered_prompt` + `output_summary` fields.

### Client side — `SmartPayload`

Public API:

```tsx
<SmartPayload
  value={step.rendered_prompt}     // or step.result, etc.
  entities={step.prompt_entities}  // optional
  cpInstanceID={step.cp_instance_id}
  variant="prompt" | "tree" | "auto"  // 'auto' picks based on value type
/>
```

Internal pipeline:

1. **Tokenize (prompt mode).** Walk the string char-by-char; track
   brace/bracket depth; emit `{kind:'text', text:'...'}` and
   `{kind:'json', src:'...'}` segments. Skip JSON inside string
   literals (handled via depth-aware quote tracking).

2. **Classify each `json` segment.**
   - If `entities` provided → hash `src` and look up in
     `entities.refs`. If found, render the corresponding chip.
   - Else (`entities` absent or hash miss) → `JSON.parse(src)`, walk
     in tree mode. Each object node is asked "does this match an
     entity shape?" via a per-kind detector. Detected nodes render as
     chips; the rest renders via `StructuredView`.

3. **Tree mode shape detectors** are duck-typed:
   - `Finding`: has `id` + `severity` + `title` + `category`.
   - `IOC`: has `kind` + `value` + (`type === 'ioc'` OR existence of
     `observation_count` / `severity_max`).
   - `Node`: has `id` + `hostname` + (`status` OR `os`).
   - `Daimon`: has `name` + `host` + (`agent_id` OR `suspended`).
   - `Run`: has `id` + `status` + (`started_at` OR `agent_name`).
   - `Investigation`: has `id` + `title` + (`severity` AND `status`)
     + a marker like `external_key` to disambiguate from Finding.
   - `Orchestration`: has `id` + `name` + `version`.

   Detectors are deliberately conservative — false positives are worse
   than false negatives (a missed entity renders as the existing
   `StructuredView` JSON tree, which is already readable). All
   detectors live in `web/src/components/SmartPayload/detectors.ts`,
   single-purpose and unit-tested.

4. **Card rendering.** Each kind has a chip component in
   `web/src/components/SmartPayload/chips/`. Chips reuse existing
   styling primitives (`StatusPill`, severity colors, monospace tags)
   and pull live data via the existing per-kind detail endpoints when
   the drawer opens.

5. **Drawer portal.** A new `<EntityDrawerHost>` rendered once at the
   `<App>` root listens for "open drawer" events emitted by chips,
   renders the appropriate drawer (`FindingDrawer` exists; the others
   we add as thin wrappers around the per-kind page detail
   components). Closing the drawer doesn't navigate.

## UI integration sites

- **Orchestrations.tsx** — replace the `<pre>` and `HarnessOutput`
  fallback for `expandedStep.rendered_prompt`; replace the
  `<StructuredView>` for `expandedStep.result`. The `output_summary`
  stays in `HarnessOutput`, but `HarnessOutput`'s `tool_call.input`
  and `tool_result.output` JSON-bubble panels swap to `SmartPayload`
  in tree mode.

- **Runs page** (`/runs?id=X`) — same swap for run prompt + harness
  output blocks.

- **Investigation evidence panel** — entity-bag JSON blocks become
  chip rows (this is already partially curated; we just upgrade the
  presentation).

- **Daimon tick previews** — the "last tick output" block on
  `/daimons?name=X` becomes harness-output-aware (it already is) +
  tool I/O entity-aware via `SmartPayload`.

- **Finding metadata** — the `attributes` JSON block on the
  `FindingDrawer` becomes `<SmartPayload value={attributes} />`.

Each integration is a one-line swap; the hard work all lives in
`SmartPayload` itself.

## Federation

Every entity ref carries `cp_instance_id`. Chip → drawer / page link
includes `cp=<cp_instance_id>` query param so the deep-link works
across federated CPs. The drawer's existing fetch path already
supports cross-CP reads via the federation aggregator. No new
federation plumbing.

## Backwards compatibility

- **Old orchestration steps without `prompt_entities`.** The column
  is null → `SmartPayload` falls back to tree-mode shape sniffing
  on the parsed JSON blocks. Operators see entity cards anyway,
  with the false-positive caveat. No-op DB migration; no data loss.

- **Old templates that emit unrecognised types.** Engine's
  classification is type-switched — unknown types skip the entity
  ref builder entirely and stringify as today. Strictly additive.

- **Legacy `<pre>` and `<StructuredView>` callers.** Untouched until
  the integration step rewrites them; `SmartPayload` is a strict
  superset of `StructuredView` (delegates to it for non-entity
  subtrees), so the swap can be incremental.

## Edge cases

- **Same entity referenced multiple times** in one prompt — each
  occurrence gets its own chip rendering at its own splice site;
  `literal_hash` lookup handles match.

- **Deleted entity at view time** — drawer fetch returns 404, drawer
  shows tombstone state. Chip itself still renders (it has the
  snapshot).

- **Federated entity, parent CP offline at view time** — drawer fetch
  fails; chip renders snapshot + a small "live data unavailable"
  warning in the drawer body. Chip click never produces a hard error.

- **Malformed JSON inside the prompt** — tokenizer falls through to
  text segment; renders as plain text. Same behavior as today.

- **Huge prompt (10s of MB)** — tokenizer is single-pass O(n).
  Existing `StructuredView` already collapses past depth/length
  thresholds. `SmartPayload` keeps the same caps.

- **Tool I/O inside `HarnessOutput`** — only the `tool_call.input`
  and `tool_result.output` JSON segments are routed through
  `SmartPayload` tree mode. The conversation rendering itself
  doesn't change.

## Testing

### Server side

- `controlplane/orchestrator/engine_test.go`: extend existing render
  tests so a step that templates in `[]DispatchedFinding` or `IOCRef`
  produces a non-empty `prompt_entities` with the right kinds, ids,
  and `literal_hash`es matching the JSON literals in the rendered
  prompt.
- `controlplane/db/orchestrations_test.go`: round-trip insert/get of
  the new column.
- `controlplane/api/orchestrations_test.go`: wire-shape includes the
  new field; omit-when-null behavior verified.

### Client side

- `web/src/components/SmartPayload/SmartPayload.test.tsx`:
  - Prompt mode: prose + finding JSON + prose → exactly one
    FindingChip rendered at the splice site, prose intact.
  - Prompt mode with no `entities`: falls back to detector,
    finds the same chip via shape sniffing.
  - Prompt mode `literal_hash` match across duplicates: two
    findings of the same shape both render as their own ref.
  - Tree mode: `result.matched_findings: [Finding[]]` renders an
    array of FindingChips.
  - Tree mode: ambiguous shape (an investigation that looks
    finding-shaped) prefers the more specific detector via
    `external_key`.
  - Federation: ref with `cp_instance_id` produces a chip whose
    drawer-open carries the `cp=` param.
- `web/src/components/SmartPayload/detectors.test.ts`:
  per-kind positive + negative cases. Each detector independently
  tested.

### Manual lab smoke (post-merge)

- Run an orchestration that includes a `triage` step with
  `{{trigger.finding}}` templated in. View the run detail; confirm
  the prompt shows a FindingChip in place of the JSON literal,
  click opens the drawer, "↗" navigates to `/findings?id=…`.
- Same flow with an IOC trigger.
- Same flow with a federated child finding (`cp_instance_id` set);
  verify the chip → drawer fetches across federation correctly.
- Open the underlying agent run from the orchestration step; verify
  prompts there also entity-render.
- Open an investigation that has a populated evidence panel; verify
  evidence rows render as chips.

## Out of scope (deferred)

- **Inline action affordances on chips** (e.g. "mark resolved" right
  on the FindingChip). v1 is read+navigate; bulk actions stay on
  the existing pages.
- **Diff highlighting** when the same entity is referenced before and
  after a step (e.g. "this finding's status went OPEN → RESOLVED
  during the orchestration").
- **Plain-text mention detection** ("finding 42" in prose).
- **Server-typed `result_entities`** — for v1 the result-side relies
  on client shape detection. If detection turns out fragile in
  practice, a later phase mirrors the prompt-side capture for
  results.
- **Entity recognition in non-orchestration / non-run pages**
  beyond the listed integration sites — e.g. global search results,
  command palette previews. Strictly additive, easy follow-up.

## Files (planned)

### New

- `controlplane/db/migrations/sqlite/{N}_orchestration_steps_prompt_entities.sql`
- `controlplane/db/migrations/postgres/{N}_orchestration_steps_prompt_entities.sql`
- `controlplane/orchestrator/prompt_entities.go` — engine-side
  capture + classification
- `controlplane/orchestrator/prompt_entities_test.go`
- `web/src/components/SmartPayload/index.tsx` — entry component
- `web/src/components/SmartPayload/tokenize.ts` — prose ↔ JSON
  tokenizer (prompt mode)
- `web/src/components/SmartPayload/detectors.ts`
- `web/src/components/SmartPayload/detectors.test.ts`
- `web/src/components/SmartPayload/SmartPayload.test.tsx`
- `web/src/components/SmartPayload/chips/FindingChip.tsx`
- `web/src/components/SmartPayload/chips/IOCChip.tsx`
- `web/src/components/SmartPayload/chips/NodeChip.tsx`
- `web/src/components/SmartPayload/chips/DaimonChip.tsx`
- `web/src/components/SmartPayload/chips/RunChip.tsx`
- `web/src/components/SmartPayload/chips/InvestigationChip.tsx`
- `web/src/components/SmartPayload/chips/OrchestrationChip.tsx`
- `web/src/components/EntityDrawerHost.tsx` — single drawer host
  portal'd at `<App>` root

### Modified

- `controlplane/db/orchestrations.go` — read/write the new column
- `controlplane/orchestrator/engine.go` — call the new capture
  helper at template render time
- `controlplane/api/orchestrations.go` — add `prompt_entities` to
  the wire shape
- `web/src/api.ts` — `OrchestrationStepView` gains the new field
- `web/src/pages/Orchestrations.tsx` — swap `<pre>` /
  `<StructuredView>` for `<SmartPayload>` at the four sites
- `web/src/pages/Runs.tsx` — same swap for the run detail
- `web/src/components/HarnessOutput.tsx` — route tool I/O JSON
  through `<SmartPayload>` instead of `<pre>`
- `web/src/pages/Findings.tsx` — `FindingDrawer` `attributes` block
  uses `<SmartPayload>`
- `web/src/pages/Investigations.tsx` — evidence panel rows use
  `<SmartPayload>`
- `web/src/pages/Daimons.tsx` — last-tick block tool I/O via
  `<SmartPayload>`
- `web/src/App.tsx` — mount `<EntityDrawerHost />` at root
