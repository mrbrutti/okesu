# Catalog Federation — Design Spec

**Date:** 2026-04-30
**Status:** Approved (pending user review of this document)

## Goal

Make every Catalog API endpoint federation-aware so a parent CP browsing `/catalog` sees one merged view across the entire fleet — own rows + every healthy child's rows. Same architectural posture as the rest of the platform: every single-CP API has a federated equivalent.

## Background

The Catalog UI ships today against four single-CP endpoints:

- `GET /api/iocs` — list IOCs in this CP's DB
- `GET /api/iocs/{id}` — single record
- `GET /api/iocs/{id}/observations` — observation history (this CP only)
- `GET /api/iocs/{id}/relationships` — typed edges (this CP only)

A parent CP's `/catalog` page therefore only shows its own IOCs. Operators want fleet-wide visibility: "this sha256 showed up across 3 CPs, where".

The federation layer already has the primitives:

- `controlplane/federation/aggregator.go` — `Aggregator.FanOut(ctx, fetch)` calls every healthy peer in parallel; `Aggregator.FetchJSON(ctx, peer, path, out)` does the per-peer HTTPS fetch (or S3 read for S3-transport peers).
- `controlplane/api/federation_reads.go` — established pattern: a `FederatedX(store, agg)` handler that calls the local handler via `httpRecorder`, then `FanOut`s to children, then merges in-handler.
- `RenderFederationX(store, limit) ([]byte, error)` — established pattern for child-side export endpoints under `/api/v1/federation/...`.

This spec adds IOC handlers to that pattern. Build matrix unchanged: every binary still ships with `CGO_ENABLED=0`.

## Architecture

Two-layer additions, mirroring how findings federate today:

**Child-side exports** at `/api/v1/federation/iocs*`. Returns the per-CP-local view (no merging — these are the upstream data the parent's aggregator pulls):

```
GET /api/v1/federation/iocs                                   → []IOCRecord (capped at 5000)
GET /api/v1/federation/iocs/by-kv?kind=&value=                → IOCRecord
GET /api/v1/federation/iocs/by-kv/observations?kind=&value=   → []IOCObservation
GET /api/v1/federation/iocs/by-kv/relationships?kind=&value=  → []IOCRelationship
```

**Parent-side federated wrappers** added to `controlplane/api/federation_reads.go`:

```go
FederatedListIOCs(store, agg)                  // wraps GET /api/iocs
FederatedGetIOCByKV(store, agg)                // GET /api/iocs/by-kv?kind=&value=
FederatedListIOCObservationsByKV(store, agg)   // GET /api/iocs/by-kv/observations?kind=&value=
FederatedListIOCRelationshipsByKV(store, agg)  // GET /api/iocs/by-kv/relationships?kind=&value=
```

Server.go swaps the existing `r.Get("/api/iocs", api.ListIOCs(...))` etc. for the federated versions in the cookie-auth viewer+ group. The legacy id-based routes (`/api/iocs/{id}`, `/{id}/observations`, `/{id}/relationships`) stay mounted unchanged so any external integration keeps working — they just return one CP's view.

**Pattern compliance:** every handler follows `federation_reads.go` conventions verbatim — same `httpRecorder` for the local call, same `agg.FanOut` for peers, same `X-Okesu-Federation-Warning` header for partial-failure signalling, same 10-second context timeout, same fall-through-on-error semantics.

## Merge semantics

**Determinism:** within each merge function, iterate contributing rows in ascending CP-ID order. "First non-empty wins" rules below assume that ordering, so the same input set always produces the same output regardless of fan-out completion order.

### List (`FederatedListIOCs`)

Dedup key: `(kind, normalized_value)`.

For each unique key:

- **Text metadata** (name, tags, severity_floor, attribution, classification, notes, definition_path): **first non-empty wins**, with rows where `source = "catalog"` preferred over `"observed"` (catalog rows win the tie).
- **`source`**: `"catalog"` if any contributing row is catalog, else `"observed"`.
- **`observation_count`**: sum across CPs.
- **`first_seen`**: min across CPs.
- **`last_seen`**: max across CPs.
- **`CPInstanceIDs []string`** (new field on the wire shape): list of every CP id whose DB has this row, sorted ascending.

### By-kv detail (`FederatedGetIOCByKV`)

Same merge rules, scoped to one (kind, normalized_value). 404 if no CP has the row.

### Observations (`FederatedListIOCObservationsByKV`)

Union all observations from all CPs that have the IOC. Each obs row gains `CPInstanceID string` (single CP). Sort `ObservedAt DESC` (after merging). No dedup — observations are per-host events.

### Relationships (`FederatedListIOCRelationshipsByKV`)

The wire shape changes: int64 `SubjectID`/`ObjectID` (per-CP-meaningless) become `(kind, normalized_value)` pairs.

Dedup key: tuple `(subject_kind, subject_value, predicate, object_kind, object_value)`.

For each unique tuple:

- **`Source`**: first non-empty wins (the curator name, e.g., `"agent"`, `"catalog"`).
- **`Confidence`**: first non-empty wins.
- **`CPInstanceIDs []string`**: union across CPs that recorded the edge.

## URL scheme

| Today | Federated |
|---|---|
| `GET /api/iocs/{id}` | `GET /api/iocs/by-kv?kind=&value=` |
| `GET /api/iocs/{id}/observations` | `GET /api/iocs/by-kv/observations?kind=&value=` |
| `GET /api/iocs/{id}/relationships` | `GET /api/iocs/by-kv/relationships?kind=&value=` |
| frontend `/catalog/:id` | frontend `/catalog/:kind/:value` (URL-encoded) |

Legacy id-based endpoints stay mounted; they're no-op'd from the UI but remain available for external integrations.

## Wire shape changes

`IOCRecord` JSON gains `CPInstanceIDs []string`. Federated endpoints populate it with the union of CPs that have the row; locally-served endpoints populate it with `[localCPID]`. The field is always present, never omitted — same posture as how findings handle their own CP-id field.

`IOCObservation` JSON gains `CPInstanceID string` on the federated path; locally-served endpoints populate it with `localCPID`.

The federated relationship wire shape is new — call it `FederatedIOCRelationship`:

```go
type FederatedIOCRelationship struct {
    SubjectKind     string
    SubjectValue    string   // normalized
    Predicate       string
    ObjectKind      string
    ObjectValue     string   // normalized
    Source          string
    Confidence      string
    CPInstanceIDs   []string
}
```

The local id-based `/api/iocs/{id}/relationships` keeps the existing int-id wire shape — it's only used by external integrations now.

## Frontend

**`web/src/api.ts` additions:**

```ts
api.iocByKV(kind, value)              // returns IOCRecord & { CPInstanceIDs: string[] }
api.iocObservationsByKV(kind, value)  // returns (IOCObservation & { CPInstanceID: string })[]
api.iocRelationshipsByKV(kind, value) // returns FederatedIOCRelationship[]
```

The existing `api.iocs(filter)` keeps its current signature; the response now includes `CPInstanceIDs` per row.

**Page changes:**

- **Catalog list (`/catalog`)**: new "CPs" column rendering `CPInstanceIDs` as small chips. Row click navigates to `/catalog/:kind/:value`.
- **Detail page (`/catalog/:kind/:value`)**: header shows the IOC's kind+value plus a "Seen on N CPs" chip list immediately under it.
- **Observations tab**: new "CP" column showing per-row origin.
- **Relationships tab**: subject/object render as `(kind value)` pairs (clickable to pivot to that IOC's `/catalog/:kind/:value`); new "CPs" column.

**Federation warning banner**: when the response sets `X-Okesu-Federation-Warning`, the page renders a yellow banner above the table: "Showing partial results — N of M CPs unreachable." Same pattern as elsewhere; if no shared `<FederationWarning />` component exists, build a small one in `web/src/components/`.

## Out of scope (deferred)

- **Catalog YAML federation propagation parent→child** — separate work. This spec makes the IOC table federate regardless of how YAML files arrive on each CP.
- **Real-time updates** (SSE/polling) — request-response; refresh on filter change.
- **CP filter chips on the list page** — clicking a CP chip narrows the view. Easy follow-up once the merge ships; v1 just shows the chips for visibility.
- **Cross-CP relationship traversal optimization** — clicking a relationship endpoint just navigates to `/catalog/:kind/:value` and the federated detail handler does its thing.

## Out of scope (cut)

- **Backwards-compat shim for old `/catalog/:id` URLs** — page only shipped one PR ago, nobody has bookmarks. The route stays unmounted in the UI; the API endpoints stay mounted for external integrations.

## Testing

### Backend

- **Merge unit tests** in `controlplane/api/federation_iocs_merge_test.go` (or appended to `federation_reads_test.go`):
  - `mergeIOCs` — table-test: same row from two CPs dedupes; observation_counts sum; CPInstanceIDs union'd; first_seen takes min, last_seen takes max; catalog source wins over observed for metadata.
  - `mergeIOCObservations` — union, sort by ObservedAt DESC, each row tagged with CPInstanceID.
  - `mergeIOCRelationships` — dedup by tuple; CPInstanceIDs union'd.
- **HTTP handler tests** — use the existing federation test pattern (mock peers via `httptest.Server` + the aggregator's `FetchJSON` path). One test per federated handler covering: no peers (local-only), one healthy peer, one peer-fails (warning header set), and the merge happy-path with overlapping (kind, value) pairs.
- **Render export tests** — confirm wire shape for each `RenderFederationIOCs*` function.

### Frontend

- `npm run build` clean.
- Lab-smoke: drop a yara_rule into the parent + a child's catalog YAML, observe an IOC on each, hit `/catalog`, verify (a) deduped row with both CP names in the chip list, (b) detail page shows union of observations, (c) relationships tab unions edges.

## Risks

- **Per-CP failure cascades** — if a child CP is slow, the parent's `/catalog` blocks until the 10s aggregator timeout. The existing pattern accepts this; the warning header surfaces partial failures. If the catalog page becomes a hot path and timeouts hurt, follow-up work could background-cache children's IOC lists in the aggregator (Phase A.2-style snapshot publishing).
- **Relationship deduplication inconsistency** — if two CPs disagree on the predicate for the same `(subject_kv, object_kv)` (e.g., one says `resolves-to`, another says `hosted-at`), they're treated as distinct edges. That's correct — the predicate vocabulary is a fixed v1 set, and disagreement is a real signal worth surfacing.
- **Frontend route breakage** — `/catalog/:id` URLs break for anyone bookmarked. Acceptable since the page shipped <2 days ago.

## Open follow-ups (post-v1)

- CP filter chip click narrows the list (currently chips are display-only)
- Aggregator-backed snapshot caching for IOCs (avoid per-page-load fan-out)
- Catalog YAML federation propagation parent↔child
- Per-CP observation count breakdown in the IOC detail header (e.g., "12 obs on parent, 4 on child-1, 0 on child-2")
