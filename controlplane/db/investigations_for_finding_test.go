package db

import (
	"testing"
)

// TestListInvestigationsForFinding_ReturnsLinkedCases — sanity
// check for the new lookup the t2-hypothesis-test orchestration
// (PR E) and the finding-detail UI use. Empty result is an empty
// slice (not nil) so JSON consumers see [].
func TestListInvestigationsForFinding_ReturnsLinkedCases(t *testing.T) {
	st := openTempStore(t)

	// Seed two investigations.
	id1, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case A"})
	id2, _ := st.CreateInvestigation(&InvestigationInsert{Title: "case B"})

	// Need a real finding row so the FK on investigation_findings is
	// satisfied. The test uses a minimal valid event/finding shape.
	if _, err := st.Exec(`
		INSERT INTO events (id, ts, type, raw_json) VALUES (1, 1, 'finding', '{}')
	`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := st.Exec(`
		INSERT INTO findings (id, event_id, ts, severity, title, raw_json)
		VALUES (101, 1, 1, 'INFO', 'seed', '{}')
	`); err != nil {
		t.Fatalf("seed finding: %v", err)
	}

	if err := st.LinkFindingToInvestigation(id1, 101); err != nil {
		t.Fatalf("link to id1: %v", err)
	}
	if err := st.LinkFindingToInvestigation(id2, 101); err != nil {
		t.Fatalf("link to id2: %v", err)
	}

	got, err := st.ListInvestigationsForFinding(101)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	// Newest-link-first; id2 was linked second.
	if got[0].ID != id2 || got[1].ID != id1 {
		t.Errorf("ordering wrong: got [%d, %d], want [%d, %d]", got[0].ID, got[1].ID, id2, id1)
	}
}

func TestListInvestigationsForFinding_NoLinksReturnsEmpty(t *testing.T) {
	st := openTempStore(t)
	got, err := st.ListInvestigationsForFinding(99999)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil; want empty slice (JSON callers expect [])")
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}
