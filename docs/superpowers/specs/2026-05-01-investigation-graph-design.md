# Investigation graph view (bipartite relationship visualisation)

## Goal

A new "Graph" tab on the investigation detail page that visualises
the case as a bipartite graph: linked findings on the left, the
entities they touch (hosts / daimons / IOCs) on the right, with
edges showing which finding hits which entity. Operators get an
at-a-glance "this IOC is the hub across N findings" pattern that
the existing summary cards on the Overview tab can't surface.

Follow-up #1 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
parked in the `investigation_overview_followups.md` memory.

## Architecture

One new server endpoint, one new client tab, one new client
component:

1. **`GET /api/investigations/{id}/graph?limit=20`** — returns
   `{nodes, edges, total_findings, limit_applied}`. New handler in
   `controlplane/api/investigation_graph.go`. Federates via the
   existing `?cp=<instance_id>` proxy convention.

2. **New tab `'graph'`** in the investigation detail page, between
   `'overview'` and `'findings'`. Lazy-loaded.

3. **`web/src/components/investigations/CaseGraph.tsx`** — react-flow
   canvas (using `@xyflow/react`, already a dep — used by the
   orchestration run canvas). Bipartite columns with degree-sort
   within each column.

**No DB schema changes.** The endpoint joins existing tables
(`investigation_findings` for case membership, `findings` for the
finding rows, `ioc_observations` for finding↔IOC linkage, `iocs` for
IOC details). No new audit emission (read-only screen view; no
persistent artifact).

## Wire shape

`GET /api/investigations/{id}/graph?limit=20`

Query params:
- `limit` (default 20, max 100) — top-N findings to include, sorted
  by severity desc + Ts desc.

Response (200 OK):

```json
{
  "total_findings": 42,
  "limit_applied": 20,
  "nodes": [
    {"id": "f:142", "kind": "finding",  "label": "Suspicious cron job", "severity": "HIGH",  "host": "edr-fedora-3", "agent": "edr-agent"},
    {"id": "h:edr-fedora-3", "kind": "host",   "label": "edr-fedora-3"},
    {"id": "d:edr-agent", "kind": "daimon", "label": "edr-agent",     "finding_count": 9},
    {"id": "i:sha256:abc...", "kind": "ioc", "label": "sha256:...aaaa",  "ioc_kind": "sha256", "ioc_value": "abc...aaaa", "obs_count": 23, "host_count": 4}
  ],
  "edges": [
    {"id": "f:142|h:edr-fedora-3", "source": "f:142", "target": "h:edr-fedora-3"},
    {"id": "f:142|d:edr-agent",    "source": "f:142", "target": "d:edr-agent"},
    {"id": "f:142|i:sha256:abc...",  "source": "f:142", "target": "i:sha256:abc..."}
  ]
}
```

**Node `id` scheme:**
- Findings: `f:<id>`
- Hosts: `h:<host-string>`
- Daimons: `d:<agent-name>`
- IOCs: `i:<kind>:<full-value>` (full value, not truncated; needed
  for `entity:open` routing)

**Edge `id`:** `<source>|<target>`. Each finding↔entity pair is
unique within the case.

**Server-side query plan** (in `controlplane/db/investigation_graph.go`):

1. **Findings.** Fetch the case's findings with severity rank +
   `Ts`, sorted desc, capped at `limit`:
   ```sql
   SELECT f.id, f.severity, f.title, f.host, f.agent, f.ts
     FROM investigation_findings inv
     JOIN findings f ON f.id = inv.finding_id
    WHERE inv.investigation_id = ?
    ORDER BY <severity_rank DESC>, f.ts DESC
    LIMIT ?
   ```
   `total_findings` is a separate `COUNT(*)` over the same join
   without the `LIMIT`.

2. **Hosts + Daimons** are derived in Go from the kept findings'
   `Host` / `Agent` fields. No additional query.

3. **IOCs.** For the kept finding IDs, fetch IOC linkages via
   `ioc_observations`, then JOIN `iocs` for kind/value/severity:
   ```sql
   SELECT DISTINCT obs.finding_id, i.id, i.kind, i.value
     FROM ioc_observations obs
     JOIN iocs i ON i.id = obs.ioc_id
    WHERE obs.finding_id IN (?, ?, ...)
   ```
   The aggregated `obs_count` and `host_count` for each IOC come
   from a second query mirroring `ListIOCsForInvestigation`'s
   shape, scoped to the kept finding IDs.

4. **Build response.** Findings array → finding nodes; distinct
   hosts → host nodes; distinct daimons → daimon nodes (with
   `finding_count` from the existing `daimons` aggregator scoped
   to the kept findings); distinct IOCs → IOC nodes. Edges are
   the cross-product where each finding points at its host,
   daimon, and each IOC observed on it.

**RBAC.** Same as `GET /api/investigations/{id}` — anyone who can
view the case can read the graph.

## Federation

Parent-side `FederatedInvestigationGraph(store, agg)` proxies via
`?cp=<id>` to the owning child; falls through to the local handler
otherwise. Child-side `FederationInvestigationGraph(store)` is
token-authed via `requireFederationToken`. Same wrapper pattern
as the existing report endpoint (PR #117).

Mounted at:
- `r.Get("/api/investigations/{id}/graph", api.FederatedInvestigationGraph(s.store, s.fedAgg))` (cookie-auth admin group)
- `r.Get("/api/v1/federation/investigations/{id}/graph", api.FederationInvestigationGraph(s.store))` (federation-token group)

## Client component

### Tab wiring

`web/src/pages/InvestigationDetail.tsx`:
- Extend the `Tab` union type with `'graph'`.
- Add `'graph'` to the tab nav ordering: `overview → graph → findings → runs → iocs → daimons → orchestrations → notes → audit`.
- Lazy-load `CaseGraph` via `React.lazy()` matching the existing
  pattern for `OrchestrationRunCanvas` (the orchestration page
  also lazy-loads heavy canvas components).

### `CaseGraph.tsx`

**Props:**
```ts
interface Props {
  investigationID: number;
  cpInstanceID?: string;
  bundleFindingsCount: number;  // for the empty-state shortcut
}
```

The component fetches `/api/investigations/{id}/graph` on mount and
on every parent-bundle replacement (the existing 30s/5s war-room
poll triggers a re-render on the parent; passing
`bundleFindingsCount` changes triggers re-fetch).

### Layout

A panel with `min-h-[600px]`. Three regions:

1. **Banner** (top, only when `total_findings > limit_applied`):
   `"20 of 42 findings shown — sorted by severity"` plus an
   `Open Findings tab →` link that navigates to
   `/investigations/{id}?tab=findings` (preserving any `cp=` param).

2. **Canvas** — react-flow viewport. Background grid; pan + zoom
   enabled; node drag disabled (deterministic layout).

3. **Empty state** — when `bundleFindingsCount === 0` OR the
   endpoint returns zero finding nodes: centered message
   "No findings linked yet — link findings on the Findings tab to
   see relationships."

### Layout math

Computed before mounting react-flow nodes (deterministic):

- **Left column x = 80**, **right column x = 520**.
  Edges have ~440px to bend.
- **Node width 220px**, **height 56px**, **vertical gap 12px**.
- **Findings (left)**: sorted by edge degree desc → severity rank
  desc → `id` desc.
- **Right column** stack order: hosts top, then 24px gap, then
  daimons, then 24px gap, then IOCs.
- Within each right-column kind: sorted by edge degree desc.
- Both columns vertically centered if the kind-stack is shorter
  than the canvas.

### Per-kind node rendering

| Kind     | Visual                                             | Click action                           |
|----------|----------------------------------------------------|----------------------------------------|
| Finding  | severity-tinted left border, `Finding #{id}` + truncated title (40 chars) | fires `entity:open kind=finding` |
| Host     | neutral slate background, hostname text             | navigates to `/findings?host={host}` (hosts have no drawer) |
| Daimon   | indigo accent, agent name + `(N findings)`         | fires `entity:open kind=daimon` (matches existing chip behavior) |
| IOC      | purple accent, `kind:...last4` + `obs/hosts` line   | fires `entity:open kind=ioc`            |

All four reuse the SmartPayload `entity:open` event bus + the
existing `EntityDrawerHost` mounted at the App root (PR #82). No
new drawer infrastructure.

### Edges + hover

Default edge styling: neutral grey 1px Bezier curves (react-flow's
`smoothstep` edge type).

On hover of a finding node:
- Connected edges fade to brand color + 2px thickness.
- Non-connected nodes fade to 30% opacity.
- Connected entity nodes get a 2px brand-color outline.

Implemented via react-flow's `useNodesState` + `useEdgesState` plus
a `hoveredFindingID` local state that toggles per-edge `style` and
per-node `data.faded` flags.

### Cap banner action

When `total_findings > limit_applied`, the `Open Findings tab →`
link in the banner navigates to the Findings tab. v1 doesn't
preserve severity filters — operator manually re-applies. (Severity
filter chips on the Graph tab itself are out of scope per
brainstorming Q3.)

### Loading + error states

- **Loading** — skeleton: greyed-out columns at expected positions
  (~600px tall placeholder).
- **Error** — small red panel at the top: `"Couldn't load graph
  data: <message>"` + Retry button. Same pattern as
  `CaseTimeline`'s audit-fetch error path.

## Edge cases

- **Findings without `Host` field** — finding still renders; host
  edge is skipped.
- **Findings without `Agent` field** — same; daimon edge skipped.
- **IOCs not observed during the case window** — naturally absent
  (the `ioc_observations` join filters to the kept finding IDs).
- **Federated investigation** — endpoint proxies to child; child
  renders against its own DB. No cross-CP host collision possible
  because the graph is single-investigation-scoped.
- **Bundle mid-poll race** — `CaseGraph` re-fetches on every
  parent-bundle replacement; node IDs are stable, so react-flow
  re-renders deterministically.
- **Identical IOC values across kinds** (e.g. `domain:foo.com` and
  `url:foo.com`) — node ID includes both kind and value
  (`i:<kind>:<value>`), so they're distinct nodes.

## Out of scope (explicit cuts)

- **Drag-to-rearrange.** Layout is deterministic; pan/zoom only.
- **Save / share custom layouts.**
- **Edge filtering** ("show only IOC edges"). Degree-sort already
  surfaces hub nodes.
- **Severity filter chips on the Graph tab.** Operators filter via
  the Findings tab.
- **Multi-edge between same node pair** (e.g. finding → host
  with two observation timestamps). Single edge per pair.
- **Cross-case graphs** ("show me findings from other cases
  sharing this IOC"). v1 is single-case scope.
- **Animation / physics / force-directed layout.** Static.
- **Per-edge tooltips with IOC observation timestamps.** Edges are
  visual only.
- **Severity filter built into the canvas.** Above-cap operators
  use the Findings tab to narrow.
- **Audit-log row for graph view.** Read-only screen view; no
  persistent artifact.

## Testing

### Server-side (Go)

`controlplane/api/investigation_graph_test.go`:
- Empty case → `{nodes:[], edges:[], total_findings:0, limit_applied:20}`.
- 5 findings + 2 hosts + 1 daimon + 3 IOCs → returns 5+2+1+3 nodes
  and the right edge count (each finding's edges = 1 host + 1
  daimon + N IOC observations on it).
- 50 findings, `limit=20` → `total_findings=50, limit_applied=20`,
  exactly 20 finding nodes, kept findings are top-20 by severity.
- 404 on unknown investigation id.
- Severity-rank sort: 1 CRITICAL + 21 LOW with `limit=20` → returns
  the 1 CRITICAL + 19 LOWs (by `Ts` desc tie-break).
- `limit > 100` clamped to 100.

`controlplane/db/investigation_graph_test.go`:
- Store-level: round-trip a synthetic investigation through the
  graph query helper and assert finding/host/daimon/IOC counts.

### Client-side (Vitest)

`web/src/components/investigations/CaseGraph.test.tsx`:
- Renders nodes + edges from a synthetic graph response.
- Click finding node → `entity:open kind=finding identityKey={id}`.
- Click host node → navigates to `/findings?host=...`.
- Banner appears when `total_findings > limit_applied`; absent
  otherwise.
- Empty state when `bundleFindingsCount === 0`.
- Loading skeleton during in-flight fetch (mock `api.investigations.graph`).
- Error panel + retry on fetch failure.

### Manual lab smoke (post-merge)

- Open an active case with 5+ findings, 1+ shared IOC, multiple
  hosts. Click `Graph view` tab. Verify the bipartite layout
  renders with edges crossing the gap.
- Click a finding → FindingDrawer opens.
- Click an IOC → IOC chip drawer / navigation works.
- Hover a finding → edges and connected entities highlight; non-
  connected fade.
- Trigger the cap by linking 25 findings; verify banner shows
  "20 of 25 findings shown" and only top-20 are rendered.
- Federated case (`cp_source` set) → graph endpoint proxies to
  child correctly.

## Files (planned)

### New

- `controlplane/db/investigation_graph.go` — store helpers (graph
  query + total count).
- `controlplane/db/investigation_graph_test.go`
- `controlplane/api/investigation_graph.go` — handler + federation
  wrappers.
- `controlplane/api/investigation_graph_test.go`
- `web/src/components/investigations/CaseGraph.tsx`
- `web/src/components/investigations/CaseGraph.test.tsx`

### Modified

- `controlplane/server.go` — mount the new routes (cookie-auth
  admin + federation-token).
- `web/src/pages/InvestigationDetail.tsx` — extend `Tab` union;
  add tab nav entry; lazy-load + render `CaseGraph` for
  `tab === 'graph'`.
- `web/src/api.ts` — add `GraphResponse` / `GraphNode` /
  `GraphEdge` types + `api.investigations.graph(id, opts)` helper.
- `docs/architecture.md` — new section.
