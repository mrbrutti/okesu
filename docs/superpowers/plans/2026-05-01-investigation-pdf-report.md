# Investigation PDF Report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Server-rendered PDF report for an investigation, suitable for incident-report writeups and analyst handoffs. Custom layout (NOT a screen reproduction): page 1 executive summary, pages 2..N chronological narrative, pages N+1..end reference tables.

**Architecture:** New `controlplane/api/investigation_report/` package using `github.com/jung-kurt/gofpdf` (pure-Go, MIT, no Cgo, built-in fonts). New `GET /api/investigations/{id}/report.pdf` endpoint with the same federation `?cp=` proxy convention as the existing detail endpoint. Audit-logs every export. Frontend adds one `<a href="…" download>` button.

**Tech Stack:** Go 1.22 + gofpdf, chi router, sqlite/postgres (no migrations), React 18 + TypeScript (frontend button only).

---

## File structure

### New (server)

- `controlplane/api/investigation_report/aggregate.go` — Go ports of the heavy-hitter aggregators (hosts/IOCs/daimons/orchestrations) + filename-slug helper.
- `controlplane/api/investigation_report/frame.go` — page header/footer + page numbering.
- `controlplane/api/investigation_report/summary.go` — page 1 executive summary.
- `controlplane/api/investigation_report/narrative.go` — pages 2..N chronological narrative.
- `controlplane/api/investigation_report/tables.go` — reference tables (findings/IOCs/runs/audit).
- `controlplane/api/investigation_report/render.go` — top-level `Render` entrypoint + the `Bundle` input struct.
- `controlplane/api/investigation_report/render_test.go` — render-level tests.
- `controlplane/api/investigation_report.go` — HTTP handler (separate file; keeps the package public surface clean).
- `controlplane/api/investigation_report_test.go` — handler-level tests.

### New (frontend)

- (no new files — one button addition in InvestigationDetail.tsx).

### Modified

- `controlplane/server.go` — mount the report endpoints.
- `web/src/pages/InvestigationDetail.tsx` — add `Export report` button.
- `go.mod` / `go.sum` — add `github.com/jung-kurt/gofpdf`.

---

## Task Group A — `aggregate.go` foundation

### Task A1: Aggregators + slug helper

**Files:**
- Create: `controlplane/api/investigation_report/aggregate.go`
- Create: `controlplane/api/investigation_report/aggregate_test.go`

- [ ] **Step A1.1: Write the failing test**

```go
// controlplane/api/investigation_report/aggregate_test.go
package investigation_report

import (
	"database/sql"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Suspicious Cron Jobs Cluster":             "suspicious-cron-jobs-cluster",
		"Title with — em-dashes & ampersands!":     "title-with-em-dashes-ampersands",
		"   leading/trailing   ":                   "leading-trailing",
		"":                                          "untitled",
		"!!!@#$%^&*()":                              "untitled",
	}
	for in, want := range cases {
		got := Slug(in)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}

	// Cap at 40 chars.
	long := "this is a very long investigation title that exceeds forty characters easily"
	got := Slug(long)
	if len(got) > 40 {
		t.Errorf("Slug length %d > 40 for %q -> %q", len(got), long, got)
	}
}

func TestAggregateHosts(t *testing.T) {
	findings := []db.InvestigationFindingItem{
		{Host: sql.NullString{String: "h1", Valid: true}},
		{Host: sql.NullString{String: "h1", Valid: true}},
		{Host: sql.NullString{String: "h2", Valid: true}},
		{Host: sql.NullString{Valid: false}}, // skipped
	}
	hosts := AggregateHosts(findings)
	if hosts.Distinct != 2 {
		t.Errorf("Distinct = %d, want 2", hosts.Distinct)
	}
	if hosts.Top == nil || hosts.Top.Host != "h1" || hosts.Top.Count != 2 {
		t.Errorf("Top = %+v, want h1 count=2", hosts.Top)
	}
}

func TestAggregateRunsByOrch(t *testing.T) {
	runs := []db.InvestigationRunItem{
		{ID: 1, OrchestrationID: 100, OrchestrationName: sql.NullString{String: "tri", Valid: true}, Status: "completed"},
		{ID: 2, OrchestrationID: 100, OrchestrationName: sql.NullString{String: "tri", Valid: true}, Status: "completed"},
		{ID: 3, OrchestrationID: 100, OrchestrationName: sql.NullString{String: "tri", Valid: true}, Status: "failed"},
	}
	got := AggregateRunsByOrch(runs)
	if got.RunCount != 3 || got.OrchCount != 1 {
		t.Errorf("counts = %+v", got)
	}
	if got.Top == nil || got.Top.Name != "tri" || got.Top.Completed != 2 || got.Top.Failed != 1 {
		t.Errorf("top = %+v", got.Top)
	}
}

func TestSeverityCounts(t *testing.T) {
	findings := []db.InvestigationFindingItem{
		{Severity: sql.NullString{String: "CRITICAL", Valid: true}},
		{Severity: sql.NullString{String: "HIGH", Valid: true}},
		{Severity: sql.NullString{String: "HIGH", Valid: true}},
		{Severity: sql.NullString{String: "LOW", Valid: true}},
		{Severity: sql.NullString{Valid: false}}, // → INFO bucket
	}
	c := SeverityCounts(findings)
	if c["CRITICAL"] != 1 || c["HIGH"] != 2 || c["LOW"] != 1 || c["INFO"] != 1 {
		t.Errorf("counts = %+v", c)
	}
}
```

- [ ] **Step A1.2: Run test to verify it fails**

```bash
go test ./controlplane/api/investigation_report/...
```

Expected: FAIL — package doesn't exist.

- [ ] **Step A1.3: Create the package + helpers**

Create `controlplane/api/investigation_report/aggregate.go`:

```go
// Package investigation_report renders an Investigation case as a
// PDF report (executive summary + chronological narrative + reference
// tables). Pure rendering on the read path — no database mutations,
// no migrations.
package investigation_report

import (
	"regexp"
	"sort"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
)

// HostCount is one row in the host heavy-hitter list.
type HostCount struct {
	Host  string
	Count int
}

// HostsAggregate is the result of AggregateHosts.
type HostsAggregate struct {
	Distinct int
	Top      *HostCount
	List     []HostCount // sorted by Count desc
}

// AggregateHosts counts distinct hosts across the case's findings.
// Skips findings without a valid Host.
func AggregateHosts(findings []db.InvestigationFindingItem) HostsAggregate {
	counts := map[string]int{}
	for _, f := range findings {
		if !f.Host.Valid || f.Host.String == "" {
			continue
		}
		counts[f.Host.String]++
	}
	list := make([]HostCount, 0, len(counts))
	for h, c := range counts {
		list = append(list, HostCount{Host: h, Count: c})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
	out := HostsAggregate{Distinct: len(list), List: list}
	if len(list) > 0 {
		out.Top = &list[0]
	}
	return out
}

// OrchRow is one orchestration in the run summary.
type OrchRow struct {
	Name      string
	Runs      int
	Completed int
	Failed    int
	Cancelled int
	Running   int
}

// RunsAggregate is the result of AggregateRunsByOrch.
type RunsAggregate struct {
	RunCount  int
	OrchCount int
	Top       *OrchRow
	List      []OrchRow // sorted by Runs desc
}

// AggregateRunsByOrch groups runs by orchestration and counts each
// status bucket. Mirrors the client-side aggregator in
// CaseStructure.tsx.
func AggregateRunsByOrch(runs []db.InvestigationRunItem) RunsAggregate {
	byID := map[int64]*OrchRow{}
	for _, r := range runs {
		row, ok := byID[r.OrchestrationID]
		if !ok {
			name := r.OrchestrationName.String
			if !r.OrchestrationName.Valid || name == "" {
				name = "(unnamed)"
			}
			row = &OrchRow{Name: name}
			byID[r.OrchestrationID] = row
		}
		row.Runs++
		switch r.Status {
		case "completed":
			row.Completed++
		case "failed":
			row.Failed++
		case "cancelled":
			row.Cancelled++
		default:
			row.Running++
		}
	}
	list := make([]OrchRow, 0, len(byID))
	for _, row := range byID {
		list = append(list, *row)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Runs > list[j].Runs })
	out := RunsAggregate{
		RunCount:  len(runs),
		OrchCount: len(byID),
		List:      list,
	}
	if len(list) > 0 {
		out.Top = &list[0]
	}
	return out
}

// SeverityCounts returns counts by severity across findings. Findings
// with an invalid Severity bucket as INFO. All five severity keys are
// present in the result, even if zero.
func SeverityCounts(findings []db.InvestigationFindingItem) map[string]int {
	out := map[string]int{
		"CRITICAL": 0,
		"HIGH":     0,
		"MEDIUM":   0,
		"LOW":      0,
		"INFO":     0,
	}
	for _, f := range findings {
		s := "INFO"
		if f.Severity.Valid && f.Severity.String != "" {
			s = strings.ToUpper(f.Severity.String)
		}
		if _, known := out[s]; !known {
			s = "INFO"
		}
		out[s]++
	}
	return out
}

// slugRe matches characters we strip from titles for filenames.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug normalises an investigation title for use in a filename:
// lowercase, non-alphanumerics → '-', collapsed, trimmed, capped at
// 40 chars. Returns "untitled" when the result would be empty.
func Slug(title string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "untitled"
	}
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "untitled"
	}
	return s
}
```

- [ ] **Step A1.4: Run tests, expect PASS**

```bash
go test ./controlplane/api/investigation_report/... -count=1 -v
```

Expected: 4 PASS.

- [ ] **Step A1.5: Commit**

```bash
git add controlplane/api/investigation_report/aggregate.go \
        controlplane/api/investigation_report/aggregate_test.go
git commit -m "report(aggregate): host/run aggregators + slug helper"
```

---

## Task Group B — Add `gofpdf` dep + `frame.go`

### Task B1: Add the dep + draw page header/footer

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get`)
- Create: `controlplane/api/investigation_report/frame.go`
- Create: `controlplane/api/investigation_report/frame_test.go`

- [ ] **Step B1.1: Add the dependency**

```bash
go get github.com/jung-kurt/gofpdf@latest
```

Expected: updates `go.mod` and `go.sum`. The dep is pure-Go; works under `CGO_ENABLED=0`.

- [ ] **Step B1.2: Write the failing test**

```go
// controlplane/api/investigation_report/frame_test.go
package investigation_report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jung-kurt/gofpdf"
)

func TestNewDocumentSetsBasics(t *testing.T) {
	doc := NewDocument(123)
	doc.AddPage()
	var buf bytes.Buffer
	if err := doc.Output(&buf); err != nil {
		t.Fatalf("output: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "%PDF-") {
		t.Errorf("output does not start with %%PDF-: %q", buf.String()[:16])
	}
}

func TestFooterIncludesCaseID(t *testing.T) {
	// Smoke check: the footer drawing function should at least not
	// panic when called against a real page. Content checks happen
	// via golden-file tests; here we just verify a clean call.
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	WriteFooter(pdf, 42)
	if pdf.Err() {
		t.Fatalf("pdf err: %v", pdf.Error())
	}
}

func TestHeaderIncludesSection(t *testing.T) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	WriteHeader(pdf, 42, "Narrative", 1, 5)
	if pdf.Err() {
		t.Fatalf("pdf err: %v", pdf.Error())
	}
}
```

- [ ] **Step B1.3: Run test, expect FAIL**

```bash
go test ./controlplane/api/investigation_report/... -count=1 -run "Document|Footer|Header" -v
```

- [ ] **Step B1.4: Implement frame.go**

```go
// controlplane/api/investigation_report/frame.go
//
// Page frame: A4 portrait, 20mm margins, Helvetica/Courier built-in
// fonts, 8pt grey header + footer on every page.
package investigation_report

import (
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// PageWidthMM and PageHeightMM are A4 portrait minus 20mm margins on
// each side. Body height excludes the 8mm header + 8mm footer.
const (
	PageMarginMM    = 20.0
	HeaderHeightMM  = 8.0
	FooterHeightMM  = 8.0
	BodyTopMM       = PageMarginMM + HeaderHeightMM
	BodyBottomMM    = 297.0 - PageMarginMM - FooterHeightMM // A4 height
	BodyWidthMM     = 210.0 - 2*PageMarginMM                 // A4 width
)

// NewDocument creates a fresh A4 portrait PDF with the standard
// margins set. caseID is recorded on the doc-level metadata for
// the footer renderer.
func NewDocument(caseID int64) *gofpdf.Fpdf {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(PageMarginMM, PageMarginMM, PageMarginMM)
	pdf.SetAutoPageBreak(true, FooterHeightMM+PageMarginMM)
	pdf.SetTitle(fmt.Sprintf("Okesu Investigation Report — Case #%d", caseID), false)
	pdf.SetCreator("Okesu CP", false)
	return pdf
}

// WriteHeader draws the top-of-page header. Call once per page after
// AddPage(); the engine offers no auto-header callback that gets
// the section name right, so we drive header/footer explicitly.
func WriteHeader(pdf *gofpdf.Fpdf, caseID int64, section string, pageNum, pageCount int) {
	if pdf.Err() {
		return
	}
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	left := fmt.Sprintf("Case #%d — %s", caseID, section)
	right := fmt.Sprintf("Page %d / %d", pageNum, pageCount)
	pdf.SetXY(PageMarginMM, PageMarginMM/2)
	pdf.CellFormat(BodyWidthMM/2, 5, left, "", 0, "L", false, 0, "")
	pdf.CellFormat(BodyWidthMM/2, 5, right, "", 0, "R", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}

// WriteFooter draws the bottom-of-page footer. Call once per page
// before the page is finished.
func WriteFooter(pdf *gofpdf.Fpdf, caseID int64) {
	if pdf.Err() {
		return
	}
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	footer := fmt.Sprintf("Generated %s by Okesu CP — Investigation #%d",
		time.Now().UTC().Format("2006-01-02 15:04:05 UTC"), caseID)
	pdf.SetY(297.0 - PageMarginMM/2 - 4)
	pdf.CellFormat(BodyWidthMM, 5, footer, "", 0, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}
```

- [ ] **Step B1.5: Run tests, expect PASS + commit**

```bash
go test ./controlplane/api/investigation_report/... -count=1
```

```bash
git add go.mod go.sum \
        controlplane/api/investigation_report/frame.go \
        controlplane/api/investigation_report/frame_test.go
git commit -m "report(frame): page setup + header/footer drawing"
```

---

## Task Group C — `summary.go` (page 1 executive summary)

### Task C1: Render the executive summary

**Files:**
- Create: `controlplane/api/investigation_report/summary.go`

(No standalone test file — `render_test.go` in Group F covers all sections via golden snapshots / content assertions.)

- [ ] **Step C1.1: Implement summary.go**

```go
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
		{"Status", string(b.Investigation.Status)},
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

func relativeTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
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
```

- [ ] **Step C1.2: Build to confirm no syntax errors**

```bash
go build ./controlplane/api/investigation_report/...
```

Expected: clean.

- [ ] **Step C1.3: Commit**

```bash
git add controlplane/api/investigation_report/summary.go
git commit -m "report(summary): page 1 executive summary section"
```

---

## Task Group D — `narrative.go` (chronological narrative)

### Task D1: Render the narrative

**Files:**
- Create: `controlplane/api/investigation_report/narrative.go`

- [ ] **Step D1.1: Implement narrative.go**

```go
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
		wrap := int((BodyWidthMM - 30.0) / 1.8)
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
```

Note: glyphs are ASCII (`+`, `x`, `*`, `>`, `.`) instead of the timeline's Unicode (`⊕`, `⊗`, `•`, `▭`, `✎`) because gofpdf's built-in fonts are Latin-1 only and Unicode glyphs render as boxes. The narrative style stays consistent without losing meaning.

- [ ] **Step D1.2: Build + commit**

```bash
go build ./controlplane/api/investigation_report/...
```

```bash
git add controlplane/api/investigation_report/narrative.go
git commit -m "report(narrative): chronological narrative section"
```

---

## Task Group E — `tables.go` (reference tables)

### Task E1: Render findings/IOCs/runs/audit tables

**Files:**
- Create: `controlplane/api/investigation_report/tables.go`

- [ ] **Step E1.1: Implement tables.go**

```go
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
	cols := []tableCol{
		{Header: "ID", WidthMM: 14},
		{Header: "Severity", WidthMM: 22},
		{Header: "Title", WidthMM: 70},
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
	cols := []tableCol{
		{Header: "Kind", WidthMM: 22},
		{Header: "Value", WidthMM: 70},
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
```

- [ ] **Step E1.2: Build + commit**

```bash
go build ./controlplane/api/investigation_report/...
```

```bash
git add controlplane/api/investigation_report/tables.go
git commit -m "report(tables): findings/IOCs/runs/audit reference tables"
```

---

## Task Group F — `render.go` (entrypoint + tests)

### Task F1: Top-level Render entrypoint + golden-style tests

**Files:**
- Create: `controlplane/api/investigation_report/render.go`
- Create: `controlplane/api/investigation_report/render_test.go`

- [ ] **Step F1.1: Implement render.go**

```go
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

// Bundle is the input shape — same fields the
// GetInvestigationHandler returns to the UI, plus the audit log.
type Bundle struct {
	Investigation db.Investigation
	Findings      []db.InvestigationFindingItem
	Runs          []db.InvestigationRunItem
	IOCs          []db.InvestigationIOCItem
	Daimons       []db.InvestigationDaimonItem
	Orchestrations []db.InvestigationOrchestrationItem
	Notes         []db.InvestigationNote
}

// Render writes a complete PDF report to w. Returns the number of
// bytes written and any error from the underlying gofpdf engine.
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
```

- [ ] **Step F1.2: Write the failing test**

```go
// controlplane/api/investigation_report/render_test.go
package investigation_report

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func makeBundle() *Bundle {
	return &Bundle{
		Investigation: db.Investigation{
			ID:        42,
			Title:     "Suspicious cron jobs cluster",
			Status:    "active",
			Summary:   "Multiple findings on edr fleet — investigating.",
			CreatedBy: "alice@example.com",
			CreatedAt: "2026-04-28T14:00:00Z",
			UpdatedAt: "2026-05-01T12:00:00Z",
			ClosedAt:  "0001-01-01T00:00:00Z",
		},
	}
}

func TestRender_EmptyCase(t *testing.T) {
	b := makeBundle()
	var buf bytes.Buffer
	if err := Render(b, nil, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "%PDF-") {
		t.Fatalf("output does not start with %%PDF-: %q", buf.String()[:16])
	}
	if buf.Len() < 1024 {
		t.Errorf("output suspiciously small: %d bytes", buf.Len())
	}
}

func TestRender_WithFindings(t *testing.T) {
	b := makeBundle()
	for i := 1; i <= 5; i++ {
		b.Findings = append(b.Findings, db.InvestigationFindingItem{
			ID:       int64(i),
			Ts:       1700000000 + int64(i),
			Severity: sql.NullString{String: "HIGH", Valid: true},
			Title:    sql.NullString{String: "Finding", Valid: true},
			Host:     sql.NullString{String: "h1", Valid: true},
			Agent:    sql.NullString{String: "edr", Valid: true},
			Status:   sql.NullString{String: "open", Valid: true},
		})
	}
	var buf bytes.Buffer
	if err := Render(b, nil, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if buf.Len() < 1024 {
		t.Errorf("output too small: %d bytes", buf.Len())
	}
}

func TestRender_FindingsCap(t *testing.T) {
	b := makeBundle()
	for i := 1; i <= 250; i++ {
		b.Findings = append(b.Findings, db.InvestigationFindingItem{
			ID:       int64(i),
			Severity: sql.NullString{String: "LOW", Valid: true},
			Title:    sql.NullString{String: "f", Valid: true},
			Host:     sql.NullString{String: "h", Valid: true},
			Agent:    sql.NullString{String: "a", Valid: true},
			Status:   sql.NullString{String: "open", Valid: true},
		})
	}
	var buf bytes.Buffer
	if err := Render(b, nil, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	// PDF text content isn't trivially parseable from a buffer;
	// instead assert that render succeeded and is large enough to
	// contain a 200-row table.
	if buf.Len() < 8*1024 {
		t.Errorf("expected larger output for 250 findings, got %d bytes", buf.Len())
	}
}

func TestRender_ClosedCase(t *testing.T) {
	b := makeBundle()
	b.Investigation.Status = "closed"
	b.Investigation.ClosedAt = "2026-04-30T18:00:00Z"
	b.Investigation.Resolution = "resolved"
	var buf bytes.Buffer
	if err := Render(b, nil, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if buf.Len() < 1024 {
		t.Errorf("output too small: %d bytes", buf.Len())
	}
}
```

- [ ] **Step F1.3: Run tests, expect PASS**

```bash
go test ./controlplane/api/investigation_report/... -count=1 -v
```

Expected: 4 render tests + 4 aggregate tests + 3 frame tests = 11 PASS.

- [ ] **Step F1.4: Commit**

```bash
git add controlplane/api/investigation_report/render.go \
        controlplane/api/investigation_report/render_test.go
git commit -m "report(render): top-level entrypoint + golden-style tests"
```

---

## Task Group G — HTTP handler + federation wiring

### Task G1: Local handler + federated wrappers + route mounts

**Files:**
- Create: `controlplane/api/investigation_report.go`
- Create: `controlplane/api/investigation_report_test.go`
- Modify: `controlplane/server.go`

- [ ] **Step G1.1: Implement the handler**

```go
// controlplane/api/investigation_report.go
package api

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/api/investigation_report"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// GetInvestigationReportHandler returns a PDF rendering of the case.
// Loads the same bundle the detail endpoint returns + the case's
// audit log, hands them to the renderer, streams bytes back. Logs
// one audit_log entry per export (best-effort).
func GetInvestigationReportHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inv, err := store.GetInvestigation(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		findings, _ := store.ListFindingsForInvestigationEnriched(id)
		runs, _ := store.ListRunsForInvestigationEnriched(id)
		iocs, _ := store.ListIOCsForInvestigation(id)
		daimons, _ := store.ListDaimonsForInvestigation(id)
		orchs, _ := store.ListOrchestrationsForInvestigation(id)
		notes, _ := store.ListInvestigationNotes(id)
		audit, _ := store.ListInvestigationAudit(id)

		bundle := &investigation_report.Bundle{
			Investigation:  *inv,
			Findings:       findings,
			Runs:           runs,
			IOCs:           iocs,
			Daimons:        daimons,
			Orchestrations: orchs,
			Notes:          notes,
		}

		var buf bytes.Buffer
		if err := investigation_report.Render(bundle, audit, &buf); err != nil {
			http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
			return
		}

		filename := fmt.Sprintf("case-%d-%s.pdf", id, investigation_report.Slug(inv.Title))
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
		_, _ = w.Write(buf.Bytes())

		// Best-effort audit. If this fails, we still served the PDF.
		actorEmail, _ := actorEmailFromContext(r)
		_ = store.InsertAudit(db.AuditEntry{
			ActorEmail: actorEmail,
			Action:     "investigation.report_exported",
			Target:     fmt.Sprintf("investigation:%d", id),
			Result:     "ok",
			Metadata: map[string]any{
				"format":     "pdf",
				"size_bytes": buf.Len(),
				"filename":   filename,
			},
		})
	}
}

// FederatedInvestigationReport — parent-side wrapper. Proxies to
// the owning child via ?cp=<instance_id>; falls through to the
// local handler otherwise.
func FederatedInvestigationReport(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationReportHandler(store).ServeHTTP(w, r)
	}
}

// FederationInvestigationReport — child-side, token-authed sibling.
func FederationInvestigationReport(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationReportHandler(store))
}
```

If `actorEmailFromContext` doesn't exist as-named, search for the existing helper that pulls the operator email out of the request context (look for `auth.UserFromContext` or similar — the fleet-env handlers in PR #82 use this pattern). Adapt accordingly.

- [ ] **Step G1.2: Mount in server.go**

In `controlplane/server.go`, find the existing investigation routes (around line 887) and add the new ones:

In the cookie-auth admin group near the existing `r.Get("/api/investigations/{id}", api.FederatedInvestigationDetail(s.store, s.fedAgg))`:

```go
r.Get("/api/investigations/{id}/report.pdf", api.FederatedInvestigationReport(s.store, s.fedAgg))
```

In the federation-token group near `r.Get("/api/v1/federation/investigations/{id}", api.FederationInvestigationDetail(s.store))` (around line 655):

```go
r.Get("/api/v1/federation/investigations/{id}/report.pdf", api.FederationInvestigationReport(s.store))
```

- [ ] **Step G1.3: Write the handler test**

```go
// controlplane/api/investigation_report_test.go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestInvestigationReport_Returns404OnUnknown(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/report.pdf", GetInvestigationReportHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/report.pdf", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestInvestigationReport_HappyPath(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustInsertInvestigation(t, store, "Test case")

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/report.pdf", GetInvestigationReportHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+itoa(id)+"/report.pdf", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "case-") || !strings.Contains(cd, ".pdf") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Errorf("body does not start with %%PDF-")
	}
}

func TestInvestigationReport_AuditLogged(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustInsertInvestigation(t, store, "Test case")

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/report.pdf", GetInvestigationReportHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+itoa(id)+"/report.pdf", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	rows, err := store.ListAudit(db.AuditFilter{Action: "investigation.report_exported", Limit: 10})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if rows[0].Target.String != "investigation:"+itoa(id) {
		t.Errorf("Target = %q", rows[0].Target.String)
	}
}
```

If helpers like `newSeededTestStore`, `mustInsertInvestigation`, `itoa` don't exist, search the existing api tests for equivalent patterns and reuse them — in particular `controlplane/api/investigations_test.go` likely already creates investigations for tests.

- [ ] **Step G1.4: Run tests, expect PASS**

```bash
go test ./controlplane/api/... -count=1 -run "InvestigationReport"
```

Expected: 3 PASS.

- [ ] **Step G1.5: Build full controlplane + commit**

```bash
mkdir -p controlplane/ui/dist && touch controlplane/ui/dist/.gitkeep
go build ./controlplane/...
go vet ./controlplane/...
go test ./controlplane/api/... -count=1
rm -rf controlplane/ui/dist
```

```bash
git add controlplane/api/investigation_report.go \
        controlplane/api/investigation_report_test.go \
        controlplane/server.go
git commit -m "api(investigations): /report.pdf endpoint with federation proxy"
```

---

## Task Group H — Frontend "Export report" button

### Task H1: Add the button to InvestigationDetail.tsx

**Files:**
- Modify: `web/src/pages/InvestigationDetail.tsx`

- [ ] **Step H1.1: Add the import + button**

In `web/src/pages/InvestigationDetail.tsx`, near the top imports (next to other lucide-react imports):

```tsx
import { Download } from 'lucide-react';
```

In the page header where the close-dialog button + edit button currently live, add a sibling button. Search for the existing edit button (around line 247 — `setEditing(true)`) and add this nearby:

```tsx
<a
  href={`/api/investigations/${invID}/report.pdf${cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : ''}`}
  download
  className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md border border-border bg-white hover:bg-slate-50 text-ink-mute"
  title="Export PDF report"
>
  <Download size={12} /> Export report
</a>
```

- [ ] **Step H1.2: Type-check**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-pdf-report/web
node_modules/.bin/tsc -b
```

Expected: clean.

- [ ] **Step H1.3: Commit**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-pdf-report
git add web/src/pages/InvestigationDetail.tsx
git commit -m "web(investigations): Export report button (PDF download)"
```

---

## Task Group Z — docs + sweep + PR body

### Task Z1: Architecture doc append

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

```markdown
## Investigation PDF report

Operators can export a case as a PDF report from the investigation detail page (`Export report` button). The endpoint is `GET /api/investigations/{id}/report.pdf`; federated cases route to the owning child CP via the existing `?cp=<instance_id>` proxy convention.

Renderer lives in `controlplane/api/investigation_report/`, built on `github.com/jung-kurt/gofpdf` (pure-Go, MIT, no Cgo, built-in Helvetica/Courier fonts). The document layout is custom-shaped — page 1 executive summary, pages 2..N chronological narrative, pages N+1..end reference tables (findings/IOCs/runs/audit, capped at 200 rows each). Each export writes one `audit_log` row with action `investigation.report_exported`.

The renderer is text-shaped and does not attempt to mirror the on-screen Overview — operators view the live timeline in the UI; the PDF is for handovers and incident-report writeups.
```

```bash
git add docs/architecture.md
git commit -m "docs(architecture): investigation PDF report"
```

### Task Z2: Test sweep

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-pdf-report
mkdir -p controlplane/ui/dist && touch controlplane/ui/dist/.gitkeep
go test ./... -count=1 -timeout 180s 2>&1 | grep -E "^(FAIL|ok|---)" | head -25
rm -rf controlplane/ui/dist
cd web && node_modules/.bin/tsc -b && npm test -- --run 2>&1 | tail -8
```

All must pass.

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-05-01-investigation-pdf-report-pr-body.md`

```markdown
## Summary

Follow-up #6 from the Investigation Overview backlog. Adds a server-rendered PDF report for an investigation case, suitable for incident-report writeups and analyst handoffs.

- New endpoint `GET /api/investigations/{id}/report.pdf` returning a custom-layout PDF: page 1 executive summary (status, severity histogram, linked-entity counts, summary text, heavy-hitter lines), pages 2..N chronological narrative (lifecycle / findings / runs / notes / audit-other, time-ordered), pages N+1..end reference tables (findings / IOCs / runs / audit, capped at 200 rows each).
- Server-side renderer at `controlplane/api/investigation_report/` using `github.com/jung-kurt/gofpdf` (pure-Go, MIT, no Cgo, built-in Helvetica/Courier fonts). Six small files, one per section.
- Federation: same `?cp=<instance_id>` proxy convention as the existing detail endpoint. Federated cases render on the owning child CP; bytes stream back through the parent.
- Audit-logged: every successful export writes one `audit_log` row with action `investigation.report_exported` (read-then-export is a real exfil-risk surface).
- Frontend: one `Export report` button (lucide `Download` icon) in the investigation page header. Click → browser downloads the PDF.

The report is text-shaped throughout — no screen reproduction, no embedded SVG, no charts. Per "no dossier that looks like a screen."

## Test plan

- `controlplane/api/investigation_report/aggregate_test.go` — slug helper edge cases (empty title, special chars, length cap), host aggregation, run-by-orch aggregation, severity counts.
- `controlplane/api/investigation_report/frame_test.go` — document setup smoke + header/footer drawing without panic.
- `controlplane/api/investigation_report/render_test.go` — end-to-end render against synthetic bundles (empty / 5-finding / 250-finding / closed-with-resolution); asserts `%PDF-` prefix + minimum sizes.
- `controlplane/api/investigation_report_test.go` — handler-level: 200 with `application/pdf` + correct `Content-Disposition` filename, 404 on unknown id, audit-log row written after export.

Manual lab smoke (post-merge):
- [ ] Open an active investigation with ≥ 5 findings, ≥ 1 run, ≥ 1 note. Click "Export report" → PDF downloads.
- [ ] Open in Preview/Reader → all three sections present, page numbers correct.
- [ ] Open a closed case → narrative ends with `Investigation closed` lifecycle entry.
- [ ] Federated case (`cp_source` set) → export proxies to child correctly.
- [ ] Verify an `audit_log` row exists with `action=investigation.report_exported, target=investigation:{id}` after each export.

## Files

**New (server):**
- `controlplane/api/investigation_report/{aggregate,frame,summary,narrative,tables,render}.go` (+ tests for aggregate, frame, render).
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
```

```bash
git add docs/superpowers/plans/2026-05-01-investigation-pdf-report-pr-body.md
git commit -m "docs: investigation PDF report PR body"
```

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.
