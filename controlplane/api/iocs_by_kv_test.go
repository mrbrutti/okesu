package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestGetIOCByKV_Found(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Name: "test"}); err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256&value=abc", nil)
	GetIOCByKVHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got db.IOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestGetIOCByKV_NotFound(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256&value=missing", nil)
	GetIOCByKVHandler(st)(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetIOCByKV_MissingParams(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256", nil)
	GetIOCByKVHandler(st)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListIOCObservationsByKV_HTTP(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	eventID, err := st.InsertEvent(&db.Event{Ts: 0, Type: "finding", RawJSON: "{}"})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	findingID, err := st.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
		Severity: "INFO", Title: "t", RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	if err := st.RecordIOCObservation(iocID, &db.IOCObservation{FindingID: findingID, Host: "host-1"}); err != nil {
		t.Fatalf("RecordIOCObservation: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv/observations?kind=sha256&value=abc", nil)
	ListIOCObservationsByKVHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []db.IOCObservation
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Host != "host-1" {
		t.Errorf("expected one observation host=host-1; got %+v", got)
	}
}

func TestListIOCRelationshipsByKV_HTTP(t *testing.T) {
	st := newTestStore(t)
	aID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "evil.com", NormalizedValue: "evil.com"})
	bID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
	if err := st.AddIOCRelationship(&db.IOCRelationshipInsert{SubjectID: aID, Predicate: "resolves-to", ObjectID: bID, Source: "agent"}); err != nil {
		t.Fatalf("AddIOCRelationship: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv/relationships?kind=domain&value=evil.com", nil)
	ListIOCRelationshipsByKVHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []db.IOCRelationshipPaired
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Predicate != "resolves-to" {
		t.Errorf("expected one resolves-to edge; got %+v", got)
	}
}

func TestListIOCObservationsByKV_MissingParams(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv/observations?kind=sha256", nil)
	ListIOCObservationsByKVHandler(st)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListIOCRelationshipsByKV_MissingParams(t *testing.T) {
	st := newTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv/relationships?value=evil.com", nil)
	ListIOCRelationshipsByKVHandler(st)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
