package api

import (
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// Wire formats — must match agent/mgmt.go exactly.
//
// These types are duplicated here (rather than imported from agent/) so that
// the Control Plane never needs to depend on the daemon's internal types.

type registrationPayload struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Version  string `json:"version"`
}

type heartbeatPayload struct {
	Host      string `json:"host"`
	Ts        int64  `json:"ts"`
	TickCount int64  `json:"tick_count,omitempty"`
}

type remoteConfigResponse struct {
	MaxTurns  int    `json:"max_turns,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
}

// agentNameFromCert returns the CN of the verified TLS client cert.
// Returns empty string if no client cert was presented (rejected before this
// handler runs when ClientAuth is RequireAndVerifyClientCert).
func agentNameFromCert(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return r.TLS.PeerCertificates[0].Subject.CommonName
}

// MgmtRegister handles POST /api/v1/agents/register.
// The agent name is the CN of the client cert; the body's Name field must
// match (or be empty). Returns 200 on success.
func MgmtRegister(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certName := agentNameFromCert(r)
		if certName == "" {
			http.Error(w, "client cert required", http.StatusUnauthorized)
			return
		}

		var p registrationPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if p.Name != "" && p.Name != certName {
			http.Error(w, "name mismatch with cert CN", http.StatusForbidden)
			return
		}
		// Trust the cert CN as the canonical name.
		if err := store.UpsertAgentRegistration(
			certName, p.Host, p.Provider, p.Model, p.Version,
		); err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// MgmtHeartbeat handles POST /api/v1/agents/{name}/heartbeat.
// Updates last heartbeat time and tick count.
func MgmtHeartbeat(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certName := agentNameFromCert(r)
		urlName := chi.URLParam(r, "name")
		if certName == "" {
			http.Error(w, "client cert required", http.StatusUnauthorized)
			return
		}
		if certName != urlName {
			http.Error(w, "url name mismatch with cert CN", http.StatusForbidden)
			return
		}

		var p heartbeatPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := store.RecordHeartbeat(certName, p.Host, p.TickCount); err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// MgmtConfig handles GET /api/v1/agents/{name}/config.
// Returns the desired remote config (max_turns, effort, suspended).
// Empty/zero values mean "no override".
func MgmtConfig(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certName := agentNameFromCert(r)
		urlName := chi.URLParam(r, "name")
		if certName == "" {
			http.Error(w, "client cert required", http.StatusUnauthorized)
			return
		}
		if certName != urlName {
			http.Error(w, "url name mismatch with cert CN", http.StatusForbidden)
			return
		}

		agent, err := store.AgentByName(certName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Unknown agent — return empty config.
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{}"))
				return
			}
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp := remoteConfigResponse{Suspended: agent.DesiredSuspended}
		if agent.DesiredMaxTurns.Valid {
			resp.MaxTurns = int(agent.DesiredMaxTurns.Int64)
		}
		if agent.DesiredEffort.Valid {
			resp.Effort = agent.DesiredEffort.String
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// MgmtKnownIssues handles GET /api/v1/agents/{name}/known-issues — the
// daemon's pull cache of fingerprints the operator has triaged. Used to
// suppress local re-emission of `false_positive`/`wontfix`/`resolved`
// without waiting for the daemon's per-tick LLM logic to figure it out.
//
// Scoped to the calling agent's name (cert CN) across all hosts. Lookback
// defaults to 7 days; override via ?since=<unix-ms>.
func MgmtKnownIssues(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certName := agentNameFromCert(r)
		urlName := chi.URLParam(r, "name")
		if certName == "" || certName != urlName {
			http.Error(w, "url name mismatch with cert CN", http.StatusForbidden)
			return
		}
		// Default 7-day window; daemons can override with a tighter ?since=.
		// (Wider isn't useful — older triage has likely been superseded.)
		sinceMs := r.URL.Query().Get("since")
		var sinceMsInt int64
		if sinceMs != "" {
			_, _ = fmt.Sscanf(sinceMs, "%d", &sinceMsInt)
		}
		if sinceMsInt == 0 {
			sinceMsInt = (time.Now().Add(-7 * 24 * time.Hour)).UnixMilli()
		}
		issues, err := store.KnownIssuesForAgent(certName, sinceMsInt, 1000)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if issues == nil {
			issues = []db.KnownIssue{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(issues)
	}
}

// MgmtFindingsLookup handles GET /api/v1/agents/{name}/findings/search?q=...
// Backs the daemon-side `lookup_findings` LLM tool. Same agent scope, all
// hosts, last 30 days. Caps results at 15.
func MgmtFindingsLookup(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certName := agentNameFromCert(r)
		urlName := chi.URLParam(r, "name")
		if certName == "" || certName != urlName {
			http.Error(w, "url name mismatch with cert CN", http.StatusForbidden)
			return
		}
		q := r.URL.Query().Get("q")
		if len(q) < 2 {
			http.Error(w, "q (min 2 chars) required", http.StatusBadRequest)
			return
		}
		var limit int
		_, _ = fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
		results, err := store.LookupFindings(certName, q, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if results == nil {
			results = []db.LookupFindingsResult{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results)
	}
}

// MTLSAccessLog wraps a handler to log the agent CN for observability.
// Without this, mTLS-protected endpoints look anonymous in the logs.
func MTLSAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject unverified peer certs explicitly. tls.Config with
		// RequireAndVerifyClientCert handles this at the TLS layer, but the
		// check is cheap and defensive against config drift.
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			http.Error(w, "verified client cert required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Ensure we use x509 to keep gofmt-happy when adding fields later.
var _ = x509.ErrUnsupportedAlgorithm
