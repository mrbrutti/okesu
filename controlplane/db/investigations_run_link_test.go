package db

import "testing"

// TestLinkRunToInvestigationsForFinding_LinksToAllInvestigations
// confirms the auto-thread step fired by the engine's
// link_run_to_finding action lights up every case the finding is
// currently in. Operators who manually re-run an orchestration on
// a case-tracked finding now see the run in the workspace's Runs
// tab without an extra click.
func TestLinkRunToInvestigationsForFinding_LinksToAllInvestigations(t *testing.T) {
	st := openTempStore(t)

	// Two cases that both reference the same finding.
	caseA, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case A"})
	caseB, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case B"})

	// Seed an orchestration + a run + a finding to satisfy FKs.
	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("event seed: %v", err)
	}
	if _, err := st.Exec(`INSERT INTO findings (id, event_id, ts, severity, title, raw_json) VALUES (101, 1, 1, 'INFO', 'seed', '{}')`); err != nil {
		t.Fatalf("finding seed: %v", err)
	}
	// Orchestration + run rows. Field set is minimal — the FK
	// targets are id and orchestration_id only.
	if _, err := st.Exec(`INSERT INTO orchestrations (id, name, description, spec_yaml) VALUES (10, 'o', 'd', '---')`); err != nil {
		t.Fatalf("orch seed: %v", err)
	}
	if _, err := st.Exec(`INSERT INTO orchestration_runs (id, orchestration_id, status, trigger_kind) VALUES (200, 10, 'completed', 'manual')`); err != nil {
		t.Fatalf("run seed: %v", err)
	}

	// Both cases reference the finding.
	_ = st.LinkFindingToInvestigation(caseA, 101)
	_ = st.LinkFindingToInvestigation(caseB, 101)

	n, err := st.LinkRunToInvestigationsForFinding(200, 101)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if n != 2 {
		t.Fatalf("linked count = %d, want 2", n)
	}

	// Re-running is a no-op.
	n, err = st.LinkRunToInvestigationsForFinding(200, 101)
	if err != nil {
		t.Fatalf("idempotent link: %v", err)
	}
	if n != 0 {
		t.Errorf("second call linked %d, want 0 (idempotency)", n)
	}

	// Both cases should now show the run.
	runs, err := st.ListRunsForInvestigationEnriched(caseA)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != 200 {
		t.Errorf("case A runs = %+v; want one row id=200", runs)
	}
}

// TestLinkRunToInvestigationsForFinding_NoCases is silent — a
// finding outside any case shouldn't error, just link 0 rows.
func TestLinkRunToInvestigationsForFinding_NoCases(t *testing.T) {
	st := openTempStore(t)

	// Minimal seeds for FK satisfaction (no case linked).
	st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`)
	st.Exec(`INSERT INTO findings (id, event_id, ts, severity, title, raw_json) VALUES (101, 1, 1, 'INFO', 'seed', '{}')`)
	st.Exec(`INSERT INTO orchestrations (id, name, description, spec_yaml) VALUES (10, 'o', 'd', '---')`)
	st.Exec(`INSERT INTO orchestration_runs (id, orchestration_id, status, trigger_kind) VALUES (200, 10, 'completed', 'manual')`)

	n, err := st.LinkRunToInvestigationsForFinding(200, 101)
	if err != nil {
		t.Fatalf("expected nil error when no cases linked, got: %v", err)
	}
	if n != 0 {
		t.Errorf("linked %d, want 0", n)
	}
}
