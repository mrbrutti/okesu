// War-room draft relay. Forwards Yjs binary frames between operators
// in the same case. Server-side state is intentionally byte-blind:
// snapshots come from a leader-elected client, not from a Go-side
// Y.Doc. See yjs_protocol.go for the few helpers that peek at frame
// type bytes.
package api

import (
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// frameSender is the interface the relay uses to push bytes to a
// client. Real WebSocket clients implement it via a channel + write
// pump (see investigation_draft_ws.go); test clients implement it
// directly.
type frameSender interface {
	Send(b []byte)
	Close()
}

// userIdentity is the shape the relay needs from a connected operator.
// The real auth.User satisfies this; tests use a stub.
type userIdentity interface {
	UserEmail() string
}

// emailIdentity wires an email string into the userIdentity
// interface.
type emailIdentity struct{ email string }

func (e emailIdentity) UserEmail() string { return e.email }

// wsClient is one connected operator. The WebSocket handler builds
// these and passes them to the hub; the relay treats them as opaque
// participants.
type wsClient struct {
	user   userIdentity
	sender frameSender
	roomID int64

	// Awareness state (latest payload) for backfilling new joiners.
	awarenessMu sync.Mutex
	awareness   []byte
}

// NewWsClient constructs a wsClient from the WebSocket handler's
// auth context. Used by investigation_draft_ws.go.
func NewWsClient(email string, sender frameSender) *wsClient {
	return &wsClient{user: emailIdentity{email: email}, sender: sender}
}

// room holds per-case relay state.
type room struct {
	investigationID int64
	hub             *RelayHub

	mu             sync.Mutex
	clients        map[*wsClient]struct{}
	leader         *wsClient // designated snapshot source; nil = no leader
	snapshot       []byte    // last-known canonical state; persisted on tick
	dirty          bool      // set on broadcast; cleared on snapshot
	snapshotTicker *time.Ticker
	teardownTimer  *time.Timer
	finalizing     bool // set during a finalize POST
}

// RelayHub holds all live rooms.
type RelayHub struct {
	store         *db.Store
	mu            sync.RWMutex
	rooms         map[int64]*room
	teardownDelay time.Duration

	// snapshotInterval is how often the snapshot timer fires per room.
	snapshotInterval time.Duration

	stopCh chan struct{}
}

// NewRelayHub allocates a hub. Caller must call Shutdown on process
// exit. Defaults: teardownDelay 30s, snapshotInterval 5s.
func NewRelayHub(store *db.Store) *RelayHub {
	return &RelayHub{
		store:            store,
		rooms:            map[int64]*room{},
		teardownDelay:    30 * time.Second,
		snapshotInterval: 5 * time.Second,
		stopCh:           make(chan struct{}),
	}
}

// Shutdown closes all rooms and stops timers. Idempotent.
func (h *RelayHub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.rooms {
		r.mu.Lock()
		r.shutdownLocked()
		r.mu.Unlock()
	}
	h.rooms = map[int64]*room{}
	select {
	case <-h.stopCh:
	default:
		close(h.stopCh)
	}
}

// LiveRoomIDs returns a snapshot of investigation IDs that currently
// have a live in-memory room (including post-empty teardown grace).
// Used by the GC sweep.
func (h *RelayHub) LiveRoomIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]int64, 0, len(h.rooms))
	for id := range h.rooms {
		out = append(out, id)
	}
	return out
}

// JoinOrLoad returns the room for the case, creating it (and loading
// the persisted snapshot) on first use. Adds the client to the
// room's roster.
func (h *RelayHub) JoinOrLoad(invID int64, client *wsClient) *room {
	h.mu.Lock()
	r, ok := h.rooms[invID]
	if !ok {
		r = h.newRoomLocked(invID)
		h.rooms[invID] = r
	}
	h.mu.Unlock()

	r.mu.Lock()
	r.clients[client] = struct{}{}
	if r.teardownTimer != nil {
		r.teardownTimer.Stop()
		r.teardownTimer = nil
	}
	if r.leader == nil {
		r.leader = client
	}
	snap := append([]byte(nil), r.snapshot...)
	r.mu.Unlock()

	// On first connect with a non-empty snapshot, push a sync-step-2
	// envelope so the client renders the existing draft immediately.
	if len(snap) > 0 {
		envelope := make([]byte, 0, 2+len(snap)+8)
		envelope = append(envelope, 0, 2)
		envelope = appendVarUint(envelope, uint64(len(snap)))
		envelope = append(envelope, snap...)
		go client.sender.Send(envelope)
	}
	return r
}

func (h *RelayHub) newRoomLocked(invID int64) *room {
	r := &room{
		investigationID: invID,
		hub:             h,
		clients:         map[*wsClient]struct{}{},
	}
	if snap, err := h.store.GetInvestigationNoteDraft(invID); err == nil {
		r.snapshot = snap
	}
	r.snapshotTicker = time.NewTicker(h.snapshotInterval)
	go r.snapshotLoop()
	return r
}

// Broadcast sends bytes to every client in the room except the
// sender. Marks the room dirty.
func (r *room) Broadcast(sender *wsClient, frame []byte) {
	r.mu.Lock()
	r.dirty = true
	clients := make([]*wsClient, 0, len(r.clients))
	for c := range r.clients {
		if c == sender {
			continue
		}
		clients = append(clients, c)
	}
	r.mu.Unlock()
	for _, c := range clients {
		c.sender.Send(frame)
	}
}

// broadcastAll sends bytes to every client in the room, including
// the sender. Used by the finalize handler to deliver session-end.
func (r *room) broadcastAll(frame []byte) {
	r.mu.Lock()
	clients := make([]*wsClient, 0, len(r.clients))
	for c := range r.clients {
		clients = append(clients, c)
	}
	r.mu.Unlock()
	for _, c := range clients {
		c.sender.Send(frame)
	}
}

// SetAwareness records the latest awareness payload from a client.
func (r *room) SetAwareness(client *wsClient, payload []byte) {
	client.awarenessMu.Lock()
	client.awareness = append(client.awareness[:0], payload...)
	client.awarenessMu.Unlock()
}

// Leave removes the client from the room. Picks a new leader if the
// departing client was the leader; tears down the room after the
// configured grace period if it's now empty.
func (r *room) Leave(client *wsClient) {
	r.mu.Lock()
	delete(r.clients, client)
	if r.leader == client {
		r.leader = nil
		for c := range r.clients {
			r.leader = c
			break
		}
	}
	empty := len(r.clients) == 0
	if empty {
		r.teardownTimer = time.AfterFunc(r.hub.teardownDelay, func() {
			r.hub.removeRoom(r.investigationID)
		})
		// Force a final snapshot before the room can vanish.
		go r.requestSnapshotFromLeader()
	}
	r.mu.Unlock()
}

// requestSnapshotFromLeader sends the leader an empty sync-step-1.
// The leader replies (via the normal recv path) with a sync-step-2
// containing its full state; the recv handler calls SetSnapshot.
func (r *room) requestSnapshotFromLeader() {
	r.mu.Lock()
	leader := r.leader
	r.mu.Unlock()
	if leader == nil {
		return
	}
	leader.sender.Send(encodeSyncStep1Empty())
}

// snapshotLoop runs the per-room 5s tick. On each tick (while the
// room is dirty), it requests a fresh snapshot from the leader.
func (r *room) snapshotLoop() {
	for {
		select {
		case <-r.snapshotTicker.C:
			r.mu.Lock()
			dirty := r.dirty
			r.mu.Unlock()
			if !dirty {
				continue
			}
			r.requestSnapshotFromLeader()
		case <-r.hub.stopCh:
			return
		}
	}
}

// SetSnapshot stores fresh canonical bytes (received from the leader
// as a sync-step-2 reply). Persists to DB.
func (r *room) SetSnapshot(bytes []byte) {
	r.mu.Lock()
	r.snapshot = append(r.snapshot[:0], bytes...)
	r.dirty = false
	hub := r.hub
	r.mu.Unlock()
	_ = hub.store.UpsertInvestigationNoteDraft(r.investigationID, bytes)
}

// SnapshotBytes returns a copy of the current snapshot. Used by the
// finalize handler to extract the body string.
func (r *room) SnapshotBytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.snapshot))
	copy(out, r.snapshot)
	return out
}

// MarkFinalizing returns true if this caller acquired the right;
// false if another finalize is in flight.
func (r *room) MarkFinalizing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalizing {
		return false
	}
	r.finalizing = true
	return true
}

func (r *room) shutdownLocked() {
	if r.snapshotTicker != nil {
		r.snapshotTicker.Stop()
	}
	if r.teardownTimer != nil {
		r.teardownTimer.Stop()
	}
	for c := range r.clients {
		c.sender.Close()
	}
	r.clients = map[*wsClient]struct{}{}
}

func (h *RelayHub) removeRoom(invID int64) {
	h.mu.Lock()
	r, ok := h.rooms[invID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.rooms, invID)
	h.mu.Unlock()
	r.mu.Lock()
	r.shutdownLocked()
	r.mu.Unlock()
}

// appendVarUint writes a Yjs varint at the end of buf and returns the
// new buf.
func appendVarUint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}
