package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// LoginRequest is the JSON body of POST /api/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginHandler issues a session cookie on valid credentials.
func LoginHandler(store *db.Store, mgr *auth.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		u, err := auth.VerifyPassword(store, req.Email, req.Password)
		if err != nil {
			audit.EmitWithUser(r, store, nil, db.AuditEntry{
				Action:   "auth.login",
				Target:   fmt.Sprintf("email:%s", req.Email),
				Result:   "denied",
				Metadata: map[string]any{"method": "password", "reason": "invalid credentials"},
			})
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		if err := mgr.Issue(w, r, u.ID); err != nil {
			http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
			return
		}
		audit.EmitWithUser(r, store, u, db.AuditEntry{
			Action:   "auth.login",
			Target:   fmt.Sprintf("user:%d", u.ID),
			Metadata: map[string]any{"method": "password"},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    u.ID,
			"email": u.Email,
			"role":  u.Role,
		})
	}
}

// LogoutHandler revokes the session cookie.
func LogoutHandler(store *db.Store, mgr *auth.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u := auth.UserFromContext(r.Context()); u != nil {
			audit.Emit(r, store, db.AuditEntry{
				Action: "auth.logout",
				Target: fmt.Sprintf("user:%d", u.ID),
			})
		}
		mgr.Revoke(w, r)
		w.WriteHeader(http.StatusNoContent)
	}
}

// MeHandler returns the currently authenticated user.
func MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    u.ID,
			"email": u.Email,
			"role":  u.Role,
		})
	}
}

// AuthConfig describes the auth methods this CP exposes to the login UI.
type AuthConfig struct {
	OIDCEnabled bool   `json:"oidc_enabled"`
	OIDCLabel   string `json:"oidc_label,omitempty"`
}

// AuthConfigHandler returns auth options so the login page knows whether to
// render the SSO button. Public — no auth required.
func AuthConfigHandler(oidcEnabled bool, oidcLabel string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AuthConfig{
			OIDCEnabled: oidcEnabled,
			OIDCLabel:   oidcLabel,
		})
	}
}
