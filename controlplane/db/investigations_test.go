package db

import (
	"testing"
)

func TestCreateInvestigation_InsertsRow(t *testing.T) {
	s := openTempStore(t)
	id, err := s.CreateInvestigation(&InvestigationInsert{
		Title:     "C2 callbacks from web-prod-01",
		CreatedBy: "alice@example.com",
		Summary:   "Initial sighting at 14:32 UTC",
	})
	if err != nil {
		t.Fatalf("CreateInvestigation: %v", err)
	}
	if id == 0 {
		t.Errorf("expected non-zero id")
	}
	got, err := s.GetInvestigation(id)
	if err != nil {
		t.Fatalf("GetInvestigation: %v", err)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if got.Title != "C2 callbacks from web-prod-01" {
		t.Errorf("Title mismatch: %+v", got)
	}
}

func TestUpdateInvestigation_TransitionsToClosed(t *testing.T) {
	s := openTempStore(t)
	id, _ := s.CreateInvestigation(&InvestigationInsert{Title: "test"})
	err := s.UpdateInvestigation(id, &InvestigationUpdate{
		Status:     "closed",
		Resolution: "resolved",
	})
	if err != nil {
		t.Fatalf("UpdateInvestigation: %v", err)
	}
	got, _ := s.GetInvestigation(id)
	if got.Status != "closed" || got.Resolution != "resolved" {
		t.Errorf("after close: %+v", got)
	}
	if got.ClosedAt == 0 {
		t.Errorf("expected ClosedAt to be set on close")
	}
}

func TestUpdateInvestigation_RejectsInvalidStatus(t *testing.T) {
	s := openTempStore(t)
	id, _ := s.CreateInvestigation(&InvestigationInsert{Title: "test"})
	if err := s.UpdateInvestigation(id, &InvestigationUpdate{Status: "invalid"}); err == nil {
		t.Errorf("expected error for invalid status")
	}
}

func TestUpdateInvestigation_RejectsResolutionOnActive(t *testing.T) {
	s := openTempStore(t)
	id, _ := s.CreateInvestigation(&InvestigationInsert{Title: "test"})
	if err := s.UpdateInvestigation(id, &InvestigationUpdate{Resolution: "resolved"}); err == nil {
		t.Errorf("expected error for resolution without status=closed")
	}
}

func TestLinkFindingToInvestigation_Idempotent(t *testing.T) {
	s := openTempStore(t)
	id, _ := s.CreateInvestigation(&InvestigationInsert{Title: "test"})
	// Need a real finding to FK against.
	res, _ := s.Exec(`INSERT INTO events (ts, type, agent, raw_json) VALUES (1, 'finding', 't', '{}')`)
	eventID, _ := res.LastInsertId()
	findingID, _ := s.InsertFinding(&FindingInsert{
		EventID: eventID, Ts: 1, Title: "x", Severity: "MEDIUM",
	})
	if err := s.LinkFindingToInvestigation(id, findingID); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkFindingToInvestigation(id, findingID); err != nil {
		t.Errorf("expected idempotent; got %v", err)
	}
	findings, _ := s.ListFindingsForInvestigation(id)
	if len(findings) != 1 {
		t.Errorf("expected 1 linked finding; got %d", len(findings))
	}
}

func TestAddInvestigationNote_AppendsAndLists(t *testing.T) {
	s := openTempStore(t)
	id, _ := s.CreateInvestigation(&InvestigationInsert{Title: "test"})
	if _, err := s.AddInvestigationNote(id, "alice@example.com", "first note"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddInvestigationNote(id, "bob@example.com", "second note"); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.ListInvestigationNotes(id)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes; got %d", len(notes))
	}
	if notes[0].Body != "second note" {
		t.Errorf("expected newest first; got %q", notes[0].Body)
	}
}
