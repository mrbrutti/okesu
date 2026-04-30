package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListIOCObservationsHandler returns the observation history for a
// given IOC: every (finding_id, run_id, host, observed_at) row in
// ioc_observations linked to this IOC. Used by the Catalog detail
// page's Observations tab.
func ListIOCObservationsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad ioc id", http.StatusBadRequest)
			return
		}
		obs, err := store.ListIOCObservations(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(obs)
	}
}
