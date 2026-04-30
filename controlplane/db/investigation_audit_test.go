package db

import (
	"testing"
)

// TestListInvestigationAudit walks a case through every event kind
// and verifies the timeline returns them in chronological order with
// the right `kind`, `by`, and structured details.
func TestListInvestigationAudit(t *testing.T) {
	st := openTempStore(t)

	// Seed: case + a finding row + a note + an orchestration_run row,
	// then link the finding (with provenance) and the run.
	caseA, err := st.CreateInvestigation(&InvestigationInsert{
		Title:     "case A",
		CreatedBy: "alice@x",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	if _, err := st.Exec(`INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, raw_json)
		VALUES (101, 1, 1, 'HIGH', 'a', '{}')`); err != nil {
		t.Fatalf("seed finding: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO orchestrations (id, name, spec_yaml) VALUES (1, 'orch', 'name: orch')`); err != nil {
		t.Fatalf("seed orch: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO orchestration_runs (id, orchestration_id, status, trigger_kind)
		VALUES (1, 1, 'completed', 'manual')`); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if err := st.LinkFindingToInvestigationWithProvenance(
		caseA, 101, LinkMethodManual, "alice@x",
	); err != nil {
		t.Fatalf("link finding: %v", err)
	}
	if err := st.LinkRunToInvestigation(caseA, 1); err != nil {
		t.Fatalf("link run: %v", err)
	}
	if _, err := st.AddInvestigationNote(caseA, "alice@x", "first note"); err != nil {
		t.Fatalf("add note: %v", err)
	}

	got, err := st.ListInvestigationAudit(caseA)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	// Expect 4 events: created + finding_linked + run_linked + note.
	// (Case isn't closed yet, so no closed event.)
	if len(got) < 4 {
		t.Fatalf("expected ≥4 events, got %d: %+v", len(got), got)
	}
	kinds := map[string]int{}
	for _, e := range got {
		kinds[e.Kind]++
	}
	for _, want := range []string{"created", "finding_linked", "run_linked", "note"} {
		if kinds[want] != 1 {
			t.Errorf("expected exactly 1 %s event, got %d", want, kinds[want])
		}
	}

	// Provenance threaded through: finding_linked event carries
	// method=manual + linked_by=alice@x.
	for _, e := range got {
		if e.Kind != "finding_linked" {
			continue
		}
		if e.By != "alice@x" {
			t.Errorf("finding_linked.by = %q, want alice@x", e.By)
		}
		if m, _ := e.Details["method"].(string); m != "manual" {
			t.Errorf("finding_linked.details.method = %v, want manual", e.Details["method"])
		}
		if id, _ := e.Details["finding_id"].(int64); id != 101 {
			t.Errorf("finding_linked.details.finding_id = %v, want 101", e.Details["finding_id"])
		}
	}

	// Chronological order: created should be first.
	if got[0].Kind != "created" {
		t.Errorf("first event = %q, want created", got[0].Kind)
	}

	// Close the case → another sweep returns 5 events including a
	// "closed" with the resolution.
	if err := st.UpdateInvestigation(caseA, &InvestigationUpdate{
		Status:     "closed",
		Resolution: "resolved",
	}); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, _ = st.ListInvestigationAudit(caseA)
	closed := 0
	for _, e := range got {
		if e.Kind == "closed" {
			closed++
			if r, _ := e.Details["resolution"].(string); r != "resolved" {
				t.Errorf("closed.details.resolution = %v, want resolved", e.Details["resolution"])
			}
		}
	}
	if closed != 1 {
		t.Errorf("expected 1 closed event after update, got %d", closed)
	}
}
