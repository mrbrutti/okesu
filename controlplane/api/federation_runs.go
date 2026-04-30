// Federation wrappers for ad-hoc agent runs (CreateRun /
// RunStatus / RunsList). Mirrors the pattern used for orchestration
// runs in federation_writes.go — each parent-side wrapper merges
// local rows with each child's federation_runs response, and each
// child-side sibling is the local handler with token auth wrapped
// around it.
//
// Cancel is wired separately in federation_writes_deploy_run.go;
// CreateRun is wired in federation_writes_deploy_run.go too. This
// file completes the read-side picture so an operator opening the
// Runs page on the parent sees ad-hoc runs from every healthy child.
//
// Streaming log SSE (`/api/runs/{id}/log`) is intentionally NOT
// federated in v1 — bridging long-lived SSE streams across CPs is
// expensive and brittle. The runs detail page deep-links to the
// child's UI for live log when `cp_source` is present on the row.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// FederatedRunsList wraps RunsList, fanning out to every healthy
// peer and tagging remote rows with cp_source so the UI can show
// origin + deep-link to the child for streaming log.
//
// Returns a JSON array; query string (limit/offset) is forwarded
// verbatim to each peer.
func FederatedRunsList(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localRR := httpRecorder()
		RunsList(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var localRows []map[string]any
		_ = json.Unmarshal(localRR.body, &localRows)
		if localRows == nil {
			localRows = []map[string]any{}
		}

		peerPath := "/api/v1/federation/runs"
		if rq := r.URL.RawQuery; rq != "" {
			peerPath += "?" + rq
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var mu sync.Mutex
		merged := append([]map[string]any{}, localRows...)
		results, _ := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			body, err := agg.FetchRaw(ctx, peer, peerPath)
			if err != nil {
				return err
			}
			var rows []map[string]any
			if err := json.Unmarshal(body, &rows); err != nil {
				return err
			}
			tag := map[string]any{
				"instance_id":  peer.Snapshot.InstanceID,
				"display_name": peer.Snapshot.DisplayName,
				"region":       peer.Snapshot.Region,
			}
			for i := range rows {
				rows[i]["cp_source"] = tag
			}
			mu.Lock()
			merged = append(merged, rows...)
			mu.Unlock()
			return nil
		})
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		// Newest-first by started_at.
		sort.SliceStable(merged, func(i, j int) bool {
			si, _ := merged[i]["started_at"].(string)
			sj, _ := merged[j]["started_at"].(string)
			return si > sj
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederationRunsList — child-side, token-authed sibling.
func FederationRunsList(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, RunsList(store))
}

// FederatedRunStatus wraps RunStatus with the ?cp= proxy. A run id
// is per-CP-local, so a federated row's detail click MUST forward
// to the owning child or 404.
func FederatedRunStatus(reg *RunRegistry, store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/runs/", "/api/v1/federation/runs/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		RunStatus(reg, store).ServeHTTP(w, r)
	}
}

// FederationRunStatus — child-side, token-authed.
func FederationRunStatus(reg *RunRegistry, store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, RunStatus(reg, store))
}
