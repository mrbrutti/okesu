package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListCrossCPPatternsHandler returns IOCs hitting at least N
// observations within a window. Drives the cross-CP supervisor
// daimon's tick loop.
func ListCrossCPPatternsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		minObs := 2
		if v := r.URL.Query().Get("min_observations"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				minObs = n
			}
		}
		windowH := 1
		if v := r.URL.Query().Get("window_hours"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				windowH = n
			}
		}
		cutoff := time.Now().Add(-time.Duration(windowH) * time.Hour)
		patterns, err := store.ListCrossCPIOCPatterns(minObs, cutoff)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if patterns == nil {
			patterns = []db.IOCPattern{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(patterns)
	}
}
