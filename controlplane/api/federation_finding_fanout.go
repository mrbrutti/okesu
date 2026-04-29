// Federated finding-trigger fan-out (Phase 9.x — task #155).
//
// Problem: a parent CP's OnFinding hook only fires for findings
// projected on the parent itself. Findings projected by a child
// CP's daimons stay siloed — a finding-trigger orchestration
// installed on the global parent never sees them, so operators
// who centralize their orchestration library at the parent miss
// every event from the fleet.
//
// Fix: a small poller on the parent that periodically lists each
// healthy peer's recent findings, dedupes against an in-memory
// per-peer high-water mark, and calls the parent's existing
// OnFinding hook for every newly-projected finding. The hook is
// already wired into the OrchestrationCoordinator, so the rest of
// the trigger pipeline (filter eval, run creation, audit) gets
// reused unchanged.
//
// Why polling vs push:
//
//   - Push from child → parent would need a new endpoint, child-side
//     state ("which parents to notify"), and another auth surface.
//     The pre-existing federation read endpoints already authenticate
//     parent → child polling, so leaning on them avoids new wire
//     contracts.
//   - Latency: a 15s poll interval is well inside the response time
//     orchestrations actually need (most fire criteria-checks, not
//     keystroke-tracking). Operators who need real-time can dial the
//     interval down via the constructor.
//
// Restart safety: on the FIRST poll for a given peer the fanout
// records the current max id without firing OnFinding. Only
// findings projected AFTER parent boot fire — we don't replay
// hours of backlog through the orchestrator on restart.

package api

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/federation"
	"github.com/section9labs/okesu/controlplane/orchestrator"
)

// FederationFindingFanout polls each healthy federation peer for
// new findings and routes them through the parent's OnFinding hook.
// Run lives for the process lifetime; cancel via the ctx passed to
// Run.
type FederationFindingFanout struct {
	agg      *federation.Aggregator
	hook     func(orchestrator.FindingPayload)
	interval time.Duration
	maxBatch int

	mu        sync.Mutex
	lastSeen  map[int64]int64  // peer.ID → max finding id observed so far
	bootstrap map[int64]bool   // peer.ID → has had its first poll yet
}

// NewFederationFindingFanout constructs a fanout that fires `hook`
// for every newly-observed federated finding. interval defaults to
// 15s when zero.
func NewFederationFindingFanout(agg *federation.Aggregator, hook func(orchestrator.FindingPayload), interval time.Duration) *FederationFindingFanout {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &FederationFindingFanout{
		agg:       agg,
		hook:      hook,
		interval:  interval,
		maxBatch:  500,
		lastSeen:  map[int64]int64{},
		bootstrap: map[int64]bool{},
	}
}

// Run blocks until ctx is cancelled, polling peers every interval.
func (f *FederationFindingFanout) Run(ctx context.Context) {
	t := time.NewTicker(f.interval)
	defer t.Stop()
	// Tick once immediately so the bootstrap pass populates watermarks
	// before the first interval elapses; otherwise a finding projected
	// in the first 15s after parent boot would slip through.
	f.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.tick(ctx)
		}
	}
}

// tick performs one fan-out pass: list peers, fetch each one's
// recent findings, fire hook for new ones. Peer-level errors are
// logged and swallowed — one flaky child shouldn't stall the rest.
func (f *FederationFindingFanout) tick(ctx context.Context) {
	peers, err := f.agg.HealthyPeers()
	if err != nil {
		log.Printf("federation finding fanout: list peers: %v", err)
		return
	}
	if len(peers) == 0 {
		return
	}
	for _, peer := range peers {
		f.tickPeer(ctx, peer)
	}
}

// federatedFinding captures the subset of findingJSON the trigger
// payload needs. We deliberately keep it shallow — the wire shape
// has many fields the trigger doesn't use, and binding to all of
// them would create unwanted coupling.
type federatedFinding struct {
	ID         int64          `json:"id"`
	Severity   string         `json:"severity"`
	Title      string         `json:"title"`
	Agent      string         `json:"agent"`
	Host       string         `json:"host"`
	Category   string         `json:"category"`
	DedupKey   string         `json:"dedup_key"`
	Resource   string         `json:"resource"`
	Attributes map[string]any `json:"-"` // populated separately if needed
	// Raw attributes blob — comes through as json.RawMessage on the
	// wire because findingJSON.Attributes is a json.RawMessage. We
	// unmarshal lazily only when the trigger payload demands it.
	AttributesRaw json.RawMessage `json:"attributes,omitempty"`
}

func (f *FederationFindingFanout) tickPeer(ctx context.Context, peer federation.Peer) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// state=all + ordering — the FindingsList handler returns rows
	// newest-first by default, so we get the most recent findings up
	// front. Limit caps the per-tick fan-out.
	path := "/api/v1/federation/findings?state=all&limit=200"
	var rows []federatedFinding
	if err := f.agg.FetchJSON(rctx, peer, path, &rows); err != nil {
		log.Printf("federation finding fanout: peer=%d fetch: %v", peer.Row.ID, err)
		return
	}
	if len(rows) == 0 {
		return
	}

	f.mu.Lock()
	booted := f.bootstrap[peer.Row.ID]
	prev := f.lastSeen[peer.Row.ID]
	// Compute new max in this batch.
	newMax := prev
	for _, r := range rows {
		if r.ID > newMax {
			newMax = r.ID
		}
	}
	f.lastSeen[peer.Row.ID] = newMax
	if !booted {
		// First time seeing this peer — record watermark, don't fire
		// hooks for the backlog. Future polls will fire only on
		// findings with id > newMax.
		f.bootstrap[peer.Row.ID] = true
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()

	// Fire hook for findings with id > prev. Process oldest-first so
	// orchestrations see them in projection order even though the API
	// returned newest-first.
	new := make([]federatedFinding, 0, len(rows))
	for _, r := range rows {
		if r.ID > prev {
			new = append(new, r)
		}
	}
	// Sort ascending by id without pulling in sort just for two lines.
	for i := 1; i < len(new); i++ {
		for j := i; j > 0 && new[j-1].ID > new[j].ID; j-- {
			new[j-1], new[j] = new[j], new[j-1]
		}
	}
	for _, r := range new {
		var attrs map[string]any
		if len(r.AttributesRaw) > 0 {
			_ = json.Unmarshal(r.AttributesRaw, &attrs)
		}
		f.hook(orchestrator.FindingPayload{
			ID:         r.ID,
			Severity:   r.Severity,
			Title:      r.Title,
			Agent:      r.Agent,
			Host:       r.Host,
			Category:   r.Category,
			DedupKey:   r.DedupKey,
			Resource:   r.Resource,
			Attributes: attrs,
		})
	}
}
