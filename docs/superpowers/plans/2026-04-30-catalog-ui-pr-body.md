## Summary

A read-only `/catalog` page that lets operators browse, filter, and
inspect every IOC the system knows about — catalog-curated and
observation-derived rows alike — with a side drawer + full detail
page (Overview / Observations / Relationships tabs).

- New page at `/catalog` with kind / source / search filters.
- Drawer for quick scan; "Open full page →" navigates to `/catalog/:id`.
- Detail page tabs: Overview (identity, metadata, observability,
  rule body for yara_rule/sigma_rule), Observations
  (finding/run/host/observed_at), Relationships (typed edges,
  click-to-pivot).
- Sidebar entry under the existing `Triage` group.
- Backend additions: `GET /api/iocs/{id}`,
  `GET /api/iocs/{id}/observations`, and `source` + `q` filters on
  the existing `GET /api/iocs`.

No schema changes. Built on top of the Phase 22.1–22.5 work.

## Test plan

Lab smoke (post-merge, operator-side):

- [ ] `go test ./...` passes (excluding pre-existing UI dist embed)
- [ ] `npm run build` clean in `web/`
- [ ] Drop the existing `catalog/iocs/example-yara-mimikatz.yaml` and
      `example-sigma-suspicious-powershell.yaml` into a CP's catalog
      directory; SIGHUP; navigate to `/catalog`; verify both rows
      appear with the right kind / source / name / tags / severity.
- [ ] Click a yara_rule row → drawer slides in with metadata + tags;
      "Open full page →" navigates to `/catalog/:id`.
- [ ] On the detail page, switch to the Observations tab — should be
      empty for catalog-only rows. Manually emit an `enrich_ioc`
      action against an observed sha256 and re-check; observation
      row should appear with finding link, host, observed_at.
- [ ] Switch to Relationships tab — `resolves-to` edge between domain
      and ipv4 IOCs should appear with both endpoints linkable.
- [ ] Filter the list by `?source=catalog` and `?q=mimikatz` —
      narrowing works.

## Files

- New endpoints: `GET /api/iocs/{id}`, `GET /api/iocs/{id}/observations`.
- Filter additions on existing `GET /api/iocs`: `?source=`, `?q=`.
- New pages: `web/src/pages/Catalog.tsx`, `web/src/pages/CatalogDetail.tsx`.
- New component: `web/src/components/CatalogDrawer.tsx`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-29-catalog-ui-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-catalog-ui.md`

## Open follow-ups

- Syntax highlighting for rule bodies (deferred; revisit if operators
  ask). Library candidates: `shiki` (heavier, prettier) or
  `prism-react-renderer` (lighter).
- Relationship graph viz (list view in v1).
- Pagination for `/api/iocs` once the catalog grows past ~1k rows.
- Inline catalog editing — currently YAML-on-disk + auto-load.
- Frontend test framework — codebase has none today; v1 QA is
  `npm run build` + lab-smoke.
- Style alignment between Catalog page header and Investigations page
  header (the latter uses a gradient bar + bordered structure; Catalog
  currently uses plain `p-6 space-y-4`). Pre-merge polish or follow-up.
