package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeEnricher struct {
	name  string
	calls int
	resp  *Result
	err   error
}

func (f *fakeEnricher) Name() string { return f.name }
func (f *fakeEnricher) Supports(kind string) bool {
	return kind == "sha256" || kind == "ipv4" || kind == "domain"
}
func (f *fakeEnricher) Lookup(ctx context.Context, kind, normalizedValue string) (*Result, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeStore struct {
	enrichments map[string]*Result
}

func newFakeStore() *fakeStore { return &fakeStore{enrichments: map[string]*Result{}} }
func (s *fakeStore) GetFresh(iocID int64, adapter string) (*Result, error) {
	if r, ok := s.enrichments[adapterKey(iocID, adapter)]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}
func (s *fakeStore) Upsert(iocID int64, adapter string, r *Result) error {
	s.enrichments[adapterKey(iocID, adapter)] = r
	return nil
}

func TestService_Enrich_CacheMissCallsAdapter(t *testing.T) {
	enricher := &fakeEnricher{
		name: "vt",
		resp: &Result{Adapter: "vt", Verdict: "malicious", Score: 80, RawJSON: "{}"},
	}
	store := newFakeStore()
	svc := New([]Enricher{enricher}, store, time.Hour, 100)
	got, err := svc.Enrich(context.Background(), 42, "sha256", "abc")
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(got) != 1 || got[0].Verdict != "malicious" {
		t.Errorf("unexpected: %+v", got)
	}
	if enricher.calls != 1 {
		t.Errorf("expected 1 adapter call; got %d", enricher.calls)
	}
}

func TestService_Enrich_CacheHitSkipsAdapter(t *testing.T) {
	enricher := &fakeEnricher{name: "vt", resp: &Result{}}
	store := newFakeStore()
	store.Upsert(42, "vt", &Result{Adapter: "vt", Verdict: "clean", RawJSON: "{}"})
	svc := New([]Enricher{enricher}, store, time.Hour, 100)
	got, _ := svc.Enrich(context.Background(), 42, "sha256", "abc")
	if len(got) != 1 || got[0].Verdict != "clean" {
		t.Errorf("unexpected: %+v", got)
	}
	if enricher.calls != 0 {
		t.Errorf("expected 0 adapter calls (cache hit); got %d", enricher.calls)
	}
}

func TestService_Enrich_SkipsAdapterThatDoesNotSupportKind(t *testing.T) {
	enricher := &fakeEnricher{
		name: "shodan",
		resp: &Result{Adapter: "shodan", RawJSON: "{}"},
	}
	store := newFakeStore()
	svc := New([]Enricher{enricher}, store, time.Hour, 100)
	got, _ := svc.Enrich(context.Background(), 42, "cve", "CVE-2024-1234")
	if len(got) != 0 {
		t.Errorf("expected no results for unsupported kind; got %+v", got)
	}
	if enricher.calls != 0 {
		t.Errorf("expected adapter to be skipped; got %d calls", enricher.calls)
	}
}

func TestRateLimiter_BlocksWhenRPSExceeded(t *testing.T) {
	rl := newRateLimiter(2.0)
	start := time.Now()
	for i := 0; i < 4; i++ {
		rl.wait(context.Background())
	}
	elapsed := time.Since(start)
	if elapsed < 800*time.Millisecond {
		t.Errorf("rate limiter did not throttle; elapsed=%v", elapsed)
	}
}
