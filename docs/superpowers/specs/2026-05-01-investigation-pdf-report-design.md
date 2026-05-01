# Investigation PDF report

## Goal

A server-rendered PDF report for an investigation, suitable for
incident-report writeups and analyst handoffs. Custom-shaped
document (NOT a 1:1 of the screen Overview): page 1 executive
summary, pages 2..N chronological narrative, pages N+1..end
reference tables.

Follow-up #6 from
`docs/superpowers/specs/2026-05-01-investigation-overview-design.md`,
parked in the `investigation_overview_followups.md` memory.

## Architecture

One new endpoint, one new package, one new UI button. No new
schema, no new migration. Pure rendering on the read path.

1. **`GET /api/investigations/{id}/report.pdf`** — sync handler in
   `controlplane/api/investigation_report.go`. Returns
   `application/pdf` with `Content-Disposition: attachment;
   filename="case-{id}-{slug}.pdf"`. Loads the same
   `InvestigationDetail` bundle the page uses + the audit log via
   `Store.ListInvestigationAudit`, hands it to the renderer, streams
   bytes back. Federation routes via the existing
   `?cp=<instance_id>` proxy convention used by the detail handler.

2. **`controlplane/api/investigation_report/`** — new package with
   the renderer (one section per file):
   - `render.go` — `Render(bundle *InvestigationBundle, audit []db.InvestigationAuditEvent, w io.Writer) error` top-level entrypoint.
   - `summary.go` — page 1 executive summary.
   - `narrative.go` — pages 2..N chronological narrative.
   - `tables.go` — reference tables (findings, IOCs, runs, audit).
   - `frame.go` — page header / footer / page numbers.
   - `aggregate.go` — Go ports of the heavy-hitter aggregators
     already in `web/src/components/investigations/CaseStructure.tsx`
     (hosts/IOCs/daimons/orchestrations).

   Library: **`github.com/jung-kurt/gofpdf`**. Active fork, MIT
   license, pure-Go (works under `CGO_ENABLED=0`), no external font
   deps (uses built-in Helvetica + Courier).

3. **Frontend button** — small `Export report` button in
   `web/src/pages/InvestigationDetail.tsx` header, next to the
   existing close-dialog button. Click → `<a href="/api/investigations/{id}/report.pdf?cp=…" download>`
   triggers download. No new types in `web/src/api.ts` (binary
   download, not a JSON fetch).

**Audit log.** Every successful render writes one `audit_log` entry
via the existing `Store.InsertAudit`:
```
action  = "investigation.report_exported"
target  = "investigation:{id}"
result  = "ok"
metadata = {"format": "pdf", "size_bytes": N, "filename": "…"}
```
Read-then-export is a real exfil risk surface; the audit trail is
cheap insurance. Audit-log write failure does NOT fail the export
(log warning, return the PDF — the document is more valuable than
the row).

**Authorization.** Same as `GET /api/investigations/{id}` — anyone
who can view the case can export. RBAC follows the existing
investigation visibility rules from the trilogy
(PRs #79/#81/#84/#85).

## Document layout

A4 portrait, 20mm margins, Helvetica + Courier (built-in gofpdf
fonts).

### Page 1 — Executive summary

- Header strip: "Okesu — Investigation Report" + generation
  timestamp.
- Title block: `Case #{ID}` + truncated investigation title.
- Status row (4 cells): status pill, max severity in linked
  findings, "Created Xd ago", owner email.
- Severity histogram: filled-rectangle proportional segments per
  severity (CRIT/HIGH/MED/LOW/INFO) with counts spelled out.
- Linked-entity counts line: `12 findings · 5 IOCs · 3 daimons · 4
  runs · 7 notes`.
- Summary text (`inv.Summary`) verbatim, wrapped to page width.
- Heavy-hitter lines (one each):
  - "Hosts touched: edr-fedora-3 (8), edr-deb-1 (2), …"
  - "Top IOCs: sha256:…aaaa (23 obs / 4 hosts), …"
  - "Top daimons: edr-agent (9 findings, last 4m ago)"
  - "Run summary: triage-then-quarantine (3 runs: 2 ✓ 1 ✗)"

Aggregations come from `aggregate.go`, a Go port of the same
client-side logic in `CaseStructure.tsx`.

### Pages 2 to N — Chronological narrative

A single time-ordered stream merging every dated signal:

- Lifecycle: case created (`⊕`), case closed (`⊗`).
- Findings linked: bullet `•` + `Finding #{id} linked ({severity})`
  + indented title + category line.
- Runs: `▭` + `Run #{id} started ({orchestration})` + a paired
  `Run #{id} completed/failed/cancelled` event.
- Notes: `✎` + author + indented full body (no truncation).
- Audit (other): `↗` + audit-kind + actor + title.

Format per entry:

```
2026-04-28 14:01:33  •  Finding #142 linked (HIGH)
                         "Suspicious cron job on edr-fedora-3"
                         category: process
```

Page breaks are entry-aligned (no half-entry across pages —
`gofpdf.SetAutoPageBreak` plus per-entry height pre-calc).

Source ordering: merge `bundle.findings` (by `LinkedAt`),
`bundle.runs` (by `StartedAt`/`EndedAt`), `bundle.notes` (by
`CreatedAt`), and `audit` events (by `ts`). Lifecycle markers
(`⊕`/`⊗`) come from audit rows of kind `created`/`closed`.

### Pages N+1 to end — Reference tables

Four tables, each starting on a fresh page; per-table header
repeats on each new page so a 5-page audit log stays readable.

| Table | Columns |
|---|---|
| **Findings** | ID · Severity · Title · Host · Agent · Status · Linked-at |
| **IOCs** | Kind · Value (truncated 24c) · Obs count · Host count · First seen · Last seen |
| **Runs** | ID · Orchestration · Status · Started · Duration · Trigger |
| **Audit** | Timestamp · Actor · Kind · Title |

Sort: findings by severity desc then `Ts` desc; IOCs by
`ObservationCount` desc; runs by `StartedAt` desc; audit by `ts`
desc.

**Caps.** Each table caps at 200 rows for v1; if the case has more,
the footer line says `200 of N <kind> shown — see {tab} tab in UI
for full list.` (operators rarely page past 200 in a static
report; full data is in the live UI).

### Frame (every page)

- **Header** (top 8mm): `Case #{ID} — {section}` (left, 8pt grey)
  + `Page X / N` (right, 8pt grey).
- **Footer** (bottom 8mm): `Generated YYYY-MM-DD HH:MM:SS UTC by
  Okesu CP — Investigation #{ID}` (centered, 8pt grey).
- Body area: 274mm tall × 170mm wide (A4 minus margins minus
  header/footer).

### Filename

`case-{id}-{slug}.pdf` where `slug = lowercase(inv.Title)` with
non-alphanumerics → `-`, capped at 40 chars.

Example: `case-42-suspicious-cron-jobs-cluster.pdf`.

## Federation

When `?cp=<instance_id>` is set, the parent CP proxies the request
to the owning child via the existing federation aggregator pattern
already used by `GET /api/investigations/{id}`. Child renders
locally and streams bytes back through the parent. Same shape as
today's federated investigation detail fetch — no new federation
code.

## Error handling

- **404** when the investigation doesn't exist or the caller lacks
  view permission.
- **403** when RBAC denies (matches existing investigation detail
  endpoint).
- **502** when `?cp=` proxy fails — child unreachable; body:
  `case lives on cp=X, currently unreachable`.
- **500** when render itself errors (malformed bundle, gofpdf
  panic). Body: `text/plain` with the wrapped error. Optional
  `--debug-render` CP flag (off by default) buffers a partial PDF
  to `/tmp/okesu-report-debug-{id}-{ts}.pdf` for post-mortem.
- **Audit-log write failure** → log warning, return the PDF (do
  not fail the export).

## Out of scope (explicit cuts)

- Custom fonts / corporate branding / Unicode beyond Latin-1.
  Built-in Helvetica/Courier covers ASCII/Latin-1; non-Latin
  characters in titles render as boxes — operators copy from the
  live UI for those.
- HTML / DOCX / Markdown export.
- Per-section toggles ("export without notes"). All three sections
  are always included.
- Async job queue / email-the-PDF. Sync only; lab cases generate
  in well under 2s.
- Batch export ("all closed cases this month"). Per-case only.
- Custom logo / cover page. Plain text header.
- Embedding screenshots of the screen Overview. Document is
  text-shaped throughout (per "no dossier that looks like a
  screen").
- Per-CP / per-tenant report templating. One layout, no theming.
- Streaming / chunked render. The whole PDF buffers in memory
  before the handler writes it (gofpdf's API requires the full
  document before flush). 200-row caps keep memory under 5MB
  even on the largest expected cases.

## Testing

### Server-side (Go)

`controlplane/api/investigation_report/render_test.go`:
- Empty bundle (case created, nothing linked) → renders, starts
  with `%PDF-`, > 1 KiB.
- Single-finding case → narrative contains the finding's id +
  title.
- 50-finding case → finding table renders all 50 rows + paginates.
- 250-finding case → finding table caps at 200 with footer line
  "200 of 250 findings shown".
- Closed case (status=closed, ClosedAt non-zero) → page 1 shows
  `closed`; narrative ends with `⊗ Investigation closed`
  lifecycle entry.
- Federated case (`bundle.investigation.cp_source` set) → no
  effect on rendering (federation is purely transport-layer; the
  child renders the same way as the parent would).
- Filename slug helper test:
  `slug("Suspicious Cron Jobs Cluster") == "suspicious-cron-jobs-cluster"`,
  `slug("Title with — em-dashes & ampersands!") == "title-with-em-dashes-ampersands"`,
  long titles cap at 40 chars.

`controlplane/api/investigation_report_handler_test.go`:
- 200 OK with `Content-Type: application/pdf` and correct
  `Content-Disposition` filename on a happy-path call.
- 404 on unknown investigation id.
- 403 when caller doesn't have view permission (use existing RBAC
  test helpers).
- Audit log row written with
  `action=investigation.report_exported` and
  `target=investigation:{id}` after a successful export.
- Audit log write failure (force via store mock) does NOT fail
  the export — handler still returns 200 with the PDF.

### Frontend

No new vitest tests. The export button is one `<a href="…" download>`
element — smoke verify post-merge.

### Manual lab smoke (post-merge)

- Open an active investigation with ≥ 5 findings, ≥ 1 run,
  ≥ 1 note. Click "Export report". PDF downloads.
- Open in macOS Preview / Adobe Reader → verify all three sections
  render and pages number correctly.
- Truncate `inv.Summary` to a 5KB block of text → renders without
  spilling outside the body area.
- Federated case (cp_source set, parent CP) → export downloads from
  parent; the request proxies to the child correctly.

## Files (planned)

### New

- `controlplane/api/investigation_report/render.go` — top-level
  entrypoint.
- `controlplane/api/investigation_report/summary.go` — page 1.
- `controlplane/api/investigation_report/narrative.go` — pages 2..N.
- `controlplane/api/investigation_report/tables.go` — reference
  tables.
- `controlplane/api/investigation_report/frame.go` — header/footer.
- `controlplane/api/investigation_report/aggregate.go` — Go ports
  of CaseStructure aggregators.
- `controlplane/api/investigation_report/render_test.go`
- `controlplane/api/investigation_report.go` — HTTP handler.
- `controlplane/api/investigation_report_handler_test.go`

### Modified

- `controlplane/server.go` — mount the new route inside the
  cookie-auth admin group alongside the existing
  `/api/investigations/...` endpoints. Federation proxy uses the
  existing aggregator pattern.
- `web/src/pages/InvestigationDetail.tsx` — add the "Export report"
  button to the page header next to the close-dialog button.
- `go.mod` / `go.sum` — add `github.com/jung-kurt/gofpdf` as a
  direct dependency.
