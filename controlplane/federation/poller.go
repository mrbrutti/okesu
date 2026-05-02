// Package federation implements the parent side of the Okesu federation
// protocol introduced in Phase 9. A parent CP polls each registered
// child's /api/v1/cp/introspect endpoint on a fixed interval, stores
// the response on the federation_peers row, and exposes the cached
// view via /api/federation/peers for the UI.
//
// The protocol contract lives on the child side in
// controlplane/api/cp_meta.go (the IntrospectResponse struct + the
// GET /api/v1/cp/introspect handler). This package only consumes that
// contract; it never reaches into a child's internal state.
package federation

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	iocfeeds "github.com/section9labs/okesu/controlplane/ioc/feeds"
)

// PollInterval controls how often each peer is polled. Chosen to be
// fast enough to feel "live" in the parent UI but slow enough to be
// negligible network load on the child (a single HTTP GET every 30s
// per peer). The parent UI re-renders on its own ~15s refresh and
// reads from the cached row — it never hits the children directly.
const PollInterval = 30 * time.Second

// pollTimeout caps each individual introspect call. Generous enough
// for a slow child but well below PollInterval so a hung peer doesn't
// stall the whole tick.
const pollTimeout = 10 * time.Second

// Poller runs the per-peer introspect loop. One Poller covers all
// peers — a single goroutine reads peers from the store every tick
// rather than spawning one goroutine per peer, so adding/removing a
// peer at runtime is automatically picked up on the next tick without
// goroutine bookkeeping.
type Poller struct {
	store    *db.Store
	client   *http.Client
	interval time.Duration

	// onUpdate is invoked after every successful poll, so subscribers
	// (e.g. the broadcaster for the federation SSE stream) can push
	// fresh data to UI clients without polling the DB themselves.
	onUpdate func(peerID int64)

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewPoller wires a Poller against the store. Pass an optional
// onUpdate callback to be notified after each successful introspect.
// The HTTP client uses InsecureSkipVerify because federated children
// typically use self-signed certs in the lab + early production; a
// future phase will support pinned-CA and proper TLS verification.
func NewPoller(store *db.Store, onUpdate func(peerID int64)) *Poller {
	return &Poller{
		store: store,
		client: &http.Client{
			Timeout: pollTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		interval: PollInterval,
		onUpdate: onUpdate,
	}
}

// Start runs the poll loop until ctx is done. Safe to call once per
// process; calling twice replaces the running loop.
// HTTPClient exposes the configured TLS-skipping outbound client so
// sibling helpers (e.g. FleetEnvPusher) can reuse the same connection
// pool + TLS posture the poller already speaks. Returns nil when
// the Poller is nil — callers should fall back to a default client.
func (p *Poller) HTTPClient() *http.Client {
	if p == nil {
		return nil
	}
	return p.client
}

func (p *Poller) Start(ctx context.Context) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	loopCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.mu.Unlock()

	go func() {
		// Tick once on startup so a freshly-launched parent doesn't
		// wait a full PollInterval before showing peer status.
		p.tickAll(loopCtx)
		t := time.NewTicker(p.interval)
		defer t.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-t.C:
				p.tickAll(loopCtx)
			}
		}
	}()
}

// Stop cancels the loop. Safe to call multiple times.
func (p *Poller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

// tickAll fans out one introspect call per peer, in parallel. A slow
// child can't hold up a fast one because each call is its own
// goroutine bounded by pollTimeout.
func (p *Poller) tickAll(ctx context.Context) {
	peers, err := p.store.ListFederationPeers()
	if err != nil {
		log.Printf("federation poller: list peers: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, peer := range peers {
		peer := peer
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.pollOne(ctx, &peer)
		}()
	}
	wg.Wait()
}

// pollOne hits a single peer's introspect endpoint and updates its
// row. PollOnce is also exported for the API's "force refresh" button.
func (p *Poller) pollOne(ctx context.Context, peer *db.FederationPeer) {
	// s3_dead_drop peers are polled by s3reader.Loop, which writes the
	// same RecordPeerSuccess / RecordPeerFailure markers and hydrates
	// peer-cache assets from the bucket. The federation poller's
	// HTTP-dial path doesn't apply (peer.URL is a synthetic marker
	// like s3-deaddrop://… by design). Skip silently.
	if peer.Transport == "s3_dead_drop" {
		return
	}

	// Unknown transport — log + skip with a clear marker on the peer
	// row so operators see the gap. Do NOT bubble through to
	// fetchIntrospect — that produces a misleading "unsupported
	// protocol scheme" error. The empty-string check covers legacy rows
	// that predate the transport column (they default to https_pull
	// behavior).
	if peer.Transport != "" && peer.Transport != "https_pull" {
		log.Printf("federation poller: peer %d has unknown transport %q; skipping", peer.ID, peer.Transport)
		_ = p.store.RecordPeerFailure(peer.ID, "unknown federation transport: "+peer.Transport)
		return
	}

	body, err := p.fetchIntrospect(ctx, peer.URL, peer.Token)
	if err != nil {
		_ = p.store.RecordPeerFailure(peer.ID, err.Error())
		return
	}
	if err := p.store.RecordPeerSuccess(peer.ID, body); err != nil {
		log.Printf("federation poller: record success for peer %d: %v", peer.ID, err)
		return
	}
	if p.onUpdate != nil {
		p.onUpdate(peer.ID)
	}
	// Best-effort fleet-env mirror. Errors here MUST NOT downgrade peer
	// health — the peer's introspect already succeeded. We log and move
	// on. Parents that don't expose fleet-env (older builds) just 404
	// and we skip.
	p.fetchAndApplyFleetEnv(ctx, peer, body)
	// Best-effort feed-config mirror. Same posture as fleet-env: errors
	// here MUST NOT downgrade peer health. Older parents that don't
	// expose /api/v1/federation/feeds just 404 and we skip.
	p.fetchAndApplyFeeds(ctx, peer, body)
}

// fetchAndApplyFleetEnv runs the second GET against the peer's
// federation fleet-env endpoint and, on success, mirrors the keys
// into our local fleet_env row via SetFleetEnvFromFederation. The
// store call is idempotent + skips when source='local' or
// parentVersion <= currentVersion. Any error is logged and swallowed.
func (p *Poller) fetchAndApplyFleetEnv(ctx context.Context, peer *db.FederationPeer, introspectBody string) {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal([]byte(introspectBody), &probe); err != nil || probe.InstanceID == "" {
		// Already validated by fetchIntrospect; defensive parse only.
		return
	}
	anthropic, openai, version, err := p.fetchFleetEnv(ctx, peer.URL, peer.Token)
	if err != nil {
		log.Printf("federation poller: fleet-env fetch peer=%d: %v", peer.ID, err)
		return
	}
	p.applyFleetEnvFromPeer(probe.InstanceID, anthropic, openai, version)
}

// fetchFleetEnv issues a GET against {baseURL}/api/v1/federation/fleet-env
// and parses the JSON response. Returns the plaintext keys + parent's
// version. Reuses the same TLS config + token header as fetchIntrospect.
func (p *Poller) fetchFleetEnv(ctx context.Context, baseURL, token string) (string, string, int64, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/federation/fleet-env"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Okesu-Federation-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("dial: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", "", 0, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		return "", "", 0, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), snippet)
	}
	var out struct {
		AnthropicAPIKey string `json:"anthropic_api_key"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		Version         int64  `json:"version"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", 0, fmt.Errorf("invalid JSON response: %w", err)
	}
	return out.AnthropicAPIKey, out.OpenAIAPIKey, out.Version, nil
}

// applyFleetEnvFromPeer is the shared apply step. Skips silently when
// the master key isn't available yet or when there's nothing to mirror
// (parent has no fleet-env configured). Errors from the store are
// logged but swallowed — the peer is already healthy.
func (p *Poller) applyFleetEnvFromPeer(parentInstanceID, anthropic, openai string, parentVersion int64) {
	// Nothing to mirror: parent has no fleet-env yet.
	if parentVersion == 0 && anthropic == "" && openai == "" {
		return
	}
	mk, err := p.store.MasterKeyFromMeta()
	if err != nil {
		log.Printf("federation poller: fleet-env apply: master key unavailable: %v", err)
		return
	}
	if _, _, err := p.store.SetFleetEnvFromFederation(mk, parentInstanceID, anthropic, openai, parentVersion); err != nil {
		log.Printf("federation poller: fleet-env apply parent=%s version=%d: %v", parentInstanceID, parentVersion, err)
	}
}

// PollOnce performs one introspect call against a peer and records
// the result. Used by the "force refresh" admin action and by the
// "verify" probe in the Add Peer flow (verifyOnly=true skips the
// store update). Returns the body on success for the verify path.
func (p *Poller) PollOnce(ctx context.Context, peer *db.FederationPeer, verifyOnly bool) (string, error) {
	body, err := p.fetchIntrospect(ctx, peer.URL, peer.Token)
	if err != nil {
		if !verifyOnly {
			_ = p.store.RecordPeerFailure(peer.ID, err.Error())
		}
		return "", err
	}
	if !verifyOnly {
		if err := p.store.RecordPeerSuccess(peer.ID, body); err != nil {
			return "", fmt.Errorf("store update: %w", err)
		}
		if p.onUpdate != nil {
			p.onUpdate(peer.ID)
		}
	}
	return body, nil
}

// fetchIntrospect is the actual HTTP call. Errors are wrapped with
// the operator-visible prefix the UI displays in the "last error"
// column ("HTTP 401 Unauthorized" beats "post: status 401").
func (p *Poller) fetchIntrospect(ctx context.Context, baseURL, token string) (string, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/cp/introspect"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Okesu-Federation-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Pass the response snippet through so a misconfigured token
		// shows up as e.g. "HTTP 401: federation token invalid or
		// not configured" in the UI.
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		return "", fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), snippet)
	}
	// Sanity-check: the body should at minimum be valid JSON with the
	// instance_id field. Catches a child returning the UI HTML when
	// the operator pasted a wrong base URL (e.g. https://child:8444
	// where 8444 is the mgmt plane, not the API).
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", fmt.Errorf("invalid JSON response: %w", err)
	}
	if probe.InstanceID == "" {
		return "", errors.New("response missing instance_id (wrong URL?)")
	}
	return string(body), nil
}

// fetchAndApplyFeeds GETs the parent's federation/feeds list and
// reconciles our local ioc_feeds via SetFeedConfigsFromFederation.
// Errors are logged and swallowed — the peer's introspect already
// succeeded. Federated rows that the parent has uninstalled are
// returned as a toUninstall list and removed via the same
// reconcile-then-delete flow as the manual UninstallFeedHandler.
func (p *Poller) fetchAndApplyFeeds(ctx context.Context, peer *db.FederationPeer, introspectBody string) {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal([]byte(introspectBody), &probe); err != nil || probe.InstanceID == "" {
		return // defensive parse only; introspect already validated
	}
	feeds, err := p.fetchFeedConfigs(ctx, peer.URL, peer.Token)
	if err != nil {
		log.Printf("federation poller: feeds fetch peer=%d: %v", peer.ID, err)
		return
	}
	inserted, updated, toUninstall, err := p.store.SetFeedConfigsFromFederation(probe.InstanceID, feeds)
	if err != nil {
		log.Printf("federation poller: feeds apply parent=%s: %v", probe.InstanceID, err)
		return
	}
	for _, id := range toUninstall {
		fc, err := p.store.GetFeedConfig(id)
		if err != nil {
			continue
		}
		// Reconcile-to-empty drops feed-scoped iocs rows + orphans
		// observations. Same path the UninstallFeedHandler uses.
		if _, err := iocfeeds.Reconcile(p.store, fc.ID, fc.Slug, nil); err != nil {
			log.Printf("federation poller: reconcile-empty %s: %v", fc.Slug, err)
			continue
		}
		if err := p.store.DeleteFeedConfig(fc.ID); err != nil {
			log.Printf("federation poller: delete %s: %v", fc.Slug, err)
		}
	}
	if inserted+updated+len(toUninstall) > 0 {
		log.Printf("federation poller: feeds applied parent=%s ins=%d upd=%d unins=%d",
			probe.InstanceID, inserted, updated, len(toUninstall))
	}
}

// fetchFeedConfigs GETs {baseURL}/api/v1/federation/feeds and parses
// the JSON array response. Reuses the same TLS config + token header
// as fetchIntrospect.
func (p *Poller) fetchFeedConfigs(ctx context.Context, baseURL, token string) ([]db.FederationFeedConfig, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/federation/feeds"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Okesu-Federation-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		return nil, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), snippet)
	}
	var wire []struct {
		Slug                   string `json:"slug"`
		Name                   string `json:"name"`
		Kind                   string `json:"kind"`
		URL                    string `json:"url"`
		Subpath                string `json:"subpath"`
		Parser                 string `json:"parser"`
		RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
		Enabled                bool   `json:"enabled"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	out := make([]db.FederationFeedConfig, 0, len(wire))
	for _, f := range wire {
		out = append(out, db.FederationFeedConfig{
			Slug: f.Slug, Name: f.Name, Kind: f.Kind,
			URL: f.URL, Subpath: f.Subpath, Parser: f.Parser,
			RefreshIntervalSeconds: f.RefreshIntervalSeconds, Enabled: f.Enabled,
		})
	}
	return out, nil
}
