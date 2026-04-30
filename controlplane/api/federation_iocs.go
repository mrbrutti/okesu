package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// RenderFederationIOCs produces the JSON the federation S3 publisher
// writes (Phase B+) and that the parent's aggregator HTTP-fetches.
// Capped at 5000 rows by last_seen DESC; matches RenderFederationFindings.
func RenderFederationIOCs(store *db.Store, limit int) ([]byte, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := store.ListIOCs(db.IOCListFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	return json.Marshal(rows)
}

// FederationIOCs serves the local CP's IOC view at
// /api/v1/federation/iocs (mounted behind requireFederationToken in
// server.go). The parent's aggregator pulls this on every /api/iocs
// request.
func FederationIOCs(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := RenderFederationIOCs(store, 5000)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// FederationIOCByKV / Observations / Relationships re-export the local
// by-kv handlers verbatim — federation-side semantics are identical to
// "local CP's view of its own data". The federation token middleware
// is what differentiates these mounts at the server.go level.
func FederationIOCByKV(store *db.Store) http.HandlerFunc {
	return GetIOCByKVHandler(store)
}

func FederationIOCObservationsByKV(store *db.Store) http.HandlerFunc {
	return ListIOCObservationsByKVHandler(store)
}

func FederationIOCRelationshipsByKV(store *db.Store) http.HandlerFunc {
	return ListIOCRelationshipsByKVHandler(store)
}

// FederatedListIOCs is the parent-side handler that wraps ListIOCs.
// Pattern mirrors FederatedFindingsList: run local handler via
// httpRecorder, FanOut peers via FetchJSON, merge by (kind, normalized_value).
func FederatedListIOCs(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Local first.
		localRR := httpRecorder()
		ListIOCs(store).ServeHTTP(localRR, r)
		if localRR.code != http.StatusOK {
			w.WriteHeader(localRR.code)
			_, _ = w.Write(localRR.body)
			return
		}
		var localRows []*db.IOCRecord
		_ = json.Unmarshal(localRR.body, &localRows)

		localCP, err := selfCPSource(store)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		contribs := []contribIOCs{{cp: localCP, rows: localRows}}

		// Fan out to peers.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/iocs"
		if rq := r.URL.RawQuery; rq != "" {
			path += "?" + rq
		}
		var mu sync.Mutex
		results, err := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []*db.IOCRecord
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			contribs = append(contribs, contribIOCs{cp: peerCPSource(peer), rows: rows})
			return nil
		})
		if err != nil {
			w.Header().Set("X-Okesu-Federation-Warning", err.Error())
		}
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}

		merged := MergeIOCs(contribs)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederatedGetIOCByKV: single (kind, value) detail merged across CPs.
// 404 if no CP has the row.
func FederatedGetIOCByKV(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if kind == "" || value == "" {
			http.Error(w, "missing kind or value", http.StatusBadRequest)
			return
		}
		localCP, err := selfCPSource(store)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var contribs []contribIOCs
		rec, err := store.GetIOCByKV(kind, value)
		switch {
		case err == nil:
			contribs = append(contribs, contribIOCs{cp: localCP, rows: []*db.IOCRecord{rec}})
		case errors.Is(err, sql.ErrNoRows):
			// Local doesn't have it — peers might. Continue.
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/iocs/by-kv?kind=" + url.QueryEscape(kind) + "&value=" + url.QueryEscape(value)
		var mu sync.Mutex
		results, fanErr := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rec db.IOCRecord
			if err := agg.FetchJSON(ctx, peer, path, &rec); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			contribs = append(contribs, contribIOCs{cp: peerCPSource(peer), rows: []*db.IOCRecord{&rec}})
			return nil
		})
		if fanErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", fanErr.Error())
		}
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		merged := MergeIOCs(contribs)
		if len(merged) == 0 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged[0])
	}
}

// FederatedListIOCObservationsByKV: union observations across CPs.
func FederatedListIOCObservationsByKV(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if kind == "" || value == "" {
			http.Error(w, "missing kind or value", http.StatusBadRequest)
			return
		}
		localCP, err := selfCPSource(store)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		local, err := store.ListIOCObservationsByKV(kind, value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		contribs := []contribObs{{cp: localCP, rows: local}}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/iocs/by-kv/observations?kind=" + url.QueryEscape(kind) + "&value=" + url.QueryEscape(value)
		var mu sync.Mutex
		results, fanErr := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []db.IOCObservation
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			contribs = append(contribs, contribObs{cp: peerCPSource(peer), rows: rows})
			return nil
		})
		if fanErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", fanErr.Error())
		}
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		merged := MergeIOCObservations(contribs)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// FederatedListIOCRelationshipsByKV: union edges across CPs, dedup by tuple.
func FederatedListIOCRelationshipsByKV(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if kind == "" || value == "" {
			http.Error(w, "missing kind or value", http.StatusBadRequest)
			return
		}
		localCP, err := selfCPSource(store)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		local, err := store.ListIOCRelationshipsByKVPaired(kind, value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		contribs := []contribRels{{cp: localCP, rows: local}}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		path := "/api/v1/federation/iocs/by-kv/relationships?kind=" + url.QueryEscape(kind) + "&value=" + url.QueryEscape(value)
		var mu sync.Mutex
		results, fanErr := agg.FanOut(ctx, func(ctx context.Context, peer federation.Peer) error {
			var rows []db.IOCRelationshipPaired
			if err := agg.FetchJSON(ctx, peer, path, &rows); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			contribs = append(contribs, contribRels{cp: peerCPSource(peer), rows: rows})
			return nil
		})
		if fanErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", fanErr.Error())
		}
		if pErr := federation.AnyError(results); pErr != nil {
			w.Header().Set("X-Okesu-Federation-Warning", pErr.Error())
		}
		merged := MergeIOCRelationships(contribs)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(merged)
	}
}

// selfCPSource builds a CPSourceRef for the local CP from store metadata.
func selfCPSource(store *db.Store) (*CPSourceRef, error) {
	meta, err := store.CPMeta()
	if err != nil {
		return nil, err
	}
	return &CPSourceRef{
		InstanceID:  meta.InstanceID,
		DisplayName: meta.DisplayName,
		Region:      meta.Region,
	}, nil
}

// peerCPSource builds a CPSourceRef from a federation peer snapshot.
func peerCPSource(peer federation.Peer) *CPSourceRef {
	return &CPSourceRef{
		InstanceID:  peer.Snapshot.InstanceID,
		DisplayName: peer.Snapshot.DisplayName,
		Region:      peer.Snapshot.Region,
	}
}
