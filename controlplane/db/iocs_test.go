package db

import (
	"testing"
	"time"
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

	ioc, err := s.GetIOC(iocID)
	if err != nil {
		t.Fatalf("GetIOC after observation: %v", err)
	}
	if ioc.ObservationCount != 1 {
		t.Errorf("expected observation_count=1 after one observation; got %d", ioc.ObservationCount)
	}
	if time.Since(ioc.LastSeen) > 5*time.Second {
		t.Errorf("expected last_seen to be refreshed within 5s; got %v (now=%v)", ioc.LastSeen, time.Now())
	}
}

func TestUpsertIOC_NameAndTags(t *testing.T) {
	s := openTempStore(t)
	id, _, err := s.UpsertIOC(&IOCUpsert{
		Kind:            "yara_rule",
		Value:           "rule X { condition: true }",
		NormalizedValue: "rule x { condition: true }",
		Source:          "catalog",
		Name:            "rule-X",
		Tags:            "test,phase-22.5",
	})
	if err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}
	if id == 0 {
		t.Fatalf("id should be non-zero")
	}
	rows, err := s.ListIOCs(IOCListFilter{Kind: "yara_rule"})
	if err != nil {
		t.Fatalf("ListIOCs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row; got %d", len(rows))
	}
	if rows[0].Name != "rule-X" {
		t.Errorf("Name = %q, want rule-X", rows[0].Name)
	}
	if rows[0].Tags != "test,phase-22.5" {
		t.Errorf("Tags = %q, want test,phase-22.5", rows[0].Tags)
	}
}

// Catalog reload exercises the UPDATE branch of UpsertIOC. Name+Tags
// must round-trip the second time the same row is upserted from
// "catalog" source — the existing pattern overwrites all metadata
// fields unconditionally on catalog upsert (the catalog is the
// source of truth), and the new columns must follow that precedent.
func TestUpsertIOC_CatalogReloadUpdatesNameAndTags(t *testing.T) {
	s := openTempStore(t)
	id1, _, err := s.UpsertIOC(&IOCUpsert{
		Kind: "yara_rule", Value: "rule R { condition: true }",
		NormalizedValue: "rule r { condition: true }",
		Source:          "catalog",
		Name:            "old-name",
		Tags:            "old,tags",
	})
	if err != nil {
		t.Fatalf("first catalog upsert: %v", err)
	}
	id2, created, err := s.UpsertIOC(&IOCUpsert{
		Kind: "yara_rule", Value: "rule R { condition: true }",
		NormalizedValue: "rule r { condition: true }",
		Source:          "catalog",
		Name:            "new-name",
		Tags:            "new,curated,tags",
	})
	if err != nil {
		t.Fatalf("reload upsert: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected dedup on (kind, normalized_value); got id1=%d id2=%d", id1, id2)
	}
	if created {
		t.Errorf("expected created=false on second catalog upsert")
	}
	got, err := s.GetIOC(id1)
	if err != nil {
		t.Fatalf("GetIOC: %v", err)
	}
	if got.Name != "new-name" {
		t.Errorf("Name = %q, want new-name (catalog reload must update)", got.Name)
	}
	if got.Tags != "new,curated,tags" {
		t.Errorf("Tags = %q, want new,curated,tags (catalog reload must update)", got.Tags)
	}
}

func TestUpsertIOC_ObservedDoesNotOverwriteCatalog(t *testing.T) {
	s := openTempStore(t)
	// Seed a catalog row with curated metadata.
	id1, _, err := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
		Source:         "catalog",
		DefinitionPath: "catalog/iocs/test.yaml",
		SeverityFloor:  "HIGH",
		Attribution:    "apt-foo",
	})
	if err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	// Observe the same IOC (e.g., as if a finding extracted it).
	id2, created, err := s.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc",
		Source: "observed",
	})
	if err != nil {
		t.Fatalf("observed upsert: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected same row id; got %d vs %d", id1, id2)
	}
	if created {
		t.Errorf("expected created=false on duplicate")
	}
	got, err := s.GetIOC(id1)
	if err != nil {
		t.Fatalf("GetIOC: %v", err)
	}
	if got.Source != "catalog" {
		t.Errorf("observed upsert clobbered source: got %q want catalog", got.Source)
	}
	if got.SeverityFloor != "HIGH" || got.Attribution != "apt-foo" || got.DefinitionPath == "" {
		t.Errorf("observed upsert clobbered catalog metadata: %+v", got)
	}
}
