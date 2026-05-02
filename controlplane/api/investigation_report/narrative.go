// controlplane/api/investigation_report/narrative.go
//
// Pages 2..N — chronological narrative. Single time-ordered stream
// merging lifecycle / findings / runs / notes / audit-other events.
// Each entry: timestamp + 1-char glyph + 1-line summary + optional
// indented body. Page breaks are entry-aligned.
package investigation_report

import (
	"fmt"
	"sort"
	"time"

	"github.com/jung-kurt/gofpdf"

	"github.com/section9labs/okesu/controlplane/db"
)

type narrativeEntry struct {
	ts    time.Time
	glyph string
	head  string
	body  []string // optional indented lines (notes, finding details)
}

// WriteNarrative draws the chronological narrative. Returns the
// number of pages written (always >= 1) so the caller can compute
// running page numbers.
func WriteNarrative(pdf *gofpdf.Fpdf, b *Bundle, audit []db.AuditEvent, startPage, totalPages int) int {
	entries := buildEntries(b, audit)

	pdf.AddPage()
	pageNum := startPage
	WriteHeader(pdf, b.Investigation.ID, "Narrative", pageNum, totalPages)

	pdf.SetFont("Helvetica", "B", 14)
	pdf.SetXY(PageMarginMM, BodyTopMM)
	pdf.CellFormat(BodyWidthMM, 8, "Narrative", "", 1, "L", false, 0, "")
	pdf.Ln(2)

	for _, e := range entries {
		needed := entryHeight(pdf, e)
		if pdf.GetY()+needed > BodyBottomMM {
			WriteFooter(pdf, b.Investigation.ID)
			pdf.AddPage()
			pageNum++
			WriteHeader(pdf, b.Investigation.ID, "Narrative", pageNum, totalPages)
			pdf.SetXY(PageMarginMM, BodyTopMM)
		}
		writeEntry(pdf, e)
	}

	WriteFooter(pdf, b.Investigation.ID)
	return pageNum - startPage + 1
}

func buildEntries(b *Bundle, audit []db.AuditEvent) []narrativeEntry {
	out := []narrativeEntry{}

	for _, a := range audit {
		t, _ := time.Parse(time.RFC3339, a.Ts)
		var glyph, head string
		var body []string
		switch a.Kind {
		case "created":
			glyph = "+"
			head = fmt.Sprintf("Investigation created by %s", a.By)
		case "closed":
			glyph = "x"
			head = fmt.Sprintf("Investigation closed by %s", a.By)
			if res := stringFromDetails(a.Details, "resolution"); res != "" {
				head += fmt.Sprintf(" (resolution: %s)", res)
			}
		case "finding_linked":
			glyph = "*"
			head = fmt.Sprintf("Finding #%d linked by %s", int64FromDetails(a.Details, "finding_id"), a.By)
		case "run_linked":
			glyph = ">"
			head = fmt.Sprintf("Run #%d linked by %s", int64FromDetails(a.Details, "run_id"), a.By)
		case "note":
			glyph = "."
			head = fmt.Sprintf("Note by %s", a.By)
			if bodyText := stringFromDetails(a.Details, "body"); bodyText != "" {
				body = []string{bodyText}
			}
		default:
			glyph = "?"
			head = fmt.Sprintf("%s by %s", a.Kind, a.By)
		}
		out = append(out, narrativeEntry{ts: t, glyph: glyph, head: head, body: body})
	}

	for _, r := range b.Runs {
		t, _ := time.Parse(time.RFC3339, r.StartedAt)
		head := fmt.Sprintf("Run #%d started", r.ID)
		if r.OrchestrationName.Valid && r.OrchestrationName.String != "" {
			head += fmt.Sprintf(" (%s)", r.OrchestrationName.String)
		}
		out = append(out, narrativeEntry{ts: t, glyph: ">", head: head})
		if r.EndedAt.Valid {
			endT, _ := time.Parse(time.RFC3339, r.EndedAt.String)
			out = append(out, narrativeEntry{ts: endT, glyph: ">", head: fmt.Sprintf("Run #%d %s", r.ID, r.Status)})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ts.Before(out[j].ts) })
	return out
}

func stringFromDetails(d map[string]any, key string) string {
	if d == nil {
		return ""
	}
	if s, ok := d[key].(string); ok {
		return s
	}
	return ""
}

func int64FromDetails(d map[string]any, key string) int64 {
	if d == nil {
		return 0
	}
	switch v := d[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v) // JSON-decoded numerics arrive as float64
	}
	return 0
}

func entryHeight(pdf *gofpdf.Fpdf, e narrativeEntry) float64 {
	// Heuristic: 6mm per head line + 5mm per body line.
	h := 6.0
	for _, line := range e.body {
		// Wrap into ~ (BodyWidthMM-30) / 2.0 chars per line.
		charsPerLine := float64(BodyWidthMM-30.0) / 1.8
		wrap := int(charsPerLine)
		if wrap < 20 {
			wrap = 20
		}
		nlines := (len(line) + wrap - 1) / wrap
		if nlines < 1 {
			nlines = 1
		}
		h += 5.0 * float64(nlines)
	}
	return h
}

func writeEntry(pdf *gofpdf.Fpdf, e narrativeEntry) {
	pdf.SetFont("Courier", "", 9)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(36, 5, e.ts.UTC().Format("2006-01-02 15:04:05"), "", 0, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(4, 5, e.glyph, "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.MultiCell(BodyWidthMM-40, 5, e.head, "", "L", false)
	if len(e.body) > 0 {
		pdf.SetX(PageMarginMM + 40)
		pdf.SetFont("Helvetica", "I", 9)
		pdf.SetTextColor(80, 80, 80)
		for _, line := range e.body {
			pdf.MultiCell(BodyWidthMM-40, 5, line, "", "L", false)
			pdf.SetX(PageMarginMM + 40)
		}
		pdf.SetTextColor(0, 0, 0)
	}
	pdf.Ln(1)
}
