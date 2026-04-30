package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestGetIOC_Found(t *testing.T) {
	st := newTestStore(t)
	id, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
		Source: "catalog", Name: "test",
	})
	if err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/iocs/{id}", GetIOCHandler(st))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(id, 10), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got db.IOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id || got.Name != "test" {
		t.Errorf("got %+v", got)
	}
}

func TestGetIOC_NotFound(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}", GetIOCHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/9999", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetIOC_BadID(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}", GetIOCHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/not-a-number", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
