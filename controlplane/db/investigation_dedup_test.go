package db

import (
	"database/sql"
	"errors"
	"testing"
)

func TestUpsertInvestigationByExternalKey_CreatesAndReturnsExisting(t *testing.T) {
	st := openTempStore(t)

	// First call → creates.
	inv1, created1, err := st.UpsertInvestigationByExternalKey(
		"cross-cp-pattern:42",
		"Cross-CP IOC pattern: sha256 abc",
		"Observed 5 times.",
		"cross-cp-pattern-investigator",
	)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if !created1 {
		t.Fatalf("expected created=true on first upsert")
	}
	if inv1.ID == 0 {
		t.Fatalf("expected non-zero id")
	}
	if inv1.ExternalKey != "cross-cp-pattern:42" {
		t.Fatalf("ExternalKey = %q, want %q", inv1.ExternalKey, "cross-cp-pattern:42")
	}

	// Second call with same key → returns existing, no new row.
	inv2, created2, err := st.UpsertInvestigationByExternalKey(
		"cross-cp-pattern:42",
		"DIFFERENT TITLE — should not overwrite",
		"DIFFERENT SUMMARY — should not overwrite",
		"another-actor",
	)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if created2 {
		t.Fatalf("expected created=false on second upsert")
	}
	if inv2.ID != inv1.ID {
		t.Fatalf("got id %d, want %d (same row)", inv2.ID, inv1.ID)
	}
	// Title + summary preserved from first create — operator edits
	// aren't clobbered by daimon ticks.
	if inv2.Title != inv1.Title {
		t.Fatalf("title mutated: got %q, want %q", inv2.Title, inv1.Title)
	}
	if inv2.Summary != inv1.Summary {
		t.Fatalf("summary mutated: got %q, want %q", inv2.Summary, inv1.Summary)
	}
}

func TestGetInvestigationByExternalKey_EmptyKeyReturnsNoRows(t *testing.T) {
	st := openTempStore(t)
	_, err := st.GetInvestigationByExternalKey("")
	if err == nil {
		t.Fatalf("expected error on empty key, got nil")
	}
	// Specifically sql.ErrNoRows so callers can distinguish "key not
	// supplied" from a transient lookup error and fall back cleanly.
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestUpsertInvestigationByExternalKey_RejectsEmptyKey(t *testing.T) {
	st := openTempStore(t)
	_, _, err := st.UpsertInvestigationByExternalKey("", "title", "summary", "actor")
	if err == nil {
		t.Fatalf("expected error on empty external_key, got nil")
	}
}
