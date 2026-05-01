package db

import (
	"database/sql"
	"errors"
	"strings"
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

func TestListIOCs_FilterBySource(t *testing.T) {
	s := openTempStore(t)
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "a", NormalizedValue: "a", Source: "catalog"})
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "b", NormalizedValue: "b", Source: "observed"})

	rows, err := s.ListIOCs(IOCListFilter{Source: "catalog"})
	if err != nil {
		t.Fatalf("ListIOCs: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "a" {
		t.Errorf("expected only catalog row 'a'; got %+v", rows)
	}
}

func TestListIOCs_FilterByQuery(t *testing.T) {
	s := openTempStore(t)
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "AbCdEf", NormalizedValue: "abcdef", Source: "catalog", Name: "WannaCry sample"})
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc999", NormalizedValue: "abc999", Source: "catalog", Tags: "ransomware,emotet"})
	s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4", Source: "observed"})

	// Match by value (case-insensitive)
	rows, err := s.ListIOCs(IOCListFilter{Query: "ABCD"})
	if err != nil {
		t.Fatalf("ListIOCs query=ABCD: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "abcdef" {
		t.Errorf("query=ABCD expected one match (abcdef); got %+v", rows)
	}
	// Match by name
	rows, err = s.ListIOCs(IOCListFilter{Query: "wannacry"})
	if err != nil {
		t.Fatalf("ListIOCs query=wannacry: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "WannaCry sample" {
		t.Errorf("query=wannacry expected one match by name; got %+v", rows)
	}
	// Match by tag
	rows, err = s.ListIOCs(IOCListFilter{Query: "emotet"})
	if err != nil {
		t.Fatalf("ListIOCs query=emotet: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "abc999" {
		t.Errorf("query=emotet expected one match by tag; got %+v", rows)
	}
}

// Source + Query combine via AND in the SQL builder. Locks in that
// adding a second filter narrows rather than widens — guards against
// future builder rewrites that might confuse OR with AND.
func TestListIOCs_FilterBySourceAndQuery(t *testing.T) {
	s := openTempStore(t)
	// Both rows match Tags=ransomware, but only one is source=catalog.
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "a", NormalizedValue: "a", Source: "catalog", Tags: "ransomware"})
	s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "b", NormalizedValue: "b", Source: "observed", Tags: "ransomware"})

	rows, err := s.ListIOCs(IOCListFilter{Source: "catalog", Query: "ransomware"})
	if err != nil {
		t.Fatalf("ListIOCs: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "a" {
		t.Errorf("source=catalog AND query=ransomware expected one row 'a'; got %+v", rows)
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

func TestGetIOCByKV(t *testing.T) {
	s := openTempStore(t)
	if _, _, err := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "ABC", NormalizedValue: "abc", Source: "catalog", Name: "test"}); err != nil {
		t.Fatalf("UpsertIOC: %v", err)
	}

	got, err := s.GetIOCByKV("sha256", "abc")
	if err != nil {
		t.Fatalf("GetIOCByKV: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("Name = %q, want test", got.Name)
	}

	if _, err := s.GetIOCByKV("sha256", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows for missing kv; got %v", err)
	}

	// LookupIOC delegates to GetIOCByKV — same contract; lock that in
	// so a future refactor can't quietly diverge.
	gotL, err := s.LookupIOC("sha256", "abc")
	if err != nil {
		t.Fatalf("LookupIOC: %v", err)
	}
	if gotL.Name != "test" {
		t.Errorf("LookupIOC Name = %q, want test", gotL.Name)
	}
	if _, err := s.LookupIOC("sha256", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("LookupIOC expected sql.ErrNoRows; got %v", err)
	}
}

func TestListIOCObservationsByKV(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
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

	got, err := s.ListIOCObservationsByKV("sha256", "abc")
	if err != nil {
		t.Fatalf("ListIOCObservationsByKV: %v", err)
	}
	if len(got) != 1 || got[0].Host != "host-1" {
		t.Errorf("expected one observation host=host-1; got %+v", got)
	}
}

func TestUpsertIOC_WithFeedID_ScopesAndOrphans(t *testing.T) {
	st := openTempStore(t)

	feedID, err := st.InsertFeedConfig(&FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://example.com/x.yar", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true, InstalledFromRegistry: false,
	})
	if err != nil {
		t.Fatalf("insert feed: %v", err)
	}

	aID, _, err := st.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "deadbeef" + strings.Repeat("a", 56),
		NormalizedValue: "deadbeef" + strings.Repeat("a", 56),
		Source: "feed:f1", FeedID: &feedID, Name: "rule-A",
	})
	if err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	bID, _, err := st.UpsertIOC(&IOCUpsert{
		Kind: "sha256", Value: "cafef00d" + strings.Repeat("b", 56),
		NormalizedValue: "cafef00d" + strings.Repeat("b", 56),
		Source: "feed:f1", FeedID: &feedID, Name: "rule-B",
	})
	if err != nil {
		t.Fatalf("upsert B: %v", err)
	}
	if aID == 0 || bID == 0 {
		t.Fatalf("missing ids: a=%d b=%d", aID, bID)
	}

	rows, err := st.ListIOCsByFeed(feedID)
	if err != nil {
		t.Fatalf("list by feed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows scoped to feed, got %d", len(rows))
	}

	// Attach an observation to A so we can confirm orphaning.
	if err := st.RecordIOCObservation(aID, &IOCObservation{Host: "host-1"}); err != nil {
		t.Fatalf("record observation: %v", err)
	}

	// Delete A only (simulating reconcile dropping a single rule).
	if err := st.DeleteIOCsByFeed(feedID, "f1:", []int64{aID}); err != nil {
		t.Fatalf("delete by feed: %v", err)
	}

	// A's row should be gone.
	if _, err := st.GetIOC(aID); err == nil {
		t.Fatalf("expected error after delete of A")
	}
	// B's row should remain.
	if _, err := st.GetIOC(bID); err != nil {
		t.Fatalf("expected B to remain: %v", err)
	}

	// A's observation should still exist with ioc_id NULL and orphaned_rule_label set.
	// Use a direct SQL query because ListIOCObservations filters by ioc_id (not nullable on read).
	var label sql.NullString
	var nullIOC sql.NullInt64
	if err := st.QueryRow(`SELECT ioc_id, orphaned_rule_label FROM ioc_observations WHERE host = 'host-1'`).Scan(&nullIOC, &label); err != nil {
		t.Fatalf("query orphan obs: %v", err)
	}
	if nullIOC.Valid {
		t.Fatalf("expected ioc_id NULL after orphaning, got %d", nullIOC.Int64)
	}
	if !label.Valid || label.String != "f1:rule-A" {
		t.Fatalf("expected orphan label 'f1:rule-A', got %q (valid=%v)", label.String, label.Valid)
	}
}
