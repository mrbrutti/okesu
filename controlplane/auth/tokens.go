package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/section9labs/okesu/controlplane/db"
)

// Token scopes recognized by the CP. New scopes get added as new endpoints
// adopt token authentication.
const (
	ScopeFindingsWrite = "findings:write"
)

// TokenPrefix is the literal prefix every API token starts with — easy to
// grep for in logs / config files / leaked secrets.
const TokenPrefix = "okesu_"

// PrefixIndexLength is how many chars of the random tail are stored in
// plaintext for fast row lookup. The remainder gets bcrypt-hashed.
const PrefixIndexLength = 16

// IssueAPIToken generates a new token, persists it, and returns the
// plaintext value to display to the operator. The plaintext is NEVER
// retrievable from the DB after this call.
func IssueAPIToken(store *db.Store, name, scopes string,
	createdBy *db.User, expiresInDays int,
) (plaintext string, id int64, err error) {
	// 32 random bytes → 64 hex chars.
	tail := make([]byte, 32)
	if _, err := rand.Read(tail); err != nil {
		return "", 0, err
	}
	tailHex := hex.EncodeToString(tail)
	plaintext = TokenPrefix + tailHex
	prefix := tailHex[:PrefixIndexLength]

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", 0, err
	}

	var (
		userID    int64
		userEmail string
	)
	if createdBy != nil {
		userID = createdBy.ID
		userEmail = createdBy.Email
	}

	expiresAt := nullTimeIn(expiresInDays)
	id, err = store.CreateAPIToken(name, prefix, string(hash), scopes, userID, userEmail, expiresAt)
	if err != nil {
		return "", 0, err
	}
	return plaintext, id, nil
}

// VerifyAPIToken looks up and validates a token, returning its DB row on
// success. Errors are deliberately non-specific to avoid leaking whether
// a particular prefix exists.
func VerifyAPIToken(store *db.Store, presented string) (*db.APIToken, error) {
	if !strings.HasPrefix(presented, TokenPrefix) {
		return nil, errors.New("invalid token")
	}
	tail := presented[len(TokenPrefix):]
	if len(tail) < PrefixIndexLength {
		return nil, errors.New("invalid token")
	}
	prefix := tail[:PrefixIndexLength]

	row, err := store.APITokenByPrefix(prefix)
	if err != nil {
		// Run a fake bcrypt anyway to keep response timing roughly constant.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$abcdefghijklmnopqrstuv"), []byte(presented))
		return nil, errors.New("invalid token")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.Hash), []byte(presented)); err != nil {
		return nil, errors.New("invalid token")
	}
	_ = store.MarkAPITokenUsed(row.ID)
	return row, nil
}

// HasScope reports whether the token's scopes string contains scope.
func HasScope(t *db.APIToken, scope string) bool {
	if t == nil {
		return false
	}
	for _, s := range strings.Split(t.Scopes, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}

// ── HTTP middleware ────────────────────────────────────────────────────────

type tokenContextKey struct{}

// TokenFromContext returns the verified API token attached by RequireToken,
// or nil if the request was not token-authenticated.
func TokenFromContext(ctx context.Context) *db.APIToken {
	t, _ := ctx.Value(tokenContextKey{}).(*db.APIToken)
	return t
}

// RequireToken returns middleware that validates an Authorization: Bearer
// header and rejects requests lacking the required scope. On success the
// token is attached to the request context.
func RequireToken(store *db.Store, requiredScope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHdr := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(authHdr, prefix) {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			t, err := VerifyAPIToken(store, strings.TrimSpace(authHdr[len(prefix):]))
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			if requiredScope != "" && !HasScope(t, requiredScope) {
				http.Error(w, fmt.Sprintf("token missing scope %q", requiredScope), http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), tokenContextKey{}, t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// nullTimeIn returns a sql.NullTime expires_at value 'days' from now, or
// {Valid:false} when days <= 0 (no expiry).
func nullTimeIn(days int) sql.NullTime {
	if days <= 0 {
		return sql.NullTime{}
	}
	return sql.NullTime{Valid: true, Time: time.Now().Add(time.Duration(days) * 24 * time.Hour)}
}
