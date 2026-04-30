package api

import (
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/extract"
)

func TestPropagateFromIOCs_RaisesSeverityFromCatalogFloor(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		NormalizedValue: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Source:          "catalog",
		SeverityFloor:   "HIGH", Attribution: "apt-foo", Classification: "malware-c2",
	})

	hits := []extract.Hit{{
		Kind:            "sha256",
		Value:           "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		NormalizedValue: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}}
	got, err := propagateFromIOCs(st, hits, "MEDIUM")
	if err != nil {
		t.Fatalf("propagateFromIOCs: %v", err)
	}
	if got.Severity != "HIGH" {
		t.Errorf("Severity = %q, want HIGH (raised from MEDIUM by catalog floor)", got.Severity)
	}
	if got.IOCAttribution != "apt-foo" {
		t.Errorf("IOCAttribution = %q, want apt-foo", got.IOCAttribution)
	}
	if got.IOCClassification != "malware-c2" {
		t.Errorf("IOCClassification = %q, want malware-c2", got.IOCClassification)
	}
}

func TestPropagateFromIOCs_NeverLowersSeverity(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           "abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcd",
		NormalizedValue: "abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcd",
		Source:          "catalog",
		SeverityFloor:   "LOW",
	})

	hits := []extract.Hit{{
		Kind:            "sha256",
		Value:           "abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcd",
		NormalizedValue: "abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcd",
	}}
	got, err := propagateFromIOCs(st, hits, "HIGH")
	if err != nil {
		t.Fatalf("propagateFromIOCs: %v", err)
	}
	if got.Severity != "HIGH" {
		t.Errorf("Severity = %q, want HIGH (LOW floor must not lower)", got.Severity)
	}
}

func TestPropagateFromIOCs_AssignsClusterFromExistingFinding(t *testing.T) {
	st := newTestStore(t)
	iocID, _, _ := st.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           "1111111111111111111111111111111111111111111111111111111111111111",
		NormalizedValue: "1111111111111111111111111111111111111111111111111111111111111111",
		Source:          "observed",
	})

	// Seed a prior finding within the cluster window.
	res, err := st.Exec(`INSERT INTO events (ts, type, agent, raw_json) VALUES (?, 'finding', 'test', '{}')`, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	eventID, _ := res.LastInsertId()
	priorID, _ := st.InsertFinding(&db.FindingInsert{
		EventID:   eventID,
		Ts:        time.Now().UnixMilli(),
		Title:     "prior",
		Severity:  "MEDIUM",
		ClusterID: "prior-cluster",
	})
	st.RecordIOCObservation(iocID, &db.IOCObservation{FindingID: priorID})

	hits := []extract.Hit{{
		Kind:            "sha256",
		Value:           "1111111111111111111111111111111111111111111111111111111111111111",
		NormalizedValue: "1111111111111111111111111111111111111111111111111111111111111111",
	}}
	got, _ := propagateFromIOCs(st, hits, "MEDIUM")
	if got.ClusterID != "prior-cluster" {
		t.Errorf("ClusterID = %q, want prior-cluster", got.ClusterID)
	}
}

func TestPropagateFromIOCs_NoIOCsReturnsBaseline(t *testing.T) {
	st := newTestStore(t)
	got, _ := propagateFromIOCs(st, nil, "MEDIUM")
	if got.Severity != "MEDIUM" || got.ClusterID != "" || got.IOCAttribution != "" {
		t.Errorf("expected baseline; got %+v", got)
	}
}
