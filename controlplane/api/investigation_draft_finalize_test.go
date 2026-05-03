package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestDraftFinalize_NoDraft404(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	id := mustCreateInvestigationForRelayTest(t, store, "case")
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/draft/finalize", GetInvestigationDraftFinalizeHandler(store, hub))

	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/draft/finalize", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no draft exists)", w.Code)
	}
}

func TestDraftFinalize_UnknownInvestigation404(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/draft/finalize", GetInvestigationDraftFinalizeHandler(store, hub))
	req := httptest.NewRequest("POST", "/api/investigations/99999/draft/finalize", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (unknown investigation)", w.Code)
	}
}

func TestDraftFinalize_EmptyDraft400(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	id := mustCreateInvestigationForRelayTest(t, store, "case")
	// Seed an empty Yjs doc snapshot — decodeYTextBody returns "".
	if err := store.UpsertInvestigationNoteDraft(id, []byte{0x00, 0x00}); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/draft/finalize", GetInvestigationDraftFinalizeHandler(store, hub))

	body := bytes.NewBufferString(`{"author":"alice@x"}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/draft/finalize", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (empty draft)", w.Code)
	}
}

func TestDraftFinalize_WritesNote(t *testing.T) {
	// The hex below is Y.encodeStateAsUpdate(doc) for doc.getText("body").insert(0, "hello world").
	const fixtureHex = "0101cce5ade20100040104626f64790b68656c6c6f20776f726c6400"

	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	id := mustCreateInvestigationForRelayTest(t, store, "case")
	fixture, err := hex.DecodeString(fixtureHex)
	if err != nil {
		t.Skipf("fixture decode: %v", err)
	}
	if err := store.UpsertInvestigationNoteDraft(id, fixture); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/draft/finalize", GetInvestigationDraftFinalizeHandler(store, hub))
	body := bytes.NewBufferString(`{"author":"alice@x"}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/draft/finalize", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		NoteID int64 `json:"note_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.NoteID == 0 {
		t.Errorf("note_id = 0")
	}
	notes, err := store.ListInvestigationNotes(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Errorf("notes count = %d, want 1", len(notes))
	} else if notes[0].Body != "hello world" {
		t.Errorf("note body = %q, want %q", notes[0].Body, "hello world")
	}
	// Draft row is gone.
	if _, err := store.GetInvestigationNoteDraft(id); err == nil {
		t.Errorf("draft row still present after finalize")
	}
}

func TestDraftFinalize_ConcurrentRace409(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	id := mustCreateInvestigationForRelayTest(t, store, "case")
	// Seed a draft and create a live room with MarkFinalizing already taken.
	const fixtureHex = "0101cce5ade20100040104626f64790b68656c6c6f20776f726c6400"
	fixture, err := hex.DecodeString(fixtureHex)
	if err != nil {
		t.Skipf("fixture decode: %v", err)
	}
	if err := store.UpsertInvestigationNoteDraft(id, fixture); err != nil {
		t.Fatal(err)
	}
	c := &fakeClient{}
	wsC := NewWsClient("first@x", c)
	room := hub.JoinOrLoad(id, wsC)
	defer room.Leave(wsC)
	if !room.MarkFinalizing() {
		t.Fatal("MarkFinalizing should have succeeded the first time")
	}

	router := chi.NewRouter()
	router.Post("/api/investigations/{id}/draft/finalize", GetInvestigationDraftFinalizeHandler(store, hub))
	body := bytes.NewBufferString(`{"author":"second@x"}`)
	req := httptest.NewRequest("POST", "/api/investigations/"+strconv.FormatInt(id, 10)+"/draft/finalize", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (concurrent finalize)", w.Code)
	}
}
