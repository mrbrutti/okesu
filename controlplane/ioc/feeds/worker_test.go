package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestWorker_RefreshOne_SingleFile_YARA(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("rule A { strings: $a = \"x\" condition: $a }\n\nrule B { strings: $b = \"y\" condition: $b }\n"))
	}))
	defer srv.Close()

	feedID, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: srv.URL, Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	w := NewWorker(st, NewFetcher(t.TempDir()))
	fc, _ := st.GetFeedConfig(feedID)
	if err := w.RefreshOne(context.Background(), fc); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	rows, _ := st.ListIOCsByFeed(feedID)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got, _ := st.GetFeedConfig(feedID)
	if got.LastRefreshStatus != "ok" {
		t.Fatalf("status: %q", got.LastRefreshStatus)
	}
	if got.LastRefreshEntryCount != 2 {
		t.Fatalf("entry count: %d", got.LastRefreshEntryCount)
	}
	if !got.LastRefreshAt.Valid {
		t.Fatalf("last_refresh_at not set")
	}
}

func TestWorker_RefreshOne_FetchErrorMarksFailure(t *testing.T) {
	st := newTestStore(t)
	// Closed server → fetch fails immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	feedID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: srv.URL, Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})

	w := NewWorker(st, NewFetcher(t.TempDir()))
	fc, _ := st.GetFeedConfig(feedID)
	err := w.RefreshOne(context.Background(), fc)
	if err == nil {
		t.Fatal("expected error from closed server")
	}
	got, _ := st.GetFeedConfig(feedID)
	if got.LastRefreshStatus != "error" {
		t.Fatalf("status: %q", got.LastRefreshStatus)
	}
	if got.LastRefreshError == "" {
		t.Fatalf("expected non-empty error message")
	}
}

func TestWorker_RefreshOne_UnknownParserErrors(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("anything"))
	}))
	defer srv.Close()

	feedID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: srv.URL, Parser: "yara", // valid initially
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	// Bypass FeedConfigInsert validation by mutating the in-memory FeedConfig.
	fc, _ := st.GetFeedConfig(feedID)
	fc.Parser = "this_does_not_exist"
	w := NewWorker(st, NewFetcher(t.TempDir()))
	if err := w.RefreshOne(context.Background(), fc); err == nil {
		t.Fatal("expected error for unknown parser enum")
	}
}
