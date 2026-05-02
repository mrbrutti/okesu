package db

import (
	"database/sql"
	"testing"
)

func TestListHostsForInvestigation_EmptyCase(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.ListHostsForInvestigation(invID)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(hosts) = %d, want 0", len(got))
	}
}

func TestListHostsForInvestigation_SortedByCountDesc(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 3 findings on host-a, 1 on host-b, 1 with empty host (excluded).
	for i := 0; i < 3; i++ {
		fid := mustStructInsertFinding(t, s, "HIGH", "host-a", "edr-agent", int64(1000+i))
		mustStructLinkFinding(t, s, invID, fid)
	}
	fid := mustStructInsertFinding(t, s, "HIGH", "host-b", "edr-agent", 2000)
	mustStructLinkFinding(t, s, invID, fid)
	fid = mustStructInsertFinding(t, s, "HIGH", "", "edr-agent", 3000)
	mustStructLinkFinding(t, s, invID, fid)

	got, err := s.ListHostsForInvestigation(invID)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(hosts) = %d, want 2 (empty host excluded)", len(got))
	}
	if got[0].Host != "host-a" || got[0].Count != 3 {
		t.Errorf("got[0] = %+v, want host-a/3", got[0])
	}
	if got[1].Host != "host-b" || got[1].Count != 1 {
		t.Errorf("got[1] = %+v, want host-b/1", got[1])
	}
}

// mustStructInsertFinding wraps Store.InsertFinding for the structure
// tests. Locally named to avoid collision with helpers in
// investigation_graph_test.go and others.
func mustStructInsertFinding(t *testing.T, s *Store, severity, host, agent string, ts int64) int64 {
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
		Title:    "t",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	return id
}

func mustStructLinkFinding(t *testing.T, s *Store, invID, findingID int64) {
	t.Helper()
	if err := s.LinkFindingToInvestigation(invID, findingID); err != nil {
		t.Fatalf("LinkFindingToInvestigation: %v", err)
	}
}

func TestListOrchestrationsForInvestigation_StatusBreakdown(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Seed orchestration row (spec_yaml is NOT NULL in the schema).
	if _, err := s.Exec(`INSERT INTO orchestrations (id, name, spec_yaml) VALUES (10, 'auto-triage', 'name: auto-triage')`); err != nil {
		t.Fatalf("seed orchestration: %v", err)
	}

	// Five orchestration_runs across statuses + link each to the case.
	type runSeed struct {
		id     int64
		status string
	}
	seeds := []runSeed{
		{100, "completed"}, {101, "completed"}, {102, "failed"},
		{103, "cancelled"}, {104, "running"},
	}
	for _, r := range seeds {
		if _, err := s.Exec(`INSERT INTO orchestration_runs (id, orchestration_id, status, trigger_kind) VALUES (?, 10, ?, 'manual')`, r.id, r.status); err != nil {
			t.Fatalf("insert run %d: %v", r.id, err)
		}
		if err := s.LinkRunToInvestigation(invID, r.id); err != nil {
			t.Fatalf("link run %d: %v", r.id, err)
		}
	}

	got, err := s.ListOrchestrationsForInvestigation(invID)
	if err != nil {
		t.Fatalf("ListOrch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(orchs) = %d, want 1", len(got))
	}
	o := got[0]
	if o.RunCount != 5 {
		t.Errorf("RunCount = %d, want 5", o.RunCount)
	}
	if o.Completed != 2 || o.Failed != 1 || o.Cancelled != 1 || o.Running != 1 {
		t.Errorf("status breakdown = %d/%d/%d/%d, want 2/1/1/1",
			o.Completed, o.Failed, o.Cancelled, o.Running)
	}
}
