// Package audit is a small helper that captures admin-action audit entries
// from inside HTTP handlers. The actor identity is read from the request
// context (populated by the auth middleware); the client IP from the request.
package audit

import (
	"net"
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// Emit records an audit event. Best-effort: errors are swallowed so a flaky
// audit table never blocks a real handler from succeeding. Callers should
// still pass through accurate Action/Target so the trail is useful.
func Emit(r *http.Request, store *db.Store, entry db.AuditEntry) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		entry.ActorID = u.ID
		entry.ActorEmail = u.Email
		entry.ActorRole = u.Role
	}
	entry.ActorIP = clientIP(r)
	_ = store.InsertAudit(entry)
}

// EmitWithUser is for handlers that don't have the user in context yet
// (e.g. the login endpoint, where the user is established mid-handler).
func EmitWithUser(r *http.Request, store *db.Store, u *db.User, entry db.AuditEntry) {
	if u != nil {
		entry.ActorID = u.ID
		entry.ActorEmail = u.Email
		entry.ActorRole = u.Role
	}
	entry.ActorIP = clientIP(r)
	_ = store.InsertAudit(entry)
}

// clientIP returns the best-guess remote IP for the request. Honors
// X-Forwarded-For (first hop) when a load balancer is in front of the CP.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if r.RemoteAddr != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			return host
		}
		return r.RemoteAddr
	}
	return ""
}
