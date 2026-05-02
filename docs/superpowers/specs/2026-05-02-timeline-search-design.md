# Investigation timeline search & filter

## Goal

Add structured filter controls above the investigation timeline so
operators can narrow events by severity, host, agent, and run
status without losing case context. Filters persist as named saved
searches under the existing `saved_searches` infrastructure
(`scope='investigation_timeline'`); a default search auto-applies on
case open.

Follow-up #4 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
parked in the `investigation_overview_followups.md` memory.

## Architecture

**Pure frontend feature.** No new server endpoints, no new DB
schema, no migrations. Reuses two pieces of existing infrastructure:

1. **`saved_searches` table** (migration 045) — already
   scope-parameterised. We write rows with
   `scope='investigation_timeline'` and `config_json` matching the
   new `TimelineFilterConfig` shape. The existing
   `api.savedSearches.list/create/update/delete` helpers work
   unchanged.

2. **`CaseTimeline.tsx`** — extend with a filter bar above the
   canvas (between the lane-toggle row and the SVG). The filter
   applies client-side to the existing `events` array via a pure
   `applyTimelineFilter(events, filter)` helper.

**No federation handling needed.** Bundle is per-CP; filtering is
client-side over events the federation aggregator already returned.
Saved searches scope by user (not by CP), so an operator's
"my CRIT-only view" works on every case they open across federated
CPs.

## File structure

### New (frontend)

- `web/src/components/investigations/timeline/filter.ts` — pure
  types + `applyTimelineFilter` predicate.
- `web/src/components/investigations/timeline/filter.test.ts`
- `web/src/components/investigations/TimelineFilterBar.tsx` —
  filter UI: severity chips, host typeahead, agent typeahead, run-
  status chips, saved-search dropdown.
- `web/src/components/investigations/TimelineFilterBar.test.tsx`

### Modified

- `web/src/components/investigations/CaseTimeline.tsx` — mount
  `<TimelineFilterBar>` and apply `applyTimelineFilter` to the
  events array before passing to `EventsLayer`. Add a
  zero-matches banner above the canvas.
- `web/src/api.ts` — add `TimelineFilterConfig` type. (No new api
  helpers; reuses existing `api.savedSearches.*` with
  `scope='investigation_timeline'`.)
- `docs/architecture.md` — new section.

## Filter shape

```ts
export interface TimelineFilterConfig {
  severities?: ('CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO')[];
  host?: string;
  agent?: string;
  run_statuses?: ('completed' | 'failed' | 'cancelled' | 'running')[];
}
```

Empty / missing fields = no filter on that dimension. An empty
config (`{}`) = no filtering at all (every event passes).

## Filter semantics — `applyTimelineFilter`

Pure function:

```ts
function applyTimelineFilter(
  events: TimelineEvent[],
  filter: TimelineFilterConfig,
): TimelineEvent[];
```

For each event, it passes the filter iff every active dimension
matches:

- **Severity** applies to `kind === 'finding'` only. Other event
  kinds pass-through (they're not severity-bearing).
- **Host** applies to `kind === 'finding'` (compares `event.host`
  exact match). Other kinds pass-through.
- **Agent** applies to `kind === 'finding'` (event.agent) AND
  `kind === 'daimon'` (event.agent). Other kinds pass-through.
- **Run status** applies to `kind === 'run'` only. Other kinds
  pass-through.

**Pass-through rationale:** notes, audit, lifecycle, IOCs always
render unless their lane is toggled off. Operators use the existing
lane toggles to hide kinds entirely; severity-filtering shouldn't
sneak-hide their notes. Setting severity = HIGH means "show me HIGH
findings" — not "hide everything that isn't a finding."

**Multi-value semantics:** `severities` and `run_statuses` are OR'd
internally (pass if ANY matches). All four dimensions are AND'd
together (pass only if every dimension's local match holds).

## Filter UI — `<TimelineFilterBar>`

Compact horizontal layout, sits between the lane-toggle row and the
SVG canvas:

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ [Saved: my-crit-only ▾]  Severity: ☑CRIT ☑HIGH ☐MED ☐LOW ☐INFO              │
│ Host: [edr-fedora-3 ▾]   Agent: [edr-agent ▾]   Runs: ☑failed ☐completed …  │
│                                                          [Save as…] [Clear] │
└──────────────────────────────────────────────────────────────────────────────┘
```

| Control | Behavior |
|---|---|
| **Saved-search dropdown** | Lists user's `scope='investigation_timeline'` rows. Click → applies that config. Default-flagged one auto-applies on mount. Empty list → control hidden. |
| **Severity chips** | Five toggle chips. Click toggles. Multi-select. Empty selection = no severity filter. |
| **Host typeahead** | `<input>` with autocomplete from `bundle.findings`'s distinct `Host.String` values. Empty = no host filter. Selected = exact match. |
| **Agent typeahead** | Same shape, distinct agents from `bundle.findings ∪ bundle.daimons`. |
| **Run-status chips** | Four toggles. Multi-select. |
| **Save as…** | Inline rename: replaces the row with a name input + Save / Cancel. On Save, calls `api.savedSearches.create({scope: 'investigation_timeline', name, config: currentFilter})`. On 409, shows error inline. |
| **Clear** | Resets to empty filter. Doesn't touch saved searches. |

**Props:**

```ts
interface TimelineFilterBarProps {
  bundle: InvestigationDetail;
  filter: TimelineFilterConfig;
  onChange: (next: TimelineFilterConfig) => void;
}
```

**Saved-search lifecycle:**

1. On mount, fetch via `api.savedSearches.list('investigation_timeline')`.
2. If a row has `is_default=true`, the parent CaseTimeline
   pre-applies its config via `onChange(parsedConfig)`.
3. Operator picks one from the dropdown → `onChange(parsedConfig)`.
4. Operator clicks "Save as…" → write a new row.

The bar is "dumb" — state lives in the parent CaseTimeline. The
parent owns the `currentFilter` state, fetches saved searches, and
threads them through props.

## CaseTimeline integration

In `CaseTimeline.tsx`:

```tsx
const [filter, setFilter] = useState<TimelineFilterConfig>({});

// Existing events derivation continues as today.
const events = useMemo(() => buildEvents(bundle, audit), [bundle, audit]);
const filteredEvents = useMemo(() => applyTimelineFilter(events, filter), [events, filter]);

// On mount, look up default saved search and pre-apply.
useEffect(() => {
  api.savedSearches.list('investigation_timeline')
    .then((rows) => {
      const def = rows.find((r) => r.is_default);
      if (def) {
        try { setFilter(JSON.parse(def.config_json) as TimelineFilterConfig); }
        catch { /* malformed config, ignore */ }
      }
    })
    .catch(() => { /* silent — filtering still works without saved searches */ });
}, []);
```

`<TimelineFilterBar>` is rendered above the SVG; the filtered
events are passed to `EventsLayer` instead of the raw events.

**Zero-matches banner.** When `filteredEvents.length === 0` AND
`events.length > 0`:

```tsx
<div className="px-3 py-2 border-b border-border text-xs text-ink-dim bg-amber-50">
  0 of {events.length} events match — adjust filters or click Clear.
</div>
```

Sits inside the SVG container, above the canvas viewport (same
position as the existing CaseGraph cap banner from PR #121). The
canvas still renders lane bands + axis; just no events.

## Edge cases

- **Filter matches zero events.** Banner renders; canvas shows
  empty lanes.
- **Saved search references a host/agent that no longer exists.**
  Filter still applies; zero events match; banner appears. No
  validation — typeahead just shows the persisted value.
- **Operator deletes a saved search while it's active.** UI keeps
  the current filter (state, not bound to the row). Dropdown no
  longer lists it.
- **Two operators on the same case.** Per-user; no conflict.
- **Federation.** Filter is client-side over already-loaded events;
  saved searches are user-scoped, so they apply on every case
  regardless of CP origin.
- **Malformed `config_json` on a saved search row.** `JSON.parse`
  fails silently; treated as no filter. Logging once per failure
  to console for diagnostic.
- **Filter dimension applies to no events of any matching kind.**
  E.g. `host: "doesnotexist"` on a case with no findings on that
  host. Same as "zero matches" — banner appears.

## Testing

`filter.test.ts` (pure):
- Empty filter → all events pass.
- Severity filter set → only matching findings pass; non-finding
  events pass-through.
- Host filter set → only matching findings pass; non-finding
  events pass-through.
- Agent filter applies to findings AND daimons.
- Run-status filter applies only to runs; other kinds pass-through.
- Combined filter (severity + host + agent) → events must match
  all dimensions.
- Empty array of events → empty result.

`TimelineFilterBar.test.tsx`:
- Renders five severity chips + host input + agent input + four
  run-status chips + Save/Clear buttons.
- Click a severity chip → calls `onChange` with the severity
  added.
- Type in host input → calls `onChange` with the host value.
- Click "Clear" → calls `onChange({})`.
- "Save as…" with a name submits via mocked
  `api.savedSearches.create({scope: 'investigation_timeline', ...})`.
- Default saved search auto-applies on mount (mock
  `api.savedSearches.list` returning a row with `is_default=true`).
- Saved-search dropdown hidden when list is empty.

`CaseTimeline.test.tsx` integration (extending existing):
- With a 5-event case, applying severity:HIGH filter renders only
  HIGH events; non-finding kinds (notes/audit/lifecycle) still
  render.
- Banner appears when filter matches zero events.

### Manual lab smoke (post-merge)

- Open an active case with mixed-severity findings, ≥ 2 hosts, ≥ 2
  agents, ≥ 2 run statuses. Toggle each filter dimension; verify
  events update live.
- Save a filter as "my CRIT-only view"; close the case; reopen ≠;
  verify it appears in the dropdown.
- Mark a saved search as default (via API or UI affordance);
  reload the case; verify it auto-applies.
- Delete a saved search while active; verify the filter UI keeps
  the values but the dropdown updates.
- Federated case (`cp_source` set) → saved searches still appear
  (they're per-user, CP-agnostic) and filter applies correctly.

## Out of scope (explicit cuts)

- **Free-text search across title/body** (Q1 picked C — structured
  controls only).
- **Time-range filter** (Q2 — zoom controls cover this).
- **Per-case localStorage persistence** (Q3 — saved searches
  replace this).
- **Sharing saved searches across users** — per-user only.
  Group-shared saved searches are an unrelated RBAC follow-on
  tracked separately.
- **Filter for IOC kind / value.** Operators use the IOCs tab.
- **"Strict" mode that hides non-applicable kinds.** Pass-through
  is the chosen semantic.
- **Server-side filtering.** Bundle is already loaded; client-side
  is fine at every realistic case size (<500 events).
- **Sharing filter state across tabs** (Findings/Runs/etc).
  `scope='investigation_timeline'` is the only new scope.
- **Default saved search "set" / "unset" UI.** v1 ships read-only
  for `is_default` — the existing saved-searches API supports it
  but exposing the toggle is a separate UX pass.
- **Saved-search rename / delete from this UI.** v1 only exposes
  list, apply, create. Update + delete UI lives on the existing
  Findings page; the same admin paths could be ported here later.
