# Investigation overview — layered status / timeline / structure

## Goal

Replace today's text-shaped Overview tab in the investigation
workspace (`/investigations/{id}`) with a layered visual that
answers three operator questions at once: case status (where am I?),
chronology (what happened?), and structure (what's involved?).

The current Overview tab renders Summary text + Suggested-Findings
card + Identity sidebar. This is fine for narrow info but loses the
case's shape — operators today have to walk every tab (Findings,
Runs, IOCs, Daimons, Orchestrations, Notes, Audit) to reconstruct
the picture.

## Architecture

Three stacked sections inside the Overview tab, top → bottom:

1. **Status header** (~110px, single row, war-room-aware).
2. **Timeline** (~320px, horizontal Gantt band, curated default with
   filter toggles).
3. **Structure** (4-column grid of summary cards).

```
┌────────────────────────────────────────────────────────────────┐
│ STATUS HEADER                                                  │
│ status pill │ severity histogram │ freshness │ counts │ summary│
├────────────────────────────────────────────────────────────────┤
│ TIMELINE  (lane toggles ▣ findings ▣ runs ▣ notes ☐ iocs …)    │
│ findings  • · ● · ●         ●  ●        · ●─────              │
│ runs                ▭▭▭▭     ▭▭▭▭▭▭▭                          │
│ notes              ✎             ✎    ✎                       │
│ iocs      (off by default)                                    │
│ daimons   (off by default)                                    │
│ audit     (off by default)                                    │
│ lifecycle ⊕                                ⊗                  │
│  ───────  09:00 ─── 12:00 ─── 15:00 ─── 18:00 ───  [Fit][1h]…│
├────────────────────────────────────────────────────────────────┤
│ STRUCTURE                                                      │
│ Hosts (12) │ IOCs (5) │ Daimons (3) │ Runs (4 / 2 orchs)       │
└────────────────────────────────────────────────────────────────┘
```

**No new server-side endpoints.** The existing `InvestigationDetail`
bundle (loaded via `api.investigations.get(id, cpInstanceID)`)
already contains every signal the three sections render —
`investigation`, `findings[]`, `runs[]`, `iocs[]`, `daimons[]`,
`orchestrations[]`, `notes[]`, `audit[]`. The visualisations are
pure derivations of that bundle; the existing 30s / 5s-war-room
poll keeps them fresh.

**Click affordances reuse the SmartPayload pattern.** Events on the
timeline and entities in the structure cards open the matching
detail drawer via the existing `entity:open` custom event +
`EntityDrawerHost` mounted at the App root.

## Components

### `web/src/components/investigations/CaseStatusBar.tsx` (new)

Five-cell row, fits ~110px on desktop, 2×3 grid on narrow viewports.

| Cell | Content |
|---|---|
| **Status** | Status pill (`active` / `closed` / `archived`) + assignee chip + war-room badge if any linked finding has the `war-bridge` tag. Click status → existing close-dialog. |
| **Severity histogram** | Stacked horizontal bar across all linked findings: CRITICAL/HIGH/MEDIUM/LOW/INFO segments scaled to count. Click a segment → Findings tab filtered by that severity. |
| **Freshness** | "Last activity 12m ago" — most recent of any finding's `Ts`, any note's `CreatedAt`, any run's `StartedAt`/`EndedAt`, any audit row's `LinkedAt`. Plus "Created 3d ago" subline. |
| **Counts** | `findings: 12 · runs: 4 · IOCs: 5 · daimons: 3 · notes: 7`. Each click-throughs to the matching tab. |
| **Summary** | Existing Summary text, line-clamped to 3 lines. `Edit` button opens the inline editor that exists today. |

Implementation: ~150 LoC, no new deps. Severity histogram is a
flexbox of proportionally-sized colored segments. Freshness uses
`Math.max(...timestamps)` against `Date.now()`.

### `web/src/components/investigations/CaseTimeline.tsx` (new)

Horizontal Gantt band, ~320px tall, hand-rolled SVG (no
`vis-timeline` / `d3-timeline` dep — data shapes are simple, our
investigations span < 500 events at the high end, full styling
control matters).

**Lanes (top → bottom):** lifecycle, findings, runs, notes — these
four are on by default. Then iocs, daimons, audit when toggled on.
Lane order is fixed; toggling hides the lane entirely (remaining
lanes reflow up).

**Time axis:**
- **Default zoom** = fit-all from `min(events.firstTime)` to
  `max(now, events.lastTime)`. War-room cases (any `war-bridge`
  tag in linked findings) auto-zoom to "last 1 hour".
- **Zoom controls** above the band: `[Fit] [1h] [24h] [7d]` plus
  `+`/`−` buttons. Keyboard: `[`/`]` halve/double the visible
  range; `←`/`→` pan by half-window.
- **"Now" line** — vertical brand-color rule at present moment;
  rendered only on `active` cases.

**Event rendering:**
- **Finding dot** — colored circle, severity tone (red/orange/amber/blue/slate); size scales with severity weight. Hover tooltip = title + agent + host. Click fires `entity:open` for `kind=finding, identityKey=<id>`.
- **Run bar** — rounded rectangle from `StartedAt` → `EndedAt` (or `now` if still running, with a pulsing right edge). Color = run status (green completed / red failed / blue running / slate cancelled). Click fires `entity:open` for `kind=run`.
- **Note icon** — pen icon at `CreatedAt`. Hover = first 80 chars + author. Click scrolls to / activates the Notes tab anchored to that note.
- **IOC bar** — slim bar from `FirstSeen` → `LastSeen`. Click fires `entity:open` for `kind=ioc`.
- **Daimon tick** — small square at the `Ts` of each finding emitted by that daimon (toggle-only). Hover = agent name + finding title. Click → `/daimons?name=…`.
- **Audit icon** — direction-arrow icon at `LinkedAt` per audit row. Hover = action + actor + target. No drawer (audit doesn't have one); click is a no-op or opens the Audit tab anchored.
- **Lifecycle marker** — `⊕` for case opened; `⊗` for case closed; `⚠` for war-room toggle on; `✓` for war-room toggle off. Pulled from audit rows whose action is `investigation.create / close / war_bridge.toggle`.

**Toggle bar** above the timeline:

```
findings ✓   runs ✓   notes ✓   lifecycle ✓   iocs ☐   daimons ☐   audit ☐
```

State persists in `localStorage` keyed by
`investigation:overview:lanes`.

**Collision avoidance.** When ≥ 4 dots overlap within a few pixels
on the same lane, render a single stacked `+N` dot; click opens a
mini-popover listing the N findings with their titles + click → drawer.

**Federation.** Events from federated child CPs (entities whose
`cp_instance_id` is non-empty) get a small CP-color dot prefix
on their dot/bar. Single-CP cases render no prefix. Tooltip names
the source CP.

**Empty state.** When no events exist (newly-created case, no
links yet): centered hint "No signals yet — link findings to
populate the timeline" + button that opens the Suggested-Findings
panel.

Implementation: ~400 LoC. Deterministic placement:
`x = ((t - tMin) / (tMax - tMin)) * width`,
`y = laneIndex * laneHeight + laneHeight/2`. SVG `<g>` per lane;
events are `<circle>` / `<rect>` / `<text>` per kind. Tooltip is a
shared `<div>` portaled into the body and positioned on hover.

### `web/src/components/investigations/CaseStructure.tsx` (new)

Four cards in a 4-column grid (1-column on mobile). Each card:

| Card | Top-line | Body | Click |
|---|---|---|---|
| **Hosts** | `Hosts (12)` | Most-hit: edr-fedora-3 — 8 findings. Plus a 3-row mini-list of the next hosts with their counts. | → Findings tab filtered by host. |
| **IOCs** | `IOCs (5)` | Most-observed: sha256:…aaaa — 23 obs across 4 hosts. 3-row mini-list with kind + last4 + obs count. | → IOCs tab; row click opens IOC drawer. |
| **Daimons** | `Daimons (3)` | Top emitter: edr-agent — 9 findings, last seen 4m ago. Mini-list with count + last-seen. | → Daimons tab; row click navigates to `/daimons?name=…`. |
| **Orchestrations** | `Runs (4 / 2 orchs)` | Most-run: triage-then-quarantine — 3 runs (2 ✓ 1 ✗). Mini-list grouped by orchestration with run-status counts. | → Runs tab; row click opens run drawer / page. |

**Aggregation rules** (deterministic so two operators see the same
numbers):

- **Hosts**: distinct `Host.String` across `bundle.findings` (skip
  `Valid: false`); count per host.
- **IOCs**: from `bundle.iocs` directly (server-aggregated); top
  by `ObservationCount`.
- **Daimons**: from `bundle.daimons` directly; top by
  `FindingCount`, last-seen tie-break.
- **Orchestrations**: from `bundle.orchestrations` directly; status
  counts derived by joining `bundle.runs` to the orchestration id.

**Empty state.** A card with zero items renders dim with "no X
linked to this case" instead of `0`; keeps the grid balanced.

Implementation: ~200 LoC of pure presentation; per-card aggregator
is a 10-line `useMemo` over the bundle.

### `web/src/pages/InvestigationDetail.tsx` (modified)

The existing `OverviewPanel` function becomes a thin compositor of
the three new components. The current Summary block moves into the
status header's Summary cell; the Suggested-Findings card stays
where it is today, below the three new sections (it's load-bearing
for the active-triage workflow and not part of the
status/timeline/structure layering).

```tsx
<section className="space-y-4">
  <CaseStatusBar bundle={bundle} onEdit={…} />
  <CaseTimeline bundle={bundle} />
  <CaseStructure bundle={bundle} />
  {showSuggestions && <SuggestedFindingsCard … />}
</section>
```

The old grid (`grid-cols-1 lg:grid-cols-3`) collapses; the Identity
sidebar moves into the status header's existing cells (Status +
Counts) so we don't lose any info.

## Refresh / realtime

No new polling. The existing `InvestigationDetail` page already
polls `api.investigations.get(id, cpInstanceID)` every 30s (5s in
war-room mode). The three components re-derive their visualisations
from the bundle on every poll. The timeline auto-extends `tMax`
toward `now` so live runs/findings appear in real time without
operator action.

## Federation

Linked entities from federated child CPs already carry a
`cp_instance_id` in their respective payloads (the existing tabs
handle this via `?cp=…`). Timeline + structure-cards prefix those
entities with a small CP-color dot using the same per-CP color
scheme the federation page already uses. Tooltip names the source
CP. Single-CP cases render no prefix.

## Error / loading states

First load shows a 320px skeleton in each of the three sections.
Subsequent poll failures keep the previous frame and show a small
`⚠ refresh failed (Xs ago)` chip in the status row — same pattern
the page already uses elsewhere.

## Accessibility

- Timeline events get `role="button"` + `aria-label` describing
  the event ("Finding #42, severity HIGH, on edr-fedora-3, 14:32").
- Toggle chips are real `<button>`s with `aria-pressed`.
- Color is never the only signal — severity histogram + run-status
  colors include text labels in their tooltips.
- Keyboard nav: tabbing through the timeline iterates events in
  time order; Enter activates the same handler as click.

## Out of scope (cuts — tracked in
`investigation_overview_followups.md` memory for post-PR work)

1. Bipartite relationship graph (deferred behind a future "Graph
   view" tab when topology becomes the dominant question).
2. Server-side aggregation endpoints (client-side derivation
   suffices at lab scale; revisit at >500 findings/case).
3. Note streaming / collaborative editing.
4. Search / filter expressions on the timeline (toggles only for
   v1; no query language).
5. Pinned annotations / range-select / case "phases" on the
   timeline.
6. PDF / print export of the overview.

## Testing

### Component tests (vitest + @testing-library/react)

- `CaseStatusBar.test.tsx`:
  - severity histogram renders correct segment widths from a
    findings array with mixed severities;
  - freshness picks the latest of all dated fields;
  - war-room badge appears when any finding has the `war-bridge`
    tag;
  - empty findings → "no findings linked yet" instead of crash.
- `CaseTimeline.test.tsx`:
  - lane toggles default state matches the spec (findings/runs/
    notes/lifecycle on; iocs/daimons/audit off);
  - finding dots are placed at correct x given a synthetic
    `findings` array;
  - run bars span the right x-range from `StartedAt` to
    `EndedAt`;
  - empty state renders the hint when bundle has no signals;
  - localStorage persists toggle state across re-renders;
  - federated entities (cp_instance_id set) get the CP-color
    prefix.
- `CaseStructure.test.tsx`:
  - hosts/IOCs/daimons/orchestrations cards count correctly from
    a synthetic bundle;
  - empty card renders dim "no X linked" message;
  - top-hitter row matches the highest-count entity.

### Visual / smoke

- Lab smoke (post-merge):
  - Open an active investigation with ≥ 5 findings, ≥ 1 run,
    ≥ 1 note. Confirm the three sections render correctly.
  - Toggle on `iocs`, `daimons`, `audit` lanes; verify rendering.
  - Click a finding dot → FindingDrawer opens.
  - Click a run bar → run drawer / detail page opens.
  - Resize narrow → status row collapses to 2×3 grid.
  - War-room a case (tag a linked finding `war-bridge`) →
    auto-zoom to last hour, war-room badge appears.

## Files (planned)

### New

- `web/src/components/investigations/CaseStatusBar.tsx`
- `web/src/components/investigations/CaseStatusBar.test.tsx`
- `web/src/components/investigations/CaseTimeline.tsx`
- `web/src/components/investigations/CaseTimeline.test.tsx`
- `web/src/components/investigations/CaseStructure.tsx`
- `web/src/components/investigations/CaseStructure.test.tsx`

### Modified

- `web/src/pages/InvestigationDetail.tsx` — `OverviewPanel`
  becomes a thin compositor of the three new components; Identity
  sidebar's content folds into the new status header; Suggested-
  Findings card moves below the three sections.
