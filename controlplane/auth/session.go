package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

const (
	cookieName       = "okesu_cp_session"
	sessionTTL       = 24 * time.Hour
	sessionKeyMetaKey = "session_hmac_key"
)

// Manager creates, verifies, and revokes user sessions.
type Manager struct {
	store *db.Store
	key   []byte // HMAC key for cookie signing
}

// NewManager builds a Manager. If providedKey is empty, a key is loaded from
// (or generated and persisted into) the meta table.
func NewManager(store *db.Store, providedKey string) (*Manager, error) {
	var key []byte
	if providedKey != "" {
		k, err := base64.StdEncoding.DecodeString(providedKey)
		if err != nil || len(k) < 32 {
			return nil, errors.New("session key must be base64-encoded, >=32 bytes")
		}
		key = k
	} else {
		stored, err := store.MetaGet(sessionKeyMetaKey)
		if err != nil {
			return nil, err
		}
		if stored == "" {
			k := make([]byte, 32)
			if _, err := rand.Read(k); err != nil {
				return nil, err
			}
			if err := store.MetaSet(sessionKeyMetaKey, base64.StdEncoding.EncodeToString(k)); err != nil {
				return nil, err
			}
			key = k
		} else {
			k, err := base64.StdEncoding.DecodeString(stored)
			if err != nil {
				return nil, err
			}
			key = k
		}
	}
	return &Manager{store: store, key: key}, nil
}

// Issue creates a new session for userID and writes a signed cookie to w.
func (m *Manager) Issue(w http.ResponseWriter, r *http.Request, userID int64) error {
	id, err := randomHex(32)
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionTTL)
	if err := m.store.CreateSession(id, userID, expires); err != nil {
		return err
	}
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    m.signCookie(id),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
	return nil
}

// Revoke removes the session referenced by the request cookie.
func (m *Manager) Revoke(w http.ResponseWriter, r *http.Request) {
	if id := m.sessionIDFromRequest(r); id != "" {
		_ = m.store.DeleteSession(id)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// User returns the authenticated user for the request, or nil if unauthenticated.
func (m *Manager) User(r *http.Request) *db.User {
	id := m.sessionIDFromRequest(r)
	if id == "" {
		return nil
	}
	u, err := m.store.SessionUser(id)
	if err != nil {
		return nil
	}
	return u
}

// CurrentSessionID returns the verified session ID from the request cookie,
// or empty string if absent / invalid. Used to mark the current session in
// session-listing endpoints.
func (m *Manager) CurrentSessionID(r *http.Request) string {
	return m.sessionIDFromRequest(r)
}

// Middleware enforces that a request has a valid session. On failure it
// responds with 401 (for /api/* paths) or redirects to /login otherwise.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := m.User(r)
		if u == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), userKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserFromContext returns the authenticated user attached by Middleware.
func UserFromContext(ctx context.Context) *db.User {
	u, _ := ctx.Value(userKey{}).(*db.User)
	return u
}

type userKey struct{}

func (m *Manager) signCookie(sessionID string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(sessionID))
	sig := hex.EncodeToString(mac.Sum(nil))
	return sessionID + "." + sig
}

func (m *Manager) verifyCookie(value string) string {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	id, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(id))
	expect := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expect)) != 1 {
		return ""
	}
	return id
}

func (m *Manager) sessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return m.verifyCookie(c.Value)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
