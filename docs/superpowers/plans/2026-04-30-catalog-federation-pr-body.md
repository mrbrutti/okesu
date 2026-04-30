## Summary

Make every Catalog API endpoint federation-aware. Parent CP's
`/catalog` page merges rows from itself plus every healthy child;
detail/observations/relationships likewise.

- Backend: four new federated wrappers (`FederatedListIOCs`,
  `FederatedGetIOCByKV`, `FederatedListIOCObservationsByKV`,
  `FederatedListIOCRelationshipsByKV`) in
  `controlplane/api/federation_iocs.go`.
- Backend: four new child-side exports under
  `/api/v1/federation/iocs*` behind `requireFederationToken`.
- Backend: three pure merge functions (`MergeIOCs`,
  `MergeIOCObservations`, `MergeIOCRelationships`) — heavily
  unit-tested with deterministic CP-ID iteration and stable
  output ordering.
- Backend: three new local handlers `GetIOCByKVHandler`,
  `ListIOCObservationsByKVHandler`,
  `ListIOCRelationshipsByKVHandler` for the kv-shaped detail
  endpoints. The legacy id-based routes stay mounted for external
  integrations.
- Frontend: `/catalog/:id` → `/catalog/:kind/:value` with URL-encoded
  value. Catalog list and detail pages render `cp_sources` as chips;
  observations table adds a CP column; relationships render kv pairs
  with click-to-pivot.
- Frontend: `X-Okesu-Federation-Warning` header surfaces as a yellow
  banner above the list when a peer fails.

## Test plan

Lab smoke (post-merge, operator-side):

- [ ] `go test ./...` passes
- [ ] `npm run build` clean in `web/`
- [ ] On a parent CP federated to a child: drop the same yara_rule
      into both catalog directories; SIGHUP; navigate to `/catalog`;
      verify a single deduped row with both CP names in the chips,
      summed observation_count, max(last_seen).
- [ ] Observe an IOC on each CP; navigate to its
      `/catalog/{kind}/{value}` detail page; verify the Observations
      tab shows both observations with their per-CP origin labels.
- [ ] Add a `resolves-to` relationship on each CP between the same
      `(domain → ipv4)` pair; verify the Relationships tab shows one
      deduped tuple with both CPs in the CPs column.
- [ ] Stop a child CP; reload `/catalog`; verify the yellow banner
      appears and the list still renders the parent's rows.

## Files

- New endpoints (parent-side):
  `GET /api/iocs/by-kv?kind=&value=`,
  `GET /api/iocs/by-kv/observations?kind=&value=`,
  `GET /api/iocs/by-kv/relationships?kind=&value=`.
- New endpoints (child-side):
  `GET /api/v1/federation/iocs`,
  `GET /api/v1/federation/iocs/by-kv`,
  `GET /api/v1/federation/iocs/by-kv/observations`,
  `GET /api/v1/federation/iocs/by-kv/relationships`.
- `GET /api/iocs` now federated (was local-only).
- New page route `/catalog/:kind/:value` (replaces `/catalog/:id`).

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-catalog-federation-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-catalog-federation.md`

## Open follow-ups

- CP-chip click filtering on the list page
- Aggregator-backed snapshot caching for IOCs (avoid per-page-load fan-out)
- Catalog YAML federation propagation parent↔child
- Per-CP observation count breakdown in the detail header
- Unify the `X-Okesu-Federation-Warning` banner pattern across pages (build a `<FederationWarningBanner />`)
- FanOut/FetchJSON plumbing test (covers wrong-path regressions; deferred from Group D)
