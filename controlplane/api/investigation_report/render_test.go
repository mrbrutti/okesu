package investigation_report

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

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
			CreatedAt: time.Date(2026, 4, 28, 14, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
			// ClosedAt zero-value (time.Time{}) means "not closed".
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
	b.Investigation.ClosedAt = time.Date(2026, 4, 30, 18, 0, 0, 0, time.UTC)
	b.Investigation.Resolution = "resolved"
	var buf bytes.Buffer
	if err := Render(b, nil, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if buf.Len() < 1024 {
		t.Errorf("output too small: %d bytes", buf.Len())
	}
}
