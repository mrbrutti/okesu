package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// federationPeerJSON is the wire shape returned by /api/federation/peers.
// We deliberately do NOT include the plaintext token — it's a secret
// and the UI never needs it once the peer has been added.
type federationPeerJSON struct {
	ID             int64                  `json:"id"`
	URL            string                 `json:"url"`
	DisplayName    string                 `json:"display_name"`
	AddedAt        string                 `json:"added_at"`
	LastPolledAt   string                 `json:"last_polled_at,omitempty"`
	LastSeenAt     string                 `json:"last_seen_at,omitempty"`
	LastError      string                 `json:"last_error,omitempty"`
	HeartbeatAgeS  int64                  `json:"heartbeat_age_sec,omitempty"`
	Healthy        bool                   `json:"healthy"`
	Introspect     map[string]any         `json:"introspect,omitempty"`
}

// peerHealthThreshold defines "fresh enough." Two missed polls (60s)
// is when a peer flips to unhealthy in the UI — gives the operator a
// signal without flapping on a single transient failure.
const peerHealthThreshold = 60 * time.Second

// FederationListPeers handles GET /api/federation/peers.
func FederationListPeers(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		peers, err := store.ListFederationPeers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]federationPeerJSON, 0, len(peers))
		for _, p := range peers {
			out = append(out, toPeerJSON(p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// federationAddRequest is the POST body. URL is required and must
// include a scheme; token is required and is sent on every poll.
// DisplayName is optional — empty falls back to the remote's
// display_name once the first poll lands.
type federationAddRequest struct {
	URL         string `json:"url"`
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
}

// FederationAddPeer handles POST /api/federation/peers. Verifies the
// peer responds correctly to introspect BEFORE persisting — this
// gives operators immediate feedback on bad URL or wrong token,
// rather than "added successfully" + a stale UI row that the poller
// will reject 30s later.
func FederationAddPeer(store *db.Store, poller *federation.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req federationAddRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		req.URL = strings.TrimRight(strings.TrimSpace(req.URL), "/")
		if req.URL == "" || req.Token == "" {
			http.Error(w, "url and token are required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(req.URL, "https://") && !strings.HasPrefix(req.URL, "http://") {
			http.Error(w, "url must start with https:// (http:// only for local dev)", http.StatusBadRequest)
			return
		}

		// Probe the child before persisting. We pass a synthetic peer
		// with id=0; the verifyOnly flag tells PollOnce not to write
		// anything. Surface the failure verbatim — the operator
		// almost always wants to see the underlying HTTP error.
		probe := &db.FederationPeer{URL: req.URL, Token: req.Token}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if _, err := poller.PollOnce(ctx, probe, true); err != nil {
			http.Error(w, "verify failed: "+err.Error(), http.StatusBadGateway)
			return
		}

		peer, err := store.AddFederationPeer(req.URL, req.DisplayName, req.Token)
		if err != nil {
			if errors.Is(err, db.ErrDuplicatePeer) {
				http.Error(w, "peer already registered", http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Trigger an immediate poll so the response carries the
		// introspect snapshot rather than empty fields.
		_, _ = poller.PollOnce(ctx, peer, false)
		fresh, err := store.FederationPeer(peer.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toPeerJSON(*fresh))
	}
}

// FederationDeletePeer handles DELETE /api/federation/peers/{id}.
func FederationDeletePeer(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteFederationPeer(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// FederationRefreshPeer handles POST /api/federation/peers/{id}/refresh.
// Forces an out-of-cycle poll and returns the updated row.
func FederationRefreshPeer(store *db.Store, poller *federation.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		peer, err := store.FederationPeer(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		_, _ = poller.PollOnce(ctx, peer, false)
		fresh, _ := store.FederationPeer(id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPeerJSON(*fresh))
	}
}

// toPeerJSON shapes the wire response, including parsing the cached
// introspect blob into a generic map so the UI can render fields
// without sharing the IntrospectResponse Go type. Healthy is computed
// from last_seen_at age vs peerHealthThreshold.
func toPeerJSON(p db.FederationPeer) federationPeerJSON {
	out := federationPeerJSON{
		ID:          p.ID,
		URL:         p.URL,
		DisplayName: p.DisplayName,
		AddedAt:     p.AddedAt.UTC().Format(time.RFC3339),
		LastError:   p.LastError,
	}
	if p.LastPolledAt.Valid {
		out.LastPolledAt = p.LastPolledAt.Time.UTC().Format(time.RFC3339)
	}
	if p.LastSeenAt.Valid {
		out.LastSeenAt = p.LastSeenAt.Time.UTC().Format(time.RFC3339)
		age := time.Since(p.LastSeenAt.Time)
		out.HeartbeatAgeS = int64(age.Seconds())
		out.Healthy = age <= peerHealthThreshold
	}
	if p.IntrospectJSON != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(p.IntrospectJSON), &m); err == nil {
			out.Introspect = m
		}
	}
	return out
}
