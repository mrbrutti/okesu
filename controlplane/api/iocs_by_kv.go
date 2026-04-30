package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// GetIOCByKVHandler returns a single IOC identified by (kind, value)
// query params. Used as the local-side query for the federated by-kv
// endpoint: row ids are per-CP-meaningless, so cross-CP detail lookups
// must use the (kind, value) shape.
func GetIOCByKVHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if kind == "" || value == "" {
			http.Error(w, "missing kind or value", http.StatusBadRequest)
			return
		}
		ioc, err := store.GetIOCByKV(kind, value)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ioc)
	}
}

// ListIOCObservationsByKVHandler is the (kind, value) twin of
// ListIOCObservationsHandler.
func ListIOCObservationsByKVHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if kind == "" || value == "" {
			http.Error(w, "missing kind or value", http.StatusBadRequest)
			return
		}
		obs, err := store.ListIOCObservationsByKV(kind, value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(obs)
	}
}

// ListIOCRelationshipsByKVHandler returns paired (kind, value) edges
// touching the given IOC. The wire shape uses kv pairs instead of int
// ids so federation can dedup by tuple across CPs.
func ListIOCRelationshipsByKVHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if kind == "" || value == "" {
			http.Error(w, "missing kind or value", http.StatusBadRequest)
			return
		}
		rels, err := store.ListIOCRelationshipsByKVPaired(kind, value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rels)
	}
}
