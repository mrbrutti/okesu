// Package enrichment exposes the IOC-enrichment service. The Service
// fans out to per-vendor adapters, caches results in the supplied
// Store, and rate-limits per adapter. Adapters are tested individually
// against httptest fakes; the Service is tested against fake adapters.
package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Result is the normalized response returned from any adapter. RawJSON
// is the full vendor body for forensic re-inspection.
type Result struct {
	Adapter string        `json:"adapter"`
	Verdict string        `json:"verdict"`
	Score   int64         `json:"score"`
	RawJSON string        `json:"raw_json"`
	TTL     time.Duration `json:"-"`
}

// Enricher is the per-vendor adapter contract. Implementations live in
// sibling files (virustotal.go, abuseipdb.go, shodan.go).
type Enricher interface {
	Name() string
	Supports(kind string) bool
	Lookup(ctx context.Context, kind, normalizedValue string) (*Result, error)
}

// Store is the cache interface the Service uses. Implemented by the
// real db.Store via a thin wrapper in api/orchestration_actions.go.
type Store interface {
	GetFresh(iocID int64, adapter string) (*Result, error)
	Upsert(iocID int64, adapter string, r *Result) error
}

// EnrichedEvent is what the post-write hook fires with. The trigger
// path uses these to spawn `on: ioc_enriched` orchestrations.
//
// Only fires after a CACHE MISS that resulted in a real adapter
// call AND a successful Upsert — cache hits don't fire the hook
// because no new information was learned. Otherwise every step
// that calls `enrich_ioc` would re-fire any matching trigger
// orchestration, leading to runaway loops.
type EnrichedEvent struct {
	IOCID           int64
	IOCKind         string
	NormalizedValue string
	Adapter         string
	Verdict         string
	Score           int64
}

// Service orchestrates the cache→adapter→cache flow per IOC.
type Service struct {
	adapters   []Enricher
	store      Store
	defaultTTL time.Duration
	rateLimit  float64
	mu         sync.Mutex
	limiters   map[string]*rateLimiter
	// onEnriched is called once per fresh cache write. nil = no hook
	// installed, in which case enrichment is purely cache-and-return.
	onEnriched func(EnrichedEvent)
}

func New(adapters []Enricher, store Store, defaultTTL time.Duration, rateRPS float64) *Service {
	return &Service{
		adapters:   adapters,
		store:      store,
		defaultTTL: defaultTTL,
		rateLimit:  rateRPS,
		limiters:   make(map[string]*rateLimiter),
	}
}

// SetEnrichedHook installs the post-write hook fired once per fresh
// cache miss + successful Upsert. Cache hits do NOT fire the hook
// (already-known data shouldn't re-trigger orchestrations). Called
// at server boot to wire the enrichment-trigger dispatch path.
func (s *Service) SetEnrichedHook(fn func(EnrichedEvent)) {
	s.onEnriched = fn
}

// Enrich runs every adapter that supports the kind. Adapters fail
// independently — a vendor outage on one doesn't block the others.
func (s *Service) Enrich(ctx context.Context, iocID int64, kind, normalizedValue string) ([]Result, error) {
	var out []Result
	for _, a := range s.adapters {
		if !a.Supports(kind) {
			continue
		}
		if r, err := s.store.GetFresh(iocID, a.Name()); err == nil && r != nil {
			out = append(out, *r)
			continue
		}
		s.limiterFor(a.Name()).wait(ctx)
		r, err := a.Lookup(ctx, kind, normalizedValue)
		if err != nil || r == nil {
			continue
		}
		if upErr := s.store.Upsert(iocID, a.Name(), r); upErr == nil && s.onEnriched != nil {
			// Best-effort hook fire. Run synchronously so the
			// trigger dispatch happens on the same goroutine that
			// just enriched — keeps the test ergonomics simple
			// (no fan-in race) and the trigger layer's downstream
			// already kicks runs asynchronously via the engine.
			func() {
				defer func() {
					// A panicking hook must not corrupt the
					// enrichment loop.
					_ = recover()
				}()
				s.onEnriched(EnrichedEvent{
					IOCID:           iocID,
					IOCKind:         kind,
					NormalizedValue: normalizedValue,
					Adapter:         a.Name(),
					Verdict:         r.Verdict,
					Score:           r.Score,
				})
			}()
		}
		out = append(out, *r)
	}
	return out, nil
}

func (s *Service) AdapterTTL(r *Result) time.Duration {
	if r.TTL > 0 {
		return r.TTL
	}
	return s.defaultTTL
}

func (s *Service) limiterFor(adapter string) *rateLimiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rl, ok := s.limiters[adapter]; ok {
		return rl
	}
	rl := newRateLimiter(s.rateLimit)
	s.limiters[adapter] = rl
	return rl
}

func adapterKey(iocID int64, adapter string) string {
	return strconv.FormatInt(iocID, 10) + "|" + adapter
}

// rateLimiter is a simple token-bucket: capacity 1, refill at `rps`
// tokens/sec. wait blocks until a token is available or ctx cancels.
type rateLimiter struct {
	rps    float64
	mu     sync.Mutex
	nextOk time.Time
}

func newRateLimiter(rps float64) *rateLimiter {
	if rps <= 0 {
		rps = 1
	}
	return &rateLimiter{rps: rps}
}

// wait blocks until the limiter would allow another call. Burst is 1.
func (r *rateLimiter) wait(ctx context.Context) {
	r.mu.Lock()
	now := time.Now()
	if r.nextOk.Before(now) {
		r.nextOk = now
	}
	wait := r.nextOk.Sub(now)
	r.nextOk = r.nextOk.Add(time.Duration(float64(time.Second) / r.rps))
	r.mu.Unlock()
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
}

// EncodeRawJSON helper for adapters: marshal arbitrary vendor body to
// a string for storage.
func EncodeRawJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"_error":%q}`, err.Error())
	}
	return string(b)
}
