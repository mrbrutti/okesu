// WebSocket handler for war-room draft sessions. Upgrades the request,
// registers the client with the relay hub, runs read + write pumps,
// and routes inbound frames either to broadcast (most messages) or to
// snapshot capture (sync-step-2 replies to our own internal step-1).
package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

var draftAcceptOpts = &websocket.AcceptOptions{
	InsecureSkipVerify: true, // origin check handled by cookie/token auth
}

// testUserKey is a context key used in tests to inject a synthetic user
// identity without requiring a live session cookie. The key type is
// unexported so it cannot collide with any external package.
type testUserKey struct{}

// userIdentityFromContext returns the operator email for the request.
// In production it comes from the cookie session via auth.UserFromContext;
// in tests, from a test-only ctx key. Empty string = unauthenticated.
func userIdentityFromContext(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.Email
	}
	if v, ok := ctx.Value(testUserKey{}).(string); ok {
		return v
	}
	return ""
}

// frameSenderForWS adapts a *websocket.Conn to the relay's
// frameSender interface. Each adapter has its own write pump
// goroutine to serialize writes to the same conn.
type frameSenderForWS struct {
	conn    *websocket.Conn
	sendCh  chan []byte
	once    sync.Once
	closed  chan struct{}
	dropped atomic.Int64
}

func newFrameSenderForWS(conn *websocket.Conn) *frameSenderForWS {
	s := &frameSenderForWS{
		conn:   conn,
		sendCh: make(chan []byte, 64),
		closed: make(chan struct{}),
	}
	go s.writePump()
	return s
}

func (s *frameSenderForWS) Send(b []byte) {
	select {
	case s.sendCh <- b:
	case <-s.closed:
	default:
		// Backpressure: drop the frame if the channel is full. Yjs
		// will recover via its next sync round-trip.
		s.dropped.Add(1)
	}
}

// Close is idempotent: it closes the done channel exactly once via
// sync.Once, causing the write pump to exit.
func (s *frameSenderForWS) Close() {
	s.once.Do(func() {
		close(s.closed)
		if d := s.dropped.Load(); d > 0 {
			log.Printf("draft-ws: dropped %d frame(s) due to backpressure", d)
		}
	})
}

func (s *frameSenderForWS) writePump() {
	ctx := context.Background()
	defer s.conn.CloseNow()
	for {
		select {
		case b := <-s.sendCh:
			if err := s.conn.Write(ctx, websocket.MessageBinary, b); err != nil {
				return
			}
		case <-s.closed:
			return
		}
	}
}

// GetInvestigationDraftWSHandler is the local-side WS upgrader.
func GetInvestigationDraftWSHandler(store *db.Store, hub *RelayHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.GetInvestigation(invID); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		email := userIdentityFromContext(r.Context())
		if email == "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, draftAcceptOpts)
		if err != nil {
			// Accept already wrote an error response to w.
			return
		}
		conn.SetReadLimit(4 << 20) // 4 MiB; war-room Yjs snapshots can comfortably grow large

		sender := newFrameSenderForWS(conn)
		client := NewWsClient(email, sender)
		room := hub.JoinOrLoad(invID, client)
		defer room.Leave(client)
		defer sender.Close()

		runReadPump(r.Context(), conn, client, room)
	}
}

// runReadPump reads frames from the WebSocket and routes them. Returns
// when the connection closes or errors.
func runReadPump(ctx context.Context, conn *websocket.Conn, client *wsClient, room *room) {
	for {
		_, frame, err := conn.Read(ctx)
		if err != nil {
			return
		}
		mt, ok := peekMessageType(frame)
		if !ok {
			continue
		}
		switch mt {
		case 0: // sync
			subType, ok := peekSyncSubType(frame)
			if ok && subType == 2 {
				// sync-step-2 reply: relay this as the canonical
				// snapshot AND broadcast to other clients.
				if payload := extractSyncStep2Payload(frame); payload != nil {
					room.SetSnapshot(payload)
				}
			}
			room.Broadcast(client, frame)
		case 1: // awareness
			room.SetAwareness(client, frame)
			room.Broadcast(client, frame)
		case 2: // session-end (clients should never send; ignore)
			continue
		}
	}
}

// extractSyncStep2Payload reads the length-prefixed update bytes from
// a sync-step-2 frame [0, 2, lenVarint, ...payload].
func extractSyncStep2Payload(frame []byte) []byte {
	if len(frame) < 2 {
		return nil
	}
	rest := frame[2:]
	// Read varint length
	var v uint64
	var shift uint
	i := 0
	for ; i < len(rest) && i < 10; i++ {
		b := rest[i]
		v |= uint64(b&0x7F) << shift
		if b < 0x80 {
			i++
			break
		}
		shift += 7
	}
	if uint64(i)+v > uint64(len(rest)) {
		return nil
	}
	return rest[i : uint64(i)+v]
}

// FederatedInvestigationDraftWS — parent-side wrapper. Proxies via
// the WS proxy when ?cp is set; falls through to the local handler
// otherwise.
func FederatedInvestigationDraftWS(store *db.Store, hub *RelayHub, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cpID := r.URL.Query().Get("cp")
		if cpID != "" {
			proxyDraftWS(w, r, agg, cpID)
			return
		}
		GetInvestigationDraftWSHandler(store, hub).ServeHTTP(w, r)
	}
}

// proxyDraftWS upgrades the parent-side connection, dials the child
// CP's federation WebSocket endpoint with the federation token, and
// pumps bytes both directions until either side closes.
func proxyDraftWS(w http.ResponseWriter, r *http.Request, agg *federation.Aggregator, cpID string) {
	// Find the target child peer in the federation aggregator.
	peers, _ := agg.HealthyPeers()
	var target *federation.Peer
	for i := range peers {
		if peers[i].Snapshot.InstanceID == cpID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "target CP not found or unhealthy: "+cpID, http.StatusNotFound)
		return
	}

	// Build the child URL: replace /api/investigations/ with the
	// federation path, and switch http(s):// to ws(s)://.
	childPath := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
	rawURL := target.Row.URL
	rawURL = strings.Replace(rawURL, "https://", "wss://", 1)
	rawURL = strings.Replace(rawURL, "http://", "ws://", 1)
	childURL := strings.TrimRight(rawURL, "/") + childPath

	// Dial the child with the federation token + operator email in
	// the upgrade headers. Honour the request's context so a parent
	// disconnect cancels the dial.
	dialCtx, cancelDial := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancelDial()

	childConn, _, err := websocket.Dial(dialCtx, childURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"X-Okesu-Federation-Token":    {target.Row.Token},
			"X-Okesu-Federation-Operator": {operatorEmailFromContext(r.Context())},
		},
	})
	if err != nil {
		http.Error(w, "cp unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	childConn.SetReadLimit(4 << 20)

	// Upgrade the parent-side connection.
	parentConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		childConn.Close(websocket.StatusAbnormalClosure, "parent upgrade failed")
		return
	}
	parentConn.SetReadLimit(4 << 20)

	// Two goroutines pump bytes bidirectionally. First close ends both.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		pumpDraftBytes(ctx, parentConn, childConn, "parent→child")
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		pumpDraftBytes(ctx, childConn, parentConn, "child→parent")
	}()
	wg.Wait()

	// Best-effort close on both sides. Idempotent.
	parentConn.Close(websocket.StatusNormalClosure, "")
	childConn.Close(websocket.StatusNormalClosure, "")
}

// pumpDraftBytes copies binary frames from src to dst until either
// the context cancels or a read/write errors. Logs the direction on
// the first error for federation diagnostics.
func pumpDraftBytes(ctx context.Context, src, dst *websocket.Conn, direction string) {
	for {
		if ctx.Err() != nil {
			return
		}
		mt, frame, err := src.Read(ctx)
		if err != nil {
			return
		}
		if mt != websocket.MessageBinary {
			// War-room protocol is binary-only. Silently drop other
			// message types — defence against a misconfigured peer.
			continue
		}
		if err := dst.Write(ctx, websocket.MessageBinary, frame); err != nil {
			log.Printf("draft-ws proxy: %s write failed: %v", direction, err)
			return
		}
	}
}

// operatorEmailFromContext extracts the operator's email from the
// request context for forwarding to the child as a header. Empty
// string when unauthenticated (acceptable — the child will fall
// back to "federated-operator").
func operatorEmailFromContext(ctx context.Context) string {
	if u := auth.UserFromContext(ctx); u != nil {
		return u.Email
	}
	return ""
}

// FederationInvestigationDraftWS — child-side, token-authed.
// The parent-side proxy lives in Task 5 (proxyDraftWS).
func FederationInvestigationDraftWS(store *db.Store, hub *RelayHub) http.HandlerFunc {
	return requireFederationToken(store, federationDraftWSHandler(store, hub))
}

// federationDraftWSHandler is the child-side handler that takes the
// operator email from the parent's X-Okesu-Federation-Operator
// header instead of cookie session.
func federationDraftWSHandler(store *db.Store, hub *RelayHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.GetInvestigation(invID); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		email := r.Header.Get("X-Okesu-Federation-Operator")
		if email == "" {
			email = "federated-operator"
		}

		conn, err := websocket.Accept(w, r, draftAcceptOpts)
		if err != nil {
			return
		}
		conn.SetReadLimit(4 << 20) // 4 MiB; war-room Yjs snapshots can comfortably grow large

		sender := newFrameSenderForWS(conn)
		client := NewWsClient(email, sender)
		room := hub.JoinOrLoad(invID, client)
		defer room.Leave(client)
		defer sender.Close()

		// requireFederationToken populates auth.UserFromContext with a
		// synthetic "federation@parent" user. We deliberately ignore that
		// here and read the actual operator's email from the parent's
		// X-Okesu-Federation-Operator header so the room's awareness chips
		// show the real operator, not the proxy CP.
		runReadPump(r.Context(), conn, client, room)
	}
}
