// Parent → child fleet-env push. Phase 22.10 follow-up.
//
// The federation poller already runs "child polls parent" via
// fetchAndApplyFleetEnv: a CP that has registered upstream peers
// pulls their fleet-env on every tick. That model breaks down in
// star topologies where only the parent has peers registered (lab
// pattern: global has east+west as peers; east+west have no peers).
// The fleet-env pull then can't fire because the children don't know
// where to pull from.
//
// PushFleetEnvToPeers closes the gap: the parent CP iterates every
// registered peer and POSTs the current fleet-env to each via the
// new /api/v1/federation/fleet-env push endpoint. Same outbound TLS
// + token auth the poller already uses, so no new credentials.

package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// FleetEnvPusher fans an updated fleet-env out to every registered
// peer. Constructed once per Server; reuses the same TLS-skip
// http.Client the Poller already uses (lab self-signed certs).
type FleetEnvPusher struct {
	store  *db.Store
	client *http.Client
	// Per-CP instance id of the parent (us). Sent as parent_cp_id so
	// children's SetFleetEnvFromFederation tags the row correctly.
	parentInstanceID func() string
}

// NewFleetEnvPusher builds a pusher. parentInstanceID is a getter so
// the boot order that fills cp_meta after the pusher is constructed
// still resolves correctly at first push time.
func NewFleetEnvPusher(store *db.Store, client *http.Client, parentInstanceID func() string) *FleetEnvPusher {
	return &FleetEnvPusher{store: store, client: client, parentInstanceID: parentInstanceID}
}

// PushNow fan-outs the current fleet-env to every registered peer.
// Best-effort per peer — one peer's 5xx doesn't stop the others.
// Returns the count of peers we successfully posted to so callers
// (server.go onChange) can log.
func (p *FleetEnvPusher) PushNow(ctx context.Context) (int, error) {
	if p == nil || p.store == nil {
		return 0, nil
	}
	mk, err := p.store.MasterKeyFromMeta()
	if err != nil {
		return 0, fmt.Errorf("master key: %w", err)
	}
	fe, err := p.store.GetFleetEnvWithKeys(mk)
	if err != nil {
		return 0, fmt.Errorf("read fleet env: %w", err)
	}
	if fe.Version == 0 && fe.AnthropicAPIKey == "" && fe.OpenAIAPIKey == "" {
		// Nothing to push yet. Childen wouldn't accept the no-op
		// either (the apply guard skips empty payloads).
		return 0, nil
	}
	parentID := ""
	if p.parentInstanceID != nil {
		parentID = p.parentInstanceID()
	}
	if parentID == "" {
		return 0, fmt.Errorf("parent instance_id unset (cp_meta not seeded yet)")
	}
	peers, err := p.store.ListFederationPeers()
	if err != nil {
		return 0, fmt.Errorf("list peers: %w", err)
	}
	if len(peers) == 0 {
		return 0, nil
	}
	body, err := json.Marshal(map[string]any{
		"anthropic_api_key": fe.AnthropicAPIKey,
		"openai_api_key":    fe.OpenAIAPIKey,
		"version":           fe.Version,
		"parent_cp_id":      parentID,
	})
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}

	var (
		mu        sync.Mutex
		successes int
		wg        sync.WaitGroup
	)
	for _, peer := range peers {
		peer := peer
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.pushOne(ctx, peer, body); err != nil {
				log.Printf("fleet-env-push: peer=%d url=%s: %v", peer.ID, peer.URL, err)
				return
			}
			mu.Lock()
			successes++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return successes, nil
}

func (p *FleetEnvPusher) pushOne(ctx context.Context, peer db.FederationPeer, body []byte) error {
	url := strings.TrimRight(peer.URL, "/") + "/api/v1/federation/fleet-env"
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Okesu-Federation-Token", peer.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(rb))
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return nil
}
