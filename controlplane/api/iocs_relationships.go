package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListIOCRelationshipsHandler returns every typed edge touching the
// IOC (in either direction). Path: /api/iocs/{id}/relationships.
func ListIOCRelationshipsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad ioc id", http.StatusBadRequest)
			return
		}
		rels, err := store.ListIOCRelationships(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rels)
	}
}
