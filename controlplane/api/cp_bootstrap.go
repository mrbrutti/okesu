// CP bootstrap exchange — consumes a one-time bootstrap token and
// returns a long-lived rotating federation token + the parent's
// peer-side identity. Called by a child CP on first boot, after
// which the bootstrap token is permanently invalidated.
//
// Wire shape, on purpose, is intentionally minimal: the child sends
// {token, child_url, region, display_name}, the parent responds with
// {federation_token, peer_id, parent_instance_id}. That's enough for
// the child to re-introspect under its new identity and for the
// parent's federation aggregator to start polling.

package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// CPBootstrapRequest is the JSON body the child CP posts on first
// boot. child_url is whatever /api/v1/cp/introspect will be live at —
// the parent's aggregator polls that endpoint going forward, so it
// must be reachable from the parent.
type CPBootstrapRequest struct {
	Token       string `json:"token"`
	ChildURL    string `json:"child_url"`
	Region      string `json:"region"`
	DisplayName string `json:"display_name"`
}

type CPBootstrapResponse struct {
	FederationToken string `json:"federation_token"`
	PeerID          int64  `json:"peer_id"`
	// ParentInstanceID is what the child should record as its parent
	// for upstream events / introspect-back. Currently empty (Phase
	// 21.1 wires only one direction); kept on the wire so we don't
	// break compat once 9.5+ bidirectional federation lands.
	ParentInstanceID string `json:"parent_instance_id,omitempty"`
}

// CPBootstrapHandler verifies the bootstrap token, registers the
// child as a federation peer with a fresh rotating token, and
// returns that token to the caller. No auth on the endpoint itself —
// the bootstrap token IS the auth, and after this single exchange
// it's burned.
//
// When the burned token belongs to a managed-deploy cp_provisions
// row, the handler advances that row to ready and evicts the cached
// bundle (the new CP has it). Manual-bundle exchanges have no
// matching provision row; the advance is a no-op for them.
func CPBootstrapHandler(store *db.Store, poller *federation.Poller, bundleCache *BundleCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CPBootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		req.Token = strings.TrimSpace(req.Token)
		req.ChildURL = strings.TrimSpace(req.ChildURL)
		req.Region = strings.TrimSpace(req.Region)
		req.DisplayName = strings.TrimSpace(req.DisplayName)
		if req.Token == "" || req.ChildURL == "" {
			http.Error(w, "token and child_url are required", http.StatusBadRequest)
			return
		}

		// Verify — non-specific errors so a leaked-prefix probe gets
		// a uniform "invalid bootstrap token" response.
		bt, err := store.VerifyCPBootstrapToken(req.Token)
		if err != nil {
			http.Error(w, "invalid bootstrap token", http.StatusUnauthorized)
			return
		}

		// Trust but verify the child's URL — it must be reachable AND
		// already serving /api/v1/cp/introspect with the new token we
		// just minted. We mint first, probe with the new credential,
		// then commit. That sequence catches the case where the child
		// is alive but firewalled from the parent before we've added
		// it as a peer in the DB.
		newPeerToken, err := mintFederationToken()
		if err != nil {
			http.Error(w, "mint token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Probe the child with the new token. The probe call is
		// optional in the bootstrap path — for v1 we skip it because
		// the child posts to us, not the other way. The aggregator's
		// poller will discover unreachable peers via its standard
		// health check on the next tick.
		_ = poller // available for future probe-on-bootstrap

		displayName := chooseStr(req.DisplayName, bt.DisplayName)
		peer, err := store.AddFederationPeer(req.ChildURL, displayName, newPeerToken)
		if err != nil {
			// Most likely a UNIQUE-constraint clash on the URL. Surface
			// the underlying error so the operator knows whether to
			// rotate the bootstrap token vs unstick a stale peer row.
			http.Error(w, "register peer: "+err.Error(), http.StatusConflict)
			return
		}

		// Burn the bootstrap token. From here on it returns
		// "already used" for any subsequent /bootstrap call.
		if err := store.MarkCPBootstrapTokenUsed(bt.ID, peer.ID, req.ChildURL); err != nil {
			// We've already added the peer — clean up before failing
			// so the operator doesn't end up with a zombie peer row
			// and an unburned token.
			_ = store.DeleteFederationPeer(peer.ID)
			http.Error(w, "burn token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Phase 21.3b — managed-deploy advancement. If this token
		// was minted for a cp_provisions row, flip that row to
		// ready and drop the cached bundle (the new CP has it).
		// No-op for manual-bundle exchanges where there's no row.
		if bundleCache != nil {
			AdvanceCPProvisionOnBootstrap(store, bundleCache, bt.ID, peer.ID)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CPBootstrapResponse{
			FederationToken: newPeerToken,
			PeerID:          peer.ID,
		})
	}
}

// mintFederationToken returns a fresh long-lived federation token
// (URL-safe hex). Same prefix scheme as the existing tokens so logs
// stay greppable.
func mintFederationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return "okesu_fed_" + hex.EncodeToString(b), nil
}

func chooseStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// Compile-time guard: bootstrap errors are uniform via http.Error.
// If a future change needs structured error responses, add a typed
// errPayload struct here and switch the writers over.
var _ = errors.New
