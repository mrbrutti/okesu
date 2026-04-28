package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// Phase 9.7: federation writes.
//
// Pattern: each affected parent write handler is wrapped by a thin
// "forwarder" that peeks the request body for `target_cp_instance_id`.
// If set and matches a registered peer, the handler proxies the same
// body to the peer's federation write endpoint (token-authed) and
// streams the response back. Otherwise it falls through to the local
// handler.
//
// The child side is just the existing local handlers wrapped with
// requireFederationToken — same code path, same wire shape, different
// auth.

const proxyTimeout = 30 * time.Second

// proxyIfTargetCP looks at the JSON body for `target_cp_instance_id`.
// Returns:
//
//   - (false, nil)   — no target set, caller should run local handler
//   - (true,  nil)   — proxied to a peer, response already written
//   - (true,  err)   — proxy attempt failed and a 4xx/5xx was already
//                      written; caller should not also touch w
//
// The body is read once and re-attached to r.Body so a falling-through
// caller can still decode it.
func proxyIfTargetCP(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, federationPath string) (handled bool, err error) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Probe the body for the target field. Empty body / non-JSON
	// just falls through to the local handler.
	if len(body) == 0 || body[0] != '{' {
		return false, nil
	}
	var probe struct {
		TargetCPInstanceID string `json:"target_cp_instance_id"`
	}
	_ = json.Unmarshal(body, &probe)
	if probe.TargetCPInstanceID == "" {
		return false, nil
	}

	peers, _ := agg.HealthyPeers()
	var target *federation.Peer
	for i := range peers {
		if peers[i].Snapshot.InstanceID == probe.TargetCPInstanceID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "target CP not found or unhealthy: "+probe.TargetCPInstanceID, http.StatusNotFound)
		return true, nil
	}

	url := strings.TrimRight(target.Row.URL, "/") + federationPath
	// Strip the target_cp_instance_id from the proxied body — the
	// child doesn't need it (it's the ultimate target) and leaving
	// it in could confuse downstream tooling.
	cleaned, _ := stripTargetField(body)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(cleaned))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true, err
	}
	req.Header.Set("X-Okesu-Federation-Token", target.Row.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout:   proxyTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "proxy to "+target.Snapshot.DisplayName+": "+err.Error(), http.StatusBadGateway)
		return true, err
	}
	defer resp.Body.Close()
	// Stream the child's response back unchanged so the UI sees the
	// usual contract (e.g. {id, name, ...} for NodeCreate).
	for k, vs := range resp.Header {
		// Skip hop-by-hop headers Go's net/http copies on its own.
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	// Add a marker header so the operator's audit log can pick up
	// the cross-CP hop.
	w.Header().Set("X-Okesu-Forwarded-To", target.Snapshot.InstanceID)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true, nil
}

// stripTargetField removes target_cp_instance_id from a JSON body so
// the child doesn't see the parent's targeting field.
func stripTargetField(body []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body, err
	}
	delete(m, "target_cp_instance_id")
	return json.Marshal(m)
}

// ForwardingNodeCreate wraps NodeCreate. When the body carries
// target_cp_instance_id, the request is proxied to the chosen child
// CP's /api/v1/federation/nodes — so an operator can register a node
// in the East CP from the Global UI without leaving the page.
func ForwardingNodeCreate(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled, _ := proxyIfTargetCP(w, r, agg, "/api/v1/federation/nodes"); handled {
			return
		}
		NodeCreate(store).ServeHTTP(w, r)
	}
}

// FederationNodeCreate is the child-side endpoint that ForwardingNodeCreate
// proxies to. Token-authed; otherwise identical to NodeCreate.
func FederationNodeCreate(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, NodeCreate(store))
}
