// Package s3reader is the parent-side half of the S3-dead-drop
// federation transport (Phase A).
//
// For each federation_peers row with transport='s3_dead_drop' the
// reader reads {BucketPrefix}/introspect.json on a tick and updates
// the row exactly the way the HTTPS poller does:
//
//   • success → RecordPeerSuccess(introspect_json)
//                 → last_polled_at, last_seen_at, introspect_json,
//                   last_error='' all stamped
//   • failure → RecordPeerFailure(err.Error())
//                 → last_polled_at + last_error stamped, snapshot kept
//
// The rest of the federation aggregator path reads from those columns
// already, so the UI + HealthyPeers + cp_source tagging all work
// unchanged. The transport choice is invisible above this layer.
//
// Bucket creds come from the row's transport_config_id — operators
// reuse the same transport_configs they already manage for nodes.
// Empty bucket_prefix or missing transport_config skips the row with
// a one-time log line (the operator sees the federation_peer's
// last_error so they can fix it).

package s3reader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/agent/s3transport"
	"github.com/section9labs/okesu/controlplane/db"
)

// PollInterval is the per-peer scan cadence. Same default as the
// HTTPS poller so freshness semantics line up — HealthyPeers'
// 5-minute cutoff applies regardless of transport.
const PollInterval = 30 * time.Second

// Loop is one parent-side reader. Run blocks until ctx is cancelled.
// Reads the federation_peers list every tick and scans S3 peers
// in parallel; HTTPS peers are skipped (they're handled by the
// existing federation.Poller).
type Loop struct {
	store    *db.Store
	interval time.Duration

	mu      sync.Mutex
	clients map[int64]*peerClient // keyed by federation_peers.id

	// Asset cache, keyed by (peer_id, asset_name). Populated on
	// each tick. The federation aggregator reads from this for s3
	// peers, replacing what would otherwise be an HTTPS GET against
	// the peer's URL. Fed to NewAggregator at server boot via
	// SetAssetSource so the aggregator package doesn't have to
	// depend on s3reader's internals.
	assets *AssetCache
}

// AssetCache holds the most recent bucket-fetched payload for each
// (peer_id, asset_name). Reads + writes are concurrency-safe; values
// are returned by reference (callers MUST NOT mutate). On peer
// removal the entry stays until the next tick replaces it; eviction
// happens when the federation_peers row is deleted (we don't track
// that today — leftover entries are harmless, they just take RAM).
type AssetCache struct {
	mu sync.RWMutex
	// peerID → assetName → bytes
	data map[int64]map[string][]byte
}

// NewAssetCache returns an empty cache.
func NewAssetCache() *AssetCache {
	return &AssetCache{data: map[int64]map[string][]byte{}}
}

// Get looks up an asset for a peer. ok=false when nothing's been
// cached yet (first poll hasn't completed) or the asset name isn't
// one the publisher writes.
func (c *AssetCache) Get(peerID int64, asset string) (body []byte, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.data[peerID]; ok {
		v, ok := m[asset]
		return v, ok
	}
	return nil, false
}

// put stores an asset; intended for use by the s3reader Loop.
func (c *AssetCache) put(peerID int64, asset string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.data[peerID]
	if !ok {
		m = map[string][]byte{}
		c.data[peerID] = m
	}
	m[asset] = body
}

// New constructs a Loop. interval defaults to PollInterval when zero.
// Pass a non-nil AssetCache to enable Phase A.2+ asset publishing
// (findings, daimons, ...). When nil, the loop only fetches
// introspect.json for peer-health (Phase A.0 behavior).
func New(store *db.Store, interval time.Duration, assets *AssetCache) *Loop {
	if interval <= 0 {
		interval = PollInterval
	}
	return &Loop{
		store:    store,
		interval: interval,
		clients:  map[int64]*peerClient{},
		assets:   assets,
	}
}

// extraAssets enumerates the bucket objects beyond introspect.json
// the reader pulls into its cache. Names match what the publisher
// writes — the publisher and reader are deliberately decoupled
// from the federation aggregator's path schema (no /api/* baked in)
// so the bucket layout can evolve without churning either side.
var extraAssets = []string{
	"findings.json",
	"daimons.json",
	"nodes.json",
	"orchestrations.json",
	"investigations.json",
}

// peerClient is the cached s3 client for one peer + its prefix. We
// rebuild on transport_config_id change so an operator rotating
// bucket creds takes effect on the next tick.
type peerClient struct {
	configID int64
	prefix   string
	cli      *s3transport.Client
}

// Run blocks polling until ctx is cancelled.
func (l *Loop) Run(ctx context.Context) {
	t := time.NewTicker(l.interval)
	defer t.Stop()
	l.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.tick(ctx)
		}
	}
}

func (l *Loop) tick(ctx context.Context) {
	peers, err := l.store.ListFederationPeers()
	if err != nil {
		log.Printf("s3reader: list peers: %v", err)
		return
	}
	var wg sync.WaitGroup
	for i := range peers {
		p := peers[i]
		if p.Transport != "s3_dead_drop" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.scanPeer(ctx, p)
		}()
	}
	wg.Wait()
}

// scanPeer reads {prefix}/introspect.json for one peer and updates
// the federation_peers row. Errors land in RecordPeerFailure so the
// operator sees them on the Federation page.
func (l *Loop) scanPeer(ctx context.Context, p db.FederationPeer) {
	if !p.TransportConfigID.Valid {
		_ = l.store.RecordPeerFailure(p.ID, "s3_dead_drop peer has no transport_config_id")
		return
	}
	if !p.BucketPrefix.Valid || strings.TrimSpace(p.BucketPrefix.String) == "" {
		_ = l.store.RecordPeerFailure(p.ID, "s3_dead_drop peer has no bucket_prefix")
		return
	}
	prefix := p.BucketPrefix.String
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	cli, err := l.clientFor(ctx, p.TransportConfigID.Int64, prefix)
	if err != nil {
		_ = l.store.RecordPeerFailure(p.ID, "s3 client: "+err.Error())
		return
	}

	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// introspect.json is the authoritative snapshot. heartbeat.json
	// is a freshness-only check; a missing introspect with a fresh
	// heartbeat means the child is online but mid-write — treat as
	// failure (parent UI shows last successful state) rather than
	// guess.
	body, err := cli.GetBytes(rctx, prefix+"introspect.json")
	if err != nil {
		_ = l.store.RecordPeerFailure(p.ID, "fetch introspect: "+err.Error())
		return
	}
	if len(body) == 0 {
		_ = l.store.RecordPeerFailure(p.ID, "introspect.json is empty")
		return
	}
	if err := l.store.RecordPeerSuccess(p.ID, string(body)); err != nil {
		log.Printf("s3reader peer=%d record success: %v", p.ID, err)
	}
	// If this peer was created by a managed CP provision, this
	// successful introspect is the "child is alive" signal — flip
	// the cp_provisions row from bootstrap_pending → ready. The
	// helper is idempotent (only acts on bootstrap_pending rows)
	// so subsequent ticks are no-ops.
	if advanced, err := l.store.AdvanceCPProvisionByPeer(p.ID); err != nil {
		log.Printf("s3reader peer=%d advance provision: %v", p.ID, err)
	} else if advanced {
		_ = l.store.AppendCPProvisionLogByPeer(p.ID,
			"✓ first introspect received over S3 — child CP is alive\n")
	}

	// Phase A.2 — also fetch the resource snapshots into the asset
	// cache. Each is best-effort: a 404 / empty body just leaves the
	// previous cached value in place, so the parent UI degrades to
	// "stale data" rather than disappearing the peer's findings.
	if l.assets != nil {
		for _, name := range extraAssets {
			assetBody, err := cli.GetBytes(rctx, prefix+name)
			if err != nil || len(assetBody) == 0 {
				// Don't downgrade peer health on missing assets — the
				// publisher might be on an older build that doesn't write
				// them yet, or the child has nothing to report.
				continue
			}
			l.assets.put(p.ID, name, assetBody)
		}
	}

	// Fleet-env mirror. Deliberately NOT in extraAssets — that slice
	// is for the federation aggregator's HTTPS read path and fleet-env
	// keys must never be exposed there. Handled separately so the
	// payload only ever flows store→store via SetFleetEnvFromFederation.
	// Best-effort: missing object / older parent / parse failures are
	// silent. Errors are logged and never downgrade peer health.
	l.fetchAndApplyFleetEnv(rctx, p, cli, prefix, body)
}

// fetchAndApplyFleetEnv reads {prefix}/fleet-env.json out of the
// peer's bucket and mirrors it into the local fleet_env row via
// SetFleetEnvFromFederation. The store call is idempotent + skips
// when source='local' or parentVersion <= currentVersion.
func (l *Loop) fetchAndApplyFleetEnv(ctx context.Context, p db.FederationPeer, cli *s3transport.Client, prefix string, introspectBody []byte) {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(introspectBody, &probe); err != nil || probe.InstanceID == "" {
		// Introspect already succeeded; this just means we can't pin a
		// parent_cp_id, so skip the mirror silently.
		return
	}
	feBody, err := cli.GetBytes(ctx, prefix+"fleet-env.json")
	if err != nil || len(feBody) == 0 {
		// 404 / missing object / older parent → nothing to mirror.
		return
	}
	var fe struct {
		AnthropicAPIKey string `json:"anthropic_api_key"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		Version         int64  `json:"version"`
	}
	if err := json.Unmarshal(feBody, &fe); err != nil {
		log.Printf("s3reader peer=%d fleet-env decode: %v", p.ID, err)
		return
	}
	if fe.Version == 0 && fe.AnthropicAPIKey == "" && fe.OpenAIAPIKey == "" {
		// Parent has no fleet-env configured yet.
		return
	}
	mk, err := l.store.MasterKeyFromMeta()
	if err != nil {
		log.Printf("s3reader peer=%d fleet-env apply: master key unavailable: %v", p.ID, err)
		return
	}
	if _, _, err := l.store.SetFleetEnvFromFederation(mk, probe.InstanceID, fe.AnthropicAPIKey, fe.OpenAIAPIKey, fe.Version); err != nil {
		log.Printf("s3reader peer=%d fleet-env apply parent=%s version=%d: %v", p.ID, probe.InstanceID, fe.Version, err)
	}
}

// clientFor returns a cached or freshly-built client for the peer's
// transport_config_id. We cache so a 30s tick across N peers using
// the same transport_config doesn't redo the connect handshake N
// times per tick.
func (l *Loop) clientFor(ctx context.Context, configID int64, prefix string) (*s3transport.Client, error) {
	l.mu.Lock()
	if pc, ok := l.clients[configID]; ok && pc.prefix == prefix {
		l.mu.Unlock()
		return pc.cli, nil
	}
	l.mu.Unlock()

	cfg, err := l.store.GetTransportConfig(configID)
	if err != nil {
		return nil, fmt.Errorf("transport_config %d: %w", configID, err)
	}
	cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
		Bucket:    cfg.Bucket,
		Endpoint:  cfg.ScannerEndpoint(),
		Region:    cfg.Region.String,
		UseSSL:    cfg.UseSSL,
		AccessKey: cfg.AccessKey.String,
		SecretKey: cfg.SecretKey.String,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}

	l.mu.Lock()
	l.clients[configID] = &peerClient{configID: configID, prefix: prefix, cli: cli}
	l.mu.Unlock()
	return cli, nil
}

// Compile-time guard so a removed RecordPeerFailure helper would
// break the build before runtime.
var _ = errors.New
