# Case-structure server-side aggregation endpoint

## Goal

Move the four investigation Overview-tab "structure card"
aggregations (hosts / IOCs / daimons / orchestrations) behind a new
`GET /api/investigations/{id}/structure` endpoint. Keeps the client
fast at scale while preserving the existing client-side aggregators
as a graceful fallback for federated children running an older
binary.

Follow-up #2 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
parked in the `investigation_overview_followups.md` memory.

## Architecture

A new endpoint returns four pre-aggregated, pre-sorted lists in one
JSON payload. The handler is thin — it composes four Store methods
and emits the response. Federation follows the existing
`FederatedInvestigationGraph` pattern: parent proxies via `?cp=<id>`;
child is token-authed.

`CaseStructure.tsx` switches to fetch-on-mount and renders from the
response. On any failure (404, network, malformed JSON) the
component derives the same shape from the bundle using the existing
aggregators. The wire shape is identical between server and
fallback paths so the rest of the render is shared.

The existing `GetInvestigationHandler` is untouched; the
`InvestigationDetail` bundle keeps its current shape so other tabs
(Findings / Runs / IOCs) are unaffected.

## File structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/db/investigation_enriched.go` | MODIFY | Add `InvestigationHostItem` + `ListHostsForInvestigation`; extend `InvestigationOrchestrationItem` with `Completed/Failed/Cancelled/Running` and the SQL behind it |
| `controlplane/db/investigation_enriched_test.go` | MODIFY | Tests for the new host method + extended orch shape |
| `controlplane/api/investigation_structure.go` | NEW | `GetInvestigationStructureHandler` + federation pair |
| `controlplane/api/investigation_structure_test.go` | NEW | Handler-level tests |
| `controlplane/server.go` | MODIFY | Register `/structure` routes (parent + child) |
| `web/src/api.ts` | MODIFY | `api.investigations.structure(id, cpInstanceID?)` + `InvestigationStructure`, `InvestigationHostItem` types; extend `InvestigationOrchestrationItem` with the four status fields |
| `web/src/components/investigations/CaseStructure.tsx` | MODIFY | Fetch `/structure` on mount; render from response on success; fall back to client aggregators against the bundle on failure |
| `web/src/components/investigations/caseStructure/derive.ts` | NEW | Move the four existing client aggregators here so they're independently testable + reusable from the fallback |
| `web/src/components/investigations/caseStructure/derive.test.ts` | NEW | Unit tests for the extracted aggregators |
| `web/src/components/investigations/CaseStructure.test.tsx` | NEW | Component tests covering both code paths (server data + fallback) |
| `docs/architecture.md` | MODIFY | New section under "Investigation Overview" |

## Wire shape

```json
{
  "hosts": [{"host": "edr-fedora-3", "count": 12}, ...],
  "iocs": [
    {
      "id": 9, "kind": "ip", "value": "10.0.0.1",
      "severity": "HIGH",
      "observation_count": 14, "host_count": 3,
      "first_seen": "2026-04-30T10:00:00Z",
      "last_seen": "2026-05-01T11:30:00Z"
    },
    ...
  ],
  "daimons": [
    {"agent": "edr-agent", "finding_count": 8, "last_seen_ts": 1714559400},
    ...
  ],
  "orchestrations": [
    {
      "orchestration_id": 4, "orchestration_name": "auto-triage",
      "run_count": 12,
      "completed": 8, "failed": 2, "cancelled": 1, "running": 1,
      "last_started_at": "2026-05-01T11:30:00Z"
    },
    ...
  ]
}
```

All four arrays are pre-sorted descending by their primary count
(host count, observation count, finding count, run count), matching
the current client-side ordering. Empty case → all four arrays are
`[]`, not `null`.

## DB type changes (`controlplane/db/investigation_enriched.go`)

```go
// NEW
type InvestigationHostItem struct {
    Host  string
    Count int
}

// ListHostsForInvestigation returns distinct hosts seen in the
// case's linked findings, sorted desc by count. Missing/empty Host
// strings are excluded.
func (s *Store) ListHostsForInvestigation(invID int64) ([]InvestigationHostItem, error)

// EXTENDED — adds four status counts. SQL changes from a single
// COUNT(*) to one COUNT-with-CASE-WHEN per status, all in one
// GROUP BY.
type InvestigationOrchestrationItem struct {
    OrchestrationID   sql.NullInt64
    OrchestrationName string
    RunCount          int
    Completed         int  // NEW
    Failed            int  // NEW
    Cancelled         int  // NEW
    Running           int  // NEW
    LastStartedAt     string
}
```

`InvestigationIOCItem` and `InvestigationDaimonItem` are unchanged —
already aggregated correctly. `ListIOCsForInvestigation` and
`ListDaimonsForInvestigation` get an `ORDER BY` tweak only if
their existing sort doesn't match descending-by-primary-count;
otherwise we sort in the handler before emitting.

## Server-side handlers (`controlplane/api/investigation_structure.go`)

```go
package api

import (
    "encoding/json"
    "net/http"
    "strings"

    "github.com/section9labs/okesu/controlplane/db"
    "github.com/section9labs/okesu/controlplane/federation"
)

type structureResponse struct {
    Hosts          []db.InvestigationHostItem          `json:"hosts"`
    IOCs           []db.InvestigationIOCItem           `json:"iocs"`
    Daimons        []db.InvestigationDaimonItem        `json:"daimons"`
    Orchestrations []db.InvestigationOrchestrationItem `json:"orchestrations"`
}

func GetInvestigationStructureHandler(store *db.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, err := investigationIDFromChi(r)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        if _, err := store.GetInvestigation(id); err != nil {
            http.Error(w, "not found", http.StatusNotFound)
            return
        }
        hosts, _   := store.ListHostsForInvestigation(id)
        iocs, _    := store.ListIOCsForInvestigation(id)
        daimons, _ := store.ListDaimonsForInvestigation(id)
        orchs, _   := store.ListOrchestrationsForInvestigation(id)

        if hosts == nil   { hosts = []db.InvestigationHostItem{} }
        if iocs == nil    { iocs = []db.InvestigationIOCItem{} }
        if daimons == nil { daimons = []db.InvestigationDaimonItem{} }
        if orchs == nil   { orchs = []db.InvestigationOrchestrationItem{} }

        resp := structureResponse{
            Hosts: hosts, IOCs: iocs, Daimons: daimons, Orchestrations: orchs,
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(resp)
    }
}

// FederatedInvestigationStructure — parent-side wrapper. Proxies to
// the owning child via ?cp=<instance_id>; falls through to the local
// handler otherwise.
func FederatedInvestigationStructure(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
        if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
            return
        }
        GetInvestigationStructureHandler(store).ServeHTTP(w, r)
    }
}

// FederationInvestigationStructure — child-side, token-authed sibling.
func FederationInvestigationStructure(store *db.Store) http.HandlerFunc {
    return requireFederationToken(store, GetInvestigationStructureHandler(store))
}
```

**Routes registered in `controlplane/server.go`** next to the
existing `/graph` registrations:

```go
r.Get("/api/investigations/{id}/structure",
    api.FederatedInvestigationStructure(s.store, s.federationAgg))
r.Get("/api/v1/federation/investigations/{id}/structure",
    api.FederationInvestigationStructure(s.store))
```

**Failure modes:**

- Unknown investigation ID → 404 (matches the graph handler).
- DB errors on the four list methods → swallowed, replaced with
  empty arrays. Same as `GetInvestigationHandler`. Operators see
  empty cards; logs from the Store layer indicate the underlying
  cause.
- Federation: `?cp=<id>` proxies to the child via
  `proxyToCPByQuery`. On child unreachable → 502 from the proxy.

## Client refactor (`web/src/components/investigations/CaseStructure.tsx`)

```tsx
export function CaseStructure({ bundle, cpInstanceID }: Props) {
  const [data, setData] = useState<InvestigationStructure | null>(null);
  const invID = bundle.investigation.ID;

  useEffect(() => {
    let cancelled = false;
    api.investigations.structure(invID, cpInstanceID)
      .then((r) => { if (!cancelled) setData(r); })
      .catch(() => {
        if (!cancelled) {
          setData(null);
          // Logged so federated CPs running an older binary are
          // visible during operator support.
          console.warn('case-structure fetch failed; using bundle-derived view', invID);
        }
      });
    return () => { cancelled = true; };
  }, [invID, cpInstanceID]);

  // data === null → either still loading or fetch failed; fall back
  // to client-side derivation. Same wire shape so the rest of the
  // render is shared.
  const view: InvestigationStructure = data ?? deriveFromBundle(bundle);

  // ...four cards rendered from `view`
}
```

The four existing aggregators (`aggregateHosts`, `topIOCs`,
`topDaimons`, `topOrchestrations`) move to
`web/src/components/investigations/caseStructure/derive.ts`.
`topOrchestrations` becomes `deriveOrchestrations`, returning the
new wire shape with the per-status counts (the existing in-line
logic already computes these — just exposing them now).

```ts
export function deriveFromBundle(b: InvestigationDetail): InvestigationStructure {
  return {
    hosts:          aggregateHosts(b),
    iocs:           topIOCs(b),
    daimons:        topDaimons(b),
    orchestrations: deriveOrchestrations(b),
  };
}
```

**Subtleties:**

- **First-paint flicker.** The first paint shows the bundle-derived
  view. When `/structure` resolves a few hundred ms later, the cards
  re-render from server data. Wire shape is identical, so visually
  the cards stay put — only the underlying numbers may shift if the
  bundle is stale. We deliberately do NOT show a spinner; the
  bundle-derived view is correct enough that operators don't need to
  wait.
- **Stale bundle vs fresh structure.** During the war-room poll the
  bundle refreshes every 5s; `/structure` re-fetches when `invID` or
  `cpInstanceID` changes. Last-write-wins on `data`; the
  `cancelled` cleanup flag prevents a slow first response from
  overwriting a fresh second one.
- **Federation.** `cpInstanceID` is threaded into the fetch
  identically to how `audit` already does it. No new federation
  logic in the client.

## Testing

### Server-side

`controlplane/db/investigation_enriched_test.go` (extend):

- `ListHostsForInvestigation` returns rows sorted desc by count.
- Findings with empty/null host are excluded.
- Empty case → empty result.
- Extended `ListOrchestrationsForInvestigation` populates
  `Completed/Failed/Cancelled/Running` per orch (seed runs across
  every status).

`controlplane/api/investigation_structure_test.go` (new):

- `GET /api/investigations/99999/structure` → 404.
- Non-empty case → 200 with all four keys present, well-formed JSON.
- Empty case → 200 with all four keys as `[]`, not `null`.
- Federation child endpoint rejects without a federation token (uses
  the existing `requireFederationToken` test helper).

### Client-side

`web/src/components/investigations/caseStructure/derive.test.ts`
(new): unit tests for the four extracted aggregators against
synthetic bundles. Ports the existing in-component logic.

`web/src/components/investigations/CaseStructure.test.tsx` (new):

- Renders host count, top IOC, top daimon, top orch from
  `/structure` when the fetch resolves.
- Falls back to bundle-derived numbers on 404 (mock
  `api.investigations.structure` to reject).
- Falls back on network error.
- Re-fetches when `invID` prop changes.
- Stale-fetch race: cleanup flag prevents a slow first response
  from overwriting a fresh second response.

### Manual lab smoke (post-merge)

- Open an active case with mixed-severity findings ≥ 5 hosts. Verify
  the four cards populate from `/structure` (DevTools → Network →
  one structure call).
- Block `/structure` in DevTools and reload. Verify the cards still
  render correct numbers via the fallback.
- Federated case (`cp_source` set) → confirm one `/structure?cp=<id>`
  request fires; cards populate.
- Federated case where the child is running an older binary without
  the route → confirm 404 in DevTools, `console.warn` logged,
  fallback path renders correctly.

## Edge cases

- **Investigation with zero linked entities** — handler returns
  `{hosts:[], iocs:[], daimons:[], orchestrations:[]}`; cards show
  "no items linked yet".
- **Federated child without `/structure`** — 404; fallback path.
  Logged once via `console.warn` so operators can spot which CPs
  need upgrading.
- **DB partial failure** — e.g. `ListHostsForInvestigation` errors
  but daimons succeeds. Empty array for the failed query; surviving
  cards still render. Matches the permissive pattern of
  `GetInvestigationHandler`.
- **War-room poll race** — bundle and structure responses can land
  out-of-order; cards re-render from whichever lands last. Visual
  stability is preserved by the identical wire shape.

## Out of scope (explicit cuts)

- **Caching / TTL.** Query latency is well under the
  perceived-render budget at lab scale. Revisit if profiling shows
  hot DB time.
- **Per-severity host breakdown / time-bucket aggregations.** The
  structure cards don't need them.
- **Auto-detect "case is large" + omit heavy bundle arrays.**
  Orthogonal optimization; bundle stays the same in this PR.
- **`InvestigationDetail` extension.** Explicitly NOT done; the
  feature lives at a separate endpoint per Q1=B (graceful fallback
  for federated children that don't yet have the route).
- **Always-server, no-fallback mode.** Q1 explicitly picked the
  fallback option to support rolling federation upgrades.
- **Threshold gate** ("only call /structure when findings > 500").
  Adds branching for a perf bump that doesn't matter at lab scale.
