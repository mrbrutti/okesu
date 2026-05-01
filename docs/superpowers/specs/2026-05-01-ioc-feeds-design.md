# IOC Feeds — Design Spec

**Date:** 2026-05-01
**Status:** Draft (pending user review of this document)

## Goal

Let operators install and manage threat-intel feeds (public YARA rule packs, Sigma rule packs, hash/IP/domain/CVE feeds) from the CP, so the catalog is populated by something more scalable than hand-curated YAML files. Close the existing Sigma gap so daimons can act on Sigma rules the way `binary-analyzer` already acts on YARA.

## Background

Today the IOC catalog is fed exclusively by YAML files under `catalog/iocs/`, loaded at CP boot and on `SIGHUP` (`controlplane/ioc/catalog/catalog.go`). The catalog already understands `yara_rule` and `sigma_rule` kinds alongside hashes/IPs/domains/CVEs/MITRE. Daimons consume the catalog through CP-side endpoints — `binary-analyzer.md` curls `/api/catalog/yara-rules.yar` and pipes it to the `yara` CLI; other daimons query `/api/iocs?kind=…`. The agent prompts are explicit that the catalog is the single source of truth and daimons must not reach upstream feeds at scan time.

This means: if we land a "feed" abstraction whose entries upsert into the existing `iocs` table, every existing detection path lights up automatically. No daimon code changes; the wiring is purely on the ingestion side.

Sigma is a separate gap. Sigma rules are stored but no daimon consumes them, and there is no `/api/catalog/sigma-rules.yml` bundle endpoint. Shipping feeds that pull thousands of Sigma rules without anything that uses them would be misleading. This spec closes that gap inside the same project.

## Use cases (priority order)

1. **One-click install of a well-known feed.** Operator picks "abuse.ch ThreatFox" or "SigmaHQ/sigma" from a curated registry and the CP starts pulling and refreshing it on a schedule.
2. **Add a custom feed by URL.** Operator pastes an HTTPS URL or a git repo and picks a parser, with optional auth.
3. **Browse what each installed feed contributes** to the IOC catalog, with last-refresh status and per-feed counts.
4. **Sigma rules become detectable on hosts**, via a daimon that mirrors the YARA/`binary-analyzer` flow.

## Non-goals (v1)

- Archive (zip/tarball) feed kind.
- Provider adapters (AlienVault OTX, MISP, VirusTotal collections).
- Zircolite-based strict-Sigma daimon (deferred in favor of an LLM-translates-and-greps daimon).
- Parent CP pushing fetched bytes to children (each CP refreshes from upstream itself in v1).
- Per-feed branch/tag pinning for git feeds (`HEAD` of default branch only).
- Webhook-driven refresh (interval-only in v1).
- Per-rule disable/exclude UI (rule-level mute is a follow-up).
- Row-level license compliance tracking (registry shows feed licenses; rule-level provenance not enforced).

These are tracked as GitHub issues filed at implementation time:
- [#101](https://github.com/mrbrutti/okesu/issues/101) archive (zip/tarball) feed kind
- [#102](https://github.com/mrbrutti/okesu/issues/102) provider adapters (OTX/MISP/VT)
- [#103](https://github.com/mrbrutti/okesu/issues/103) zircolite-based strict Sigma daimon
- [#104](https://github.com/mrbrutti/okesu/issues/104) parent CP pushes fetched bytes to children
- [#105](https://github.com/mrbrutti/okesu/issues/105) per-feed branch/tag pinning for git feeds
- [#106](https://github.com/mrbrutti/okesu/issues/106) webhook-driven refresh
- [#107](https://github.com/mrbrutti/okesu/issues/107) per-rule mute UI
- [#108](https://github.com/mrbrutti/okesu/issues/108) row-level license tracking
- [#109](https://github.com/mrbrutti/okesu/issues/109) custom-feed add form in Settings → Feeds
- [#110](https://github.com/mrbrutti/okesu/issues/110) orphaned-rule label rendering in activity views
- [#111](https://github.com/mrbrutti/okesu/issues/111) override-locally action on federated feeds

## Architecture

### New abstraction

A `feed` is a named, refreshable source of catalog entries. Feeds live in their own table; their fetched entries upsert into the existing `iocs` table tagged `source=feed:<slug>`.

### Source kinds (v1)

- **`single_file`** — HTTPS URL to one file (`.yar`, `.yml`, JSON, CSV). Covers YARA Forge bundle, abuse.ch CSVs, CISA KEV JSON.
- **`git`** — Git repo URL with optional subpath. Covers SigmaHQ/sigma, Neo23x0/signature-base, Elastic protections-artifacts. Refresh = `git pull`.

Archive (`zip`/`tar.gz`) and provider adapters (OTX/MISP/VT) are out of scope; tracked as backlog issues.

### Process model

A single in-CP `feedScheduler` goroutine ticks every 60s. It scans `ioc_feeds` and dispatches a refresh job for each enabled feed where `now() - last_refresh_at >= refresh_interval_seconds`. Refreshes run sequentially (no per-feed parallelism in v1). Manual "Refresh now" enqueues with priority. Worker writes `last_refresh_*` fields and reconciles `iocs` rows.

### Reconcile semantics

Each successful refresh is the new source of truth for that feed. Rows the feed used to produce but no longer does are deleted. Observations of removed rules survive (their `ioc_id` is NULLed and an `orphaned_rule_label` is written so the UI can render "rule no longer in catalog (was: …)"). Failed refreshes leave existing rows intact and write the error.

This solves the additive-only limitation called out in `catalog/iocs/README.md:25-31` for feed-sourced rows. YAML-curated rows (where `feed_id IS NULL`) keep their existing additive-only semantics.

## Data model

### New table `ioc_feeds`

| Column | Type | Notes |
|---|---|---|
| `id` | `INTEGER PRIMARY KEY` | |
| `slug` | `TEXT UNIQUE NOT NULL` | also used as the value of `iocs.source` (`feed:<slug>`) |
| `name` | `TEXT NOT NULL` | display name |
| `kind` | `TEXT NOT NULL` | `single_file` or `git` |
| `url` | `TEXT NOT NULL` | HTTPS file URL or git URL |
| `subpath` | `TEXT` | for git, optional dir to walk (e.g. `rules/`) |
| `parser` | `TEXT NOT NULL` | closed enum: `yara`, `sigma`, `urlhaus_csv`, `threatfox_csv`, `cisa_kev_json` |
| `auth_credential_id` | `INTEGER` | FK into existing `credentials` table |
| `refresh_interval_seconds` | `INTEGER NOT NULL DEFAULT 86400` | covers custom feeds where the operator doesn't specify; registry seeds set their own per-feed values (see registry table) |
| `enabled` | `INTEGER NOT NULL` | bool |
| `installed_from_registry` | `INTEGER NOT NULL` | bool — vs custom |
| `last_refresh_at` | `TEXT` | ISO 8601 |
| `last_refresh_status` | `TEXT` | `ok` or `error` |
| `last_refresh_error` | `TEXT` | last failure reason (truncated to 1 KB) |
| `last_refresh_entry_count` | `INTEGER` | rows produced by the most recent successful pull |
| `created_at` | `TEXT NOT NULL` | |
| `updated_at` | `TEXT NOT NULL` | |

### Changes to existing tables

- `iocs`: add nullable `feed_id INTEGER REFERENCES ioc_feeds(id) ON DELETE SET NULL`. Existing YAML rows leave it `NULL` and continue to work.
- `iocs.source`: extends to include `feed:<slug>` values alongside `catalog` and `observed`.
- `ioc_observations`: add nullable `orphaned_rule_label TEXT` so observations can outlive their rule rows. The existing `ioc_observations.ioc_id` FK (if present) is set to `ON DELETE SET NULL` so the reconcile flow can `UPDATE ... SET orphaned_rule_label` then `DELETE` the iocs row in either order.
- `cp_meta` (or equivalent existing single-row config table): add `feeds_consent_granted_at TEXT NULL`. Set when the operator clicks "Allow" in the consent banner. The scheduler refuses to run any refresh while this is `NULL`.

One new migration adds all four columns + the new table. No data migration required.

## Backend

### Package layout

```
controlplane/ioc/feeds/
  feeds.go            // FeedConfig type, table CRUD via *db.Store
  scheduler.go        // 60s tick, dispatches refreshes
  fetcher.go          // single_file + git fetch; auth handling
  reconcile.go        // diff + upsert + delete-orphaned-rows
  parser/
    yara.go           // splits multi-rule .yar into individual rule entries
    sigma.go          // walks one or many .yml docs, splits multi-doc
    urlhaus_csv.go
    threatfox_csv.go
    cisa_kev_json.go
  registry.go         // built-in well-known feed defs
```

Parsers turn raw bytes (or a walked dir) into `[]catalog.CatalogEntry` — the same type the YAML loader already uses. Reusing this type means feeds and the YAML catalog hand the same row shape to the same `db.UpsertIOC` path; the only field feeds carry extra is `feed_id`.

YARA/Sigma parsers reuse `catalog.ParseYARAHeader` / `catalog.ParseSigmaHeader` for name/tags/severity extraction so feed-derived rules show up in the UI with the same metadata fidelity as YAML-curated ones.

### Fetcher

- `single_file`: `http.Get` with optional `Authorization` header from `auth_credential_id`. Body bytes → parser.
- `git`: shells out to `git` (assumed in PATH). Clones or `pull`s into `<state>/feeds/<slug>/`. Walks `subpath` (default repo root). Each `.yar`/`.yml` file → parser per `parser` enum. Working trees persist across refreshes; a corrupted working tree triggers a fresh clone.

### Reconcile algorithm

Per refresh, in a single transaction:

```
new_entries = parser(fetched_bytes)
existing = SELECT id, normalized_value, kind FROM iocs WHERE feed_id = ?
for each new entry:
    UPSERT iocs (... feed_id = ?)
deleted = existing - new_entries (key: (kind, normalized_value))
for each deleted row:
    UPDATE ioc_observations
       SET ioc_id = NULL,
           orphaned_rule_label = '<feed-slug>:<name>'
       WHERE ioc_id = ?
    DELETE FROM iocs WHERE id = ?
```

Uninstall runs reconcile-to-empty: same logic with `new_entries = []`, then deletes the `ioc_feeds` row.

### HTTP endpoints

All admin-only via the existing role middleware unless noted.

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/feeds` | List installed feeds + status. Viewer-readable. |
| `GET` | `/api/feeds/registry` | List well-known feeds (curated registry). |
| `POST` | `/api/feeds` | Install a feed (from registry by slug, or custom). Admin. |
| `PATCH` | `/api/feeds/{id}` | Update interval / enabled / auth. Admin. |
| `DELETE` | `/api/feeds/{id}` | Uninstall + reconcile-to-empty. Admin. |
| `POST` | `/api/feeds/{id}/refresh` | Refresh now. Admin. |
| `POST` | `/api/feeds/validate` | Dry-run fetch+parse for a custom feed spec; returns count without committing. Admin. |
| `GET` | `/api/catalog/sigma-rules.yml` | New bundle endpoint, mirrors `/api/catalog/yara-rules.yar` shape. Viewer. Optional `?tag=`. |

### Federation

Parent CP's `/api/feeds` view mirrors to child CPs via the existing `fleet_env`/LLM-keys pattern. On a child CP, feed rows surface with `source=federated_from_parent`; "Override locally" creates an independent local copy. Refreshes still run on the child — the parent doesn't push fetched bytes, just config. This keeps each CP self-contained for actual rule execution. Pushing fetched bytes is a backlog issue.

## Frontend

### Settings → Feeds

`web/src/pages/settings/Feeds.tsx`, registered in `Settings.tsx` `NAV` between `Integrations` and `Audit log`, admin-only, `Rss` icon from `lucide-react`.

Layout, top to bottom:

1. **First-boot consent banner** — shown only when no feed has refreshed and the consent flag is unset. "Threat-intel feeds pull from public sources on the internet. Click 'Allow' once to enable scheduled refreshes; airgapped deployments should leave this disabled." Yes/No. "Yes" sets `feeds_consent_granted_at`. "No" leaves it off and disables the scheduler.
2. **Installed feeds table** — name + slug, kind chip, parser chip, enabled toggle, interval, last refresh (relative time + status icon), entry count, action menu (`Refresh now`, `Edit`, `Uninstall`). Federated rows are grayed with a `federated_from_parent` chip and an `Override locally` action.
3. **Browse well-known feeds** (collapsible, expanded by default when nothing-installed). Cards: name, description, kind/parser, license, "Install" button. Already-installed entries show "Installed" + a link to the table row.
4. **Add custom feed** form (collapsed by default): slug, name, kind dropdown, URL, optional subpath, parser dropdown, optional credential picker, interval. "Validate" calls `/api/feeds/validate` and shows a count without committing; "Install" commits.

### Catalog → Feeds tab

The existing `/catalog` page becomes tabbed: `Indicators` | `Feeds`. The `Feeds` tab is read-only and viewer-accessible.

- Summary table of installed feeds: name, parser, last refresh, entry count, link to filtered IOC list.
- Each row's name links to a per-feed detail panel: description, source URL, last refresh log, count breakdown by kind.
- Entry-count cell links to `/catalog?source=feed:<slug>`. The existing `source` filter is extended to render `feed:<slug>` chips alongside `catalog`/`observed`.

### Other UI changes

- `web/src/api.ts` gains: `feeds()`, `feedsRegistry()`, `feedInstall(spec)`, `feedUpdate(id, patch)`, `feedUninstall(id)`, `feedRefresh(id)`, `feedValidate(spec)`. Types mirror Go `FeedConfig`.
- `CatalogDetail.tsx` Overview tab: feed-sourced rows show a "Source feed" pill linking back to `/settings/feeds#<slug>`. Orphaned rows render "rule no longer in catalog (was: …)".
- Sidebar: no change — Catalog already has a sidebar entry; the new tab lives inside that page.

## Sigma daimon (`log-sigma-hunter`)

A new agent definition at `agents/log-sigma-hunter.md`, modeled on the YARA section of `agents/binary-analyzer.md`.

**Inputs** (set by the orchestrator step that invokes it):

- `OKESU_CP_URL`, `OKESU_CP_COOKIE` — to fetch the bundle.
- Optional `tag` — narrows scope (e.g. `?tag=lateral_movement`).
- Optional `host_window` — time range; defaults to last hour.
- Optional `log_sources` — explicit list overriding defaults (`journalctl`, `/var/log/auth.log`, `/var/log/syslog`, `/var/log/audit/audit.log`).

**Procedure:**

1. Fetch the bundle:
   ```bash
   curl -sf "$OKESU_CP_URL/api/catalog/sigma-rules.yml${tag:+?tag=$tag}" \
       -H "Cookie: $OKESU_CP_COOKIE" \
       -o /tmp/okesu-sigma.yml
   ```
   Bundle is multi-document YAML (one rule per `---` document). Each document carries catalog metadata in comments above it (`# catalog name:`, `# tags:`, `# severity_floor:`) — same comment-header pattern the YARA endpoint uses.
2. Collect evidence within the window:
   ```bash
   journalctl --since "${host_window:-1 hour ago}" --no-pager > /tmp/sigma-evidence-journal.log
   sudo tail -n 50000 /var/log/auth.log /var/log/syslog /var/log/audit/audit.log 2>/dev/null > /tmp/sigma-evidence-files.log || true
   ```
   Permissions failures are logged as evidence gaps; the daimon continues — same posture as binary-analyzer's "if `yara` is missing, log gap and continue."
3. For each rule, the LLM:
   - Reads the rule's `detection.selection` block.
   - Translates intent into a concrete `ripgrep` invocation against the evidence files. Translation cases the prompt covers explicitly: Windows `EventID: 4624` → Linux `pam_unix(...): session opened` (or skip with documented reason); literal selections → `rg -F`; regex selections → `rg -e`; `Image|endswith` → suffix-anchored pattern; `condition: 1 of selection_*` → run each, OR results.
   - Runs the query, caps output at 200 lines per rule.
   - Records: rule name, translated query, match count, 5 example lines.
4. Emit findings for matches with non-zero count: one `ioc_observation` per matched rule, attributes `host`, `rule_slug`, `translated_query`, `example_count`, `time_window`. Resolves `ioc_id` via `GET /api/iocs?kind=sigma_rule&q=<rule-name>`.
5. Honesty section in the report — explicit "Translation caveats" block listing rules skipped (Windows-only with no Linux analog, fields not present in evidence) plus a "These translations are approximate, not authoritative SIEM evaluation — recheck high-severity hits manually" note.

**What this is NOT:** a SIEM. It is a triage daimon. The honesty section is intentional and user-visible; operators reading the report should know they're seeing LLM-translated grep output, not Sigma backend execution.

**Orchestration plumbing:**

- New action class entry in `agents/_orchestration-actions.md` for `log_sigma_hunt`.
- `log-investigator.md` gets a one-line cross-reference to `log-sigma-hunter` for Sigma-rule-based hunting.
- Invoked the same way other daimons are: orchestration step → CP dispatches → result is a finding bundle.

## Built-in registry

`controlplane/ioc/feeds/registry.go` is a compiled-in slice. Editing requires a code change on purpose, mirroring `validKinds` in `catalog.go`. The implementation step verifies each URL by hand against the project's current canonical URL before merging.

| Slug | Name | Kind | Parser | Default-installed | License | Interval |
|---|---|---|---|---|---|---|
| `cisa-kev` | CISA Known Exploited Vulnerabilities | `single_file` | `cisa_kev_json` | yes | U.S. Govt — public domain | daily |
| `abusech-threatfox` | abuse.ch ThreatFox | `single_file` | `threatfox_csv` | yes | CC0 1.0 | daily |
| `yara-forge-core` | YARA Forge — Core bundle | `single_file` | `yara` | yes | mixed (per-rule) | daily |
| `abusech-urlhaus` | abuse.ch URLhaus | `single_file` | `urlhaus_csv` | no | CC0 1.0 | daily |
| `sigmahq-sigma` | SigmaHQ — Sigma | `git` | `sigma` | no | DRL 1.1 | weekly |
| `neo23x0-signature-base` | Neo23x0/signature-base | `git` | `yara` | no | DRL 1.1 | weekly |
| `elastic-protections-artifacts` | Elastic Protections Artifacts | `git` | `yara` | no | Elastic 2.0 | weekly |

Default-installed rationale: small + high-signal feeds boot the system with useful detections without a 500MB git clone. Heavy ones (SigmaHQ, Neo23x0, Elastic) are one click away in the registry browse UI.

## First-boot seeding & consent

CP boot calls `feeds.SeedRegistryDefaults(store)` after the existing `loadIOCCatalogs` call in `controlplane/server.go`. This:

1. Inserts a registry row for every `DefaultInstalled: true` entry **with `enabled = false`** until the consent flag is granted. Idempotent: skips slugs already present.
2. After consent is granted in the UI, the CP flips `enabled = true` for those rows and the scheduler picks them up on its next tick.
3. Existing CPs upgrading: same seed pass adds the rows on first start of the new binary; consent banner appears; same flow.

Default-disabled-until-consent on existing CPs is deliberate: an upgrade should not silently start hitting `cisa.gov` and `abuse.ch` from a deployment whose operator has not seen the feature exist. A banner on first Settings → Feeds visit is clearer than retroactive consent.

## Refresh, auth, federation defaults

- **Refresh cadence**: configurable per feed. Custom feeds default to 24h (the column default). Registry-seeded feeds carry their own interval — single-file at 24h, git at 7d (see registry table). Per-feed "Refresh now" button. Failed refreshes preserve last-good rows.
- **Auth**: `single_file` supports an `Authorization` header from `auth_credential_id`. `git` supports PAT-in-URL or an SSH key path via the same credential store. Public feeds need none.
- **Federation**: parent feed configs mirror to children read-only, with override-locally semantics (matches `fleet_env`).

## Testing

| Layer | What we cover | Style |
|---|---|---|
| Parsers (`feeds/parser/*_test.go`) | YARA bundle splitting, multi-doc Sigma walking, ThreatFox/URLhaus/CISA-KEV CSV/JSON shapes | Table-driven, fixtures in `testdata/` |
| Reconcile (`feeds/reconcile_test.go`) | First-refresh insert; second-refresh add+remove with observation preservation; uninstall = reconcile-to-empty | Real `*db.Store`, tmp SQLite, mirrors `iocs_test.go` |
| Fetcher (`feeds/fetcher_test.go`) | `single_file` via `httptest.Server` (with and without auth); `git` build-tagged so it skips when `git` is missing; both error paths assert no row mutation | `httptest` + tmp dir |
| Scheduler (`feeds/scheduler_test.go`) | Honors interval; runs only enabled feeds; "Refresh now" priority; last-good preserved on parser error | Stub fetcher, fast-forwarded clock |
| API (`controlplane/api/feeds_test.go`) | Auth roles (admin-mutate, viewer-read); shape of `/api/feeds*` responses; federation mirror behavior on a child CP | Existing API test harness |
| `/api/catalog/sigma-rules.yml` | Mirrors `yara_catalog_test.go`: returns concatenated multi-doc YAML with comment headers; `?tag=` filter works | Same harness |
| Frontend | `Settings/Feeds.tsx` install-from-registry happy path; custom-add validation; uninstall confirmation; Catalog `Feeds` tab read-only | Existing Vitest setup |
| Sigma daimon | Manual integration test with a fixture rule + fixture log; documented in `log-sigma-hunter.md` "How to test" section. Daimon judgment is not unit-testable. | Manual |

## Rollout

Each step independently committable / reversible.

1. Migration: `ioc_feeds` table + `iocs.feed_id` + `ioc_observations.orphaned_rule_label`. No behavior change.
2. `controlplane/ioc/feeds/` package: types, parsers, fetcher, reconcile, scheduler. Tests pass against a stub registry. No HTTP exposure yet.
3. HTTP endpoints (`/api/feeds*`) + admin/viewer auth gating. No frontend yet — exercised by tests.
4. `/api/catalog/sigma-rules.yml` bundle endpoint.
5. Built-in registry + first-boot seed pass + consent flag plumbing.
6. Settings → Feeds frontend page; Catalog → Feeds tab; `source=feed:<slug>` filter on the existing Catalog list.
7. Federation mirror plumbing on top of the existing `fleet_env` pattern.
8. `log-sigma-hunter.md` daimon definition + `_orchestration-actions.md` entry + cross-link from `log-investigator.md`.
9. Backlog issues opened for archive feeds, provider adapters, zircolite Sigma daimon, parent-pushes-bytes federation, branch/tag pinning, webhook refresh, per-rule mute UI, license-compliance row tracking.

## Risks

1. **Wrong default URLs in the built-in registry** would be embarrassing on first-boot consent. Mitigation: implementation step verifies each URL by hand and lands a `validateRegistry()` smoke test that fetches each URL once during CI.
2. **Sigma daimon translation quality** depends on LLM judgment. The honesty section in the daimon's report is the user-visible guardrail. Mitigation: `log-sigma-hunter.md` says plainly in its "What this is NOT" section that this is triage, not authoritative SIEM evaluation.
3. **Disk budget for git feeds** — SigmaHQ alone is ~100MB on disk. Mitigation: documented in the registry entry; the daemon never auto-installs large feeds, only the operator can.

## References

- `controlplane/ioc/catalog/catalog.go` — current YAML catalog loader; `CatalogEntry`, `ParseYARAHeader`, `ParseSigmaHeader` reused by feeds.
- `controlplane/api/yara_catalog.go` — `/api/catalog/yara-rules.yar` endpoint that the new sigma-rules.yml endpoint mirrors.
- `agents/binary-analyzer.md` — daimon pattern that `log-sigma-hunter.md` mirrors for Sigma.
- `catalog/iocs/README.md` — additive-only limitation that feed reconcile semantics resolve for feed-sourced rows.
- `controlplane/server.go` (`loadIOCCatalogs`, SIGHUP reloader) — boot path the seed pass attaches to.
- `docs/superpowers/specs/2026-04-29-catalog-ui-design.md` — existing Catalog UI spec; the Feeds tab extends that surface.
- `docs/superpowers/specs/2026-04-30-fleet-llm-keys-design.md` — federated-config pattern the Feeds federation mirror reuses.
