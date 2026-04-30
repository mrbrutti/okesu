package db

import (
	"testing"
)

// mustInsertOrchestrationRun seeds a minimal orchestration + run pair
// and returns the run id. Used by step-level tests that just need a
// valid foreign key to point at.
func mustInsertOrchestrationRun(t *testing.T, s *Store) int64 {
	t.Helper()
	orchID, err := s.CreateOrchestration(
		"test-orch", "", "name: test-orch", "manual", "", "", 0,
	)
	if err != nil {
		t.Fatalf("create orchestration: %v", err)
	}
	runID, err := s.CreateOrchestrationRun(orchID, "manual", "", 0)
	if err != nil {
		t.Fatalf("create orchestration run: %v", err)
	}
	return runID
}

func TestOrchestrationStep_PromptEntitiesRoundTrip(t *testing.T) {
	s := openTempStore(t)
	runID := mustInsertOrchestrationRun(t, s)
	in := OrchestrationStepInsert{
		OrchestrationRunID: runID,
		StepID:             "triage",
		StepIdx:            0,
		Status:             "running",
		PromptEntities:     `{"refs":[{"kind":"finding","id":42}]}`,
	}
	if err := s.UpsertOrchestrationStep(in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, err := s.ListOrchestrationSteps(runID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if !got.PromptEntities.Valid || got.PromptEntities.String != in.PromptEntities {
		t.Errorf("PromptEntities = %+v, want %q", got.PromptEntities, in.PromptEntities)
	}
}

func TestOrchestrationStep_PromptEntitiesEmptyIsNull(t *testing.T) {
	s := openTempStore(t)
	runID := mustInsertOrchestrationRun(t, s)
	in := OrchestrationStepInsert{
		OrchestrationRunID: runID,
		StepID:             "no-entities",
		StepIdx:            0,
		Status:             "running",
	}
	if err := s.UpsertOrchestrationStep(in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, _ := s.ListOrchestrationSteps(runID)
	if rows[0].PromptEntities.Valid {
		t.Errorf("expected NULL, got %q", rows[0].PromptEntities.String)
	}
}
