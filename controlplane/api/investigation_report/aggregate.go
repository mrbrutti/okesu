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
