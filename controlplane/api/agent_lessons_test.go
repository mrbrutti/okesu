package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestListAgentLessons_HTTP(t *testing.T) {
	st := newTestStore(t)
	if err := st.RecordAgentLesson("investigator", "be specific", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAgentLesson("investigator", "validate first", 0, ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/agents/investigator/lessons", nil)
	rec := httptest.NewRecorder()
	ListAgentLessonsHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp []db.AgentLesson
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 2 {
		t.Errorf("expected 2 lessons; got %d", len(resp))
	}
	if resp[0].Text != "validate first" {
		t.Errorf("expected newest first; got %q", resp[0].Text)
	}
}

func TestListAgentLessons_HTTP_RejectsEmptyName(t *testing.T) {
	st := newTestStore(t)
	req := httptest.NewRequest("GET", "/api/agents//lessons", nil)
	rec := httptest.NewRecorder()
	ListAgentLessonsHandler(st)(rec, req)
	if rec.Code != 400 {
		t.Errorf("expected 400 for empty name; got %d", rec.Code)
	}
}
