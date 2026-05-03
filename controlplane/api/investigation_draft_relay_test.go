package api

import (
	"sync"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// fakeClient drives the relay in tests without a real WebSocket.
type fakeClient struct {
	mu     sync.Mutex
	sent   [][]byte
	closed bool
}

func (c *fakeClient) Send(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.sent = append(c.sent, append([]byte(nil), b...))
}

func (c *fakeClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func (c *fakeClient) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.sent))
	copy(out, c.sent)
	return out
}

func TestRelayHub_JoinCreatesRoom(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	c := &fakeClient{}
	wsC := NewWsClient("a@x", c)
	room := hub.JoinOrLoad(invID, wsC)
	defer room.Leave(wsC)

	if room.investigationID != invID {
		t.Errorf("room.investigationID = %d, want %d", room.investigationID, invID)
	}

	hub.mu.RLock()
	rooms := len(hub.rooms)
	hub.mu.RUnlock()
	if rooms != 1 {
		t.Errorf("hub.rooms = %d, want 1", rooms)
	}
}

func TestRelayHub_BroadcastForwardsBytes(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	a := &fakeClient{}
	b := &fakeClient{}
	wsA := NewWsClient("a@x", a)
	wsB := NewWsClient("b@x", b)
	room := hub.JoinOrLoad(invID, wsA)
	_ = hub.JoinOrLoad(invID, wsB)
	defer room.Leave(wsA)
	defer room.Leave(wsB)

	frame := []byte{0, 1, 0xab, 0xcd}
	room.Broadcast(wsA, frame)

	// b receives the bytes; a does not.
	if got := b.snapshot(); len(got) != 1 || string(got[0]) != string(frame) {
		t.Errorf("b received %v, want one frame matching %v", got, frame)
	}
	// No DB snapshot was seeded, so a should have received nothing at all.
	if got := a.snapshot(); len(got) != 0 {
		t.Errorf("a received %d frames, want 0 (sender exclusion + no snapshot push)", len(got))
	}
}

func TestRelayHub_TeardownAfterLastLeave(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)
	hub.teardownDelay = 10 * time.Millisecond // shorten for the test

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	c := &fakeClient{}
	wsC := NewWsClient("a@x", c)
	room := hub.JoinOrLoad(invID, wsC)

	room.Leave(wsC)
	time.Sleep(50 * time.Millisecond)

	hub.mu.RLock()
	_, exists := hub.rooms[invID]
	hub.mu.RUnlock()
	if exists {
		t.Errorf("room still exists after teardown delay")
	}
}

func TestRelayHub_LoadsSnapshotFromDB(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	if err := store.UpsertInvestigationNoteDraft(invID, []byte{0xde, 0xad}); err != nil {
		t.Fatal(err)
	}

	c := &fakeClient{}
	wsC := NewWsClient("a@x", c)
	room := hub.JoinOrLoad(invID, wsC)
	defer room.Leave(wsC)

	// On first join with a non-empty snapshot, the relay sends the
	// persisted snapshot wrapped in a sync-step-2 envelope. Wait
	// briefly for the goroutine to deliver it.
	time.Sleep(50 * time.Millisecond)
	got := c.snapshot()
	if len(got) == 0 {
		t.Fatalf("client received no snapshot on first join")
	}
	// First frame should be a sync (type 0).
	if got[0][0] != 0 {
		t.Errorf("first frame type = %d, want 0", got[0][0])
	}
}

func TestRelayHub_SetSnapshotPersistsToDB(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	c := &fakeClient{}
	wsC := NewWsClient("a@x", c)
	room := hub.JoinOrLoad(invID, wsC)
	defer room.Leave(wsC)

	want := []byte{0x12, 0x34, 0x56}
	room.SetSnapshot(want)

	got, err := store.GetInvestigationNoteDraft(invID)
	if err != nil {
		t.Fatalf("get from DB: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("DB has %v, want %v", got, want)
	}
}

func TestRelayHub_LiveRoomIDs(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	id1 := mustCreateInvestigationForRelayTest(t, store, "case-1")
	id2 := mustCreateInvestigationForRelayTest(t, store, "case-2")

	c1 := &fakeClient{}
	c2 := &fakeClient{}
	r1 := hub.JoinOrLoad(id1, NewWsClient("a@x", c1))
	r2 := hub.JoinOrLoad(id2, NewWsClient("b@x", c2))

	live := hub.LiveRoomIDs()
	if len(live) != 2 {
		t.Errorf("live rooms = %d, want 2", len(live))
	}
	// Order is unspecified; check membership.
	saw := map[int64]bool{}
	for _, id := range live {
		saw[id] = true
	}
	if !saw[id1] || !saw[id2] {
		t.Errorf("missing IDs from live: got %v, want %d and %d", live, id1, id2)
	}
	r1.Leave(NewWsClient("a@x", c1)) // wrong client; just exercises the path
	_ = r2
}

func TestRelayHub_LeaderHandover(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	a := &fakeClient{}
	b := &fakeClient{}
	wsA := NewWsClient("a@x", a)
	wsB := NewWsClient("b@x", b)
	room := hub.JoinOrLoad(invID, wsA)
	_ = hub.JoinOrLoad(invID, wsB)

	// a is the initial leader. After a leaves, b should be elected.
	room.Leave(wsA)

	room.mu.Lock()
	leader := room.leader
	room.mu.Unlock()
	if leader != wsB {
		t.Errorf("after leader left, new leader = %v, want wsB (%v)", leader, wsB)
	}
}

func TestRelayHub_MarkFinalizingIsExclusive(t *testing.T) {
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	c := &fakeClient{}
	room := hub.JoinOrLoad(invID, NewWsClient("a@x", c))

	if !room.MarkFinalizing() {
		t.Errorf("first MarkFinalizing returned false; want true")
	}
	if room.MarkFinalizing() {
		t.Errorf("second MarkFinalizing returned true; want false (exclusion)")
	}
}

func TestRelayHub_SnapshotLoopExitsCleanly(t *testing.T) {
	// Ensures the per-room snapshot goroutine terminates when the
	// room is removed (regression guard against the goroutine leak).
	store := openTempStoreForRelayTest(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)
	hub.teardownDelay = 5 * time.Millisecond

	invID := mustCreateInvestigationForRelayTest(t, store, "case")
	c := &fakeClient{}
	wsC := NewWsClient("a@x", c)
	room := hub.JoinOrLoad(invID, wsC)

	// Capture the per-room done channel so we can verify it closes.
	room.mu.Lock()
	done := room.done
	room.mu.Unlock()

	room.Leave(wsC)
	// Wait for teardown timer + room removal.
	time.Sleep(50 * time.Millisecond)

	select {
	case <-done:
		// ok — channel closed by shutdownLocked
	case <-time.After(100 * time.Millisecond):
		t.Errorf("room.done not closed after teardown; goroutine leaked")
	}
}

// Helpers (test-only)

func mustCreateInvestigationForRelayTest(t *testing.T, store *db.Store, title string) int64 {
	t.Helper()
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: title})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return id
}

// openTempStoreForRelayTest is a simple wrapper around the db package's
// openTempStore (which is in the db package and unexported). The api
// package can't call it directly; use the existing api-package
// equivalent if present, or open a temp DB the same way the existing
// graph/structure tests do.
//
// IMPORTANT for the implementer: this helper likely already exists in
// the api package as `newSeededTestStore` or similar. Use whichever
// helper opens an in-memory or temp-file *db.Store. Adjust this
// function body to match.
func openTempStoreForRelayTest(t *testing.T) *db.Store {
	t.Helper()
	return newSeededTestStore(t)
}
