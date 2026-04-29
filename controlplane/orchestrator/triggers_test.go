package orchestrator

import (
	"testing"
	"time"
)

func TestEvaluateFindingFilter(t *testing.T) {
	p := FindingPayload{
		ID: 42, Severity: "HIGH", Agent: "edr", Host: "web-prod-01", Category: "process",
	}
	cases := []struct {
		filter string
		want   bool
	}{
		{"finding.severity in ['HIGH', 'CRITICAL']", true},
		{"finding.severity in ['LOW']", false},
		{"finding.agent == 'edr' && finding.category == 'process'", true},
		{"finding.agent == 'edr' && finding.category == 'file'", false},
		{"finding.id > 0", true},
		{"", false}, // empty filter never fires
	}
	for _, tc := range cases {
		got, err := EvaluateFindingFilter(tc.filter, p)
		if err != nil {
			t.Errorf("filter %q: %v", tc.filter, err)
			continue
		}
		if got != tc.want {
			t.Errorf("filter %q = %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestNextCronFire(t *testing.T) {
	// 2026-04-28 14:30:00 UTC, a Tuesday.
	from := time.Date(2026, 4, 28, 14, 30, 0, 0, time.UTC)
	cases := []struct {
		expr string
		want time.Time
	}{
		{"0 * * * *", time.Date(2026, 4, 28, 15, 0, 0, 0, time.UTC)},
		{"30 14 * * *", time.Date(2026, 4, 29, 14, 30, 0, 0, time.UTC)}, // strictly greater, so tomorrow
		{"*/15 * * * *", time.Date(2026, 4, 28, 14, 45, 0, 0, time.UTC)},
		{"0 9 * * 1-5", time.Date(2026, 4, 29, 9, 0, 0, 0, time.UTC)},   // next weekday at 09:00 (Wed)
		{"0 0 1 * *", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},      // first of next month
	}
	for _, tc := range cases {
		got, err := nextCronFire(tc.expr, from)
		if err != nil {
			t.Errorf("cron %q: %v", tc.expr, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("cron %q from %s\n  got  %s\n  want %s", tc.expr, from, got, tc.want)
		}
	}
}

func TestNextCronFire_Errors(t *testing.T) {
	for _, expr := range []string{
		"",
		"* * *",                 // too few fields
		"* * * * * *",           // too many
		"60 * * * *",            // out of range
		"0 0 31 2 *",            // Feb 31 — never matches; should give "no match within a year"
		"abc * * * *",           // not a number
	} {
		_, err := nextCronFire(expr, time.Now())
		if err == nil {
			t.Errorf("expected error for %q", expr)
		}
	}
}
