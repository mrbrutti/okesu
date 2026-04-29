package db

import (
	"testing"
)

func TestUpsertIOC_InsertsNewRow(t *testing.T) {
	s := openTempStore(t)
	id, created, err := s.UpsertIOC(&IOCUpsert{
		Kind:            "sha256",
		Value:           "ABC123",
		NormalizedValue: "abc123",
		Source:          "observed",
	})
	if err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}
	if !created {
		t.Errorf("expected created=true on first upsert")
	}
	if id == 0 {
		t.Errorf("expected non-zero id")
	}
}

func TestUpsertIOC_DedupsOnKindAndNormalizedValue(t *testing.T) {
	s := openTempStore(t)
	id1, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "observed"})
	id2, created, err := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "ABC", NormalizedValue: "abc", Source: "observed"})
	if err != nil {
		t.Fatalf("second UpsertIOC: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected dedup: id1=%d id2=%d", id1, id2)
	}
	if created {
		t.Errorf("expected created=false on duplicate")
	}
}

func TestUpsertIOC_CatalogOverridesObserved(t *testing.T) {
	s := openTempStore(t)
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "observed"})
	id, _, err := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
		Source:         "catalog",
		DefinitionPath: "catalog/iocs/test.yaml",
		SeverityFloor:  "HIGH",
	})
	if err != nil {
		t.Fatalf("catalog upsert: %v", err)
	}
	got, err := s.GetIOC(id)
	if err != nil {
		t.Fatalf("GetIOC: %v", err)
	}
	if got.Source != "catalog" {
		t.Errorf("expected source=catalog after catalog upsert; got %q", got.Source)
	}
	if got.SeverityFloor != "HIGH" {
		t.Errorf("expected catalog severity_floor to win; got %q", got.SeverityFloor)
	}
}

func TestRecordObservation_CreatesLink(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})

	// Seed an event + finding so the FK on ioc_observations.finding_id
	// is satisfied. ioc_observations.finding_id REFERENCES findings(id)
	// with FK enforcement on (PRAGMA foreign_keys=1 in openSQLite), so
	// the observed finding_id has to exist.
	eventID, err := s.InsertEvent(&Event{Ts: 0, Type: "finding", RawJSON: "{}"})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	findingID, err := s.InsertFinding(&FindingInsert{
		EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
		Severity: "INFO", Title: "t", RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}

	if err := s.RecordIOCObservation(iocID, &IOCObservation{FindingID: findingID, Host: "host-1"}); err != nil {
		t.Fatalf("RecordIOCObservation: %v", err)
	}
	got, err := s.ListIOCObservations(iocID)
	if err != nil {
		t.Fatalf("ListIOCObservations: %v", err)
	}
	if len(got) != 1 || got[0].FindingID != findingID {
		t.Errorf("expected one observation for finding %d; got %+v", findingID, got)
	}
}
