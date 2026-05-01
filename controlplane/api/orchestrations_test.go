package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func chiCtxWithIDParam(id string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
}

func TestOrchestrationRunDetail_HydratesPerNode(t *testing.T) {
	store := newTestStore(t)
	// Seed orchestration + run + step + per-host dispatch rows.
	res, err := store.Exec(`INSERT INTO orchestrations
		(name, spec_yaml, trigger_kind, enabled, created_at, updated_at)
		VALUES ('fan', '---\nname: fan\n---', 'manual', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("seed orchestration: %v", err)
	}
	oid, _ := res.LastInsertId()
	runID, err := store.CreateOrchestrationRun(oid, "manual", "{}", 0)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if err := store.UpsertOrchestrationStep(db.OrchestrationStepInsert{
		OrchestrationRunID: runID,
		StepID:             "scan",
		StepIdx:            0,
		Status:             "completed",
	}); err != nil {
		t.Fatalf("seed step: %v", err)
	}
	now := time.Now().UTC()
	end := now.Add(time.Second)
	_ = store.InsertStepNodeDispatch(db.StepNodeDispatchInsert{
		RunID: runID, StepID: "scan", Host: "h1",
		Status: "running", StartedAt: &now,
	})
	_ = store.UpdateStepNodeDispatch(db.StepNodeDispatchUpdate{
		RunID: runID, StepID: "scan", Host: "h1",
		Status: "completed", AgentRunID: "agent-r1",
		FindingsCount: 2, OutputTail: "ok\n",
		EndedAt: &end,
	})
	_ = store.InsertStepNodeDispatch(db.StepNodeDispatchInsert{
		RunID: runID, StepID: "scan", Host: "h2",
		Status: "running", StartedAt: &now,
	})
	_ = store.UpdateStepNodeDispatch(db.StepNodeDispatchUpdate{
		RunID: runID, StepID: "scan", Host: "h2",
		Status: "failed", Error: "timeout",
		EndedAt: &end,
	})

	r := httptest.NewRequest("GET", fmt.Sprintf("/api/orchestration-runs/%d", runID), nil)
	r = r.WithContext(chiCtxWithIDParam(fmt.Sprintf("%d", runID)))
	w := httptest.NewRecorder()
	OrchestrationRunDetail(store).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Steps []struct {
			StepID  string `json:"step_id"`
			PerNode []struct {
				Host          string `json:"host"`
				Status        string `json:"status"`
				AgentRunID    string `json:"agent_run_id,omitempty"`
				FindingsCount int    `json:"findings_count"`
				Error         string `json:"error,omitempty"`
			} `json:"per_node,omitempty"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — body: %s", err, w.Body.String())
	}
	if len(got.Steps) != 1 {
		t.Fatalf("steps = %d", len(got.Steps))
	}
	pn := got.Steps[0].PerNode
	if len(pn) != 2 {
		t.Fatalf("per_node = %d, want 2 — body: %s", len(pn), w.Body.String())
	}
	if pn[0].Host != "h1" || pn[0].Status != "completed" || pn[0].FindingsCount != 2 {
		t.Errorf("h1 = %+v", pn[0])
	}
	if pn[1].Host != "h2" || pn[1].Status != "failed" || pn[1].Error != "timeout" {
		t.Errorf("h2 = %+v", pn[1])
	}
}

// TestOrchestrationStepView_PromptEntitiesWireShape verifies that the
// JSON-encoded prompt_entities side-channel from the DB is surfaced
// on the wire as a structured object (not a re-escaped string), so
// the browser can consume it directly without a nested JSON.parse.
func TestOrchestrationStepView_PromptEntitiesWireShape(t *testing.T) {
	run := &db.OrchestrationRun{
		ID:          1,
		Status:      "running",
		TriggerKind: "manual",
		StartedAt:   time.Now().UTC(),
	}
	st := &db.OrchestrationStep{
		StepID:         "triage",
		Status:         "running",
		PromptEntities: sql.NullString{String: `{"refs":[{"kind":"finding","id":42,"snapshot":{"severity":"HIGH"},"literal_hash":"abc1234567890abc"}]}`, Valid: true},
	}
	out := toOrchestrationRunJSON(run, []*db.OrchestrationStep{st}, nil)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"prompt_entities":{"refs":`) {
		t.Errorf("missing prompt_entities in wire shape: %s", b)
	}
}

// TestOrchestrationStepView_PromptEntitiesOmittedWhenNull verifies
// that omitempty on json.RawMessage correctly suppresses the field
// when the DB column is NULL.
func TestOrchestrationStepView_PromptEntitiesOmittedWhenNull(t *testing.T) {
	run := &db.OrchestrationRun{
		ID:          1,
		Status:      "running",
		TriggerKind: "manual",
		StartedAt:   time.Now().UTC(),
	}
	st := &db.OrchestrationStep{StepID: "triage", Status: "running"}
	out := toOrchestrationRunJSON(run, []*db.OrchestrationStep{st}, nil)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"prompt_entities"`) {
		t.Errorf("prompt_entities should be omitted when null: %s", b)
	}
}
