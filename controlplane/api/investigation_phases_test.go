package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
)

func TestInvestigationPhases_Unknown404(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/phases", GetInvestigationPhasesHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/phases", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestInvestigationPhases_EmptyList(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigationForRelayTest(t, store, "case")
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/phases", GetInvestigationPhasesHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestInvestigationPhases_Create(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigationForRelayTest(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/phases", CreateInvestigationPhaseHandler(store))

	body := bytes.NewBufferString(`{"name":"Initial detection","start_ts":1000,"end_ts":2000}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct{ ID int64 `json:"id"` }
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == 0 {
		t.Errorf("id = 0")
	}
	rows, _ := store.ListInvestigationPhases(id)
	if len(rows) != 1 || rows[0].Name != "Initial detection" {
		t.Errorf("rows = %+v, want one named \"Initial detection\"", rows)
	}
}

func TestInvestigationPhases_CreateRejectsEmptyName(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigationForRelayTest(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/phases", CreateInvestigationPhaseHandler(store))

	for _, name := range []string{`""`, `"   "`, `"` + strings.Repeat("x", 101) + `"`} {
		body := bytes.NewBufferString(`{"name":` + name + `,"start_ts":1,"end_ts":2}`)
		req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("name=%s: status = %d, want 400", name, w.Code)
		}
	}
}

func TestInvestigationPhases_CreateRejectsBackwardsTime(t *testing.T) {
	store := newSeededTestStore(t)
	id := mustCreateInvestigationForRelayTest(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/phases", CreateInvestigationPhaseHandler(store))

	body := bytes.NewBufferString(`{"name":"x","start_ts":5000,"end_ts":1000}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/phases", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestInvestigationPhases_Update(t *testing.T) {
	store := newSeededTestStore(t)
	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	phaseID, _ := store.InsertInvestigationPhase(&db.InvestigationPhaseInsert{
		InvestigationID: invID, Name: "old", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	router := chi.NewRouter()
	router.Patch("/api/investigations/{id}/phases/{phase_id}", UpdateInvestigationPhaseHandler(store))

	body := bytes.NewBufferString(`{"name":"new"}`)
	req := httptest.NewRequest("PATCH", "/api/investigations/"+strconv.FormatInt(invID, 10)+"/phases/"+strconv.FormatInt(phaseID, 10), body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rows, _ := store.ListInvestigationPhases(invID)
	if len(rows) != 1 || rows[0].Name != "new" {
		t.Errorf("name not updated: %+v", rows)
	}
}

func TestInvestigationPhases_Delete(t *testing.T) {
	store := newSeededTestStore(t)
	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	phaseID, _ := store.InsertInvestigationPhase(&db.InvestigationPhaseInsert{
		InvestigationID: invID, Name: "x", StartTs: 1, EndTs: 2, CreatedBy: "x",
	})
	router := chi.NewRouter()
	router.Delete("/api/investigations/{id}/phases/{phase_id}", DeleteInvestigationPhaseHandler(store))

	req := httptest.NewRequest("DELETE", "/api/investigations/"+strconv.FormatInt(invID, 10)+"/phases/"+strconv.FormatInt(phaseID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rows, _ := store.ListInvestigationPhases(invID)
	if len(rows) != 0 {
		t.Errorf("after delete: len(rows) = %d, want 0", len(rows))
	}
}

func TestFederationInvestigationPhases_RejectsWithoutToken(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/v1/federation/investigations/{id}/phases", FederationInvestigationPhasesList(store))

	req := httptest.NewRequest("GET", "/api/v1/federation/investigations/1/phases", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}