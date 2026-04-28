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

	"github.com/section9labs/okesu/controlplane/db"
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
	url := strings.TrimRight(peer.Row.URL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%s: build request: %w", peer.Snapshot.DisplayName, err)
	}
	req.Header.Set("X-Okesu-Federation-Token", peer.Row.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: dial: %w", peer.Snapshot.DisplayName, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024)) // 4MB cap
	if err != nil {
		return fmt.Errorf("%s: read body: %w", peer.Snapshot.DisplayName, err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		return fmt.Errorf("%s: HTTP %d: %s", peer.Snapshot.DisplayName, resp.StatusCode, snippet)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", peer.Snapshot.DisplayName, err)
	}
	return nil
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
