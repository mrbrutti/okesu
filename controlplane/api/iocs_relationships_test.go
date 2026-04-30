package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestListIOCRelationships_HTTP(t *testing.T) {
	st := newTestStore(t)
	a, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "x", NormalizedValue: "x"})
	b, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "ipv4", Value: "1.1.1.1", NormalizedValue: "1.1.1.1"})
	if err := st.AddIOCRelationship(&db.IOCRelationshipInsert{
		SubjectID: a, Predicate: "resolves-to", ObjectID: b,
	}); err != nil {
		t.Fatalf("AddIOCRelationship: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/relationships", ListIOCRelationshipsHandler(st))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(a, 10)+"/relationships", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rels []db.IOCRelationship
	if err := json.NewDecoder(rec.Body).Decode(&rels); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship; got %d", len(rels))
	}
	if len(rels) > 0 && rels[0].Predicate != "resolves-to" {
		t.Errorf("predicate = %q, want resolves-to", rels[0].Predicate)
	}
}

func TestListIOCRelationships_BadID(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/relationships", ListIOCRelationshipsHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/not-a-number/relationships", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Querying the object-side IOC must surface the same edge — the store
// method returns relationships in either direction, and the handler
// is only useful if both endpoints can pivot to it from a UI link.
func TestListIOCRelationships_Bidirectional(t *testing.T) {
	st := newTestStore(t)
	a, _, err := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "x", NormalizedValue: "x"})
	if err != nil {
		t.Fatalf("UpsertIOC a: %v", err)
	}
	b, _, err := st.UpsertIOC(&db.IOCUpsert{Kind: "ipv4", Value: "1.1.1.1", NormalizedValue: "1.1.1.1"})
	if err != nil {
		t.Fatalf("UpsertIOC b: %v", err)
	}
	if err := st.AddIOCRelationship(&db.IOCRelationshipInsert{
		SubjectID: a, Predicate: "resolves-to", ObjectID: b,
	}); err != nil {
		t.Fatalf("AddIOCRelationship: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/relationships", ListIOCRelationshipsHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(b, 10)+"/relationships", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rels []db.IOCRelationship
	if err := json.NewDecoder(rec.Body).Decode(&rels); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship from object side; got %d", len(rels))
	}
	if rels[0].SubjectID != a || rels[0].ObjectID != b {
		t.Errorf("edge endpoints = (%d → %d), want (%d → %d)", rels[0].SubjectID, rels[0].ObjectID, a, b)
	}
}

// IOC exists but has no edges — handler must return 200 with [] (not
// null), so UI code can iterate without nil-guards.
func TestListIOCRelationships_EmptyResult(t *testing.T) {
	st := newTestStore(t)
	id, _, err := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "lonely", NormalizedValue: "lonely"})
	if err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/relationships", ListIOCRelationshipsHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(id, 10)+"/relationships", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rels []db.IOCRelationship
	if err := json.NewDecoder(rec.Body).Decode(&rels); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships; got %d", len(rels))
	}
}
