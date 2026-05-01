package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// federationFeedConfigJSON is the wire shape returned to a federated
// child CP. Mirrors db.FederationFeedConfig (no auth_credential_id).
type federationFeedConfigJSON struct {
	Slug                   string `json:"slug"`
	Name                   string `json:"name"`
	Kind                   string `json:"kind"`
	URL                    string `json:"url"`
	Subpath                string `json:"subpath,omitempty"`
	Parser                 string `json:"parser"`
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	Enabled                bool   `json:"enabled"`
}

// FeedsFederation returns the parent CP's installed feed configs in a
// shape suitable for a child CP to mirror. Federation reads only —
// child CPs never see auth_credential_id (each CP manages its own
// credentials). MUST be mounted behind requireFederationToken so only
// authenticated children can read.
//
// Only feeds with Source = "local" on the parent are returned. A
// child's federated mirror of its OWN parent isn't surfaced two hops
// down — federation is one level deep in v1.
func FeedsFederation(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		all, err := store.ListFeedConfigs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]federationFeedConfigJSON, 0, len(all))
		for _, fc := range all {
			if fc.Source != "local" {
				continue
			}
			out = append(out, federationFeedConfigJSON{
				Slug:                   fc.Slug,
				Name:                   fc.Name,
				Kind:                   fc.Kind,
				URL:                    fc.URL,
				Subpath:                fc.Subpath,
				Parser:                 fc.Parser,
				RefreshIntervalSeconds: fc.RefreshIntervalSeconds,
				Enabled:                fc.Enabled,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
