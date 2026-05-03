package db

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestInvestigationNoteDraft_GetEmpty(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = s.GetInvestigationNoteDraft(invID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("got %v, want sql.ErrNoRows", err)
	}
}

func TestInvestigationNoteDraft_UpsertGet(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpsertInvestigationNoteDraft(invID, []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetInvestigationNoteDraft(invID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string([]byte{1, 2, 3, 4}) {
		t.Errorf("got %v, want [1 2 3 4]", got)
	}
}

func TestInvestigationNoteDraft_UpsertReplaces(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpsertInvestigationNoteDraft(invID, []byte{0xaa}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.UpsertInvestigationNoteDraft(invID, []byte{0xbb}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, err := s.GetInvestigationNoteDraft(invID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got[0] != 0xbb {
		t.Errorf("got %x, want bb (replace failed)", got[0])
	}
}

func TestInvestigationNoteDraft_Delete(t *testing.T) {
	s := openTempStore(t)
	invID, err := s.CreateInvestigation(&InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpsertInvestigationNoteDraft(invID, []byte{1}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.DeleteInvestigationNoteDraft(invID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = s.GetInvestigationNoteDraft(invID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("after delete: got %v, want sql.ErrNoRows", err)
	}
	// Idempotent: delete again should not error
	if err := s.DeleteInvestigationNoteDraft(invID); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestInvestigationNoteDraft_SweepRespectsLiveSet(t *testing.T) {
	s := openTempStore(t)
	stale, _ := s.CreateInvestigation(&InvestigationInsert{Title: "stale"})
	live, _ := s.CreateInvestigation(&InvestigationInsert{Title: "live"})
	young, _ := s.CreateInvestigation(&InvestigationInsert{Title: "young"})
	if err := s.UpsertInvestigationNoteDraft(stale, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertInvestigationNoteDraft(live, []byte{2}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertInvestigationNoteDraft(young, []byte{3}); err != nil {
		t.Fatal(err)
	}
	// Make stale and live both 8 days old; young stays new.
	cutoff := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(rfc3339)
	if _, err := s.Exec(`UPDATE investigation_note_drafts SET updated_at=? WHERE investigation_id IN (?, ?)`, cutoff, stale, live); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.SweepStaleInvestigationNoteDrafts(7*24*time.Hour, []int64{live})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	// stale gone, live + young preserved
	if _, err := s.GetInvestigationNoteDraft(live); err != nil {
		t.Errorf("live gone: %v", err)
	}
	if _, err := s.GetInvestigationNoteDraft(young); err != nil {
		t.Errorf("young gone: %v", err)
	}
	if _, err := s.GetInvestigationNoteDraft(stale); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("stale not deleted: got err=%v", err)
	}
}
