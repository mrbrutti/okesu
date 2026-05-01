// Secrets + selector-driven bindings API. Phase 22.8 PR γ.
//
// Read endpoints (list metadata, view bindings) are admin-only —
// the value itself is never returned (only the name + kind), but
// the metadata hints "this credential exists" and we don't want
// non-admins enumerating the keyring.
//
// The plaintext is never JSON-serialised: there's no GET /value
// endpoint. Plaintext is only consumable from inside the CP
// process, via store.GetSecretValue called by deploy / agent /
// daimon paths.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// secretJSON shapes the wire response. Notably absent: the value.
type secretJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Description    string `json:"description"`
	OwnerGroupID   int64  `json:"owner_group_id,omitempty"`
	CreatedAt      string `json:"created_at"`
	CreatedByEmail string `json:"created_by_email,omitempty"`
}

func toSecretJSON(s db.Secret) secretJSON {
	out := secretJSON{
		ID:          s.ID,
		Name:        s.Name,
		Kind:        s.Kind,
		Description: s.Description,
		CreatedAt:   s.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if s.OwnerGroupID.Valid {
		out.OwnerGroupID = s.OwnerGroupID.Int64
	}
	if s.CreatedByEmail.Valid {
		out.CreatedByEmail = s.CreatedByEmail.String
	}
	return out
}

// ListSecretsHandler — admin only.
func ListSecretsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		all, err := store.ListSecrets()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]secretJSON, 0, len(all))
		for _, s := range all {
			out = append(out, toSecretJSON(s))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// GetSecretHandler returns metadata + bindings. Admin only.
func GetSecretHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := secretIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sec, err := store.GetSecret(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		bindings, _ := store.ListSecretBindings(id)
		writeJSON(w, http.StatusOK, map[string]any{
			"secret":   toSecretJSON(*sec),
			"bindings": bindings,
		})
	}
}

// CreateSecretHandler — admin only. Body includes the plaintext
// value, which gets sealed and never returned.
func CreateSecretHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Name         string `json:"name"`
		Kind         string `json:"kind"`
		Description  string `json:"description"`
		Value        string `json:"value"`
		OwnerGroupID int64  `json:"owner_group_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		mk, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key unavailable: "+err.Error(), http.StatusInternalServerError)
			return
		}
		actor := ""
		if u := auth.UserFromContext(r.Context()); u != nil {
			actor = u.Email
		}
		sec, err := store.CreateSecret(db.SecretInsert{
			Name:           body.Name,
			Kind:           body.Kind,
			Description:    body.Description,
			Value:          body.Value,
			OwnerGroupID:   body.OwnerGroupID,
			CreatedByEmail: actor,
		}, mk)
		if err != nil {
			if errors.Is(err, db.ErrSecretNameTaken) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, toSecretJSON(*sec))
	}
}

// UpdateSecretHandler — admin only. Patches metadata or rotates
// the value; both go through the same endpoint with optional
// fields.
func UpdateSecretHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Description  *string `json:"description,omitempty"`
		OwnerGroupID *int64  `json:"owner_group_id,omitempty"`
		// Value is rotation. When non-nil + non-empty, replaces
		// the encrypted payload under a new nonce. Logged at the
		// request level (see audit_log) but never echoed.
		Value *string `json:"value,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := secretIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Metadata patch.
		if body.Description != nil || body.OwnerGroupID != nil {
			cur, err := store.GetSecret(id)
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			desc := cur.Description
			if body.Description != nil {
				desc = *body.Description
			}
			owner := int64(0)
			if cur.OwnerGroupID.Valid {
				owner = cur.OwnerGroupID.Int64
			}
			if body.OwnerGroupID != nil {
				owner = *body.OwnerGroupID
			}
			if err := store.UpdateSecretMeta(id, desc, owner); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		// Value rotation.
		if body.Value != nil && *body.Value != "" {
			mk, err := store.MasterKeyFromMeta()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := store.UpdateSecretValue(id, *body.Value, mk); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteSecretHandler — admin only. Cascades bindings.
func DeleteSecretHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := secretIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.DeleteSecret(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// AddSecretBindingHandler — admin only. Body: {selector, scope}.
func AddSecretBindingHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Selector string `json:"selector"`
		Scope    string `json:"scope"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := secretIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.AddSecretBinding(id, body.Selector, body.Scope); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RemoveSecretBindingHandler — admin only. Path: .../bindings/{bid}.
func RemoveSecretBindingHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bid, err := strconv.ParseInt(chi.URLParam(r, "bindingID"), 10, 64)
		if err != nil {
			http.Error(w, "bad binding id", http.StatusBadRequest)
			return
		}
		if err := store.RemoveSecretBinding(bid); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// secretIDFromChi reads the {id} URL param.
func secretIDFromChi(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return 0, errors.New("secret id required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("bad secret id")
	}
	return id, nil
}
