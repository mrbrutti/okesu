package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// tokenJSON is the wire shape returned by the API. The full token is only
// returned on creation, never afterwards.
type tokenJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Prefix         string `json:"prefix"`             // shown as "okesu_<prefix>…"
	Scopes         []string `json:"scopes"`
	CreatedAt      string `json:"created_at"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	LastUsedAt     string `json:"last_used_at,omitempty"`
	RevokedAt      string `json:"revoked_at,omitempty"`
	CreatedByEmail string `json:"created_by_email,omitempty"`
}

func toTokenJSON(t *db.APIToken) tokenJSON {
	tj := tokenJSON{
		ID:             t.ID,
		Name:           t.Name,
		Prefix:         auth.TokenPrefix + t.Prefix,
		Scopes:         splitScopes(t.Scopes),
		CreatedAt:      t.CreatedAt.UTC().Format(time.RFC3339),
		CreatedByEmail: t.CreatedByEmail.String,
	}
	if t.ExpiresAt.Valid {
		tj.ExpiresAt = t.ExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	if t.LastUsedAt.Valid {
		tj.LastUsedAt = t.LastUsedAt.Time.UTC().Format(time.RFC3339)
	}
	if t.RevokedAt.Valid {
		tj.RevokedAt = t.RevokedAt.Time.UTC().Format(time.RFC3339)
	}
	return tj
}

func splitScopes(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TokensList returns every token (including revoked / expired, marked as such).
func TokensList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := store.ListAPITokens()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]tokenJSON, 0, len(list))
		for _, t := range list {
			out = append(out, toTokenJSON(t))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// TokenCreate issues a fresh token. The plaintext value is returned ONCE
// in the response — never again.
type tokenCreateReq struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
}

type tokenCreateResp struct {
	tokenJSON
	Token string `json:"token"` // plaintext, shown once
}

func TokenCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req tokenCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		// Whitelist scopes the CP recognizes.
		for _, s := range req.Scopes {
			switch s {
			case auth.ScopeFindingsWrite:
			default:
				http.Error(w, "unknown scope: "+s, http.StatusBadRequest)
				return
			}
		}
		me := auth.UserFromContext(r.Context())
		plaintext, id, err := auth.IssueAPIToken(store,
			req.Name, strings.Join(req.Scopes, ","),
			me, req.ExpiresInDays)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		row, err := store.APITokenByPrefix(plaintext[len(auth.TokenPrefix) : len(auth.TokenPrefix)+auth.PrefixIndexLength])
		if err != nil {
			http.Error(w, "lookup: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "token.create",
			Target: "token:" + strconv.FormatInt(id, 10),
			Metadata: map[string]any{"name": req.Name, "scopes": req.Scopes, "expires_in_days": req.ExpiresInDays},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(tokenCreateResp{
			tokenJSON: toTokenJSON(row),
			Token:     plaintext,
		})
	}
}

// TokenRevoke marks a token revoked. Cannot be undone.
func TokenRevoke(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.RevokeAPIToken(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "token.revoke",
			Target: "token:" + strconv.FormatInt(id, 10),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
