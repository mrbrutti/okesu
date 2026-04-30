package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestListIOCObservations_HTTP(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
	})
	// Seed a finding so the FK on ioc_observations.finding_id is satisfied.
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

	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/observations", ListIOCObservationsHandler(st))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/"+strconv.FormatInt(iocID, 10)+"/observations", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []db.IOCObservation
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 observation; got %d", len(got))
	}
	if got[0].FindingID != findingID || got[0].Host != "host-1" {
		t.Errorf("observation shape wrong: %+v", got[0])
	}
	if got[0].ObservedAt.IsZero() {
		t.Errorf("ObservedAt should not be zero")
	}
}

func TestListIOCObservations_BadID(t *testing.T) {
	st := newTestStore(t)
	r := chi.NewRouter()
	r.Get("/api/iocs/{id}/observations", ListIOCObservationsHandler(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs/not-a-number/observations", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
