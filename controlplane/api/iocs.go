package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListIOCs returns IOCs filtered by:
//   - kind         (sha256|ipv4|domain|yara_rule|sigma_rule|...)
//   - source       (catalog|observed|feed:<slug>) — exact match on the source column
//   - q            (LIKE %q% against value, name, or tags; case-insensitive)
//   - finding_id   (joins ioc_observations to filter to a single finding)
//
// With no filter, returns the most recent 100 by last_seen.
func ListIOCs(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := db.IOCListFilter{
			Kind:   q.Get("kind"),
			Source: q.Get("source"),
			Query:  q.Get("q"),
			Limit:  100,
		}
		if v := q.Get("finding_id"); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "bad finding_id", http.StatusBadRequest)
				return
			}
			filter.FindingID = id
		}
		out, err := store.ListIOCs(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
