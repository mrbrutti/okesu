package db

import (
	"testing"
)

func TestInvestigationPhase_InsertGet(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id, err := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID,
		Name:            "Initial detection",
		StartTs:         1714559400000,
		EndTs:           1714561500000,
		CreatedBy:       "alice@x",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Errorf("got id=0, want non-zero")
	}
	rows, err := s.ListInvestigationPhases(invID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.Name != "Initial detection" {
		t.Errorf("name = %q, want %q", got.Name, "Initial detection")
	}
	if got.StartTs != 1714559400000 || got.EndTs != 1714561500000 {
		t.Errorf("ts = (%d, %d), want (1714559400000, 1714561500000)", got.StartTs, got.EndTs)
	}
	if !got.CreatedBy.Valid || got.CreatedBy.String != "alice@x" {
		t.Errorf("created_by = %v, want alice@x", got.CreatedBy)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt is zero — should be set by CURRENT_TIMESTAMP default")
	}
}

func TestInvestigationPhase_ListSortedByStartTs(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "B-mid", StartTs: 2000, EndTs: 3000, CreatedBy: "x",
	})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "A-early", StartTs: 1000, EndTs: 2000, CreatedBy: "x",
	})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "C-late", StartTs: 3000, EndTs: 4000, CreatedBy: "x",
	})
	rows, err := s.ListInvestigationPhases(invID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A-early", "B-mid", "C-late"}
	for i, r := range rows {
		if r.Name != want[i] {
			t.Errorf("rows[%d].Name = %q, want %q", i, r.Name, want[i])
		}
	}
}

func TestInvestigationPhase_UpdateName(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	id, _ := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "old", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if err := s.UpdateInvestigationPhaseName(invID, id, "new"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInvestigationPhases(invID)
	if rows[0].Name != "new" {
		t.Errorf("name = %q, want %q", rows[0].Name, "new")
	}
}

func TestInvestigationPhase_Delete(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	id, _ := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if err := s.DeleteInvestigationPhase(invID, id); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInvestigationPhases(invID)
	if len(rows) != 0 {
		t.Errorf("after delete: len(rows) = %d, want 0", len(rows))
	}
	// Idempotent: delete again returns nil
	if err := s.DeleteInvestigationPhase(invID, id); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestInvestigationPhase_RejectsBackwardsTimeRange(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, err := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 5000, EndTs: 1000, CreatedBy: "x",
	})
	if err == nil {
		t.Errorf("expected CHECK constraint to reject end_ts < start_ts")
	}
}

func TestInvestigationPhase_RejectsEmptyName(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, err := s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if err == nil {
		t.Errorf("expected CHECK constraint to reject empty name")
	}
}

func TestInvestigationPhase_CascadeOnInvestigationDelete(t *testing.T) {
	s := openTempStore(t)
	invID, _ := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	_, _ = s.InsertInvestigationPhase(&InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	if _, err := s.Exec(`DELETE FROM investigations WHERE id = ?`, invID); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInvestigationPhases(invID)
	if len(rows) != 0 {
		t.Errorf("after parent delete: len(rows) = %d, want 0 (cascade failed)", len(rows))
	}
}
