package db

import (
	"testing"
	"time"
)

// makeRunForFanout creates a minimal orchestration_run row so the
// node-dispatch FK has something to point at. Returns the run id.
func makeRunForFanout(t *testing.T, s *Store) int64 {
	t.Helper()
	// Bare minimum to satisfy NOT NULL constraints. Inserts directly
	// rather than going through CreateOrchestrationRun so we don't
	// also need to build an orchestrations row first — the fanout
	// table FKs orchestration_runs only.
	res, err := s.Exec(`
		INSERT INTO orchestrations (name, spec_yaml, trigger_kind, enabled, created_at, updated_at)
		VALUES ('t-orch', '---\nname: t\n---', 'manual', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("seed orchestration: %v", err)
	}
	orchID, _ := res.LastInsertId()
	runID, err := s.CreateOrchestrationRun(orchID, "manual", "{}", 0)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return runID
}

func TestStepNodeDispatch_InsertAndList(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()

	if err := s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID:     runID,
		StepID:    "step-1",
		Host:      "web-prod-01",
		Status:    "running",
		StartedAt: &now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID:     runID,
		StepID:    "step-1",
		Host:      "web-prod-02",
		Status:    "running",
		StartedAt: &now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.ListStepNodeDispatchesByRun(runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2", len(got))
	}
	if got[0].Host != "web-prod-01" || got[1].Host != "web-prod-02" {
		t.Errorf("List host order = [%q,%q], want sorted by host", got[0].Host, got[1].Host)
	}
	if got[0].Status != "running" {
		t.Errorf("Status = %q, want running", got[0].Status)
	}
}

func TestStepNodeDispatch_UpdateOnReturn(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	start := time.Now().UTC()
	end := start.Add(2 * time.Second)

	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &start,
	})
	if err := s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID:         runID,
		StepID:        "step-1",
		Host:          "h1",
		Status:        "completed",
		AgentRunID:    "run-abc",
		FindingsCount: 3,
		OutputTail:    "ok\n",
		EndedAt:       &end,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Status != "completed" || got[0].FindingsCount != 3 || got[0].AgentRunID.String != "run-abc" {
		t.Errorf("after update: %+v", got[0])
	}
	if !got[0].EndedAt.Valid {
		t.Errorf("EndedAt not set")
	}
}

func TestStepNodeDispatch_UpdateMarksFailure(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})
	end := now.Add(time.Second)
	_ = s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID:   runID,
		StepID:  "step-1",
		Host:    "h1",
		Status:  "failed",
		Error:   "timeout after 30s",
		EndedAt: &end,
	})
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if got[0].Status != "failed" || got[0].Error.String != "timeout after 30s" {
		t.Errorf("after fail: %+v", got[0])
	}
}

func TestStepNodeDispatch_CascadesOnRunDelete(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})

	// Delete the run row directly. CASCADE should drop the dispatch row.
	if _, err := s.Exec(`DELETE FROM orchestration_runs WHERE id = ?`, runID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 0 {
		t.Errorf("expected cascade to drop rows, still have %d", len(got))
	}
}

func TestStepNodeDispatch_ReconcileOrphans(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	// Two rows still in `running` (engine restarted mid-fanout) and one
	// already settled — only the running ones should be reconciled.
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h2",
		Status: "running", StartedAt: &now,
	})
	end := now.Add(time.Second)
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h3",
		Status: "completed", StartedAt: &now,
	})
	_ = s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID: runID, StepID: "step-1", Host: "h3",
		Status: "completed", EndedAt: &end,
	})

	if err := s.ReconcileStepNodeDispatchesForRun(runID, "run cancelled"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	for _, r := range got {
		switch r.Host {
		case "h1", "h2":
			if r.Status != "failed" || r.Error.String != "run cancelled" || !r.EndedAt.Valid {
				t.Errorf("%s: expected failed/cancelled, got %+v", r.Host, r)
			}
		case "h3":
			if r.Status != "completed" {
				t.Errorf("h3: expected unchanged completed, got %+v", r)
			}
		}
	}
}

func TestStepNodeDispatch_InsertIsIdempotent(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	start := time.Now().UTC()
	end := start.Add(time.Second)

	// First dispatch: starts, gets a result written via Update.
	if err := s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &start,
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := s.UpdateStepNodeDispatch(StepNodeDispatchUpdate{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "failed", Error: "timeout", EndedAt: &end,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Engine retry / fan-out replay: re-insert same key. Must not
	// error, must reset ended_at + error so the row reads as a fresh
	// dispatch in progress.
	if err := s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &start,
	}); err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Status != "running" {
		t.Errorf("status after re-insert = %q, want running", got[0].Status)
	}
	if got[0].EndedAt.Valid {
		t.Errorf("ended_at should be NULL after re-insert, got %v", got[0].EndedAt.Time)
	}
	if got[0].Error.Valid {
		t.Errorf("error should be NULL after re-insert, got %q", got[0].Error.String)
	}
}

func TestFinishOrchestrationRun_ReconcilesFanoutRows(t *testing.T) {
	s := openTempStore(t)
	runID := makeRunForFanout(t, s)
	now := time.Now().UTC()
	_ = s.InsertStepNodeDispatch(StepNodeDispatchInsert{
		RunID: runID, StepID: "step-1", Host: "h1",
		Status: "running", StartedAt: &now,
	})

	if err := s.FinishOrchestrationRun(runID, "cancelled", "operator cancelled"); err != nil {
		t.Fatalf("FinishOrchestrationRun: %v", err)
	}
	got, _ := s.ListStepNodeDispatchesByRun(runID)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Status != "failed" {
		t.Errorf("expected fanout row reconciled to failed, got %s", got[0].Status)
	}
	if got[0].Error.String != "operator cancelled" {
		t.Errorf("expected reason to propagate, got %q", got[0].Error.String)
	}
}
