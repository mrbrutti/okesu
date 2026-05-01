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

// FleetEnvFederationPushReq is the body shape for parent-push.
// parent_cp_id ties the row to the parent so a future GET from the
// child's poller is idempotent (same SetFleetEnvFromFederation
// happy-path the pull mirror uses).
type FleetEnvFederationPushReq struct {
	AnthropicAPIKey string `json:"anthropic_api_key"`
	OpenAIAPIKey    string `json:"openai_api_key"`
	Version         int64  `json:"version"`
	ParentCPID      string `json:"parent_cp_id"`
}

// FleetEnvFederationPush accepts a fleet-env push from a parent CP
// and stores it via SetFleetEnvFromFederation. MUST be mounted
// behind requireFederationToken — the auth proves the caller is a
// trusted parent on the same federation.
//
// Operator intuition: "I changed the LLM key on the global CP, the
// children should pick it up." The pull-side poller already does
// this when child→parent is configured, but in lab/star topologies
// where only the parent has peers registered, the inverse direction
// has no transport. This endpoint closes that gap.
//
// Same SetFleetEnvFromFederation guard the pull path uses — a child
// row sourced 'local' is left alone (operator-pinned override).
func FleetEnvFederationPush(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req FleetEnvFederationPushReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.ParentCPID == "" {
			http.Error(w, "parent_cp_id required", http.StatusBadRequest)
			return
		}
		mk, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_, applied, err := store.SetFleetEnvFromFederation(
			mk, req.ParentCPID, req.AnthropicAPIKey, req.OpenAIAPIKey, req.Version,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"applied":     applied,
			"version":     req.Version,
			"parent_cp_id": req.ParentCPID,
		})
	}
}
