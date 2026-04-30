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
