package investigation_report

import (
	"database/sql"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Suspicious Cron Jobs Cluster":         "suspicious-cron-jobs-cluster",
		"Title with — em-dashes & ampersands!": "title-with-em-dashes-ampersands",
		"   leading/trailing   ":               "leading-trailing",
		"":                                     "untitled",
		"!!!@#$%^&*()":                         "untitled",
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
