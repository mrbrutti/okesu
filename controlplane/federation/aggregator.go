package federation

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/agent/s3transport"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation/s3rpc"
)

// Aggregator fans out federated read requests across all healthy
// children. Used by the parent's list handlers (/api/findings,
// /api/agents, /api/nodes) to merge local rows with each child's
// equivalent rows.
//
// One Aggregator per Server. Re-uses the same TLS config as the
// poller (InsecureSkipVerify) — federated lab work uses self-signed
// certs; pinned-CA verification is a Phase 9.7 concern.
type Aggregator struct {
	store  *db.Store
	client *http.Client
	// s3 holds the per-peer bucket-cached payloads that s3reader
	// populates. FetchRaw consults this before doing its HTTPS GET
	// when the peer's transport is s3_dead_drop — that's how
	// federated findings/daimons surface from a child CP that has
	// no inbound HTTPS path. Nil disables S3 federation reads.
	s3 S3Source

	// s3Clients caches one *s3transport.Client per transport_config_id
	// so the parent's write-pipe submitter doesn't redo bucket connect
	// handshakes for every directive. Populated lazily on first
	// SubmitS3Directive against a given peer's transport config.
	s3ClientsMu sync.Mutex
	s3Clients   map[int64]*s3transport.Client
}

// S3Source is the read-side interface the aggregator uses to look
// up bucket-cached payloads for s3_dead_drop peers. Implemented by
// federation/s3reader's AssetCache — defined as an interface here
// so aggregator.go doesn't import s3reader.
type S3Source interface {
	Get(peerID int64, asset string) ([]byte, bool)
}

func NewAggregator(store *db.Store) *Aggregator {
	return &Aggregator{
		store: store,
		client: &http.Client{
			Timeout: 8 * time.Second, // total per-peer fetch
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// SetS3Source wires up the bucket-cached read path for s3_dead_drop
// peers. Called once at server boot after both the aggregator and
// s3reader have been constructed (the order matters because
// s3reader's AssetCache must outlive the aggregator).
func (a *Aggregator) SetS3Source(src S3Source) {
	a.s3 = src
}

// PeerSnapshot is the parsed introspect cached on each peer row.
// We only pull the identity fields needed to tag rows.
type PeerSnapshot struct {
	InstanceID  string
	DisplayName string
	Region      string
}

// Peer is one healthy child + its parsed introspect identity.
type Peer struct {
	Row      db.FederationPeer
	Snapshot PeerSnapshot
}

// HealthyPeers returns the set of peers the parent should fan out to
// for a federated read. A peer is healthy iff its last_seen_at is
// fresh (≤ 5 min); stale peers are skipped so a single dead child
// doesn't slow every page render.
func (a *Aggregator) HealthyPeers() ([]Peer, error) {
	rows, err := a.store.ListFederationPeers()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-5 * time.Minute)
	out := make([]Peer, 0, len(rows))
	for _, r := range rows {
		if !r.LastSeenAt.Valid || r.LastSeenAt.Time.Before(cutoff) {
			continue
		}
		var snap struct {
			InstanceID  string `json:"instance_id"`
			DisplayName string `json:"display_name"`
			Region      string `json:"region"`
		}
		if err := json.Unmarshal([]byte(r.IntrospectJSON), &snap); err != nil {
			continue
		}
		// Operator-set display_name on the peer row beats the remote's
		// own display_name — that's the label the operator typed in
		// the Add Peer dialog and expects to see consistently.
		display := r.DisplayName
		if display == "" {
			display = snap.DisplayName
		}
		out = append(out, Peer{
			Row: r,
			Snapshot: PeerSnapshot{
				InstanceID:  snap.InstanceID,
				DisplayName: display,
				Region:      snap.Region,
			},
		})
	}
	return out, nil
}

// FetchJSON does a token-authed GET against `peer.URL + path` and
// unmarshals the response into `out`. Used by handler-specific
// aggregators (findings, daimons, nodes) that know their own row
// type. Errors are wrapped with the peer's display_name so the
// parent's logs identify the offender.
func (a *Aggregator) FetchJSON(ctx context.Context, peer Peer, path string, out any) error {
	body, err := a.FetchRaw(ctx, peer, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", peer.Snapshot.DisplayName, err)
	}
	return nil
}

// FetchRaw is like FetchJSON but returns the raw response body. Used
// by handlers that have to decide how to unmarshal based on a query
// param (e.g. /api/orchestration-runs?counts=1 returns a wrapper
// object instead of an array). Same auth + size cap.
func (a *Aggregator) FetchRaw(ctx context.Context, peer Peer, path string) ([]byte, error) {
	// Phase A.2 — s3_dead_drop peers don't have an HTTPS path. Look
	// up the cached bucket payload by mapping the federation API path
	// to the asset name the publisher writes. Empty cache (publisher
	// hasn't ticked yet, or that asset isn't in the publisher's
	// schedule) returns "no data" rather than an error so federated
	// reads degrade to "show local + known-S3-peer data" cleanly.
	if peer.Row.Transport == "s3_dead_drop" {
		if a.s3 == nil {
			return nil, fmt.Errorf("%s: s3 source not configured", peer.Snapshot.DisplayName)
		}
		asset := s3AssetForPath(path)
		if asset == "" {
			return nil, fmt.Errorf("%s: s3 transport has no asset for path %s", peer.Snapshot.DisplayName, path)
		}
		body, ok := a.s3.Get(peer.Row.ID, asset)
		if !ok {
			// First poll hasn't completed yet, or the publisher
			// doesn't write this asset. Empty array is the
			// neutral element for the typical list endpoints; the
			// federated handler caller will Unmarshal it into an
			// empty slice and merge cleanly.
			return []byte("[]"), nil
		}
		return body, nil
	}

	url := strings.TrimRight(peer.Row.URL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", peer.Snapshot.DisplayName, err)
	}
	req.Header.Set("X-Okesu-Federation-Token", peer.Row.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: dial: %w", peer.Snapshot.DisplayName, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024)) // 4MB cap
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", peer.Snapshot.DisplayName, err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		return nil, fmt.Errorf("%s: HTTP %d: %s", peer.Snapshot.DisplayName, resp.StatusCode, snippet)
	}
	return body, nil
}

// s3AssetForPath maps a federation read path to the bucket asset
// the publisher writes for it. Query strings are dropped — Phase A.2
// publishes the unfiltered list and the parent-side filter is best-
// effort (operators get all-S3-peer findings even with severity/etc
// filters set in the URL). Phase B+ may publish per-filter snapshots
// or parse-and-filter at the boundary; for now, the straight
// path→asset map keeps the wiring trivial.
func s3AssetForPath(path string) string {
	// Strip query params.
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/federation/findings"):
		return "findings.json"
	case strings.HasPrefix(path, "/api/v1/federation/daimons"):
		return "daimons.json"
	case strings.HasPrefix(path, "/api/v1/federation/nodes"):
		return "nodes.json"
	case strings.HasPrefix(path, "/api/v1/federation/orchestrations"):
		return "orchestrations.json"
	// Phase B will add per-resource detail paths (.../findings/{id})
	// once the write pipe lands — they need the same kind of
	// request_id correlation as directives.
	}
	return ""
}

// SubmitS3Directive is the parent-side write helper — used by the
// federation_writes forwarding handlers when target_cp_instance_id
// names a peer with transport=s3_dead_drop. Wraps:
//
//   1. Build an s3 client from the peer's transport_config (cached
//      on the Aggregator so per-Submit calls don't redo connect).
//   2. Look up the parent's own instance_id (cp_meta singleton).
//   3. Mint a request_id, write req/<id>.json, poll resp/<id>.json,
//      return the typed Response.
//
// Errors here are operator-facing — they bubble up to the dialog
// the operator clicked Submit on. timeoutSec=0 uses the default.
func (a *Aggregator) SubmitS3Directive(ctx context.Context, peer Peer, kind string, body json.RawMessage, issuedByEmail string) (*s3rpc.Response, error) {
	if peer.Row.Transport != "s3_dead_drop" {
		return nil, fmt.Errorf("SubmitS3Directive called on non-s3 peer (transport=%s)", peer.Row.Transport)
	}
	if !peer.Row.TransportConfigID.Valid || peer.Row.TransportConfigID.Int64 == 0 {
		return nil, errors.New("s3 peer has no transport_config_id")
	}
	if !peer.Row.BucketPrefix.Valid || peer.Row.BucketPrefix.String == "" {
		return nil, errors.New("s3 peer has no bucket_prefix")
	}

	cli, err := a.s3ClientFor(ctx, peer.Row.TransportConfigID.Int64)
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	meta, err := a.store.CPMeta()
	if err != nil {
		return nil, fmt.Errorf("read parent cp_meta: %w", err)
	}
	rpcClient, err := s3rpc.NewClient(cli, meta.InstanceID, peer.Row.BucketPrefix.String)
	if err != nil {
		return nil, err
	}
	return rpcClient.Submit(ctx, s3rpc.Request{
		Kind:         kind,
		Body:         body,
		IssuedByUser: issuedByEmail,
	})
}

// s3ClientFor returns a cached or freshly-built s3transport.Client
// for the given transport_config_id. Cached because a busy parent
// can issue many directives per tick to the same peer; we don't
// want to redo connect handshakes for each.
func (a *Aggregator) s3ClientFor(ctx context.Context, configID int64) (*s3transport.Client, error) {
	a.s3ClientsMu.Lock()
	if a.s3Clients == nil {
		a.s3Clients = map[int64]*s3transport.Client{}
	}
	if cli, ok := a.s3Clients[configID]; ok {
		a.s3ClientsMu.Unlock()
		return cli, nil
	}
	a.s3ClientsMu.Unlock()

	tc, err := a.store.GetTransportConfig(configID)
	if err != nil {
		return nil, fmt.Errorf("transport_config %d: %w", configID, err)
	}
	cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
		Bucket:    tc.Bucket,
		Endpoint:  tc.ScannerEndpoint(),
		Region:    tc.Region.String,
		UseSSL:    tc.UseSSL,
		AccessKey: tc.AccessKey.String,
		SecretKey: tc.SecretKey.String,
	})
	if err != nil {
		return nil, err
	}
	a.s3ClientsMu.Lock()
	a.s3Clients[configID] = cli
	a.s3ClientsMu.Unlock()
	return cli, nil
}

// FanOutResult is one peer's outcome — either parsed rows (caller
// supplies the typed slice via the closure) or an error. Errors are
// not fatal: the merged page renders local + whichever peers
// responded, with a soft-warn header for the failures.
type FanOutResult struct {
	Peer Peer
	Err  error
}

// FanOut runs `fetch` in parallel across every healthy peer with a
// caller-supplied context. The closure is responsible for parsing
// the response and stashing rows somewhere the caller can read after
// the wait — this keeps the typed-row knowledge in the handler
// rather than forcing generics here.
//
// Returns one FanOutResult per peer (preserving order of HealthyPeers).
// Use the err to decide whether to surface a partial-results warning.
func (a *Aggregator) FanOut(ctx context.Context, fetch func(ctx context.Context, peer Peer) error) ([]FanOutResult, error) {
	peers, err := a.HealthyPeers()
	if err != nil {
		return nil, err
	}
	results := make([]FanOutResult, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		i, p := i, p
		results[i].Peer = p
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fetch(ctx, p); err != nil {
				results[i].Err = err
			}
		}()
	}
	wg.Wait()
	return results, nil
}

// AnyError returns the first non-nil error from a fan-out, or nil.
// Used to decide whether a federated handler emits a partial-result
// header.
func AnyError(rs []FanOutResult) error {
	for _, r := range rs {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

// ErrPartialResults signals "some children responded, some didn't."
// Handlers that catch it can surface the count via response headers
// (X-Okesu-Federation-Partial: <error_summary>) so the UI can warn
// without failing the whole page render.
var ErrPartialResults = errors.New("federation: partial results")
