package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/section9labs/okesu/controlplane/db"
)

// OIDCProvider wraps the discovery and OAuth2 setup needed to run the
// auth-code flow with PKCE against an OIDC IDP (e.g. Oracle Identity Domains).
type OIDCProvider struct {
	store        *db.Store
	mgr          *Manager
	verifier     *oidc.IDTokenVerifier
	oauth        *oauth2.Config
	groupsClaim  string
	roleMap      map[string]string // group → role
	stateKey     []byte            // HMAC key for signing state cookies
}

// OIDCConfig is the subset of controlplane.Config needed for OIDC setup.
// Defined locally to avoid a circular import.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	GroupsClaim  string
	RoleMap      string // "group:role,group:role"
}

// NewOIDC constructs an OIDCProvider. Discovery is performed against the
// issuer URL; failures here are returned to the caller (CP can choose to
// continue without OIDC).
func NewOIDC(ctx context.Context, store *db.Store, mgr *Manager, cfg OIDCConfig) (*OIDCProvider, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc not configured")
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	oauth := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile", "groups"},
	}

	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	stateKey := make([]byte, 32)
	if _, err := rand.Read(stateKey); err != nil {
		return nil, fmt.Errorf("state key: %w", err)
	}

	return &OIDCProvider{
		store:       store,
		mgr:         mgr,
		verifier:    verifier,
		oauth:       oauth,
		groupsClaim: groupsClaim,
		roleMap:     parseRoleMap(cfg.RoleMap),
		stateKey:    stateKey,
	}, nil
}

// LoginHandler starts the OIDC auth-code flow.
// GET /auth/oidc/login → 302 to the IDP's authorize endpoint.
func (p *OIDCProvider) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state, err := randomURLString(32)
	if err != nil {
		http.Error(w, "state: "+err.Error(), http.StatusInternalServerError)
		return
	}
	verifier, err := randomURLString(48)
	if err != nil {
		http.Error(w, "verifier: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Sign the state+verifier into a single short-lived cookie.
	cookieValue := p.signState(state + ":" + verifier)
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    cookieValue,
		Path:     "/",
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	challenge := pkceChallenge(verifier)
	authURL := p.oauth.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler completes the auth-code flow.
// GET /auth/oidc/callback?code=...&state=...
func (p *OIDCProvider) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	if errStr := r.URL.Query().Get("error"); errStr != "" {
		desc := r.URL.Query().Get("error_description")
		http.Error(w, "OIDC error: "+errStr+" "+desc, http.StatusBadRequest)
		return
	}

	stateParam := r.URL.Query().Get("state")
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	stateAndVerifier, ok := p.verifyState(cookie.Value)
	if !ok {
		http.Error(w, "invalid state cookie", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(stateAndVerifier, ":", 2)
	if len(parts) != 2 || parts[0] != stateParam {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	codeVerifier := parts[1]

	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	token, err := p.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		http.Error(w, "token exchange: "+err.Error(), http.StatusBadGateway)
		return
	}

	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		http.Error(w, "no id_token in response", http.StatusBadGateway)
		return
	}
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		http.Error(w, "id token verify: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Extract claims.
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "claims: "+err.Error(), http.StatusBadGateway)
		return
	}
	email, _ := claims["email"].(string)
	if email == "" {
		// Some IDPs put it under "preferred_username" or "sub" — fall back.
		if v, ok := claims["preferred_username"].(string); ok && v != "" {
			email = v
		} else if v, ok := claims["sub"].(string); ok && v != "" {
			email = v
		}
	}
	if email == "" {
		http.Error(w, "no email/sub claim in id_token", http.StatusBadGateway)
		return
	}

	groups := extractStrings(claims[p.groupsClaim])
	role := p.roleForGroups(groups)

	user, err := p.store.UpsertSSOUser(email, role)
	if err != nil {
		http.Error(w, "provision user: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := p.mgr.Issue(w, r, user.ID); err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Best-effort audit emit. Done inline rather than via the audit package
	// to avoid an import cycle (audit depends on auth for UserFromContext).
	_ = p.store.InsertAudit(db.AuditEntry{
		ActorID:    user.ID,
		ActorEmail: user.Email,
		ActorRole:  user.Role,
		ActorIP:    clientIPFromRequest(r),
		Action:     "auth.login",
		Target:     fmt.Sprintf("user:%d", user.ID),
		Metadata:   map[string]any{"method": "oidc", "groups": groups},
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// clientIPFromRequest is duplicated here from controlplane/audit to avoid
// importing the audit package (which imports auth — would create a cycle).
func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// roleForGroups picks the highest-ranked role for any of the user's groups.
// Falls back to viewer if no group matches the role map.
func (p *OIDCProvider) roleForGroups(groups []string) string {
	best := RoleViewer
	for _, g := range groups {
		if r, ok := p.roleMap[g]; ok && roleRank(r) > roleRank(best) {
			best = r
		}
	}
	return best
}

// parseRoleMap parses "group:role,group:role" into a map.
func parseRoleMap(raw string) map[string]string {
	m := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		i := strings.Index(pair, ":")
		if i < 1 || i == len(pair)-1 {
			continue
		}
		group := strings.TrimSpace(pair[:i])
		role := strings.TrimSpace(pair[i+1:])
		if roleRank(role) > 0 {
			m[group] = role
		}
	}
	return m
}

// extractStrings normalizes a claim that may be []any, []string, or a single string.
func extractStrings(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

// signState HMAC-signs the value using the provider's state key.
func (p *OIDCProvider) signState(value string) string {
	mac := hmac.New(sha256.New, p.stateKey)
	mac.Write([]byte(value))
	sig := hex.EncodeToString(mac.Sum(nil))
	return value + "." + sig
}

// verifyState validates a signed cookie value and returns the original payload.
func (p *OIDCProvider) verifyState(cookieValue string) (string, bool) {
	idx := strings.LastIndex(cookieValue, ".")
	if idx <= 0 {
		return "", false
	}
	value, sig := cookieValue[:idx], cookieValue[idx+1:]
	mac := hmac.New(sha256.New, p.stateKey)
	mac.Write([]byte(value))
	expect := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expect)) {
		return "", false
	}
	return value, true
}

// pkceChallenge returns the S256 code_challenge for a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomURLString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

const oidcStateCookieName = "okesu_cp_oidc_state"
