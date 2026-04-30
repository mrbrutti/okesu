package db

import (
	"testing"
)

// TestSuggestFindings_DedupKeyMatch — most-confident signal.
// Case A has finding 101 with dedup_key "K"; finding 102 has the same
// dedup_key but isn't linked. Expect 102 surfaces with score 100 (the
// dedup_key weight) and signals=[dedup_key].
func TestSuggestFindings_DedupKeyMatch(t *testing.T) {
	st := openTempStore(t)
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case A"})

	// Seed event + two findings with the same dedup_key.
	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, dedup_key, raw_json)
		VALUES
		  (101, 1, 1000, 'HIGH',   'linked',  'K', '{}'),
		  (102, 1, 2000, 'MEDIUM', 'related', 'K', '{}')`); err != nil {
		t.Fatalf("seed findings: %v", err)
	}
	if err := st.LinkFindingToInvestigation(caseA, 101); err != nil {
		t.Fatalf("link: %v", err)
	}

	got, err := st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 suggestion, got %d (%+v)", len(got), got)
	}
	if got[0].ID != 102 {
		t.Errorf("expected finding 102, got %d", got[0].ID)
	}
	if got[0].Score != 100 {
		t.Errorf("expected score 100 (dedup_key weight), got %d", got[0].Score)
	}
	if len(got[0].Signals) != 1 || got[0].Signals[0] != SignalDedupKey {
		t.Errorf("expected signals=[dedup_key], got %v", got[0].Signals)
	}
}

// TestSuggestFindings_ExcludesAlreadyLinked — already-in-case findings
// must never resurface as suggestions. They satisfy every signal
// trivially against their own case.
func TestSuggestFindings_ExcludesAlreadyLinked(t *testing.T) {
	st := openTempStore(t)
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "A"})

	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, dedup_key, raw_json)
		VALUES
		  (101, 1, 1000, 'HIGH', 'a', 'K', '{}'),
		  (102, 1, 2000, 'HIGH', 'b', 'K', '{}')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Both linked. Suggest should return nothing — every match would
	// be self-vs-already-linked, which the WHERE filter excludes.
	_ = st.LinkFindingToInvestigation(caseA, 101)
	_ = st.LinkFindingToInvestigation(caseA, 102)

	got, err := st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 suggestions (both linked), got %+v", got)
	}
}

// TestSuggestFindings_DismissTombstone — once a finding is dismissed
// for a case, it stops surfacing on that case until something un-
// dismisses it (LinkFindingToInvestigation as a side effect).
func TestSuggestFindings_DismissTombstone(t *testing.T) {
	st := openTempStore(t)
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "A"})

	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, dedup_key, raw_json)
		VALUES
		  (101, 1, 1000, 'HIGH', 'a', 'K', '{}'),
		  (102, 1, 2000, 'HIGH', 'b', 'K', '{}')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = st.LinkFindingToInvestigation(caseA, 101)

	// Sanity — 102 is suggested.
	got, _ := st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if len(got) != 1 || got[0].ID != 102 {
		t.Fatalf("baseline: expected [102], got %+v", got)
	}

	// Dismiss → suggestion disappears.
	if err := st.DismissSuggestedFinding(caseA, 102, "tester"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	got, _ = st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if len(got) != 0 {
		t.Fatalf("after dismiss: expected [], got %+v", got)
	}

	// Idempotent dismiss — second call shouldn't error.
	if err := st.DismissSuggestedFinding(caseA, 102, "tester"); err != nil {
		t.Fatalf("dismiss-idempotent: %v", err)
	}

	// Linking lifts the tombstone (Add wins).
	if err := st.LinkFindingToInvestigation(caseA, 102); err != nil {
		t.Fatalf("link: %v", err)
	}
	// 102 is now linked, so it wouldn't surface as a suggestion anyway —
	// but the tombstone row itself must be gone, otherwise an unlink
	// followed by a future score round would still hide it.
	row := st.QueryRow(`SELECT COUNT(*) FROM investigation_finding_dismissals WHERE investigation_id = ? AND finding_id = ?`, caseA, int64(102))
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count tombstones: %v", err)
	}
	if n != 0 {
		t.Errorf("expected tombstone cleared after re-link, got count=%d", n)
	}
}

// TestRelatedCasesForFinding_BasicMatch — the inverse view used by
// the Findings drawer banner. Active cases that share a signal with a
// finding score above threshold; the finding's own already-linked
// cases are excluded.
func TestRelatedCasesForFinding_BasicMatch(t *testing.T) {
	st := openTempStore(t)
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case A"})
	caseB, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case B"})

	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, dedup_key, raw_json)
		VALUES
		  (201, 1, 1000, 'HIGH', 'a', 'K', '{}'),
		  (202, 1, 2000, 'HIGH', 'b', 'K', '{}'),
		  (203, 1, 3000, 'HIGH', 'c', 'K', '{}')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// caseA already has finding 201 (which the lookup is for); caseB
	// has finding 202 with the same dedup_key. Expect caseB to surface.
	_ = st.LinkFindingToInvestigation(caseA, 201)
	_ = st.LinkFindingToInvestigation(caseB, 202)

	got, err := st.ListRelatedCasesForFinding(201, 0, 10)
	if err != nil {
		t.Fatalf("related: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 related case, got %+v", got)
	}
	if got[0].InvestigationID != caseB {
		t.Errorf("expected case B (id=%d), got %d", caseB, got[0].InvestigationID)
	}
	if got[0].Score < DefaultSuggestionThreshold {
		t.Errorf("expected score >= threshold (%d), got %d", DefaultSuggestionThreshold, got[0].Score)
	}
}
