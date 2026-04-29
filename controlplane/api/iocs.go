package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListIOCs returns IOCs filtered by finding_id (optional) or kind
// (optional). With no filter, returns the most recent 100 by last_seen.
//
// Used by the UI's IOC drawer (drilling into a finding shows the
// indicators it referenced) and by tests / smoke checks that want to
// verify finding-ingest extraction landed.
func ListIOCs(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := db.IOCListFilter{Kind: q.Get("kind"), Limit: 100}
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
