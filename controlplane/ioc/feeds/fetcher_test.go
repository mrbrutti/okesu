package feeds

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetcher_SingleFile_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer xyz" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		w.Write([]byte("rule X { strings: $a = \"x\" condition: $a }"))
	}))
	defer srv.Close()

	f := NewFetcher(t.TempDir())
	got, err := f.FetchSingleFile(srv.URL, "Bearer xyz")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.HasPrefix(string(got), "rule X") {
		t.Fatalf("body: %q", got)
	}
}

func TestFetcher_SingleFile_NoAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no auth header, got %q", got)
		}
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	f := NewFetcher(t.TempDir())
	got, err := f.FetchSingleFile(srv.URL, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("body: %q", got)
	}
}

func TestFetcher_SingleFile_NonOKReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 503)
	}))
	defer srv.Close()
	f := NewFetcher(t.TempDir())
	if _, err := f.FetchSingleFile(srv.URL, ""); err == nil {
		t.Fatal("expected error on 503")
	}
}
