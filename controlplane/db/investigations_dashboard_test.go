package db

import (
	"testing"
)

// TestDashboardInvestigationStats covers the four signals: active
// count, closed_24h, autolinked_24h, and the recent-active list.
func TestDashboardInvestigationStats(t *testing.T) {
	st := openTempStore(t)

	// Three cases: two active, one closed within the last 24h.
	a, _ := st.CreateInvestigation(&InvestigationInsert{Title: "A"})
	b, _ := st.CreateInvestigation(&InvestigationInsert{Title: "B"})
	c, _ := st.CreateInvestigation(&InvestigationInsert{Title: "C"})

	if err := st.UpdateInvestigation(c, &InvestigationUpdate{
		Status:     "closed",
		Resolution: "resolved",
	}); err != nil {
		t.Fatalf("close C: %v", err)
	}

	// Seed a finding + autolinked link on case A → autolinked_24h=1.
	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, raw_json)
		VALUES (101, 1, 1, 'HIGH', 'a', '{}'),
		       (102, 1, 1, 'HIGH', 'b', '{}')`); err != nil {
		t.Fatalf("seed findings: %v", err)
	}
	if err := st.LinkFindingToInvestigationWithProvenance(
		a, 101, LinkMethodAutoLink, "system:autolink",
	); err != nil {
		t.Fatalf("autolink: %v", err)
	}
	// Manual link doesn't count toward autolinked_24h.
	if err := st.LinkFindingToInvestigationWithProvenance(
		b, 102, LinkMethodManual, "alice@x",
	); err != nil {
		t.Fatalf("manual link: %v", err)
	}

	got, err := st.DashboardInvestigationStats(5)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if got.Active != 2 {
		t.Errorf("active = %d, want 2", got.Active)
	}
	if got.Closed24h != 1 {
		t.Errorf("closed_24h = %d, want 1", got.Closed24h)
	}
	if got.AutolinkedFindings24h != 1 {
		t.Errorf("autolinked_24h = %d, want 1 (autolink only, not manual)", got.AutolinkedFindings24h)
	}

	// Recent active list — newest first; should NOT include the
	// closed case C, must include both A and B.
	if len(got.RecentActive) != 2 {
		t.Fatalf("recent_active len = %d, want 2", len(got.RecentActive))
	}
	titles := map[string]int{}
	for _, it := range got.RecentActive {
		titles[it.Title]++
	}
	if titles["A"] != 1 || titles["B"] != 1 {
		t.Errorf("recent_active should include A and B, got %+v", got.RecentActive)
	}
	if titles["C"] != 0 {
		t.Errorf("recent_active must NOT include closed case C, got %+v", got.RecentActive)
	}

	// finding_count should be 1 on case A (autolinked finding 101).
	for _, it := range got.RecentActive {
		if it.Title == "A" && it.FindingCount != 1 {
			t.Errorf("case A finding_count = %d, want 1", it.FindingCount)
		}
		if it.Title == "B" && it.FindingCount != 1 {
			t.Errorf("case B finding_count = %d, want 1", it.FindingCount)
		}
	}
}
