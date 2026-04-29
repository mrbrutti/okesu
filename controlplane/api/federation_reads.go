package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// Phase 9.6: federated read endpoints. Each child CP exposes a
// curated set of read endpoints that a parent CP can fetch via the
// federation token. Same shape as the local session-authed
// counterparts so a federated parent can re-use the wire types it
// already speaks.
//
// Auth boundary: token-auth ONLY, never session. Mixing the two on
// the same endpoint would let a parent CP scrape with a stolen
// session cookie. The local UI continues to use the
// session-authenticated /api/findings, /api/agents, /api/nodes —
// these /api/v1/federation/* siblings exist purely for cross-CP.

// requireFederationToken is the auth gate for /api/v1/federation/*.
// Reads cp_meta on each request (cheap; one row, indexed) — letting
// the operator rotate the token via UpdateCPMeta picks up
// immediately without restarting the CP.
func requireFederationToken(store *db.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meta, err := store.CPMeta()
		if err != nil {
			http.Error(w, "cp_meta unavailable", http.StatusInternalServerError)
			return
		}
		if !meta.VerifyFederationToken(r.Header.Get("X-Okesu-Federation-Token")) {
			w.Header().Set("WWW-Authenticate", `Federation realm="okesu-cp"`)
			http.Error(w, "federation token invalid or not configured", http.StatusUnauthorized)
			return
		}
		// Inject a synthetic user so downstream write handlers that
		// gate on UserFromContext (status mutations, audit-logged
		// writes) don't 401. The audit row will show this actor;
		// authorization happened at the federation-token check above.
		ctx := auth.WithUser(r.Context(), &db.User{
			ID:    0,
			Email: "federation@parent",
			Role:  "operator",
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// FederationFindings handles GET /api/v1/federation/findings.
// Same query params as /api/findings; same wire shape.
func FederationFindings(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, FindingsList(store))
}

// FederationInsightsFindings handles GET /api/v1/federation/insights/findings.
func FederationInsightsFindings(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, InsightsFindings(store))
}

// FederatedInsightsEvents wraps InsightsEvents. Each peer's bucket
// counts are summed into the parent's response, bucket-aligned by
// the (ts, bucket_ms) key. bucket_ms is the same on every peer
// because the API uses fixed-window quantization driven by ?since.
func FederatedInsightsEvents(local http.Handler, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		local.ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var merged eventsTimelineWire
		if err := json.Unmarshal(localRR.body, &merged); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/insights/events"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var s eventsTimelineWire
			if err := agg.FetchJSON(ctx, peer, path, &s); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			merged.Buckets = mergeEventBuckets(merged.Buckets, s.Buckets)
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederatedInsightsFindings wraps InsightsFindings. Series union'd
// across CPs; per-bucket per-series counts summed.
func FederatedInsightsFindings(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		InsightsFindings(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var merged findingsTimelineWire
		if err := json.Unmarshal(localRR.body, &merged); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/insights/findings"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var s findingsTimelineWire
			if err := agg.FetchJSON(ctx, peer, path, &s); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			merged.Series = unionStrings(merged.Series, s.Series)
			merged.Buckets = mergeFindingBuckets(merged.Buckets, s.Buckets)
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		// Always non-nil arrays (Phase 9 wire-shape policy).
		if merged.Series == nil {
			merged.Series = []string{}
		}
		if merged.Buckets == nil {
			merged.Buckets = []findingsBucketWire{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// Wire shapes for the insights endpoints — duplicated locally so
// federation_reads.go doesn't reach across to the unexported
// types in dashboard.go.
type eventsTimelineWire struct {
	BucketMs int64                `json:"bucket_ms"`
	Buckets  []eventsBucketWire   `json:"buckets"`
}
type eventsBucketWire struct {
	Ts    int64 `json:"ts"`
	Count int64 `json:"count"`
}
type findingsTimelineWire struct {
	BucketMs int64                  `json:"bucket_ms"`
	GroupBy  string                 `json:"group_by"`
	Series   []string               `json:"series"`
	Buckets  []findingsBucketWire   `json:"buckets"`
}
type findingsBucketWire struct {
	Ts int64            `json:"ts"`
	By map[string]int64 `json:"by"`
}

func mergeEventBuckets(a, b []eventsBucketWire) []eventsBucketWire {
	idx := make(map[int64]int, len(a))
	for i := range a {
		idx[a[i].Ts] = i
	}
	for _, r := range b {
		if i, ok := idx[r.Ts]; ok {
			a[i].Count += r.Count
		} else {
			idx[r.Ts] = len(a)
			a = append(a, r)
		}
	}
	return a
}

func mergeFindingBuckets(a, b []findingsBucketWire) []findingsBucketWire {
	idx := make(map[int64]int, len(a))
	for i := range a {
		idx[a[i].Ts] = i
	}
	for _, r := range b {
		if i, ok := idx[r.Ts]; ok {
			if a[i].By == nil {
				a[i].By = map[string]int64{}
			}
			for k, v := range r.By {
				a[i].By[k] += v
			}
		} else {
			idx[r.Ts] = len(a)
			a = append(a, r)
		}
	}
	return a
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			a = append(a, s)
		}
	}
	return a
}

// FederationFindingsSummary handles GET /api/v1/federation/findings/summary.
func FederationFindingsSummary(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, FindingsSummary(store))
}

// FederationFindingsGrouped handles GET /api/v1/federation/findings/grouped.
func FederationFindingsGrouped(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, FindingsGrouped(store))
}

// FederationDaimons handles GET /api/v1/federation/daimons.
// "daimons" is the operator-facing name for what the local API calls
// /api/agents — keeping it under /federation/daimons here so a
// future parent CP doesn't have to know about the historical
// /api/agents URL.
func FederationDaimons(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, AgentsList(store))
}

// FederationNodes handles GET /api/v1/federation/nodes.
func FederationNodes(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, NodesList(store))
}

// ── Parent-side aggregator wrappers ───────────────────────────────────────
//
// These wrap the local handlers so the parent's existing UI URLs
// (/api/findings, /api/agents, /api/nodes) transparently merge in
// federated rows when this CP has registered children.

// FederatedFindingsList wraps FindingsList. Local rows render with
// cp_source: nil; federated rows are tagged with the source CP's
// instance_id + display_name + region. Sorted by ts desc, capped
// at the per-source limit (so 30 rows from N+1 sources = 30*(N+1)
// max in flight, sorted, taken top N).
func FederatedFindingsList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Always run the local query first — even if every peer fails,
		// the local view should render.
		localRR := httpRecorder()
		FindingsList(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			// Bubble up the local handler's error verbatim.
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var local []findingJSON
		_ = json.Unmarshal(localRR.body, &local)

		// Fan out to peers. Each peer's response is also a
		// []findingJSON, so we can de-serialize into the same type.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/findings"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		merged := append([]findingJSON{}, local...)
		results, err := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []findingJSON
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			tag := &CPSourceRef{
				InstanceID:  peer.Snapshot.InstanceID,
				DisplayName: peer.Snapshot.DisplayName,
				Region:      peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i].CPSource = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if err != nil {
			// Aggregator failed before fanning out (DB read error).
			// Local results still render — better than 500ing the page.
			w.Header().Set("X-Okesu-Federation-Warning", err.Error())
		}
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}

		// Stable sort newest-first. Findings page keys off this order.
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].Ts > merged[j].Ts })

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederatedAgentsList wraps AgentsList — same merge pattern as
// findings but keyed by registration time (newest first).
func FederatedAgentsList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		AgentsList(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var local []agentJSON
		_ = json.Unmarshal(localRR.body, &local)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/daimons"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		merged := append([]agentJSON{}, local...)
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []agentJSON
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			tag := &CPSourceRef{
				InstanceID:  peer.Snapshot.InstanceID,
				DisplayName: peer.Snapshot.DisplayName,
				Region:      peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i].CPSource = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederatedNodesList wraps NodesList. Same pattern.
func FederatedNodesList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		NodesList(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var local []nodeJSON
		_ = json.Unmarshal(localRR.body, &local)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/nodes"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		merged := append([]nodeJSON{}, local...)
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []nodeJSON
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			tag := &CPSourceRef{
				InstanceID:  peer.Snapshot.InstanceID,
				DisplayName: peer.Snapshot.DisplayName,
				Region:      peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i].CPSource = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederatedFindingsSummary wraps FindingsSummary. Merges per-CP
// summaries into one global summary: scalar counts are summed,
// per-agent and per-category breakdowns are folded by name (so
// the same agent name on east + west sums to a single row), and
// the 24-hour trend is bucket-aligned and summed.
//
// The combined summary doesn't carry cp_source — it's an aggregate.
// Callers who want per-CP detail use the introspect counts on the
// Federation page.
func FederatedFindingsSummary(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Local first.
		localRR := httpRecorder()
		FindingsSummary(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var merged db.FindingsSummary
		if err := json.Unmarshal(localRR.body, &merged); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var mu sync.Mutex
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var s db.FindingsSummary
			if err := agg.FetchJSON(ctx, peer, "/api/v1/federation/findings/summary", &s); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			merged.Open += s.Open
			merged.Critical += s.Critical
			merged.High += s.High
			merged.Medium += s.Medium
			merged.Low += s.Low
			merged.Info += s.Info
			merged.Last24h += s.Last24h
			merged.ByAgent = mergeAgentRows(merged.ByAgent, s.ByAgent)
			merged.ByCategory = mergeCategoryRows(merged.ByCategory, s.ByCategory)
			merged.Trend = mergeTrendRows(merged.Trend, s.Trend)
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

func mergeAgentRows(a, b []db.FindingsByAgent) []db.FindingsByAgent {
	idx := map[string]int{}
	for i := range a {
		idx[a[i].Agent] = i
	}
	for _, r := range b {
		if i, ok := idx[r.Agent]; ok {
			a[i].Open += r.Open
			a[i].Critical += r.Critical
			a[i].High += r.High
		} else {
			idx[r.Agent] = len(a)
			a = append(a, r)
		}
	}
	return a
}

func mergeCategoryRows(a, b []db.FindingsByCategory) []db.FindingsByCategory {
	idx := map[string]int{}
	for i := range a {
		idx[a[i].Category] = i
	}
	for _, r := range b {
		if i, ok := idx[r.Category]; ok {
			a[i].Open += r.Open
			a[i].Critical += r.Critical
			a[i].High += r.High
		} else {
			idx[r.Category] = len(a)
			a = append(a, r)
		}
	}
	return a
}

func mergeTrendRows(a, b []db.FindingsTrendBucket) []db.FindingsTrendBucket {
	idx := map[int64]int{}
	for i := range a {
		idx[a[i].HourTs] = i
	}
	for _, r := range b {
		if i, ok := idx[r.HourTs]; ok {
			a[i].Count += r.Count
		} else {
			idx[r.HourTs] = len(a)
			a = append(a, r)
		}
	}
	return a
}

// FederatedFindingsGrouped wraps FindingsGrouped. Same finding (same
// dedup_key OR same title+severity+agent fingerprint) reported on
// multiple CPs MERGES into one row with combined count + union of
// hosts + the list of contributing CPs. Operators see one row per
// distinct issue regardless of how many regions reported it.
func FederatedFindingsGrouped(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		FindingsGrouped(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var local []db.FindingGroup
		_ = json.Unmarshal(localRR.body, &local)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/findings/grouped"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		// Bucket by fingerprint (dedup_key when set, else
		// title|severity|agent). Each bucket accumulates count, hosts
		// (union), first/last_seen extremes, and the set of CPs that
		// contributed. Local rows enter with cp_sources = empty (they
		// came from this CP, no tag needed).
		type bucket struct {
			Group   db.FindingGroup
			Sources []CPSourceRef
		}
		buckets := map[string]*bucket{}
		mergeRow := func(g db.FindingGroup, src *CPSourceRef) {
			fp := g.DedupKey
			if fp == "" {
				fp = g.Title + "|" + g.Severity + "|" + g.Agent
			}
			b, ok := buckets[fp]
			if !ok {
				b = &bucket{Group: g}
				buckets[fp] = b
			} else {
				b.Group.Count += g.Count
				b.Group.Hosts = unionStrings(b.Group.Hosts, g.Hosts)
				if g.FirstSeen < b.Group.FirstSeen || b.Group.FirstSeen == 0 {
					b.Group.FirstSeen = g.FirstSeen
				}
				if g.LastSeen > b.Group.LastSeen {
					b.Group.LastSeen = g.LastSeen
					b.Group.LatestID = g.LatestID
				}
			}
			if src != nil {
				b.Sources = append(b.Sources, *src)
			}
		}
		for _, g := range local {
			mergeRow(g, nil)
		}

		var mu sync.Mutex
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []db.FindingGroup
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			src := CPSourceRef{
				InstanceID:  peer.Snapshot.InstanceID,
				DisplayName: peer.Snapshot.DisplayName,
				Region:      peer.Snapshot.Region,
			}
			mu.Lock()
			defer mu.Unlock()
			for _, g := range rows {
				mergeRow(g, &src)
			}
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}

		// Materialize the buckets back into wire-shape groups.
		// CPSources is encoded into the existing cp_source field
		// when there's exactly one source (compact for the common
		// case); when there are multiple, we use a new cp_sources
		// array so the UI can render multiple chips.
		out := make([]groupedFindingJSON, 0, len(buckets))
		for fp, b := range buckets {
			row := groupedFindingJSON{FindingGroup: b.Group}
			// Disambiguate group_key when bucket spans CPs (so React
			// keys stay unique even on dedup-less fingerprints).
			if len(b.Sources) > 0 {
				row.GroupKey = "fed:" + fp
			}
			if len(b.Sources) == 1 {
				row.CPSource = &b.Sources[0]
			} else if len(b.Sources) > 1 {
				row.CPSources = b.Sources
			}
			out = append(out, row)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// groupedFindingJSON wraps db.FindingGroup with the federated-merge
// extras. Embedding lets the wire shape stay backwards-compatible:
// a non-federated CP returns the same fields it always has, and a
// federated CP adds cp_source (single-CP origin) or cp_sources (the
// merged group spans multiple CPs).
type groupedFindingJSON struct {
	db.FindingGroup
	CPSource  *CPSourceRef  `json:"cp_source,omitempty"`
	CPSources []CPSourceRef `json:"cp_sources,omitempty"`
}

// RequireFederationToken is exported so server.go can wrap arbitrary
// handlers (e.g. EventsList) with the same token-auth gate the
// findings/daimons/nodes federation endpoints use.
func RequireFederationToken(store *db.Store, next http.Handler) http.HandlerFunc {
	return requireFederationToken(store, next.ServeHTTP)
}

// FederatedEventsStream wraps the local SSE handler. For each
// healthy peer, opens an outbound SSE connection to its
// /api/v1/federation/events/stream, parses each event line, tags it
// with the peer's cp_source (rewriting the JSON payload), and forwards
// it to the same client connection. Closing the client connection
// (or any peer connection) tears down all spawned goroutines.
//
// One peer goroutine per UI client. At small scale (a few children,
// dozens of UI clients) this is fine; at larger scale a shared
// per-peer fan-out broker would amortise the outbound connections.
// That's a Phase 9.7 concern.
// FederatedEventsStream wraps EventsStream. The local broadcaster
// supplies local events; for each healthy peer we open an outbound
// SSE connection to /api/v1/federation/events/stream and forward
// events tagged with cp_source. One peer goroutine per UI client —
// fine at small scale, a Phase 9.7 follow-up if it needs sharing.
func FederatedEventsStream(bcast EventsSubscriber, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		_, _ = w.Write([]byte(": connected\n\n"))
		flusher.Flush()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// merged carries every event line bound for the client.
		// Buffered so a slow peer can't block a healthy one.
		merged := make(chan []byte, 256)

		// Local source — subscribe directly to the broadcaster.
		localCh, unsubscribe := bcast.Subscribe()
		defer unsubscribe()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case line, ok := <-localCh:
					if !ok {
						return
					}
					select {
					case <-ctx.Done():
						return
					case merged <- line:
					}
				}
			}
		}()

		// Per-peer goroutines — one outbound SSE per healthy child.
		peers, _ := agg.HealthyPeers()
		for _, p := range peers {
			p := p
			go streamFromPeer(ctx, p, r.URL.RawQuery, merged)
		}

		// Pump merged → client until the client disconnects.
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-merged:
				if !ok {
					return
				}
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(line)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	}
}

// streamFromPeer dials peer's federation SSE, parses each "data:"
// line, injects cp_source into the JSON, and forwards to merged.
// Reconnects with exponential backoff until ctx is done.
func streamFromPeer(ctx context.Context, peer federation.Peer, rawQuery string, merged chan<- []byte) {
	tag := CPSourceRef{
		InstanceID:  peer.Snapshot.InstanceID,
		DisplayName: peer.Snapshot.DisplayName,
		Region:      peer.Snapshot.Region,
	}
	url := strings.TrimRight(peer.Row.URL, "/") + "/api/v1/federation/events/stream"
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		req.Header.Set("X-Okesu-Federation-Token", peer.Row.Token)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			backoff = time.Second
			scanSSEData(ctx, resp.Body, merged, &tag)
			resp.Body.Close()
		} else {
			if resp != nil {
				resp.Body.Close()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

// scanSSEData reads SSE-framed text from r; for each "data:" line it
// emits the body onto merged, with cp_source injected when tag != nil.
func scanSSEData(ctx context.Context, r io.Reader, merged chan<- []byte, tag *CPSourceRef) {
	br := bufio.NewReader(r)
	for {
		if ctx.Err() != nil {
			return
		}
		line, err := br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return
		}
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		body := bytes.TrimSpace(line[6:])
		if tag != nil {
			body = injectCPSource(body, tag)
		}
		select {
		case <-ctx.Done():
			return
		case merged <- body:
		}
	}
}

// injectCPSource adds cp_source to the event JSON so the UI's event
// renderer can pick it up. Cheap — unmarshal to map, set the key,
// re-marshal.
func injectCPSource(body []byte, tag *CPSourceRef) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["cp_source"] = tag
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// FederatedEventsList wraps EventsList. Each event from a federated
// child is tagged with cp_source. Sorted by ts desc after merge.
func FederatedEventsList(local http.Handler, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		local.ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var localRows []eventJSON
		_ = json.Unmarshal(localRR.body, &localRows)

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/events"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		merged := append([]eventJSON{}, localRows...)
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []eventJSON
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			tag := &CPSourceRef{
				InstanceID:  peer.Snapshot.InstanceID,
				DisplayName: peer.Snapshot.DisplayName,
				Region:      peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i].CPSource = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].Ts > merged[j].Ts })
		// Cap to the originally-requested limit so a federated 100
		// per source doesn't return 300 rows.
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := parseIntDefault(v, 100); err == nil && n > 0 {
				limit = n
			}
		}
		if len(merged) > limit {
			merged = merged[:limit]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

func parseIntDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return def, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// httpRecorder is a tiny in-memory ResponseWriter for capturing a
// handler's output before mutating it. Cheaper than wiring through
// httptest in the request path; only buffers the body once.
type recorder struct {
	header http.Header
	code   int
	body   []byte
}

func httpRecorder() *recorder {
	return &recorder{header: http.Header{}, code: http.StatusOK}
}

func (r *recorder) Header() http.Header        { return r.header }
func (r *recorder) WriteHeader(code int)       { r.code = code }
func (r *recorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}

// silence unused-import warning when fmt is only used in a debug
// branch — keeps the comment-driven code honest.
var _ = fmt.Sprintf
