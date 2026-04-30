package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrependLessons_NoneNoChange(t *testing.T) {
	got := PrependLessons("base prompt", nil)
	if got != "base prompt" {
		t.Errorf("expected pass-through; got %q", got)
	}
}

func TestPrependLessons_AddsHeaderAndBullets(t *testing.T) {
	got := PrependLessons("base prompt", []string{"first lesson", "second lesson"})
	if !strings.HasPrefix(got, "## Lessons from prior runs") {
		t.Errorf("expected lessons header at start; got %q", got)
	}
	if !strings.Contains(got, "first lesson") || !strings.Contains(got, "second lesson") {
		t.Errorf("missing lessons in output: %q", got)
	}
	if !strings.HasSuffix(got, "base prompt") {
		t.Errorf("base prompt should be suffix; got %q", got)
	}
}

func TestPrependLessons_TrimsWhitespaceOnlyEntries(t *testing.T) {
	// Whitespace-only lessons are useless — drop them rather than
	// emit empty bullets.
	got := PrependLessons("base", []string{"real lesson", "   ", ""})
	if strings.Count(got, "- ") != 1 {
		t.Errorf("expected 1 bullet (whitespace-only filtered); got: %q", got)
	}
}

// TestFetchLessons_DecodesWireShape pins the JSON contract: the CP
// emits db.AgentLesson with fields {ID, AgentName, Text,
// OrchestrationRunID, OrchestrationStepID}; the daemon only reads
// Text. If anyone renames `Text` on the producer side without a
// downstream-aware grep, this test catches it.
func TestFetchLessons_DecodesWireShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
		    {"ID":1,"AgentName":"investigator","Text":"keep grep tight","OrchestrationRunID":7,"OrchestrationStepID":"classify"},
		    {"ID":2,"AgentName":"investigator","Text":"validate before delete","OrchestrationRunID":7,"OrchestrationStepID":"classify"}
		]`))
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	got := FetchLessons(context.Background(), srv.URL, "investigator", client)
	if len(got) != 2 || got[0] != "keep grep tight" || got[1] != "validate before delete" {
		t.Errorf("unexpected lessons: %v", got)
	}
}

func TestFetchLessons_ReturnsNilOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	if got := FetchLessons(context.Background(), srv.URL, "x", client); got != nil {
		t.Errorf("expected nil on 500; got %v", got)
	}
}

func TestFetchLessons_ReturnsNilOnEmptyURL(t *testing.T) {
	if got := FetchLessons(context.Background(), "", "x", nil); got != nil {
		t.Errorf("expected nil on empty URL; got %v", got)
	}
}
