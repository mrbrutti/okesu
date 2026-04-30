// Operator-facing fleet-env handlers. Mounted in the cookie-auth
// admin route group. Never returns plaintext keys — only last4 +
// set/unset booleans + version + source. The plaintext path lives
// in fleet_env_daemon.go behind mTLS / federation-token auth.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// fleetEnvSummaryJSON is the wire shape of GET /api/fleet-env.
// Never returns plaintext keys.
type fleetEnvSummaryJSON struct {
	AnthropicSet       bool    `json:"anthropic_set"`
	AnthropicLast4     string  `json:"anthropic_last4"`
	OpenAISet          bool    `json:"openai_set"`
	OpenAILast4        string  `json:"openai_last4"`
	Version            int64   `json:"version"`
	Source             string  `json:"source"`
	ParentCPID         *string `json:"parent_cp_id"`
	UpdatedAt          string  `json:"updated_at"`
	UpdatedByUserEmail *string `json:"updated_by_user_email"`
}

// fleetEnvPatchReq is the wire shape of PUT /api/fleet-env. Per-field:
//   - omitted (JSON missing) = leave unchanged
//   - "" = delete (sealed → NULL)
//   - non-empty = encrypt + store
type fleetEnvPatchReq struct {
	AnthropicAPIKey *string `json:"anthropic_api_key,omitempty"`
	OpenAIAPIKey    *string `json:"openai_api_key,omitempty"`
}

// fleetEnvJSON is the helper that converts a *db.FleetEnv → wire
// shape. Used by GET and the success path of PUT.
func fleetEnvJSON(fe *db.FleetEnv) fleetEnvSummaryJSON {
	out := fleetEnvSummaryJSON{
		AnthropicSet:   fe.HasAnthropic,
		AnthropicLast4: fe.AnthropicLast4,
		OpenAISet:      fe.HasOpenAI,
		OpenAILast4:    fe.OpenAILast4,
		Version:        fe.Version,
		Source:         fe.Source,
		UpdatedAt:      fe.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if fe.ParentCPID.Valid {
		s := fe.ParentCPID.String
		out.ParentCPID = &s
	}
	if fe.UpdatedByUserEmail.Valid {
		s := fe.UpdatedByUserEmail.String
		out.UpdatedByUserEmail = &s
	}
	return out
}

// FleetEnvGet returns the masked summary. Never plaintext.
func FleetEnvGet(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fe, err := store.GetFleetEnv()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fleetEnvJSON(fe))
	}
}

// FleetEnvPut applies a partial update. Bumps version, sets source =
// "local". The optional onChange callback is invoked after a successful
// commit so server.go can refresh cpLocalEnv + trigger publish.
func FleetEnvPut(store *db.Store, onChange func(version int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req fleetEnvPatchReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.AnthropicAPIKey == nil && req.OpenAIAPIKey == nil {
			http.Error(w, "no fields to update", http.StatusBadRequest)
			return
		}
		mk, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		actor := operatorEmail(r)
		v, err := store.UpsertFleetEnv(mk, db.FleetEnvUpdate{
			AnthropicAPIKey:    req.AnthropicAPIKey,
			OpenAIAPIKey:       req.OpenAIAPIKey,
			UpdatedByUserEmail: actor,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.InsertAudit(db.AuditEntry{
			ActorEmail: actor,
			Action:     "fleet_env.set",
			Metadata:   map[string]any{"version": v},
		})
		if onChange != nil {
			onChange(v)
		}
		fe, err := store.GetFleetEnv()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fleetEnvJSON(fe))
	}
}

// FleetEnvOverrideLocal switches source to "local" so the federation
// poller stops overwriting the row. Keeps current key values.
func FleetEnvOverrideLocal(store *db.Store, onChange func(version int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := operatorEmail(r)
		if err := store.SetFleetEnvSource("local", actor); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.InsertAudit(db.AuditEntry{
			ActorEmail: actor,
			Action:     "fleet_env.override_local",
		})
		if onChange != nil {
			if fe, err := store.GetFleetEnv(); err == nil {
				onChange(fe.Version)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// FleetEnvRevertToParent switches source back to
// "federated_from_parent". Next federation poll will overwrite with
// parent's current value.
func FleetEnvRevertToParent(store *db.Store, onChange func(version int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := operatorEmail(r)
		if err := store.SetFleetEnvSource("federated_from_parent", actor); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.InsertAudit(db.AuditEntry{
			ActorEmail: actor,
			Action:     "fleet_env.revert_to_parent",
		})
		if onChange != nil {
			if fe, err := store.GetFleetEnv(); err == nil {
				onChange(fe.Version)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// operatorEmail extracts the authenticated user's email from the
// request context. Empty string when unauthenticated (acceptable for
// boot-time calls that bypass cookie auth — tests pass empty too).
func operatorEmail(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.Email
	}
	return ""
}
