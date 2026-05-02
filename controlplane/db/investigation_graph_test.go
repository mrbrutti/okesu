package db

import (
	"database/sql"
	"testing"
)

func TestInvestigationGraph_EmptyCase(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetInvestigationGraphData(invID, 20)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d, want 0", got.TotalFindings)
	}
	if len(got.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(got.Findings))
	}
}

func TestInvestigationGraph_TopByCount(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Insert 25 findings: 5 CRITICAL, 20 LOW.
	for i := 0; i < 5; i++ {
		fid := mustInsertGraphFinding(t, s, "CRITICAL", "host1", "edr-agent", int64(1000+i))
		mustLinkGraphFinding(t, s, invID, fid)
	}
	for i := 0; i < 20; i++ {
		fid := mustInsertGraphFinding(t, s, "LOW", "host2", "compliance", int64(500+i))
		mustLinkGraphFinding(t, s, invID, fid)
	}

	got, err := s.GetInvestigationGraphData(invID, 20)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.TotalFindings != 25 {
		t.Errorf("TotalFindings = %d, want 25", got.TotalFindings)
	}
	if got.LimitApplied != 20 {
		t.Errorf("LimitApplied = %d, want 20", got.LimitApplied)
	}
	if len(got.Findings) != 20 {
		t.Errorf("Findings = %d, want 20", len(got.Findings))
	}
	// Severity order: top 5 must all be CRITICAL.
	for i := 0; i < 5; i++ {
		if got.Findings[i].Severity != "CRITICAL" {
			t.Errorf("Findings[%d].Severity = %q, want CRITICAL", i, got.Findings[i].Severity)
		}
	}
}

func TestInvestigationGraph_LimitClampedTo100(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetInvestigationGraphData(invID, 9999)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.LimitApplied != 100 {
		t.Errorf("LimitApplied = %d, want 100 (clamped)", got.LimitApplied)
	}
}

func TestInvestigationGraph_LimitFloorIsOne(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetInvestigationGraphData(invID, 0)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if got.LimitApplied != 20 { // 0 → default
		t.Errorf("LimitApplied = %d, want 20 (default)", got.LimitApplied)
	}
}

// mustInsertGraphFinding wraps Store.InsertFinding with the minimal
// fields needed by the graph helper (severity / host / agent / ts).
// Locally named to avoid collision with helpers in other test files.
// Findings have a FK on event_id, so we insert a parent event per call.
func mustInsertGraphFinding(t *testing.T, s *Store, severity, host, agent string, ts int64) int64 {
	t.Helper()
	eventID, err := s.InsertEvent(&Event{
		Ts:      ts,
		Type:    "finding",
		Agent:   sql.NullString{String: agent, Valid: agent != ""},
		RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	id, err := s.InsertFinding(&FindingInsert{
		EventID:  eventID,
		Ts:       ts,
		Agent:    agent,
		Host:     host,
		Severity: severity,
		Title:    "test finding",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	return id
}

// mustLinkGraphFinding wraps the 2-arg LinkFindingToInvestigation
// (records link_method=manual, linked_by=NULL).
func mustLinkGraphFinding(t *testing.T, s *Store, invID, findingID int64) {
	t.Helper()
	if err := s.LinkFindingToInvestigation(invID, findingID); err != nil {
		t.Fatalf("LinkFindingToInvestigation: %v", err)
	}
}
