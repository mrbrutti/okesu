## Summary

Follow-up #7 from the Investigation Overview backlog. Two improvements to the case timeline:

1. **Cluster overlapping point events.** When ≥4 finding/note/audit/lifecycle/daimon events fall in the same 12px-wide x-bucket on the same lane, render a single `+N` glyph instead of an unreadable smudge of dots. Click the glyph → controlled-mode `<Tooltip>` popover with one row per clustered event; click a row → opens that event's detail drawer (per-kind `entity:open`) and dismisses the popover. Bars (runs / IOCs) skip clustering — their duration carries the signal.

2. **Portal'd `<Tooltip>` replaces SVG `<title>`.** New shared component at `web/src/components/Tooltip.tsx`. Renders into `document.body` so it can't be clipped by SVG viewport or parent overflow. Uncontrolled mode (hover/focus, instant `delay=0`) replaces the existing SVG `<title>` text with styled multi-line content — severity chips, status pills, monospace IDs, click-to-open hints. Controlled mode (open/onOpenChange) drives the cluster popover.

Both pieces are reusable: the cluster algorithm is a pure function (`cluster.ts`), and the Tooltip is generic enough that other parts of the page can adopt it.

## Test plan

Unit tests in this PR:
- `timeline/cluster.test.ts` — empty input, below-threshold passthrough, at-threshold cluster, no cross-bucket merging, bars skip clustering, per-lane separation, sort order, lane visibility, custom thresholds (9 tests).
- `Tooltip.test.tsx` — uncontrolled show/hide on hover + focus, hover delay, portal target = `document.body`, controlled `open`, outside-click + Escape dismissal, hover suppression in controlled mode (8 tests).
- `CaseTimeline.test.tsx` — extends existing tests with cluster glyph rendering, cluster popover open + row-click dispatch + popover dismissal (3 new tests).

Total: 92 vitest tests across 13 files, all passing. No Go code touched.

Manual lab smoke (post-merge):
- [ ] Open an investigation with ≥ 4 findings within a few minutes of each other → single cluster glyph at the bucket midpoint.
- [ ] Click cluster → popover lists each finding; row click opens FindingDrawer + popover dismisses.
- [ ] Hover an isolated finding → styled tooltip appears instantly (no native delay).
- [ ] Resize the page; tooltip + popover reposition correctly via the ResizeObserver/scroll listeners.
- [ ] Press Escape with the cluster popover open → dismisses.

## Files

**New:**
- `web/src/components/investigations/timeline/cluster.ts` (+ test)
- `web/src/components/Tooltip.tsx` (+ test)

**Modified:**
- `web/src/components/investigations/CaseTimeline.tsx` — `EventsLayer` rewrites, new helpers (`tooltipFor`, `highestSeverityColor`, `formatDuration`, `sevTone`/`runTone`, `ClusterList`, `clusterRowDetails`).
- `docs/architecture.md` — new section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-timeline-clusters-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-timeline-clusters.md`

## Closes / refs

Implements item #7 from `investigation_overview_followups.md`. Six remaining backlog items unchanged.
