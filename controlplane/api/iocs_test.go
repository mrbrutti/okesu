package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

// TestListIOCs_FiltersByFindingID exercises the GET /api/iocs handler
// with a finding_id query param: only the IOC observed against that
// finding should come back.
func TestListIOCs_FiltersByFindingID(t *testing.T) {
	store := newTestStore(t)

	// Seed an event + finding to satisfy ioc_observations.finding_id FK.
	eventID, err := store.InsertEvent(&db.Event{Ts: 0, Type: "finding", RawJSON: "{}"})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	findingID, err := store.InsertFinding(&db.FindingInsert{
		EventID: eventID, Ts: 0, Agent: "a", Host: "host-1",
		Severity: "INFO", Title: "t", RawJSON: "{}",
	})
	if err != nil {
		t.Fatalf("InsertFinding: %v", err)
	}

	// IOC #1: linked to the finding under test.
	iocLinkedID, _, err := store.UpsertIOC(&db.IOCUpsert{
		Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "observed",
	})
	if err != nil {
		t.Fatalf("upsert linked: %v", err)
	}
	if err := store.RecordIOCObservation(iocLinkedID, &db.IOCObservation{
		FindingID: findingID, Host: "host-1",
	}); err != nil {
		t.Fatalf("RecordIOCObservation: %v", err)
	}

	// IOC #2: not linked to any finding — should be filtered out by
	// finding_id=findingID but visible without a filter.
	if _, _, err := store.UpsertIOC(&db.IOCUpsert{
		Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4", Source: "observed",
	}); err != nil {
		t.Fatalf("upsert orphan: %v", err)
	}

	h := ListIOCs(store)

	// With finding_id filter — exactly one row, the linked sha256.
	req := httptest.NewRequest(http.MethodGet, "/api/iocs?finding_id="+strconv.FormatInt(findingID, 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got []*db.IOCRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("filtered: got %d entries, want 1; body=%s", len(got), rec.Body.String())
	}
	if got[0].Kind != "sha256" {
		t.Errorf("filtered[0].Kind = %q, want sha256", got[0].Kind)
	}

	// Without filter — both IOCs visible.
	req = httptest.NewRequest(http.MethodGet, "/api/iocs", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unfiltered: got %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode unfiltered: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("unfiltered: got %d entries, want 2", len(got))
	}

	// kind filter — only the ipv4.
	req = httptest.NewRequest(http.MethodGet, "/api/iocs?kind=ipv4", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("kind: got %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode kind: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "ipv4" {
		t.Errorf("kind filter: got %+v, want one ipv4 entry", got)
	}
}

func TestListIOCs_BadFindingIDReturns400(t *testing.T) {
	store := newTestStore(t)
	h := ListIOCs(store)
	req := httptest.NewRequest(http.MethodGet, "/api/iocs?finding_id=notanumber", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad finding_id: got %d, want 400", rec.Code)
	}
}

func TestListIOCs_SourceQueryParam(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "a", NormalizedValue: "a", Source: "catalog"})
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "b", NormalizedValue: "b", Source: "observed"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs?source=catalog", nil)
	ListIOCs(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var rows []db.IOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "a" {
		t.Errorf("?source=catalog expected one match; got %+v", rows)
	}
}

func TestListIOCs_QueryParam(t *testing.T) {
	st := newTestStore(t)
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc", Source: "catalog", Tags: "ransomware"})
	st.UpsertIOC(&db.IOCUpsert{Kind: "sha256", Value: "xyz", NormalizedValue: "xyz", Source: "catalog"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/iocs?q=RANSOM", nil)
	ListIOCs(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var rows []db.IOCRecord
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].NormalizedValue != "abc" {
		t.Errorf("?q=RANSOM expected one match by tag; got %+v", rows)
	}
}

func TestIOCs_SourceFilter_FeedSlug(t *testing.T) {
	st := newTestStore(t)
	feedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file", URL: "x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two IOCs: one feed-sourced, one observed.
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           "a" + strings.Repeat("b", 63),
		NormalizedValue: "a" + strings.Repeat("b", 63),
		Source:          "feed:f1",
		FeedID:          &feedID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           "c" + strings.Repeat("d", 63),
		NormalizedValue: "c" + strings.Repeat("d", 63),
		Source:          "observed",
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/iocs?source=feed:f1", nil)
	ListIOCs(st)(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}

	var got []db.IOCRecord
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected single feed:f1 row, got %d", len(got))
	}
	if got[0].Source != "feed:f1" {
		t.Fatalf("expected source=feed:f1, got %q", got[0].Source)
	}
}
