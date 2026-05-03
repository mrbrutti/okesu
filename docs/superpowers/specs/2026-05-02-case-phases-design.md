# Case Phases — pinned timeline annotations

## Goal

Let operators mark named time ranges on the investigation timeline
("Initial detection 14:00–14:35", "Containment 14:35–16:10"). Phases
are persistent, free-form, and rendered as a dedicated lane at the
top of the timeline. Drag-to-create inside the lane; click-to-rename;
hover for delete.

Follow-up #5 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
the last remaining item from the Investigation Overview backlog.

## Architecture

A new `investigation_phases` table holds per-case phase records:
`(id, investigation_id, name, start_ts, end_ts, created_by, created_at)`.
CRUD lives behind `POST/GET/PATCH/DELETE /api/investigations/{id}/phases[/{phase_id}]`,
with a federation parent/child pair identical in shape to the case-
structure endpoints from PR #125.

On the frontend, `CaseTimeline.tsx` gains a "phases" lane that is
toggleable (off by default, like audit) and lazy-fetched on first
toggle. The lane renders as a sibling React `<div>` directly above
the SVG canvas — NOT as an SVG row — so the rubber-band drag gesture
and inline rename input can use plain DOM event handlers without
fighting SVG event propagation. Pointer-down inside the lane
background starts a rubber-band selection; pointer-up opens an inline
`<input>` for the phase name; Enter commits, Escape cancels.

Phase colors are derived deterministically from `hash(name)` — no
picker UI, no category column. The same name always renders with
the same color across sessions. Overlapping phases stack vertically
within the lane via a pure first-fit row-assignment helper.

## File structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/db/migrations/sqlite/059_investigation_phases.sql` | NEW | sqlite migration |
| `controlplane/db/migrations/postgres/059_investigation_phases.sql` | NEW | postgres twin |
| `controlplane/db/investigation_phases.go` | NEW | `Insert/Get/List/UpdateName/Delete InvestigationPhase` Store methods |
| `controlplane/db/investigation_phases_test.go` | NEW | DB tests |
| `controlplane/api/investigation_phases.go` | NEW | HTTP handlers (4 CRUD ops × 3 layers: local + federation parent + federation child = 12 functions) |
| `controlplane/api/investigation_phases_test.go` | NEW | Handler tests |
| `controlplane/server.go` | MODIFY | Register the 8 new routes (4 parent-side + 4 child-side) next to the `/draft/*` routes |
| `web/src/api.ts` | MODIFY | Add `api.investigations.phases.{list,create,update,delete}` + `InvestigationPhase` type |
| `web/src/components/investigations/timeline/types.ts` | MODIFY | Add `'phases'` to `TimelineLane` and `ALL_LANES` (front of the array, opt-in) |
| `web/src/components/investigations/timeline/scale.ts` | MODIFY | Add `xToT(x, tMin, tMax, width)` |
| `web/src/components/investigations/timeline/scale.test.ts` | MODIFY | Round-trip test for `xToT` |
| `web/src/components/investigations/timeline/phasesLayer.ts` | NEW | Pure stacker for overlapping phases (first-fit row assignment) |
| `web/src/components/investigations/timeline/phasesLayer.test.ts` | NEW | Unit tests |
| `web/src/components/investigations/PhasesLane.tsx` | NEW | Lane component: rubber-band drag, pill rendering, inline rename, hover delete |
| `web/src/components/investigations/PhasesLane.test.tsx` | NEW | Component tests |
| `web/src/components/investigations/CaseTimeline.tsx` | MODIFY | Lazy phases-fetch effect + mount `<PhasesLane>` |
| `web/src/components/investigations/CaseTimeline.test.tsx` | MODIFY | Test the lazy-fetch on first lane toggle |
| `docs/architecture.md` | MODIFY | New section under "Investigation Overview" |

## Data model

```go
type InvestigationPhase struct {
    ID              int64
    InvestigationID int64
    Name            string
    StartTs         int64    // unix milliseconds
    EndTs           int64    // unix milliseconds
    CreatedBy       sql.NullString
    CreatedAt       time.Time
}

type InvestigationPhaseInsert struct {
    InvestigationID int64
    Name            string
    StartTs         int64
    EndTs           int64
    CreatedBy       string
}
```

### sqlite migration (`059_investigation_phases.sql`)

```sql
-- Operator-defined phases of a case (e.g. "Initial detection 14:00–14:35",
-- "Containment 14:35–16:10"). Free-form name; no category column.
-- Color in the UI is derived from hash(name).
CREATE TABLE investigation_phases (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  investigation_id INTEGER NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
  name             TEXT    NOT NULL,
  start_ts         INTEGER NOT NULL,
  end_ts           INTEGER NOT NULL,
  created_by       TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (end_ts >= start_ts),
  CHECK (length(name) > 0)
);
CREATE INDEX investigation_phases_inv ON investigation_phases (investigation_id);
```

### postgres twin

`BIGSERIAL`/`BIGINT` instead of sqlite's `INTEGER PRIMARY KEY AUTOINCREMENT`;
otherwise identical.

### Wire shape

Following the project convention (PascalCase Go field names, no JSON
tags), each row JSON-encodes as:

```json
{
  "ID": 12,
  "InvestigationID": 4,
  "Name": "Initial detection",
  "StartTs": 1714559400000,
  "EndTs": 1714561500000,
  "CreatedBy": { "Valid": true, "String": "alice@org" },
  "CreatedAt": "2026-05-02T14:00:00Z"
}
```

## Server-side

### Endpoints

| Method + Path | Body / Response | Behavior |
|---|---|---|
| `GET    /api/investigations/{id}/phases` | → `InvestigationPhase[]` sorted by `start_ts` ASC | List all phases for the case |
| `POST   /api/investigations/{id}/phases` | `{name, start_ts, end_ts}` → `{id}` | Create. 400 on `start_ts > end_ts`, empty name, or `len(name) > 100`. `created_by` filled from auth |
| `PATCH  /api/investigations/{id}/phases/{phase_id}` | `{name}` → 204 | Rename only. v1 doesn't support time edits |
| `DELETE /api/investigations/{id}/phases/{phase_id}` | → 204 | Idempotent |

### Validation

- `name` non-empty after `strings.TrimSpace`, max 100 chars (handler-side).
- `start_ts <= end_ts` (DB CHECK + handler-side guard with friendly 400).
- `name` may duplicate across the same case — operators sometimes have
  two "Containment" phases interspersed; we don't deduplicate.

### Failure modes

- Unknown investigation ID → 404.
- Bad JSON / empty name / name > 100 chars / `start_ts > end_ts` → 400.
- PATCH against a non-existent phase row → 500 only on SQL error;
  otherwise the SQL UPDATE is a no-op and we return 204 (the most
  common cause is a stale UI; the user gets the next refetch and
  sees the phase is gone).
- Federation child unreachable → 502 from the proxy.
- Concurrent edits (two operators rename the same phase
  simultaneously) → last-writer-wins.

### Federation

Each parent-side handler (`FederatedInvestigationPhasesList/Create/Update/Delete`)
wraps the local handler with the existing `proxyToCPByQuery` (GET) or
`proxyToCPByQueryPost` (mutating) helper. Each child-side counterpart
(`FederationInvestigationPhases*`) wraps the local handler with
`requireFederationToken`. Same template as the `/structure`,
`/graph`, and `/draft/*` endpoints.

### Routes registered in `controlplane/server.go`

```go
// Parent-side, next to the existing /draft/* registrations:
r.Get(   "/api/investigations/{id}/phases",            api.FederatedInvestigationPhasesList(s.store, s.fedAgg))
r.Post(  "/api/investigations/{id}/phases",            api.FederatedInvestigationPhaseCreate(s.store, s.fedAgg))
r.Patch( "/api/investigations/{id}/phases/{phase_id}", api.FederatedInvestigationPhaseUpdate(s.store, s.fedAgg))
r.Delete("/api/investigations/{id}/phases/{phase_id}", api.FederatedInvestigationPhaseDelete(s.store, s.fedAgg))

// Child-side, next to the federation /draft/* registrations:
r.Get(   "/api/v1/federation/investigations/{id}/phases",            api.FederationInvestigationPhasesList(s.store))
r.Post(  "/api/v1/federation/investigations/{id}/phases",            api.FederationInvestigationPhaseCreate(s.store))
r.Patch( "/api/v1/federation/investigations/{id}/phases/{phase_id}", api.FederationInvestigationPhaseUpdate(s.store))
r.Delete("/api/v1/federation/investigations/{id}/phases/{phase_id}", api.FederationInvestigationPhaseDelete(s.store))
```

## Client-side

### `api.ts` plumbing

```ts
export interface InvestigationPhase {
  ID:              number;
  InvestigationID: number;
  Name:            string;
  StartTs:         number;
  EndTs:           number;
  CreatedBy:       { Valid: boolean; String: string };
  CreatedAt:       string;
}

// Inside api.investigations:
phases: {
  list: (id: number, cpInstanceID?: string) => {
    const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
    return request<InvestigationPhase[]>(`/api/investigations/${id}/phases${qs}`);
  },
  create: (id: number, body: { name: string; start_ts: number; end_ts: number }, cpInstanceID?: string) => {
    const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
    return request<{ id: number }>(`/api/investigations/${id}/phases${qs}`, {
      method: 'POST', body: JSON.stringify(body),
    });
  },
  update: (id: number, phaseID: number, body: { name: string }, cpInstanceID?: string) => {
    const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
    return request<void>(`/api/investigations/${id}/phases/${phaseID}${qs}`, {
      method: 'PATCH', body: JSON.stringify(body),
    });
  },
  delete: (id: number, phaseID: number, cpInstanceID?: string) => {
    const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
    return request<void>(`/api/investigations/${id}/phases/${phaseID}${qs}`, {
      method: 'DELETE',
    });
  },
},
```

### `timeline/types.ts` extension

```ts
export type TimelineLane =
  | 'phases'    // NEW — added at front so it renders first
  | 'lifecycle'
  | 'findings'
  | ...

export const ALL_LANES: ReadonlyArray<TimelineLane> = ['phases', 'lifecycle', ...];
// DEFAULT_LANES_ON does NOT include 'phases' — opt-in like audit.
```

### `timeline/scale.ts` — `xToT`

```ts
/**
 * xToT — inverse of tToX. Given a pixel x within the drawable width,
 * returns the corresponding timestamp in [tMin, tMax].
 */
export function xToT(x: number, tMin: number, tMax: number, width: number): number {
  if (width <= 0) return tMin;
  const clamped = Math.min(Math.max(x, 0), width);
  return tMin + (clamped / width) * (tMax - tMin);
}
```

### Pure stacker (`timeline/phasesLayer.ts`)

```ts
export interface PhaseLayout {
  phase: InvestigationPhase;
  row:   number;   // 0 = top row, 1 = next, ...
  x:     number;   // pixel x of the start
  width: number;   // pixel width
}

export function layoutPhases(
  phases: InvestigationPhase[],
  range: Range,
  drawableWidth: number,
  laneLabelWidth: number,
): PhaseLayout[];
```

Algorithm: sort by `start_ts`. Greedy first-fit — for each phase,
place it on the lowest-numbered row whose latest `end_ts` < this
phase's `start_ts`. New row if every existing row overlaps. Returns
at most ~3-4 rows in practice; lane height grows to accommodate.

### `<PhasesLane>` React component

```tsx
interface Props {
  invID:          number;
  cpInstanceID?:  string;
  phases:         InvestigationPhase[];
  range:          Range;
  drawableWidth:  number;
  laneLabelWidth: number;
  onPhasesChange: () => void;   // re-fetch trigger after mutate
}
```

**Layout:** sibling `<div>` above the SVG, sized to match the SVG's
drawable width. Height: `LANE_HEIGHT * max(1, layoutRows)`.
Background: dashed border + hint text "drag here to mark a phase"
when `phases.length === 0`.

**Phase pill:**

```tsx
<div
  role="button"
  className="absolute rounded ring-1 px-1.5 text-[10px] font-medium truncate"
  style={{
    left:            `${x}px`,
    top:             `${row * LANE_HEIGHT + 4}px`,
    width:           `${Math.max(width, 4)}px`,
    height:          `${LANE_HEIGHT - 8}px`,
    backgroundColor: bgColor(phase.Name),
    color:           textColor(phase.Name),
    boxShadow:       `inset 0 0 0 1px ${ringColor(phase.Name)}`,
  }}
>
  {phase.Name}
</div>
```

**Drag gesture state machine:**

```tsx
type DragState =
  | { kind: 'idle' }
  | { kind: 'dragging'; startX: number; currentX: number }
  | { kind: 'naming'; startX: number; endX: number };
```

- `pointerDown` on the lane background (NOT on a phase pill) → `dragging`. Capture pointer.
- `pointerMove` while `dragging` → update `currentX`. Render rubber-band rectangle.
- `pointerUp` while `dragging` → if `|endX - startX| > 5px`, transition to `naming`. Otherwise back to `idle` (treat short clicks as not-a-drag).
- `naming` → render an autoFocused `<input>` positioned over the rubber-band rectangle. Enter commits via `phases.create({name, start_ts, end_ts})` then `onPhasesChange()`. Escape cancels. Click-away cancels.

**Inline rename:** click on a phase pill (without dragging — pointer
up within 5px of pointer down) swaps the `<div>` for an `<input>`
with the same dimensions, autoFocused. Enter calls
`api.investigations.phases.update` then `onPhasesChange()`. Escape
reverts.

**Hover delete:** `mouseEnter` on a phase pill renders a small `×`
button absolutely-positioned at the right edge. Click triggers
`window.confirm("Delete phase \"X\"?")`; on confirm, calls
`api.investigations.phases.delete` then `onPhasesChange()`.

**Empty-state hint:** when `phases.length === 0`:

```tsx
<span className="absolute inset-0 flex items-center justify-center text-[11px] text-ink-mute italic pointer-events-none">
  drag here to mark a phase
</span>
```

`pointer-events-none` ensures the hint doesn't intercept the drag.

### Color helpers

```ts
function hashHue(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return ((h % 360) + 360) % 360;
}
function bgColor(name: string)   { return `hsl(${hashHue(name)}, 70%, 92%)`; }
function ringColor(name: string) { return `hsl(${hashHue(name)}, 60%, 65%)`; }
function textColor(name: string) { return `hsl(${hashHue(name)}, 55%, 28%)`; }
```

### `CaseTimeline.tsx` integration

```tsx
const [phases, setPhases] = useState<InvestigationPhase[]>([]);
const phasesFetchedRef = useRef(false);

// Lazy-fetch phases the first time the lane is enabled.
useEffect(() => {
  if (!activeLanes.includes('phases') || phasesFetchedRef.current) return;
  phasesFetchedRef.current = true;
  api.investigations.phases.list(bundle.investigation.ID, cpInstanceID)
    .then(setPhases)
    .catch(() => { phasesFetchedRef.current = false; });
}, [activeLanes, bundle.investigation.ID, cpInstanceID]);

const refreshPhases = () => {
  api.investigations.phases.list(bundle.investigation.ID, cpInstanceID)
    .then(setPhases).catch(() => {});
};
```

Mount `<PhasesLane>` between the toggle bar and the SVG, gated on
the lane being active and `range` being available:

```tsx
{activeLanes.includes('phases') && range && (
  <PhasesLane
    invID={bundle.investigation.ID}
    cpInstanceID={cpInstanceID}
    phases={phases}
    range={range}
    drawableWidth={drawableWidth}
    laneLabelWidth={LANE_LABEL_W}
    onPhasesChange={refreshPhases}
  />
)}
```

## Testing

### DB layer (`investigation_phases_test.go`)

- Insert/Get/List/Update/Delete round-trip.
- List orders by `start_ts ASC`.
- DELETE is idempotent (no error on missing row).
- CHECK constraint on `end_ts >= start_ts` rejects bad inserts.
- Empty-name CHECK constraint.
- Cascade delete: removing the parent investigation also removes its phases.

### Handler layer (`investigation_phases_test.go`)

- `GET /phases` on unknown investigation → 404.
- `GET /phases` on empty case → 200 with `[]`.
- `POST /phases` with valid body → 200 with `{id}`; row in DB; `created_by` set from auth.
- `POST /phases` with `start_ts > end_ts` → 400.
- `POST /phases` with empty/whitespace name → 400.
- `POST /phases` with name > 100 chars → 400.
- `PATCH /phases/{id}` with non-empty name → 204; row's name updated.
- `DELETE /phases/{id}` → 204; row gone.
- Federation child rejects without `X-Okesu-Federation-Token`.

### Client tests

`web/src/components/investigations/timeline/scale.test.ts` (extend):
one round-trip test `xToT(tToX(t, ...), ...) === t` for several
timestamps + ranges.

`web/src/components/investigations/timeline/phasesLayer.test.ts`
(new): single phase → row 0; two non-overlapping → both row 0; two
overlapping → row 0 + 1; three-way overlap → rows 0/1/2; touching
boundaries (start of B == end of A) both row 0; phase outside
visible range still in output (caller handles clipping).

`web/src/components/investigations/PhasesLane.test.tsx` (new):
- Empty-state hint when `phases=[]`.
- Pills render with deterministic colors (same name → same color across renders).
- Drag in empty area → rubber-band appears mid-drag → release with `Δx > 5px` opens an input.
- Type "Initial detection" + Enter → calls `api.investigations.phases.create` with derived `start_ts`/`end_ts`.
- Click an existing pill → input replaces; Enter → `update`. Escape → reverts.
- Hover a pill → `×` button appears; click → confirm dialog → `delete`.
- Click-without-drag (`Δx < 5px`) → no rubber-band, no input.

`CaseTimeline.test.tsx` (extend):
- Toggling the `phases` lane on triggers `api.investigations.phases.list`.
- Lane is opt-in (not in `DEFAULT_LANES_ON`).
- Lazy-fetch only fires once per case open.

### Manual lab smoke (post-merge)

- Open an active case. Toggle `phases` lane on. Drag inside the lane
  to create "Initial detection 14:00–14:35". Type the name, hit
  Enter. Pill renders with a deterministic color.
- Drag again with overlap. Confirm the new phase stacks below the
  existing one in row 1.
- Click a pill → rename inline → Enter. Hover → click `×` →
  confirm. Pill disappears.
- Reload the page → phases persist.
- Open the same case in a federated browser session
  (`?cp=<id>`). Confirm phases load via the federated endpoint.
- Open with the lane toggled off — no `/phases` request should fire.

## Edge cases

- **Phase entirely outside visible range.** `tToX` already clamps to
  the canvas edges. The pill still renders but is squashed to one
  edge. Acceptable; operators usually zoom to the relevant phase
  before editing.
- **Phase with very short duration** (< 1px wide at current zoom).
  We render with `min-width: 4px` so the pill is still clickable.
- **Two operators editing simultaneously.** Last-write-wins on
  rename; concurrent deletes are idempotent. The next refetch from
  either side picks up the other's change. No real-time sync — the
  next bundle poll (every 30s in non-war-room mode) refreshes;
  faster if either operator hits Refresh.
- **Phase spans across an autoFitRange refit.** When the case bundle
  freshness advances and `autoFitRange` re-runs, the phase pills
  re-render at new positions. No data loss.
- **Deleting a case** cascades to phases via the FK. No orphan rows.
- **Color collision on similar names.** Two operators creating
  "Containment 1" and "Containment 2" get nearly-identical colors.
  Acceptable — pill text disambiguates.

## Out of scope (explicit cuts)

- **Resize via drag-the-edges** — Q4=A; operators delete + re-create.
- **Time-edit fields** — Q4=A; rename only.
- **Categories / fixed phase types** — Q2=A; free-form names.
- **Color picker** — colors are hash-derived; operators don't pick.
- **Phase notes / descriptions** — name is the only text. If we want
  longer prose, that's a notes feature, which already exists.
- **Real-time sync** — phases refetch on bundle poll; no SSE/WS.
- **Phase analytics** — "time per category" rollup is YAGNI without
  categories. Future follow-up if anyone asks.
- **Lane reordering** — phases lane is fixed at the top of
  `ALL_LANES`. No drag-to-reorder lanes.
