## Summary

Follow-up #1 from the Investigation Overview backlog. Adds a "Graph" tab to the investigation detail page rendering the case as a bipartite relationship graph: linked findings on the left, the entities they touch (hosts / daimons / IOCs) on the right, with edges showing which finding hits which entity. Operators get an at-a-glance "this IOC is the hub across N findings" pattern that the summary cards on the Overview tab can't surface.

- New endpoint `GET /api/investigations/{id}/graph?limit=20` returning `{nodes, edges, total_findings, limit_applied}`. Joins `investigation_findings` + `findings` + `ioc_observations` + `iocs`. Federation routes via the existing `?cp=<instance_id>` proxy convention.
- New client component `CaseGraph.tsx` using `@xyflow/react` (already a dep — orchestration run canvas). Deterministic bipartite layout (`graph/layout.ts`) with degree-sort within each column.
- Cap behavior: top-20 findings by severity desc + Ts desc; banner shows "20 of N findings shown" when over the cap. Operator narrows further via the Findings tab.
- Click affordances reuse the SmartPayload `entity:open` event bus + `EntityDrawerHost` portal. Hosts have no drawer; clicking navigates to `/findings?host=…` instead.
- Tab order: `overview → graph → findings → runs → iocs → daimons → orchestrations → notes → audit`.

The graph view is a static layout — no force-directed physics, no drag-to-rearrange. Pan and zoom only.

## Test plan

Unit tests in this PR:
- `controlplane/db/investigation_graph_test.go` — empty-case, top-N-by-severity, limit clamping (4 tests).
- `controlplane/api/investigation_graph_test.go` — handler returns 404/empty/correct response shape; `lastN` helper (4 tests).
- `web/src/components/investigations/graph/layout.test.ts` — column placement + kind ordering + degree sort (5 tests).
- `web/src/components/investigations/CaseGraph.test.tsx` — empty state, fetch + render, cap banner, click → entity:open (4 tests).

Total: 17 new tests + full Go suite + full vitest suite (101 tests across 15 files) all green.

Manual lab smoke (post-merge):
- [ ] Open an active case with 5+ findings, 1+ shared IOC, multiple hosts. Click "Graph" tab. Verify bipartite layout renders.
- [ ] Click a finding → FindingDrawer opens.
- [ ] Click an IOC → IOC chip drawer / navigation works.
- [ ] Click a host → navigates to `/findings?host=…`.
- [ ] Trigger the cap by linking 25 findings; verify banner shows "20 of 25 findings shown".
- [ ] Federated case (`cp_source` set) → graph endpoint proxies to child correctly.

## Files

**New (server):**
- `controlplane/db/investigation_graph.go` (+ test)
- `controlplane/api/investigation_graph.go` (+ test)

**New (frontend):**
- `web/src/components/investigations/graph/layout.ts` (+ test)
- `web/src/components/investigations/CaseGraph.tsx` (+ test)

**Modified:**
- `controlplane/server.go` — mount the two new routes (cookie-auth admin + federation-token).
- `web/src/api.ts` — `GraphResponse` types + `api.investigations.graph` helper.
- `web/src/pages/InvestigationDetail.tsx` — extend `Tab` union, add tab nav entry (Network icon), lazy-load + render CaseGraph.
- `docs/architecture.md` — new section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-investigation-graph-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-investigation-graph.md`

## Closes / refs

Implements item #1 from `investigation_overview_followups.md`. Three remaining backlog items unchanged (timeline search, server-side aggregation, note streaming, pinned annotations).
