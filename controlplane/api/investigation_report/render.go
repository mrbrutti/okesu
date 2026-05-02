// controlplane/api/investigation_report/render.go
//
// Top-level entrypoint. Pre-renders to count pages (because the
// header needs Page X/N), then re-renders for real with the right
// page count.
package investigation_report

import (
	"io"

	"github.com/section9labs/okesu/controlplane/db"
)

// Render writes a complete PDF report to w. Returns any error from
// the underlying gofpdf engine.
func Render(b *Bundle, audit []db.AuditEvent, w io.Writer) error {
	// Two-pass to get page numbers right: first pass counts pages
	// without writing the header (which needs N), second pass
	// writes for real.
	pageCount := countPages(b, audit)

	pdf := NewDocument(b.Investigation.ID)
	WriteSummary(pdf, b, pageCount)
	narrPages := WriteNarrative(pdf, b, audit, 2, pageCount)
	WriteTables(pdf, b, audit, 2+narrPages, pageCount)
	return pdf.Output(w)
}

// countPages does a discardable render to learn the final page
// count; necessary because gofpdf doesn't let us update headers
// retroactively.
func countPages(b *Bundle, audit []db.AuditEvent) int {
	pdf := NewDocument(b.Investigation.ID)
	WriteSummary(pdf, b, 1) // bogus pageCount, header value irrelevant for counting
	WriteNarrative(pdf, b, audit, 2, 1)
	WriteTables(pdf, b, audit, 999, 1) // doesn't matter — we only read PageCount
	return pdf.PageCount()
}
