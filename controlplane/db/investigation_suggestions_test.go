package db

import (
	"testing"
	"time"
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

// TestSuggestionSettings_DefaultsAndOverride — settings round-trip,
// missing/zero fields fall back to defaults, persisted overrides
// take effect at query time.
func TestSuggestionSettings_DefaultsAndOverride(t *testing.T) {
	st := openTempStore(t)

	// First read with no row → defaults.
	got, err := st.GetSuggestionSettings()
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if got.Threshold != DefaultSuggestionThreshold {
		t.Errorf("expected default threshold %d, got %d",
			DefaultSuggestionThreshold, got.Threshold)
	}
	if got.Weights[SignalDedupKey] != 100 {
		t.Errorf("expected dedup_key=100, got %d", got.Weights[SignalDedupKey])
	}
	if got.Weights[SignalIOCCrossCP] != 100 {
		t.Errorf("expected ioc_cross_cp=100 default, got %d", got.Weights[SignalIOCCrossCP])
	}

	// Persist a partial override (just threshold) — defaults should
	// fill in the rest.
	if err := st.SetSuggestionSettings(SuggestionSettings{Threshold: 50}); err != nil {
		t.Fatalf("set partial: %v", err)
	}
	got2, _ := st.GetSuggestionSettings()
	if got2.Threshold != 50 {
		t.Errorf("expected persisted threshold 50, got %d", got2.Threshold)
	}
	if got2.Weights[SignalDedupKey] != 100 {
		t.Errorf("expected dedup_key default to fill in, got %d", got2.Weights[SignalDedupKey])
	}
}

// TestSuggestFindings_RespectsThreshold — a higher persisted
// threshold filters out weaker signals (single daimon_sev hit at
// weight 30 with threshold 50 should be hidden).
func TestSuggestFindings_RespectsThreshold(t *testing.T) {
	st := openTempStore(t)
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "A"})

	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	// 101 (linked) and 102 share daimon+severity only — score = 30.
	// Use a recent ts so the daimon_sev 24h window catches them.
	now := time.Now().UnixMilli()
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, agent, severity, title, raw_json)
		VALUES
		  (101, 1, ?, 'edr', 'HIGH', 'a', '{}'),
		  (102, 1, ?, 'edr', 'HIGH', 'b', '{}')`,
		now-1000, now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = st.LinkFindingToInvestigation(caseA, 101)

	// Default threshold (30) → 102 surfaces.
	got, _ := st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if len(got) != 1 {
		t.Fatalf("baseline expected 1, got %+v", got)
	}

	// Bump persisted threshold to 50 → 102 hidden (score 30 < 50).
	if err := st.SetSuggestionSettings(SuggestionSettings{Threshold: 50}); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
	got, _ = st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if len(got) != 0 {
		t.Fatalf("expected [] with threshold 50, got %+v", got)
	}
}

// TestSuggestFindings_IOCCrossCPSignal — the cross-CP IOC signal
// only fires when the matched IOC has at least
// IOCCrossCPMinObservations observations within the window.
func TestSuggestFindings_IOCCrossCPSignal(t *testing.T) {
	st := openTempStore(t)
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "A"})

	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, raw_json)
		VALUES
		  (201, 1, 1000, 'HIGH', 'linked',  '{}'),
		  (202, 1, 2000, 'HIGH', 'related', '{}')`); err != nil {
		t.Fatalf("seed findings: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO iocs (id, kind, normalized_value, severity_floor, value, source, first_seen, last_seen)
		VALUES (1, 'sha256', 'abc', 'HIGH', 'abc', 'test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed ioc: %v", err)
	}
	// Two observations on the IOC: linked+related findings. 2 < default
	// IOCCrossCPMinObservations (3), so the cross-CP signal should NOT
	// fire — only the regular `ioc` signal at weight 80.
	if _, err := st.Exec(`
		INSERT INTO ioc_observations (ioc_id, finding_id, observed_at)
		VALUES (1, 201, CURRENT_TIMESTAMP), (1, 202, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed obs: %v", err)
	}
	_ = st.LinkFindingToInvestigation(caseA, 201)

	got, err := st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) != 1 || got[0].ID != 202 {
		t.Fatalf("expected [202], got %+v", got)
	}
	for _, sig := range got[0].Signals {
		if sig == SignalIOCCrossCP {
			t.Errorf("ioc_cross_cp signal should NOT fire with only 2 observations, signals=%v", got[0].Signals)
		}
	}

	// Add a 3rd observation → cross-CP signal fires now.
	if _, err := st.Exec(`
		INSERT INTO ioc_observations (ioc_id, finding_id, observed_at)
		VALUES (1, 202, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed extra obs: %v", err)
	}
	got, _ = st.SuggestFindingsForInvestigation(caseA, 0, 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %+v", got)
	}
	hasCrossCP := false
	for _, sig := range got[0].Signals {
		if sig == SignalIOCCrossCP {
			hasCrossCP = true
		}
	}
	if !hasCrossCP {
		t.Errorf("expected ioc_cross_cp signal at obs_count=3, signals=%v", got[0].Signals)
	}
	// Score should now be ioc(80) + ioc_cross_cp(100) = 180.
	if got[0].Score != 180 {
		t.Errorf("expected score 180 (ioc + ioc_cross_cp), got %d", got[0].Score)
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
