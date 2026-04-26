package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// userJSON is the wire shape returned by the users API. password_hash is
// never exposed.
type userJSON struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	HasPassword bool  `json:"has_password"`
	CreatedAt  string `json:"created_at"`
}

func toUserJSON(u *db.User) userJSON {
	return userJSON{
		ID:          u.ID,
		Email:       u.Email,
		Role:        u.Role,
		HasPassword: u.PasswordHash.Valid,
		CreatedAt:   u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// validRoles is the set of roles a UI / API caller can assign.
var validRoles = map[string]bool{
	auth.RoleAdmin:    true,
	auth.RoleOperator: true,
	auth.RoleViewer:   true,
}

// ── List + Detail ───────────────────────────────────────────────────────────

func UsersList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		users, err := store.ListUsers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]userJSON, 0, len(users))
		for _, u := range users {
			out = append(out, toUserJSON(u))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func UserDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		u, err := store.UserByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toUserJSON(u))
	}
}

// ── Create ──────────────────────────────────────────────────────────────────

type userCreateReq struct {
	Email    string `json:"email"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

func UserCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req userCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Email == "" {
			http.Error(w, "email required", http.StatusBadRequest)
			return
		}
		if !validRoles[req.Role] {
			http.Error(w, "role must be admin|operator|viewer", http.StatusBadRequest)
			return
		}
		if len(req.Password) < 8 {
			http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "hash: "+err.Error(), http.StatusInternalServerError)
			return
		}
		id, err := store.CreateUser(req.Email, string(hash), req.Role)
		if err != nil {
			http.Error(w, "create: "+err.Error(), http.StatusBadRequest)
			return
		}
		u, err := store.UserByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "user.create",
			Target: fmt.Sprintf("user:%d", id),
			Metadata: map[string]any{"email": req.Email, "role": req.Role},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toUserJSON(u))
	}
}

// ── Patch (role and/or password) ────────────────────────────────────────────

type userPatchReq struct {
	Role     *string `json:"role,omitempty"`
	Password *string `json:"password,omitempty"`
}

func UserPatch(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var req userPatchReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		existing, err := store.UserByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Role change.
		if req.Role != nil {
			if !validRoles[*req.Role] {
				http.Error(w, "role must be admin|operator|viewer", http.StatusBadRequest)
				return
			}
			if existing.Role == auth.RoleAdmin && *req.Role != auth.RoleAdmin {
				if err := guardLastAdmin(store, existing.ID); err != nil {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
			}
			if existing.Role != *req.Role {
				if err := store.UpdateUserRole(id, *req.Role); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				audit.Emit(r, store, db.AuditEntry{
					Action: "user.role_change",
					Target: fmt.Sprintf("user:%d", id),
					Metadata: map[string]any{
						"email": existing.Email,
						"from":  existing.Role,
						"to":    *req.Role,
					},
				})
			}
		}

		// Password change.
		if req.Password != nil {
			if len(*req.Password) < 8 {
				http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
				return
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, "hash: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if err := store.UpdateUserPassword(id, string(hash)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			audit.Emit(r, store, db.AuditEntry{
				Action: "user.password_change",
				Target: fmt.Sprintf("user:%d", id),
				Metadata: map[string]any{"email": existing.Email},
			})
		}

		updated, err := store.UserByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toUserJSON(updated))
	}
}

// ── Delete ──────────────────────────────────────────────────────────────────

func UserDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		me := auth.UserFromContext(r.Context())
		if me != nil && me.ID == id {
			http.Error(w, "cannot delete your own account", http.StatusConflict)
			return
		}
		existing, err := store.UserByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if existing.Role == auth.RoleAdmin {
			if err := guardLastAdmin(store, id); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
		}
		if err := store.DeleteUser(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "user.delete",
			Target: fmt.Sprintf("user:%d", id),
			Metadata: map[string]any{"email": existing.Email, "role": existing.Role},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

func guardLastAdmin(store *db.Store, idBeingChanged int64) error {
	count, err := store.CountUsersByRole(auth.RoleAdmin)
	if err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf("cannot remove the last remaining admin (user %d)", idBeingChanged)
	}
	return nil
}

// ── Self-service: change own password ───────────────────────────────────────

type passwordChangeReq struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

func MyPasswordChange(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := auth.UserFromContext(r.Context())
		if me == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !me.PasswordHash.Valid {
			// SSO-only user — can't set a local password without privilege escalation paths.
			http.Error(w, "this account uses SSO; password change is not supported", http.StatusBadRequest)
			return
		}
		var req passwordChangeReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if len(req.New) < 8 {
			http.Error(w, "new password must be at least 8 characters", http.StatusBadRequest)
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(me.PasswordHash.String), []byte(req.Current)); err != nil {
			http.Error(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "hash: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := store.UpdateUserPassword(me.ID, string(hash)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "auth.password_change",
			Target: fmt.Sprintf("user:%d", me.ID),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Self-service: list / revoke sessions ────────────────────────────────────

type sessionJSON struct {
	ID        string `json:"id"`        // hashed/truncated, never the full id
	IsCurrent bool   `json:"is_current"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

func MySessions(store *db.Store, mgr *auth.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := auth.UserFromContext(r.Context())
		if me == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		list, err := store.ListUserSessions(me.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		current := mgr.CurrentSessionID(r)
		out := make([]sessionJSON, 0, len(list))
		for _, s := range list {
			out = append(out, sessionJSON{
				ID:        truncateID(s.ID),
				IsCurrent: s.ID == current,
				CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
				ExpiresAt: s.ExpiresAt.UTC().Format(time.RFC3339),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func MyRevokeOtherSessions(store *db.Store, mgr *auth.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := auth.UserFromContext(r.Context())
		if me == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		current := mgr.CurrentSessionID(r)
		if err := store.DeleteUserSessionsExcept(me.ID, current); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "session.revoke_all",
			Target: fmt.Sprintf("user:%d", me.ID),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// truncateID returns a short prefix of a session id for display. The full id
// would let any UI viewer impersonate the session — we never expose it.
func truncateID(id string) string {
	if len(id) <= 12 {
		return id + "…"
	}
	return id[:12] + "…"
}
