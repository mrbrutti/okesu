package auth

import (
	"context"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// Role constants. Higher values dominate.
const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// roleRank maps a role to its privilege level. Unknown roles are 0.
func roleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether userRole grants the privileges of needed.
//
//	AtLeast("admin", "operator")   → true
//	AtLeast("operator", "operator") → true
//	AtLeast("viewer", "operator")  → false
func AtLeast(userRole, needed string) bool {
	return roleRank(userRole) >= roleRank(needed)
}

// RequireRole returns middleware that gates access to handlers requiring
// the given minimum role. Must be used AFTER Manager.Middleware so the
// authenticated user is in the request context.
//
// Phase 22.8 (PR α) — gating now consults the user's effective roles
// (union across group memberships) instead of just `users.role`. The
// migration backfilled every existing user into a default-<role>
// group, so behaviour for vanilla CPs is unchanged. Group-managed
// admins / operators added via the Groups page get effective access
// without their `users.role` column changing.
func RequireRole(store *db.Store, needed string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if userHasRole(store, u, needed) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "forbidden: requires "+needed, http.StatusForbidden)
		})
	}
}

// MustHaveRole asserts the request's authenticated user holds at least the
// needed role. Returns true if so; otherwise writes 401/403 and returns false.
// Useful inside handlers that conditionally restrict per-action.
func MustHaveRole(ctx context.Context, w http.ResponseWriter, store *db.Store, needed string) bool {
	u := UserFromContext(ctx)
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if userHasRole(store, u, needed) {
		return true
	}
	http.Error(w, "forbidden: requires "+needed, http.StatusForbidden)
	return false
}

// userHasRole resolves the gate decision. Tries effective roles via
// the store first (group-managed access); falls back to the user's
// own `users.role` column for back-compat — defensive in case the
// store call errors on a flaky read, the legacy column still gates
// the request like the pre-PR-α code did.
//
// Synthetic users (auth.WithUser injection paths — federation token,
// API token without a user backing) skip the store lookup and use
// their `Role` field directly. Their `ID` is 0 so a store query
// would return nothing useful anyway.
func userHasRole(store *db.Store, u *db.User, needed string) bool {
	if AtLeast(u.Role, needed) {
		return true
	}
	if store == nil || u.ID == 0 {
		return false
	}
	ok, err := store.HasEffectiveRole(u.ID, needed)
	return err == nil && ok
}
