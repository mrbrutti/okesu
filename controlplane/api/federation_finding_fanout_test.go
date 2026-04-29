package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
	"github.com/section9labs/okesu/controlplane/orchestrator"
)

// TestFederationFindingFanout verifies:
//
//  1. The first poll for a peer records the watermark without firing
//     hooks for the existing backlog.
//  2. A subsequent poll fires hooks for any finding with id >
//     watermark, oldest-first by id.
//  3. Findings already seen are not re-fired.
//
// We exercise tickPeer() directly so the test doesn't need a
// db.Store with seeded federation_peers rows — Aggregator.HealthyPeers
// reads from the DB and isn't easily test-injectable.
func TestFederationFindingFanout(t *testing.T) {
	var (
		mu       sync.Mutex
		findings []federatedFinding
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(findings)
	}))
	defer srv.Close()

	peer := federation.Peer{
		Row: db.FederationPeer{
			ID:    1,
			URL:   srv.URL,
			Token: "test-token",
		},
	}
	agg := federation.NewAggregator(nil)

	var fired []int64
	var fireMu sync.Mutex
	hook := func(p orchestrator.FindingPayload) {
		fireMu.Lock()
		fired = append(fired, p.ID)
		fireMu.Unlock()
	}
	fan := NewFederationFindingFanout(agg, hook, 100*time.Millisecond)

	// Backlog = three pre-existing findings. First poll must NOT fire.
	mu.Lock()
	findings = []federatedFinding{
		{ID: 12, Severity: "HIGH", Title: "old-3"},
		{ID: 11, Severity: "HIGH", Title: "old-2"},
		{ID: 10, Severity: "HIGH", Title: "old-1"},
	}
	mu.Unlock()

	ctx := context.Background()
	fan.tickPeer(ctx, peer)

	fireMu.Lock()
	if len(fired) != 0 {
		t.Errorf("expected no hooks fired on first poll (backlog skip), got %v", fired)
	}
	fireMu.Unlock()

	// Add two new findings — id 13, 14.
	mu.Lock()
	findings = []federatedFinding{
		{ID: 14, Severity: "HIGH", Title: "new-2"},
		{ID: 13, Severity: "HIGH", Title: "new-1"},
		{ID: 12, Severity: "HIGH", Title: "old-3"},
		{ID: 11, Severity: "HIGH", Title: "old-2"},
		{ID: 10, Severity: "HIGH", Title: "old-1"},
	}
	mu.Unlock()
	fan.tickPeer(ctx, peer)

	fireMu.Lock()
	if len(fired) != 2 {
		t.Fatalf("expected 2 hooks, got %v", fired)
	}
	if fired[0] != 13 || fired[1] != 14 {
		t.Errorf("expected oldest-first ordering [13, 14], got %v", fired)
	}
	fireMu.Unlock()

	// A third tick with no new findings — must not re-fire the same ids.
	fan.tickPeer(ctx, peer)
	fireMu.Lock()
	if len(fired) != 2 {
		t.Errorf("expected no re-fire, got %v", fired)
	}
	fireMu.Unlock()
}
