# Catalog UI — Design Spec

**Date:** 2026-04-29
**Status:** Approved (pending user review of this document)

## Goal

A web UI page that lets operators browse, filter, and inspect every IOC the system knows about — both catalog-curated rows and observation-derived rows — with drill-down into rule bodies, observation history, and typed relationships.

## Background

Phases 22.1 through 22.5 built out the `iocs` table and its surrounding machinery: catalog YAML loading, observation tracking, dedup/severity propagation, typed relationships, vendor enrichment, STIX 2.1 export, and YARA/Sigma rule kinds. Every IOC is reachable via the API today (`GET /api/iocs?kind=...`, `/api/iocs/{id}/relationships`, `/api/stix2/iocs`, `/api/catalog/yara-rules.yar`, `/api/iocs/cross-cp-patterns`), but the frontend has no consumer surface — the data is invisible to operators using the dashboard.

This spec covers a single-page UI surface for that data. The page is read-only in v1; catalog curation continues via YAML files on disk + SIGHUP.

## Use cases (priority order)

1. **Inspect any IOC the system knows about** (primary). "This sha256 showed up in three runs across two CPs — where did we see it, what's the catalog know, what's it related to?"
2. **Audit the curated catalog** (secondary). "What rules and IOCs do we ship today? Are they covering the right tags/families?" — same page, filtered to `source = catalog`.
3. **Browse rule bodies** (tertiary). "Show me the YARA rule the binary-analyzer matched against." — drill-down from a `yara_rule`/`sigma_rule` row to the detail page's Overview tab.

## Page surface

### List view at `/catalog`

A sortable table with these columns:

| Kind icon | Value (truncated) | Name | Tags | Severity | Source | Observed | First seen | Last seen |

Filters above the table:

- **Kind** — multi-select chips (all kinds default-on)
- **Source** — three-way: catalog / observed / both (default both)
- **Search** — `LIKE %q%` against value, name, tags

Default sort: `last_seen DESC`. Column-header click re-sorts in-memory (the API returns up to its cap and the table sorts client-side).

### Drawer (clicking a row)

A right-side drawer slides in:

- **Header**: kind icon + value (or `name` if value is unwieldy — e.g., a YARA rule body)
- **Identity**: kind, value (full), normalized_value, source, definition_path (catalog rows only)
- **Curated metadata**: name, tags, severity_floor, classification, attribution, confidence, notes
- **Observability**: observation_count, first_seen, last_seen
- **Quick links**: "Observations" and "Relationships (N)" — both navigate to the detail page on the corresponding tab. The N count comes from `GET /api/iocs/{id}/relationships`'s array length (no separate count endpoint in v1).
- **Footer**: "Open full page →" → `/catalog/:id`

### Detail page at `/catalog/:id`

Three tabs:

**1. Overview** — all drawer-body sections + for `yara_rule`/`sigma_rule` kinds, the rule body in a scrollable `<pre><code>` block. No syntax-highlighting library in v1 (monospace is enough).

**2. Observations** — table: `finding_id` (link to Findings), `orchestration_run_id` (link to Runs), `host`, `observed_at`. Sort: `observed_at DESC`.

**3. Relationships** — list view of typed edges (`subject → predicate → object`). Each IOC side linkable; direction icon indicates subject vs object side of the edge. No graph visualization in v1.

### Sidebar

Add `Catalog` leaf to the existing `Triage` group in `web/src/lib/sidebarNav.ts`, after `Live Events`. Icon from `lucide-react`: `Library`.

## Backend additions

Three changes, all in the cookie-auth viewer+ group alongside the existing IOC routes.

### `GET /api/iocs/{id}` (new)

Returns a single `IOCRecord` JSON. The DB layer's `GetIOC(id)` already exists (Phase 22.1) — just need the HTTP wrapper. Mirror `ListIOCRelationshipsHandler` pattern.

- 404 on `sql.ErrNoRows`, 500 on other errors, 400 on non-numeric id
- Route mounted before the `{id}/relationships` and `{id}/observations` sub-paths in code, but chi handles ordering correctly regardless

### `GET /api/iocs/{id}/observations` (new)

Returns `[]IOCObservation` JSON. The DB layer's `ListIOCObservations(iocID)` already exists; HTTP wrapper mirrors `ListIOCRelationshipsHandler`.

### Extend `GET /api/iocs` filter (modify existing)

Current filter: `kind`, `findingID`. Add:

- **`source=catalog|observed`** — exact match on the `source` column
- **`q=<search>`** — `LIKE %q%` against `value`, `name`, `tags` joined with OR; case-insensitive via `LOWER()` on both sides

Backward-compat: existing callers without these params keep working.

## Frontend file layout

### Create

- `web/src/pages/Catalog.tsx` — list page. Target the smaller `Investigations.tsx` shape (~300 lines), not the 1450-line `Findings.tsx`.
- `web/src/pages/CatalogDetail.tsx` — three-tab detail page.
- `web/src/components/CatalogDrawer.tsx` — drawer component.

### Modify

- `web/src/lib/sidebarNav.ts` — add `Catalog` leaf to the Triage group + import the icon.
- `web/src/api.ts` — extend `iocs(filter)` shape with `source` and `q`; add `ioc(id)`, `iocObservations(id)`. Confirm `iocRelationships(id)` already exists from Phase 22.4 (it should).
- `web/src/App.tsx` (or wherever routes register) — register `/catalog` and `/catalog/:id`.

## Out of scope (deferred)

- **Syntax highlighting** for rule bodies — `<pre>` is enough; revisit if operators ask. Library candidates: `shiki` (heavier, prettier) or `prism-react-renderer` (lighter).
- **Relationship graph visualization** — list first; d3/cytoscape later if it earns its keep.
- **Edit / CRUD on IOCs** — catalog is YAML-on-disk + auto-load.
- **Pagination** for `/api/iocs` — current 1000-row cap is fine until the catalog grows.
- **Bulk actions** (export selected, tag-batch, etc.)
- **Cross-CP-patterns integration** — that's the supervisor daimon's separate finding stream.

## Out of scope (cut, not deferred)

- **Frontend test infrastructure** — codebase has none today. v1 frontend QA is `bun run build` + lab-smoke. Adding Vitest/Playwright is a separate scope expansion.

## Testing

### Backend

Go tests appended to `controlplane/api/iocs_test.go` (or created if absent), mirroring Phase 22.4/22.5 patterns:

- `TestGetIOC_Found` — happy path, IOCRecord round-trip
- `TestGetIOC_NotFound` — 404 on missing id
- `TestGetIOC_BadID` — 400 on non-numeric id
- `TestListIOCObservations_HTTP` — happy path, observation row shape
- `TestListIOCs_FilterBySource` — `?source=catalog` returns only catalog rows
- `TestListIOCs_FilterByQuery` — `?q=foo` matches value, name, and tags

### Frontend

`bun run build` must pass. Lab-smoke validates the user-facing behavior: drop a yara_rule + a sha256 IOC into the catalog, hit `/catalog`, click into the drawer, navigate to `/catalog/:id`, verify all three tabs render.

## Risks

- **Wide table on small screens** — 9 columns force horizontal scroll on narrow layouts. Use Tailwind's `hidden md:table-cell` to drop low-priority columns (normalized_value, classification, definition_path) on smaller breakpoints. Existing pages handle this pattern.
- **Long rule bodies in `<pre>`** — a 200-line YARA rule shouldn't break layout. Wrap the rule body in a scrollable container with max-height (≈ 60vh).
- **Tag chip wrapping** — IOCs with many tags could blow up row height. Cap table tags at 3 with "+N more" overflow chip (rendered as a non-clickable indicator; click the row to see the full tag list in the drawer). Drawer and detail pages show all tags.

## Open follow-ups (post-v1)

- Syntax highlighting once we know which library fits the bundle-size budget
- Relationship graph viz when there are real edge-counts to look at
- Catalog audit log (who added/edited which YAML file when)
- Pagination + cursor-based scroll once the catalog crosses ~1k rows
