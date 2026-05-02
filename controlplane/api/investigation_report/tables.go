// controlplane/api/investigation_report/tables.go
//
// Pages N+1..end — reference tables (findings, IOCs, runs, audit).
// Each table starts on a fresh page; per-table header repeats on
// continuation pages. Each table caps at 200 rows; if more, footer
// line says "200 of N <kind> shown — see {tab} tab in UI for full
// list."
package investigation_report

import (
	"fmt"
	"sort"
	"time"

	"github.com/jung-kurt/gofpdf"

	"github.com/section9labs/okesu/controlplane/db"
)

const tableCap = 200

// WriteTables emits the four reference tables. Returns the number
// of pages added.
func WriteTables(pdf *gofpdf.Fpdf, b *Bundle, audit []db.AuditEvent, startPage, totalPages int) int {
	pageNum := startPage
	pageNum += writeFindingsTable(pdf, b, pageNum, totalPages)
	pageNum += writeIOCsTable(pdf, b, pageNum, totalPages)
	pageNum += writeRunsTable(pdf, b, pageNum, totalPages)
	pageNum += writeAuditTable(pdf, b, audit, pageNum, totalPages)
	return pageNum - startPage
}

func writeFindingsTable(pdf *gofpdf.Fpdf, b *Bundle, startPage, totalPages int) int {
	rows := make([]db.InvestigationFindingItem, len(b.Findings))
	copy(rows, b.Findings)
	sort.Slice(rows, func(i, j int) bool {
		si := severityRank(rows[i].Severity.String)
		sj := severityRank(rows[j].Severity.String)
		if si != sj {
			return si > sj
		}
		return rows[i].Ts > rows[j].Ts
	})
	// Column widths sum to 170mm (= BodyWidthMM).
	cols := []tableCol{
		{Header: "ID", WidthMM: 14},
		{Header: "Severity", WidthMM: 22},
		{Header: "Title", WidthMM: 60},
		{Header: "Host", WidthMM: 32},
		{Header: "Agent", WidthMM: 22},
		{Header: "Status", WidthMM: 20},
	}
	totalRows := len(rows)
	if totalRows > tableCap {
		rows = rows[:tableCap]
	}
	cells := make([][]string, 0, len(rows))
	for _, f := range rows {
		cells = append(cells, []string{
			fmt.Sprintf("#%d", f.ID),
			f.Severity.String,
			truncate(f.Title.String, 60),
			f.Host.String,
			f.Agent.String,
			f.Status.String,
		})
	}
	return writeTable(pdf, b.Investigation.ID, "Findings", "findings", cols, cells, totalRows, startPage, totalPages)
}

func writeIOCsTable(pdf *gofpdf.Fpdf, b *Bundle, startPage, totalPages int) int {
	rows := make([]db.InvestigationIOCItem, len(b.IOCs))
	copy(rows, b.IOCs)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ObservationCount > rows[j].ObservationCount })
	// Column widths sum to 170mm (= BodyWidthMM).
	cols := []tableCol{
		{Header: "Kind", WidthMM: 22},
		{Header: "Value", WidthMM: 60},
		{Header: "Obs", WidthMM: 16},
		{Header: "Hosts", WidthMM: 16},
		{Header: "First seen", WidthMM: 28},
		{Header: "Last seen", WidthMM: 28},
	}
	totalRows := len(rows)
	if totalRows > tableCap {
		rows = rows[:tableCap]
	}
	cells := make([][]string, 0, len(rows))
	for _, i := range rows {
		v := i.Value
		if len(v) > 24 {
			v = v[:21] + "..."
		}
		cells = append(cells, []string{
			i.Kind,
			v,
			fmt.Sprintf("%d", i.ObservationCount),
			fmt.Sprintf("%d", i.HostCount),
			shortIso(i.FirstSeen),
			shortIso(i.LastSeen),
		})
	}
	return writeTable(pdf, b.Investigation.ID, "IOCs", "iocs", cols, cells, totalRows, startPage, totalPages)
}

func writeRunsTable(pdf *gofpdf.Fpdf, b *Bundle, startPage, totalPages int) int {
	rows := make([]db.InvestigationRunItem, len(b.Runs))
	copy(rows, b.Runs)
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartedAt > rows[j].StartedAt })
	// Column widths sum to 168mm (under BodyWidthMM 170mm).
	cols := []tableCol{
		{Header: "ID", WidthMM: 14},
		{Header: "Orchestration", WidthMM: 50},
		{Header: "Status", WidthMM: 22},
		{Header: "Started", WidthMM: 30},
		{Header: "Duration", WidthMM: 22},
		{Header: "Trigger", WidthMM: 30},
	}
	totalRows := len(rows)
	if totalRows > tableCap {
		rows = rows[:tableCap]
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		dur := ""
		if r.EndedAt.Valid {
			s, _ := time.Parse(time.RFC3339, r.StartedAt)
			e, _ := time.Parse(time.RFC3339, r.EndedAt.String)
			dur = e.Sub(s).Round(time.Second).String()
		} else {
			dur = "running"
		}
		cells = append(cells, []string{
			fmt.Sprintf("#%d", r.ID),
			r.OrchestrationName.String,
			r.Status,
			shortIso(r.StartedAt),
			dur,
			r.TriggerKind,
		})
	}
	return writeTable(pdf, b.Investigation.ID, "Runs", "runs", cols, cells, totalRows, startPage, totalPages)
}

func writeAuditTable(pdf *gofpdf.Fpdf, b *Bundle, audit []db.AuditEvent, startPage, totalPages int) int {
	rows := make([]db.AuditEvent, len(audit))
	copy(rows, audit)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Ts > rows[j].Ts })
	// Column widths sum to 170mm (= BodyWidthMM).
	cols := []tableCol{
		{Header: "Timestamp", WidthMM: 36},
		{Header: "Actor", WidthMM: 40},
		{Header: "Kind", WidthMM: 30},
		{Header: "Title", WidthMM: 64},
	}
	totalRows := len(rows)
	if totalRows > tableCap {
		rows = rows[:tableCap]
	}
	cells := make([][]string, 0, len(rows))
	for _, a := range rows {
		cells = append(cells, []string{
			shortIso(a.Ts),
			a.By,
			a.Kind,
			truncate(a.Title, 60),
		})
	}
	return writeTable(pdf, b.Investigation.ID, "Audit", "audit", cols, cells, totalRows, startPage, totalPages)
}

type tableCol struct {
	Header  string
	WidthMM float64
}

// writeTable emits one section's table starting on a fresh page.
// Returns the number of pages added.
func writeTable(pdf *gofpdf.Fpdf, caseID int64, title, tabName string, cols []tableCol, cells [][]string, totalRows, startPage, totalPages int) int {
	pdf.AddPage()
	pageNum := startPage
	WriteHeader(pdf, caseID, title, pageNum, totalPages)

	pdf.SetXY(PageMarginMM, BodyTopMM)
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(BodyWidthMM, 8, title, "", 1, "L", false, 0, "")
	pdf.Ln(2)

	drawTableHeader(pdf, cols)
	rowH := 5.0
	for _, row := range cells {
		if pdf.GetY()+rowH > BodyBottomMM {
			WriteFooter(pdf, caseID)
			pdf.AddPage()
			pageNum++
			WriteHeader(pdf, caseID, title, pageNum, totalPages)
			pdf.SetXY(PageMarginMM, BodyTopMM)
			pdf.SetFont("Helvetica", "B", 14)
			pdf.CellFormat(BodyWidthMM, 8, title+" (cont.)", "", 1, "L", false, 0, "")
			pdf.Ln(2)
			drawTableHeader(pdf, cols)
		}
		pdf.SetFont("Helvetica", "", 8)
		for i, c := range cols {
			pdf.CellFormat(c.WidthMM, rowH, row[i], "B", 0, "L", false, 0, "")
		}
		pdf.Ln(rowH)
	}

	if totalRows > tableCap {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		msg := fmt.Sprintf("%d of %d %s shown — see %s tab in UI for full list.", tableCap, totalRows, title, tabName)
		pdf.CellFormat(BodyWidthMM, 5, msg, "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	}

	WriteFooter(pdf, caseID)
	return pageNum - startPage + 1
}

func drawTableHeader(pdf *gofpdf.Fpdf, cols []tableCol) {
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(240, 240, 240)
	for _, c := range cols {
		pdf.CellFormat(c.WidthMM, 5, c.Header, "B", 0, "L", true, 0, "")
	}
	pdf.Ln(5)
}

func severityRank(s string) int {
	switch s {
	case "CRITICAL":
		return 5
	case "HIGH":
		return 4
	case "MEDIUM":
		return 3
	case "LOW":
		return 2
	default:
		return 1
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func shortIso(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.UTC().Format("2006-01-02 15:04")
}
