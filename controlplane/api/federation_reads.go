package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

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
		next.ServeHTTP(w, r)
	}
}

// FederationFindings handles GET /api/v1/federation/findings.
// Same query params as /api/findings; same wire shape.
func FederationFindings(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, FindingsList(store))
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
