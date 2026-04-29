// Cloud credentials API surface.
//
// CRUD over the cloud_credentials table plus a /test endpoint that
// validates the payload's shape (Phase 21.2). Real cloud API calls
// land in Phase 21.3 when the per-cloud provisioner ships — this
// layer's responsibility today is encrypted storage + structural
// validation so an operator gets immediate feedback that they
// haven't pasted the wrong field into the wrong slot.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// cloudCredentialJSON is the metadata-only wire shape — payload is
// always omitted. Operators that need the plaintext are typically
// editing the credential, in which case they re-enter the secrets
// from scratch (we never roundtrip plaintext through the browser).
type cloudCredentialJSON struct {
	ID             int64  `json:"id"`
	Cloud          string `json:"cloud"`
	Name           string `json:"name"`
	Region         string `json:"region,omitempty"`
	CreatedAt      string `json:"created_at"`
	CreatedByEmail string `json:"created_by_email,omitempty"`
	LastUsedAt     string `json:"last_used_at,omitempty"`
	LastTestAt     string `json:"last_test_at,omitempty"`
	LastTestOK     *bool  `json:"last_test_ok,omitempty"`
	LastTestError  string `json:"last_test_error,omitempty"`
}

func toCloudCredentialJSON(c *db.CloudCredential) cloudCredentialJSON {
	out := cloudCredentialJSON{
		ID:        c.ID,
		Cloud:     c.Cloud,
		Name:      c.Name,
		CreatedAt: c.CreatedAt.UTC().Format(rfc3339),
	}
	if c.Region.Valid {
		out.Region = c.Region.String
	}
	if c.CreatedByEmail.Valid {
		out.CreatedByEmail = c.CreatedByEmail.String
	}
	if c.LastUsedAt.Valid {
		out.LastUsedAt = c.LastUsedAt.Time.UTC().Format(rfc3339)
	}
	if c.LastTestAt.Valid {
		out.LastTestAt = c.LastTestAt.Time.UTC().Format(rfc3339)
	}
	if c.LastTestOK.Valid {
		v := c.LastTestOK.Bool
		out.LastTestOK = &v
	}
	if c.LastTestError.Valid {
		out.LastTestError = c.LastTestError.String
	}
	return out
}

// CloudCredentialsList — GET /api/cloud-credentials[?cloud=...]
// Admin-only. Returns metadata only — payload never crosses the wire.
func CloudCredentialsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cloud := strings.TrimSpace(r.URL.Query().Get("cloud"))
		rows, err := store.ListCloudCredentials(cloud)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]cloudCredentialJSON, len(rows))
		for i := range rows {
			out[i] = toCloudCredentialJSON(&rows[i])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// cloudCredentialCreateReq is the JSON body shape for POST.
// `payload` carries the cloud-specific fields (e.g. for OCI:
// {tenancy_ocid, user_ocid, fingerprint, private_key, region}).
// The store validates per-cloud shape via a registered validator.
type cloudCredentialCreateReq struct {
	Cloud   string         `json:"cloud"`
	Name    string         `json:"name"`
	Region  string         `json:"region,omitempty"`
	Payload map[string]any `json:"payload"`
}

// CloudCredentialCreate — POST /api/cloud-credentials
func CloudCredentialCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cloudCredentialCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := validateCloudCredentialPayload(req.Cloud, req.Payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, err := json.Marshal(req.Payload)
		if err != nil {
			http.Error(w, "marshal payload: "+err.Error(), http.StatusBadRequest)
			return
		}

		masterKey, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var userID int64
		var userEmail string
		if u := auth.UserFromContext(r.Context()); u != nil {
			userID = u.ID
			userEmail = u.Email
		}
		c, err := store.InsertCloudCredential(db.CloudCredentialInsert{
			Cloud:          req.Cloud,
			Name:           req.Name,
			Region:         req.Region,
			Payload:        raw,
			CreatedByUser:  userID,
			CreatedByEmail: userEmail,
		}, masterKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "cloud_credential.create",
			Target: fmt.Sprintf("cloud_credential:%d", c.ID),
			Metadata: map[string]any{
				"cloud":  c.Cloud,
				"name":   c.Name,
				"region": req.Region,
			},
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toCloudCredentialJSON(c))
	}
}

// CloudCredentialDelete — DELETE /api/cloud-credentials/{id}
func CloudCredentialDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteCloudCredential(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "cloud_credential.delete",
			Target: fmt.Sprintf("cloud_credential:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// CloudCredentialTest — POST /api/cloud-credentials/{id}/test
//
// Phase 21.2: structural validation only — decrypt the payload,
// re-validate per-cloud field shape, write the result to last_test_*
// columns. Phase 21.3 will replace the body of validateLive* with
// real SDK calls (list-instances etc.) so an operator finds out at
// save time whether the credential actually works against the API.
func CloudCredentialTest(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		c, err := store.GetCloudCredential(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		masterKey, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := store.DecryptCloudCredential(id, masterKey)
		if err != nil {
			_ = store.RecordCloudCredentialTest(id, false, "decrypt: "+err.Error())
			http.Error(w, "decrypt: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			_ = store.RecordCloudCredentialTest(id, false, "payload not json")
			http.Error(w, "payload not json", http.StatusInternalServerError)
			return
		}
		if err := validateCloudCredentialPayload(c.Cloud, payload); err != nil {
			_ = store.RecordCloudCredentialTest(id, false, err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// All good — for now. Phase 21.3 will replace this block
		// with a per-cloud "real call" probe.
		_ = store.RecordCloudCredentialTest(id, true, "")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"message": "structural validation passed (live API probe lands in Phase 21.3)",
		})
	}
}

// validateCloudCredentialPayload checks per-cloud required fields.
// Adding a new cloud means: append its kind to db.AllowedCloudKinds
// and a case here that lists the required keys.
func validateCloudCredentialPayload(cloud string, payload map[string]any) error {
	switch cloud {
	case "oci":
		return requireKeys(payload, "tenancy_ocid", "user_ocid", "fingerprint", "private_key", "region")
	case "aws":
		// access_key + secret OR role_arn for assume-role flows. We
		// treat region as required since every AWS API call needs it.
		if _, hasKey := payload["access_key_id"]; hasKey {
			return requireKeys(payload, "access_key_id", "secret_access_key", "region")
		}
		return requireKeys(payload, "role_arn", "region")
	case "gcp":
		return requireKeys(payload, "service_account_json")
	case "azure":
		return requireKeys(payload, "tenant_id", "client_id", "client_secret", "subscription_id")
	case "digitalocean":
		return requireKeys(payload, "api_token", "region")
	default:
		return fmt.Errorf("unknown cloud %q", cloud)
	}
}

func requireKeys(payload map[string]any, keys ...string) error {
	var missing []string
	for _, k := range keys {
		v, ok := payload[k]
		if !ok {
			missing = append(missing, k)
			continue
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
			missing = append(missing, k+" (empty)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

const rfc3339 = "2006-01-02T15:04:05Z"
