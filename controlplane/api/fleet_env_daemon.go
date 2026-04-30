package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// fleetEnvWithKeysJSON is the wire shape returned by the
// daemon-mTLS-authed and federation-token-authed endpoints.
// Plaintext keys included.
type fleetEnvWithKeysJSON struct {
	AnthropicAPIKey string `json:"anthropic_api_key"`
	OpenAIAPIKey    string `json:"openai_api_key"`
	Version         int64  `json:"version"`
}

// FleetEnvDaemon returns the plaintext keys for daemons.
// MUST be mounted in the mTLS-authed daemon-management group.
func FleetEnvDaemon(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mk, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		fe, err := store.GetFleetEnvWithKeys(mk)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := fleetEnvWithKeysJSON{
			AnthropicAPIKey: fe.AnthropicAPIKey,
			OpenAIAPIKey:    fe.OpenAIAPIKey,
			Version:         fe.Version,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// FleetEnvFederation returns the plaintext keys for a federated
// child CP. MUST be mounted behind requireFederationToken.
// Identical wire shape to FleetEnvDaemon — auth differs at mount.
func FleetEnvFederation(store *db.Store) http.HandlerFunc {
	return FleetEnvDaemon(store)
}
