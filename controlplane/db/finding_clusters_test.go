package db

import (
	"testing"
	"time"
)

func TestFindClusterIDForIOCs_NoMatchReturnsEmpty(t *testing.T) {
	s := openTempStore(t)
	got, err := s.FindClusterIDForIOCs([]int64{}, time.Hour)
	if err != nil {
		t.Fatalf("FindClusterIDForIOCs: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty cluster_id; got %q", got)
	}
}

func TestFindClusterIDForIOCs_ReturnsExistingClusterWithinWindow(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "abc",
		NormalizedValue: "abc", Source: "observed",
	})

	// Seed a finding with cluster_id="seed-cluster" linked to the IOC.
	// We need an event row first because findings.event_id has a FK.
	res, err := s.Exec(`INSERT INTO events (ts, type, agent, raw_json) VALUES (?, 'finding', 'test', '{}')`, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	eventID, _ := res.LastInsertId()
	findingID, err := s.InsertFinding(&FindingInsert{
		EventID: eventID, Ts: time.Now().UnixMilli(),
		Title: "seed", Severity: "MEDIUM",
		ClusterID: "seed-cluster",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIOCObservation(iocID, &IOCObservation{FindingID: findingID}); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindClusterIDForIOCs([]int64{iocID}, time.Hour)
	if err != nil {
		t.Fatalf("FindClusterIDForIOCs: %v", err)
	}
	if got != "seed-cluster" {
		t.Errorf("expected seed-cluster; got %q", got)
	}
}

func TestMaxSeverityFloor_PicksHighestMatch(t *testing.T) {
	s := openTempStore(t)
	low, _, _ := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "low", NormalizedValue: "low",
		Source: "catalog", SeverityFloor: "LOW",
	})
	high, _, _ := s.UpsertIOC(&IOCUpsert{
		Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4",
		Source: "catalog", SeverityFloor: "HIGH",
	})

	got, err := s.MaxSeverityFloor([]int64{low, high})
	if err != nil {
		t.Fatalf("MaxSeverityFloor: %v", err)
	}
	if got != "HIGH" {
		t.Errorf("expected HIGH; got %q", got)
	}
}

func TestPropagatedMetadataForIOCs_FirstWinsForAttribution(t *testing.T) {
	s := openTempStore(t)
	id1, _, _ := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "h1", NormalizedValue: "h1",
		Source: "catalog", Attribution: "actor-a", Classification: "malware-c2",
		Confidence: "high", SeverityFloor: "HIGH",
	})
	id2, _, _ := s.UpsertIOC(&IOCUpsert{
		Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4",
		Source: "catalog", Attribution: "actor-b", Classification: "phishing",
		Confidence: "medium", SeverityFloor: "MEDIUM",
	})

	got, err := s.PropagatedMetadataForIOCs([]int64{id1, id2})
	if err != nil {
		t.Fatalf("PropagatedMetadataForIOCs: %v", err)
	}
	// SeverityFloor should be HIGHEST across the set
	if got.SeverityFloor != "HIGH" {
		t.Errorf("SeverityFloor = %q, want HIGH", got.SeverityFloor)
	}
	// Attribution / classification / confidence: first non-empty wins
	if got.Attribution == "" || got.Classification == "" || got.Confidence == "" {
		t.Errorf("expected metadata from first match; got %+v", got)
	}
}

func TestPropagatedMetadataForIOCs_IgnoresObservedSource(t *testing.T) {
	s := openTempStore(t)
	// observed-source IOC with severity_floor (defensive — shouldn't carry it but might)
	id, _, _ := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "obs", NormalizedValue: "obs",
		Source: "observed", SeverityFloor: "HIGH",
	})
	got, _ := s.PropagatedMetadataForIOCs([]int64{id})
	if got.SeverityFloor != "" {
		t.Errorf("observed-source IOC should not contribute; got %+v", got)
	}
}
