// controlplane/api/investigation_report/summary.go
//
// Page 1 — executive summary. Title + status row + severity
// histogram + linked-entity counts + summary text + heavy-hitter
// lines. Static one-page block.
package investigation_report

import (
	"fmt"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"

	"github.com/section9labs/okesu/controlplane/db"
)

// Severity → fill RGB for the histogram strip.
var severityColors = map[string][3]int{
	"CRITICAL": {220, 38, 38},
	"HIGH":     {234, 88, 12},
	"MEDIUM":   {245, 158, 11},
	"LOW":      {59, 130, 246},
	"INFO":     {148, 163, 184},
}

var severityOrder = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}

// WriteSummary draws the entire executive-summary page (header,
// body, footer). Caller must NOT call AddPage before this.
func WriteSummary(pdf *gofpdf.Fpdf, b *Bundle, pageCount int) {
	pdf.AddPage()
	WriteHeader(pdf, b.Investigation.ID, "Summary", 1, pageCount)

	// Title block
	pdf.SetXY(PageMarginMM, BodyTopMM)
	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(BodyWidthMM, 10, fmt.Sprintf("Case #%d", b.Investigation.ID), "", 1, "L", false, 0, "")

	pdf.SetFont("Helvetica", "", 13)
	title := b.Investigation.Title
	if title == "" {
		title = "(no title)"
	}
	pdf.MultiCell(BodyWidthMM, 6, title, "", "L", false)

	pdf.Ln(4)

	// Status row — 4 cells.
	statusCells := [][]string{
		{"Status", b.Investigation.Status},
		{"Findings", fmt.Sprintf("%d", len(b.Findings))},
		{"Created", relativeTime(b.Investigation.CreatedAt)},
		{"Owner", b.Investigation.CreatedBy},
	}
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	cellW := BodyWidthMM / 4
	for _, c := range statusCells {
		pdf.CellFormat(cellW, 5, strings.ToUpper(c[0]), "", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
	pdf.Ln(5)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "B", 11)
	for _, c := range statusCells {
		pdf.CellFormat(cellW, 6, c[1], "", 0, "L", false, 0, "")
	}
	pdf.Ln(10)

	// Severity histogram strip — 5 colored segments, scaled by count.
	counts := SeverityCounts(b.Findings)
	total := len(b.Findings)
	if total > 0 {
		pdf.SetFont("Helvetica", "", 8)
		pdf.CellFormat(BodyWidthMM, 4, "SEVERITY", "", 1, "L", false, 0, "")
		x0 := pdf.GetX()
		y0 := pdf.GetY()
		for _, sev := range severityOrder {
			n := counts[sev]
			if n == 0 {
				continue
			}
			w := BodyWidthMM * float64(n) / float64(total)
			c := severityColors[sev]
			pdf.SetFillColor(c[0], c[1], c[2])
			pdf.Rect(x0, y0, w, 4, "F")
			x0 += w
		}
		pdf.Ln(6)
		// Per-severity counts spelled out.
		parts := []string{}
		for _, sev := range severityOrder {
			if counts[sev] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", sev, counts[sev]))
			}
		}
		pdf.CellFormat(BodyWidthMM, 5, strings.Join(parts, "   "), "", 1, "L", false, 0, "")
		pdf.Ln(2)
	}

	// Linked-entity counts line.
	linked := fmt.Sprintf("Linked: %d findings · %d IOCs · %d daimons · %d runs · %d notes",
		len(b.Findings), len(b.IOCs), len(b.Daimons), len(b.Runs), len(b.Notes))
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(BodyWidthMM, 5, linked, "", 1, "L", false, 0, "")
	pdf.Ln(3)

	// Summary text.
	if b.Investigation.Summary != "" {
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(BodyWidthMM, 4, "SUMMARY", "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(BodyWidthMM, 5, b.Investigation.Summary, "", "L", false)
		pdf.Ln(2)
	}

	// Heavy-hitter lines.
	hosts := AggregateHosts(b.Findings)
	if hosts.Distinct > 0 {
		writeHeavyLine(pdf, "Hosts touched", formatHostsLine(hosts))
	}
	if len(b.IOCs) > 0 {
		writeHeavyLine(pdf, "Top IOCs", formatIOCsLine(b.IOCs))
	}
	if len(b.Daimons) > 0 {
		writeHeavyLine(pdf, "Top daimons", formatDaimonsLine(b.Daimons))
	}
	runsAgg := AggregateRunsByOrch(b.Runs)
	if runsAgg.RunCount > 0 {
		writeHeavyLine(pdf, "Run summary", formatRunsLine(runsAgg))
	}

	WriteFooter(pdf, b.Investigation.ID)
}

func writeHeavyLine(pdf *gofpdf.Fpdf, label, body string) {
	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(28, 5, label+":", "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.MultiCell(BodyWidthMM-28, 5, body, "", "L", false)
}

func formatHostsLine(h HostsAggregate) string {
	max := 4
	if len(h.List) < max {
		max = len(h.List)
	}
	parts := make([]string, 0, max)
	for i := 0; i < max; i++ {
		parts = append(parts, fmt.Sprintf("%s (%d)", h.List[i].Host, h.List[i].Count))
	}
	if len(h.List) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(h.List)-max))
	}
	return strings.Join(parts, ", ")
}

func formatIOCsLine(iocs []db.InvestigationIOCItem) string {
	max := 3
	if len(iocs) < max {
		max = len(iocs)
	}
	parts := make([]string, 0, max)
	for i := 0; i < max; i++ {
		v := iocs[i].Value
		if len(v) > 16 {
			v = "…" + v[len(v)-8:]
		}
		parts = append(parts, fmt.Sprintf("%s:%s (%d obs / %d hosts)",
			iocs[i].Kind, v, iocs[i].ObservationCount, iocs[i].HostCount))
	}
	if len(iocs) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(iocs)-max))
	}
	return strings.Join(parts, ", ")
}

func formatDaimonsLine(daimons []db.InvestigationDaimonItem) string {
	max := 3
	if len(daimons) < max {
		max = len(daimons)
	}
	parts := make([]string, 0, max)
	for i := 0; i < max; i++ {
		parts = append(parts, fmt.Sprintf("%s (%d findings)", daimons[i].Agent, daimons[i].FindingCount))
	}
	if len(daimons) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(daimons)-max))
	}
	return strings.Join(parts, ", ")
}

func formatRunsLine(r RunsAggregate) string {
	if r.Top == nil {
		return ""
	}
	return fmt.Sprintf("%s (%d runs: %d ✓ %d ✗)", r.Top.Name, r.Top.Runs, r.Top.Completed, r.Top.Failed)
}

// relativeTime renders a time.Time as a coarse "Nm ago / Nh ago / Nd
// ago" label. Investigation.CreatedAt is time.Time (not a string), so
// we accept time.Time directly. A zero time falls back to "—".
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
