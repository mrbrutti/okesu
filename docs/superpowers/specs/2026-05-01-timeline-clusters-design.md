# Investigation timeline — collision avoidance + portal'd tooltip

## Goal

When the case timeline gets dense, dots smudge into illegibility and
operators can't click individual events. Cluster overlapping point
events into a single `+N` glyph that expands into a clickable
mini-list; replace SVG `<title>` hover tooltips with a portal'd
`<div>` so we get instant appearance, styled rendering, and richer
content.

This is follow-up #7 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
parked in the `investigation_overview_followups.md` memory.

## Architecture

Two new files plus one modified:

1. **`web/src/components/investigations/timeline/cluster.ts`** (new,
   ~50 LoC) — pure function bucketing point events on
   `(lane, x-bucket)`. Below threshold, events pass through as
   singletons; at-or-above threshold, the bucket emits a single
   cluster entry with all member events.

2. **`web/src/components/Tooltip.tsx`** (new, ~120 LoC) — generic
   portal'd hover/focus tooltip with an opt-in controlled mode for
   the cluster popover. Replaces SVG `<title>` everywhere on the
   timeline today.

3. **`web/src/components/investigations/CaseTimeline.tsx`** (modified)
   — `EventsLayer` pipes events through `clusterEvents` and uses
   `<Tooltip>` for both per-event hover and the cluster popover.

No server changes. No new endpoints. No new dependencies.

## Cluster algorithm — `cluster.ts`

```ts
type ClusterOrEvent =
  | { kind: 'single'; event: TimelineEvent }
  | { kind: 'cluster'; lane: TimelineLane; events: TimelineEvent[]; cx: number };

function clusterEvents(
  events: TimelineEvent[],
  visibleLanes: TimelineLane[],
  tToX: (t: number) => number,
  bucketPx?: number,    // default 12
  threshold?: number,   // default 4
): ClusterOrEvent[];
```

**Behavior:**

1. Walk events in input order; each event has a primary timestamp
   (`ts` for points; `startTs` for bars).
2. **Bars (`run`, `ioc`) skip clustering** — pass through as
   `{kind:'single', event}` unconditionally. Their duration is
   itself the visual signal; clustering them would erase that.
3. **Point events** (`finding`, `note`, `audit`, `lifecycle`,
   `daimon`) are bucketed by
   `(lane, Math.floor(tToX(primaryTs(event)) / bucketPx))`.
4. A bucket with `< threshold` events emits each as
   `{kind:'single', event}` (preserves stable per-event rendering).
5. A bucket with `≥ threshold` events emits one
   `{kind:'cluster', lane, events, cx}` where `cx` is the bucket's
   midpoint x (so the glyph sits visually at the cluster's center).
6. The `events` array inside a cluster is sorted ascending by primary
   timestamp (popover lists events in chronological order).

**Defaults — why these values:**

- `bucketPx = 12` — finding dots are r=5 (10px diameter). At 6px
  buckets, two dots in adjacent buckets visually overlap. 12px gives
  one dot's diameter of separation between cluster boundaries; the
  eye reads adjacent clusters as clearly distinct.
- `threshold = 4` — three overlapping dots are still visually
  scannable; four is when the eye merges them into a smudge.

**Out of scope (cuts):**

- Mixed-lane clusters. Finding clusters and daimon clusters at the
  same time-window stay separate (one per lane).
- Severity-aware bucketing. All findings in the bucket cluster
  regardless of severity; the cluster glyph surfaces the worst
  severity (see "Cluster glyph appearance" below).
- Bars clustering. Runs and IOCs always render individually.

**Tests (`cluster.test.ts`):**

- Empty events → empty output.
- Singletons across different lanes pass through.
- 3 finding dots within one bucket → 3 singletons (below threshold).
- 5 finding dots within one bucket → 1 cluster of 5, no singletons.
- 5 finding dots split 3+2 across two adjacent buckets → 5
  singletons (each bucket below threshold).
- Run + IOC events pass through as singletons even when their x
  bucket would otherwise cluster.
- Cluster `events` array is sorted by primary timestamp ascending.

## Tooltip component — `Tooltip.tsx`

Generic portal'd tooltip. Single component, two modes:

```tsx
<Tooltip
  content={ReactNode}
  placement?: 'top' | 'bottom'    // default 'top'
  open?: boolean                   // controlled mode
  onOpenChange?: (open: boolean) => void
  delay?: number                   // hover delay; default 0
>
  {children}
</Tooltip>
```

**Modes:**

- **Uncontrolled** (no `open` prop): opens on `mouseenter`/`focus`
  of the anchor with `delay`, closes on `mouseleave`/`blur`.
  Replaces SVG `<title>` for individual events. `delay=0` makes
  appearance instant, eliminating the native ~1s browser-tooltip
  delay.
- **Controlled** (`open` + `onOpenChange` set): opens/closes via the
  caller. Used by the cluster popover. Closes on outside-click,
  `Escape`, or any descendant calling `onOpenChange(false)`. No
  hover behavior in this mode.

**Implementation details:**

- **Portal target** — `document.body` so the tooltip never gets
  clipped by SVG viewport or parent `overflow:hidden`.
- **Position** — computed from anchor's `getBoundingClientRect()`;
  re-computed via `ResizeObserver` on the anchor and a `scroll`
  listener on the window. No polling.
- **Accessibility** — `role="tooltip"` (uncontrolled) /
  `role="dialog"` (controlled, with focus trap). `aria-describedby`
  wired from anchor → tooltip in uncontrolled mode.
- **Style** — `bg-panel border border-border rounded-md shadow-lg
  px-2 py-1.5 text-xs text-ink z-50 max-w-sm`. Arrow is a
  CSS-rotated `<div>` outside the content padding. Light-theme
  tokens; matches existing chip aesthetic.

**Per-kind tooltip content** (used by `EventsLayer` when wrapping
each event AND by the cluster popover row renderer):

| Kind | Tooltip body |
|---|---|
| `finding` | severity chip + `Finding #${id}` + title (bold) + `${agent} on ${host}` + dim "Click to open drawer" |
| `run` | status pill + `Run #${id}` + orchestration name + duration (`formatDuration(start, end)` or "in progress") |
| `note` | author (small, dim) + body (full text, monospace, 4-line clamp) |
| `ioc` | `kind:value` (monospace, `…last4` if value > 32 chars) + observation/host count |
| `daimon` | `${agent} → Finding #${findingID}` |
| `audit` | author + auditKind + title |
| `lifecycle` | the existing title text |

**Tests (`Tooltip.test.tsx`):**

- Uncontrolled hover anchor → tooltip appears; mouseleave →
  disappears.
- `delay` honored using `vi.useFakeTimers()`.
- Controlled `open=true` → rendered; `open=false` → not rendered.
- Outside-click in controlled mode fires `onOpenChange(false)`.
- Escape in controlled mode fires `onOpenChange(false)`.
- Portal target is `document.body` (assert tooltip element's parent
  is body).

## EventsLayer integration — `CaseTimeline.tsx`

Three changes to the existing `EventsLayer`:

1. **Pipe through `clusterEvents`.** The current
   `events.map((e, i) => switch (e.kind) {...})` becomes
   `clustered.map((c, i) => c.kind === 'single' ? renderSingle(c.event, i) : renderCluster(c, i))`.

2. **`renderSingle`** keeps today's per-kind switch but each event's
   shape is wrapped in `<Tooltip content={tooltipFor(event)} delay={0}>`
   (uncontrolled). The existing `<title>` element is removed. Click
   handler unchanged — fires `entity:open` per kind.

3. **`renderCluster`** for `{kind:'cluster', lane, events, cx}`:
   - For `lane === 'findings'`: render
     `<circle cx={cx} cy={yMid(lane)} r={7} fill={highestSeverityColor(events)} />`
     plus a `<text>+N</text>` overlay centered on the circle.
   - For other point lanes (`notes`, `audit`, `lifecycle`,
     `daimons`): render the lane's existing glyph at slightly larger
     size and a `+N` badge sibling text element.
   - Wrap in a `<g>` that's the controlled-`<Tooltip>` anchor:
     ```tsx
     <Tooltip
       open={openCluster === c}
       onOpenChange={(o) => setOpenCluster(o ? c : null)}
       content={<ClusterList events={c.events} onPick={() => setOpenCluster(null)} />}
     >
       <g
         onClick={() => setOpenCluster(c)}
         role="button"
         aria-label={`Cluster of ${c.events.length} ${lane}`}
         tabIndex={0}
         style={{ cursor: 'pointer' }}
       >
         {/* glyph */}
       </g>
     </Tooltip>
     ```

**`ClusterList`** is a tiny inline component (within `CaseTimeline.tsx`):

- Renders one row per event reusing the `tooltipFor(event)` row
  shape.
- Row click fires the kind's `entity:open` AND calls `onPick()` to
  close the popover.
- For `lifecycle` rows, no click handler (no drawer for lifecycle
  markers — they're informational only).

**`highestSeverityColor`** returns the SEV_FILL color for the highest
severity in the cluster: CRITICAL > HIGH > MEDIUM > LOW > INFO. Pure
helper, ~5 LoC.

**State:** one `useState<ClusterOrEvent | null>(null)` for
`openCluster` at the `EventsLayer` level. Closed by row click,
outside-click, or Escape (delivered by `<Tooltip>`'s controlled-mode
behavior).

**Z-index ordering.** Clusters render after singles in the same
draw pass — but a cluster glyph sits at the bucket midpoint; only
events inside a cluster bucket share x-space, and we pick exactly
one of (cluster glyph) OR (the N originals); never both. So no
z-index thrash.

**Stale-popover invariant.** The existing 30s/5s war-room poll
re-derives `events` and re-clusters. A `useEffect` in `EventsLayer`
watches the new `clustered` array; if `openCluster` references a
cluster object no longer present (e.g. range shifted bucket
boundaries), the popover closes itself.

**Tests (extend `CaseTimeline.test.tsx`):**

- 5 findings within ~6ms of each other → 1 cluster glyph rendered
  at the bucket midpoint, 0 individual finding dots, glyph has
  `aria-label` with count.
- Click cluster glyph → `Tooltip` opens with 5 rows.
- Click a row → `entity:open` fires for that finding's id AND
  popover closes.
- Hover an isolated finding dot → tooltip appears (instant,
  `delay=0`).
- After re-render with a different bucket layout, popover state
  resets cleanly (no orphaned popover).

## Out of scope (explicit cuts)

- **Bar clustering.** Runs/IOCs always pass through as singletons.
- **Mixed-lane clusters.** Findings + daimons at the same time
  stay as separate clusters (one per lane).
- **Severity-stacked cluster glyph** (segmented pie / striped dot
  showing all severities present). The "highest severity wins"
  rule is simpler and matches operator triage instinct.
- **Animated expand/collapse** of the cluster popover. Instant show/
  hide; portal'd, no SVG animation.
- **Severity legend on hover-tooltip.** The chip already shows the
  color.
- **Tooltip pin-to-cursor mode.** Tooltip pins to the anchor; not
  draggable.

## Federation

The `cpInstanceID` prop on `CaseTimeline` is forwarded into both
single-event click handlers and cluster-row click handlers, so
`entity:open` events carry the right CP routing. No new federation
behavior — existing infrastructure handles cross-CP drawer opens.

## Refresh / realtime

No new polling. The existing `InvestigationDetail` 30s / 5s-war-room
poll already drives the bundle. Re-render replays clustering on every
poll. Open popovers gracefully close when the underlying cluster
object is no longer in the new clustered array (see above).

## Files

### New

- `web/src/components/investigations/timeline/cluster.ts`
- `web/src/components/investigations/timeline/cluster.test.ts`
- `web/src/components/Tooltip.tsx`
- `web/src/components/Tooltip.test.tsx`

### Modified

- `web/src/components/investigations/CaseTimeline.tsx` —
  `EventsLayer` rewrites: `renderSingle` wraps each shape in
  `<Tooltip>`, removes `<title>`; `renderCluster` introduced; new
  `ClusterList` inline; new `tooltipFor` per-kind content function;
  new `useState<ClusterOrEvent | null>(null)`.
- `web/src/components/investigations/CaseTimeline.test.tsx` —
  extend with cluster + tooltip integration tests.
