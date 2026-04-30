package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"
)

// hookFakeEnricher returns a canned Result the test controls.
type hookFakeEnricher struct {
	name    string
	supports map[string]bool
	result  *Result
	err     error
}

func (f *hookFakeEnricher) Name() string             { return f.name }
func (f *hookFakeEnricher) Supports(k string) bool   { return f.supports[k] }
func (f *hookFakeEnricher) Lookup(_ context.Context, _, _ string) (*Result, error) {
	return f.result, f.err
}

// hookCapture captures every EnrichedEvent the service fires.
type hookCapture struct {
	events []EnrichedEvent
}

func (c *hookCapture) onEnriched(e EnrichedEvent) {
	c.events = append(c.events, e)
}

func TestService_EnrichedHook_FiresOnFreshWrite(t *testing.T) {
	store := newFakeStore()
	enr := &hookFakeEnricher{
		name:    "vt",
		supports: map[string]bool{"sha256": true},
		result: &Result{Adapter: "vt", Verdict: "malicious", Score: 90},
	}
	svc := New([]Enricher{enr}, store, time.Hour, 100)

	cap := &hookCapture{}
	svc.SetEnrichedHook(cap.onEnriched)

	if _, err := svc.Enrich(context.Background(), 42, "sha256", "abc123"); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if len(cap.events) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(cap.events))
	}
	got := cap.events[0]
	if got.IOCID != 42 || got.IOCKind != "sha256" || got.NormalizedValue != "abc123" {
		t.Errorf("hook payload mismatch: %+v", got)
	}
	if got.Adapter != "vt" || got.Verdict != "malicious" || got.Score != 90 {
		t.Errorf("hook payload mismatch: %+v", got)
	}
}

func TestService_EnrichedHook_DoesNotFireOnCacheHit(t *testing.T) {
	store := newFakeStore()
	// Pre-populate cache so the call hits store.GetFresh's success
	// path and skips the adapter.
	store.Upsert(42, "vt", &Result{Adapter: "vt", Verdict: "clean", Score: 0})

	enr := &hookFakeEnricher{
		name:    "vt",
		supports: map[string]bool{"sha256": true},
		result: &Result{Adapter: "vt", Verdict: "should not be called", Score: -1},
	}
	svc := New([]Enricher{enr}, store, time.Hour, 100)

	cap := &hookCapture{}
	svc.SetEnrichedHook(cap.onEnriched)

	if _, err := svc.Enrich(context.Background(), 42, "sha256", "abc123"); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if len(cap.events) != 0 {
		t.Fatalf("hook fired on cache hit (should be silent): %v", cap.events)
	}
}

func TestService_EnrichedHook_DoesNotFireOnAdapterError(t *testing.T) {
	store := newFakeStore()
	enr := &hookFakeEnricher{
		name:    "vt",
		supports: map[string]bool{"sha256": true},
		err:     errors.New("vendor 503"),
	}
	svc := New([]Enricher{enr}, store, time.Hour, 100)

	cap := &hookCapture{}
	svc.SetEnrichedHook(cap.onEnriched)

	if _, err := svc.Enrich(context.Background(), 42, "sha256", "abc123"); err != nil {
		t.Fatalf("enrich (errors are soft per impl): %v", err)
	}
	if len(cap.events) != 0 {
		t.Fatalf("hook fired despite adapter error: %v", cap.events)
	}
}

func TestService_EnrichedHook_PanicDoesNotKillEnrichment(t *testing.T) {
	store := newFakeStore()
	enr := &hookFakeEnricher{
		name:    "vt",
		supports: map[string]bool{"sha256": true},
		result: &Result{Adapter: "vt", Verdict: "malicious", Score: 90},
	}
	svc := New([]Enricher{enr}, store, time.Hour, 100)
	svc.SetEnrichedHook(func(_ EnrichedEvent) {
		panic("hook gone wrong")
	})

	// Should not panic even though the hook does.
	if _, err := svc.Enrich(context.Background(), 42, "sha256", "abc123"); err != nil {
		t.Fatalf("enrich: %v", err)
	}
}
