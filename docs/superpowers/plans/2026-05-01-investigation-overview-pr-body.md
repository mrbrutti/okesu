## Summary

Replaces the text-shaped Investigation Overview tab with a layered visual that answers three operator questions at a glance: case status (where am I?), chronology (what happened?), and structure (what's involved?). Pure client-side derivation of the existing `InvestigationDetail` bundle plus the existing `api.investigations.audit` endpoint when the audit lane is toggled on. **No new server-side endpoints.**

- **`CaseStatusBar`** (`web/src/components/investigations/CaseStatusBar.tsx`) — 5-cell row: status pill + war-room badge, severity histogram with click-through filtering, freshness ("last activity Xm ago"), linked-entity counts, editable Summary.
- **`CaseTimeline`** (`web/src/components/investigations/CaseTimeline.tsx`) — horizontal Gantt band, hand-rolled SVG. Default-curated lanes (lifecycle / findings / runs / notes); opt-in lanes (iocs / daimons / audit). Audit-lane fetch is lazy. Lane state persists per-browser via `localStorage`. War-room cases auto-zoom to last 1 hour. Zoom presets: Fit / 1h / 24h / 7d.
- **`CaseStructure`** (`web/src/components/investigations/CaseStructure.tsx`) — four summary cards (hosts / IOCs / daimons / runs × orchestrations) with top-hitter line + 3-row mini-list per card.
- `OverviewPanel` in `InvestigationDetail.tsx` becomes a thin compositor of the three new components; Identity sidebar's content folds into the new status header; `LabelEditor` block moves below the three sections alongside `SuggestedFindingsCard`.

Click affordances reuse the SmartPayload `entity:open` event bus + `EntityDrawerHost` mounted at the App root (shipped in PR #82) — finding dots / run bars / IOC bars all open the matching detail drawer in-place.

## Test plan

Unit tests landed in this PR (component-level, vitest + @testing-library/react):
- `CaseStatusBar.test.tsx` — status pill + war-room badge, severity histogram counting, freshness derivation, edit toggle (5 tests).
- `CaseStructure.test.tsx` — four-card aggregations (hosts/IOCs/daimons/orchestrations) + heavy-hitter selection + empty-state rendering (5 tests).
- `CaseTimeline.test.tsx` — default lane state, toggle persistence to localStorage, finding-dot / run-bar rendering, click → `entity:open` dispatch (7 tests).
- `timeline/buildEvents.test.ts` — lifecycle markers, audit lane filtering, event-stream sorting (8 tests).
- `timeline/scale.test.ts` — `tToX` clamping, `autoFitRange` fallback, war-room override, range presets (7 tests).

Total: **72 vitest tests, all passing**. Full Go suite passes unchanged (no Go code touched in this PR).

Manual lab smoke (post-merge):
- [ ] Open an active investigation with ≥ 5 findings, ≥ 1 run, ≥ 1 note. Confirm the three sections render correctly.
- [ ] Toggle on `iocs`, `daimons`, `audit` lanes; verify rendering and that the audit lane fetches lazily on first toggle (network tab).
- [ ] Click a finding dot → FindingDrawer opens.
- [ ] Click a run bar → run drawer / detail page opens.
- [ ] Resize narrow → status row collapses to 2-column / 1-column grid.
- [ ] Tag a linked finding `war-bridge` → war-room badge appears and the timeline auto-zooms to "last 1 hour" on next reload.

## Files

**New:**
- `web/src/components/investigations/{CaseStatusBar,CaseTimeline,CaseStructure}.tsx` + `.test.tsx`
- `web/src/components/investigations/timeline/{types.ts,buildEvents.ts,scale.ts}` + tests for the latter two

**Modified:**
- `web/src/pages/InvestigationDetail.tsx` — `OverviewPanel` becomes a thin compositor; Identity sidebar's content folds into the new status header; `LabelEditor` block moves below the three sections.
- `docs/architecture.md` — new "Investigation Overview" section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-investigation-overview-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-investigation-overview.md`

## Open follow-ups

Tracked in the `investigation_overview_followups.md` memory:

1. Bipartite relationship graph as a "Graph view" tab.
2. Server-side aggregation endpoints for case-overview at scale.
3. Note streaming / collaborative editing.
4. Search / filter expressions on the timeline.
5. Pinned annotations / range-select / case "phases".
6. PDF / print export of the overview.
7. Timeline collision-avoidance (≥4 dots within ~6px → stacked +N) + portal'd tooltip for richer-than-`<title>` formatting.
