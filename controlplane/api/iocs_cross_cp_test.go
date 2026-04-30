package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

// seedRun creates an orchestration + a run so RecordIOCObservation's
// FK on orchestration_run_id passes (PRAGMA foreign_keys=1 is on).
// name must be unique across orchestrations, so callers pass a salt.
func seedRun(t *testing.T, st *db.Store, salt string) int64 {
	t.Helper()
	orchID, err := st.CreateOrchestration("t-"+salt, "", "name: t\n", "manual", "", "", 0)
	if err != nil {
		t.Fatalf("CreateOrchestration: %v", err)
	}
	runID, err := st.CreateOrchestrationRun(orchID, "manual", "", 0)
	if err != nil {
		t.Fatalf("CreateOrchestrationRun: %v", err)
	}
	return runID
}

func TestListCrossCPPatterns_HTTP(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	// Need an event + finding so the FK on observation succeeds. Use the
	// helper InsertEvent (Phase 22.3 precedent).
	eventID, err := st.InsertEvent(&db.Event{
		Ts: 1, Type: "finding", Agent: sql.NullString{String: "t", Valid: true}, RawJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	findingID, err := st.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 1, Agent: "a", Host: "host-1",
		Title: "x", Severity: "MEDIUM", RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	// Three observations linking the same IOC to three distinct runs.
	for i := 0; i < 3; i++ {
		runID := seedRun(t, st, fmt.Sprintf("a%d", i))
		if err := st.RecordIOCObservation(iocID, &db.IOCObservation{
			FindingID: findingID, OrchestrationRunID: runID,
		}); err != nil {
			t.Fatalf("RecordIOCObservation: %v", err)
		}
	}

	req := httptest.NewRequest("GET", "/api/iocs/cross-cp-patterns?min_observations=3", nil)
	rec := httptest.NewRecorder()
	ListCrossCPPatternsHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp []db.IOCPattern
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected 1 pattern; got %d (body=%s)", len(resp), rec.Body.String())
	}
	if resp[0].IOCID != iocID {
		t.Errorf("expected ioc_id %d; got %d", iocID, resp[0].IOCID)
	}
	if resp[0].DistinctRuns != 3 {
		t.Errorf("expected DistinctRuns=3; got %d", resp[0].DistinctRuns)
	}
}

func TestListCrossCPPatterns_HTTP_FiltersByObservationCount(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	eventID, err := st.InsertEvent(&db.Event{
		Ts: 1, Type: "finding", Agent: sql.NullString{String: "t", Valid: true}, RawJSON: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	findingID, err := st.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 1, Agent: "a", Host: "host-1",
		Title: "x", Severity: "MEDIUM", RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}
	runID := seedRun(t, st, "solo")
	if err := st.RecordIOCObservation(iocID, &db.IOCObservation{
		FindingID: findingID, OrchestrationRunID: runID,
	}); err != nil {
		t.Fatalf("RecordIOCObservation: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/iocs/cross-cp-patterns?min_observations=5", nil)
	rec := httptest.NewRecorder()
	ListCrossCPPatternsHandler(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp []db.IOCPattern
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("expected 0 patterns under threshold; got %d", len(resp))
	}
}
