package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// Local-only path: no peers configured. The federated handler should
// return the same shape as the local handler with a single CPSource
// per row (the local CP).
func TestFederatedListIOCs_LocalOnly(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Name: "test"}); err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}

	// Aggregator with no peers — FanOut becomes a no-op so the handler
	// runs only the local query + merge path.
	agg := federation.NewAggregator(st)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs", nil)
	FederatedListIOCs(st, agg).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rows []FederatedIOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row; got %d", len(rows))
	}
	if rows[0].Name != "test" {
		t.Errorf("Name = %q", rows[0].Name)
	}
	if len(rows[0].CPSources) != 1 {
		t.Errorf("expected 1 CPSource; got %d", len(rows[0].CPSources))
	}
}

func TestFederatedGetIOCByKV_LocalOnly(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Name: "test"}); err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}
	agg := federation.NewAggregator(st)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256&value=abc", nil)
	FederatedGetIOCByKV(st, agg).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got FederatedIOCRecord
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Name != "test" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.CPSources) != 1 {
		t.Errorf("expected 1 CPSource; got %d", len(got.CPSources))
	}
}

func TestFederatedGetIOCByKV_NotFound(t *testing.T) {
	st := newTestStore(t)
	agg := federation.NewAggregator(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256&value=missing", nil)
	FederatedGetIOCByKV(st, agg).ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFederatedGetIOCByKV_MissingParams(t *testing.T) {
	st := newTestStore(t)
	agg := federation.NewAggregator(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv?kind=sha256", nil)
	FederatedGetIOCByKV(st, agg).ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFederatedListIOCObservationsByKV_LocalOnly(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	eventID, _ := st.InsertEvent(&db.Event{Ts: 0, Type: "finding", RawJSON: "{}"})
	findingID, _ := st.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
		Severity: "INFO", Title: "t", RawJSON: "{}",
	})
	if err := st.RecordIOCObservation(iocID, &db.IOCObservation{FindingID: findingID, Host: "host-1"}); err != nil {
		t.Fatalf("RecordIOCObservation: %v", err)
	}

	agg := federation.NewAggregator(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv/observations?kind=sha256&value=abc", nil)
	FederatedListIOCObservationsByKV(st, agg).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []FederatedIOCObservation
	json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 1 || got[0].Host != "host-1" {
		t.Errorf("expected 1 observation host=host-1; got %+v", got)
	}
	if got[0].CPSource == nil {
		t.Errorf("CPSource should be set on local-CP observation")
	}
}

func TestFederatedListIOCRelationshipsByKV_LocalOnly(t *testing.T) {
	st := newTestStore(t)
	aID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "domain", Value: "evil.com", NormalizedValue: "evil.com"})
	bID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
	if err := st.AddIOCRelationship(&db.IOCRelationshipInsert{SubjectID: aID, Predicate: "resolves-to", ObjectID: bID, Source: "agent"}); err != nil {
		t.Fatalf("AddIOCRelationship: %v", err)
	}

	agg := federation.NewAggregator(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/by-kv/relationships?kind=domain&value=evil.com", nil)
	FederatedListIOCRelationshipsByKV(st, agg).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []FederatedIOCRelationship
	json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 1 || got[0].Predicate != "resolves-to" {
		t.Errorf("expected 1 resolves-to edge; got %+v", got)
	}
	if len(got[0].CPSources) != 1 {
		t.Errorf("expected 1 CPSource; got %d", len(got[0].CPSources))
	}
}

func TestRenderFederationIOCs_WireShape(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog"})
	body, err := RenderFederationIOCs(st, 5000)
	if err != nil {
		t.Fatalf("RenderFederationIOCs: %v", err)
	}
	var rows []db.IOCRecord
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "abc" {
		t.Errorf("expected 1 row 'abc'; got %+v", rows)
	}
}
