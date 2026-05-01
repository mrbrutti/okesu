package feeds

import (
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/catalog"
)

func TestReconcile_FirstPassInsertsAll(t *testing.T) {
	st := newTestStore(t)

	feedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400,
	})
	if err != nil {
		t.Fatal(err)
	}

	entries := []catalog.CatalogEntry{
		{Kind: "yara_rule", Value: "rule A {}", NormalizedValue: "norm-A", Name: "A"},
		{Kind: "yara_rule", Value: "rule B {}", NormalizedValue: "norm-B", Name: "B"},
	}
	res, err := Reconcile(st, feedID, "f1", entries)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Inserted != 2 || res.Deleted != 0 {
		t.Fatalf("first pass: %+v", res)
	}

	rows, _ := st.ListIOCsByFeed(feedID)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestReconcile_SecondPassDeletesRemovedAndOrphansObservations(t *testing.T) {
	st := newTestStore(t)

	feedID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400,
	})

	first := []catalog.CatalogEntry{
		{Kind: "yara_rule", Value: "rule A {}", NormalizedValue: "norm-A", Name: "A"},
		{Kind: "yara_rule", Value: "rule B {}", NormalizedValue: "norm-B", Name: "B"},
	}
	if _, err := Reconcile(st, feedID, "f1", first); err != nil {
		t.Fatal(err)
	}

	rowsBefore, _ := st.ListIOCsByFeed(feedID)
	if len(rowsBefore) != 2 {
		t.Fatalf("seed: %d", len(rowsBefore))
	}

	// Find row A (named "A") and attach an observation to it.
	var aID int64
	for _, r := range rowsBefore {
		if r.Name == "A" {
			aID = r.ID
		}
	}
	if aID == 0 {
		t.Fatal("missing A row")
	}
	if err := st.RecordIOCObservation(aID, &db.IOCObservation{Host: "host-1"}); err != nil {
		t.Fatalf("record obs: %v", err)
	}

	// Second pass: A's body changes (still keyed on norm-A so it's an update),
	// B is dropped, C is added.
	second := []catalog.CatalogEntry{
		{Kind: "yara_rule", Value: "rule A v2 {}", NormalizedValue: "norm-A", Name: "A"},
		{Kind: "yara_rule", Value: "rule C {}", NormalizedValue: "norm-C", Name: "C"},
	}
	res, err := Reconcile(st, feedID, "f1", second)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Deleted != 1 {
		t.Fatalf("expected 1 deleted (B), got %d (%+v)", res.Deleted, res)
	}
	if res.Inserted != 1 {
		t.Fatalf("expected 1 inserted (C), got %d (%+v)", res.Inserted, res)
	}
	if res.Updated != 1 {
		t.Fatalf("expected 1 updated (A), got %d (%+v)", res.Updated, res)
	}

	// A's observation must still exist with ioc_id = aID (unchanged).
	obsForA, err := st.ListIOCObservations(aID)
	if err != nil {
		t.Fatalf("list obs for A: %v", err)
	}
	if len(obsForA) != 1 {
		t.Fatalf("A observation lost: %d", len(obsForA))
	}
}

func TestReconcile_UninstallReconcileToEmpty(t *testing.T) {
	st := newTestStore(t)

	feedID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara", RefreshIntervalSeconds: 86400,
	})
	entries := []catalog.CatalogEntry{
		{Kind: "yara_rule", Value: "rule A {}", NormalizedValue: "norm-A", Name: "A"},
	}
	if _, err := Reconcile(st, feedID, "f1", entries); err != nil {
		t.Fatal(err)
	}

	// Reconcile-to-empty.
	res, err := Reconcile(st, feedID, "f1", nil)
	if err != nil {
		t.Fatalf("reconcile-to-empty: %v", err)
	}
	if res.Deleted != 1 || res.Inserted != 0 || res.Updated != 0 {
		t.Fatalf("expected 1 deleted: %+v", res)
	}
	rows, _ := st.ListIOCsByFeed(feedID)
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows after reconcile-to-empty, got %d", len(rows))
	}
}
