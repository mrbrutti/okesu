## Summary

Follow-up #6 from the Investigation Overview backlog. Adds a server-rendered PDF report for an investigation case, suitable for incident-report writeups and analyst handoffs.

- New endpoint `GET /api/investigations/{id}/report.pdf` returning a custom-layout PDF: page 1 executive summary (status, severity histogram, linked-entity counts, summary text, heavy-hitter lines), pages 2..N chronological narrative (lifecycle / findings / runs / notes / audit-other, time-ordered), pages N+1..end reference tables (findings / IOCs / runs / audit, capped at 200 rows each).
- Server-side renderer at `controlplane/api/investigation_report/` using `github.com/jung-kurt/gofpdf` (pure-Go, MIT, no Cgo, built-in Helvetica/Courier fonts). Six small files, one per section.
- Federation: same `?cp=<instance_id>` proxy convention as the existing detail endpoint. Federated cases render on the owning child CP; bytes stream back through the parent.
- Audit-logged: every successful export writes one `audit_log` row with action `investigation.report_exported` (read-then-export is a real exfil-risk surface).
- Frontend: one `Export report` button (lucide `Download` icon) in the investigation page header. Click → browser downloads the PDF.

The report is text-shaped throughout — no screen reproduction, no embedded SVG, no charts. Per "no dossier that looks like a screen."

## Test plan

Unit tests in this PR:
- `controlplane/api/investigation_report/aggregate_test.go` — slug helper edge cases, host aggregation, run-by-orch aggregation, severity counts (4 tests).
- `controlplane/api/investigation_report/frame_test.go` — document setup smoke + header/footer drawing without panic (3 tests).
- `controlplane/api/investigation_report/render_test.go` — end-to-end render against synthetic bundles (empty / 5-finding / 250-finding / closed-with-resolution); asserts `%PDF-` prefix + minimum sizes (4 tests).
- `controlplane/api/investigation_report_test.go` — handler-level: 200 with `application/pdf` + correct `Content-Disposition` filename, 404 on unknown id, audit-log row written after export (3 tests).

Total: 14 new Go tests + full vitest suite (92) + full Go suite all green.

Manual lab smoke (post-merge):
- [ ] Open an active investigation with ≥ 5 findings, ≥ 1 run, ≥ 1 note. Click "Export report" → PDF downloads.
- [ ] Open in Preview/Reader → all three sections present, page numbers correct.
- [ ] Open a closed case → narrative ends with `Investigation closed` lifecycle entry.
- [ ] Federated case (`cp_source` set) → export proxies to child correctly.
- [ ] Verify an `audit_log` row exists with `action=investigation.report_exported, target=investigation:{id}` after each export.

## Files

**New (server):**
- `controlplane/api/investigation_report/{aggregate,bundle,frame,summary,narrative,tables,render}.go` (+ tests for aggregate, frame, render).
- `controlplane/api/investigation_report.go` — HTTP handler + federated wrappers.
- `controlplane/api/investigation_report_test.go`

**Modified:**
- `controlplane/server.go` — mount the new routes (cookie-auth admin + federation-token).
- `web/src/pages/InvestigationDetail.tsx` — `Export report` button.
- `docs/architecture.md` — new section.
- `go.mod` / `go.sum` — add `github.com/jung-kurt/gofpdf`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-investigation-pdf-report-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-investigation-pdf-report.md`

## Closes / refs

Implements item #6 from `investigation_overview_followups.md`. Five remaining backlog items unchanged.
