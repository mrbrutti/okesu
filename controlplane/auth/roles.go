package auth

import (
	"context"
	"net/http"
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
func RequireRole(needed string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !AtLeast(u.Role, needed) {
				http.Error(w, "forbidden: requires "+needed, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MustHaveRole asserts the request's authenticated user holds at least the
// needed role. Returns true if so; otherwise writes 401/403 and returns false.
// Useful inside handlers that conditionally restrict per-action.
func MustHaveRole(ctx context.Context, w http.ResponseWriter, needed string) bool {
	u := UserFromContext(ctx)
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !AtLeast(u.Role, needed) {
		http.Error(w, "forbidden: requires "+needed, http.StatusForbidden)
		return false
	}
	return true
}
