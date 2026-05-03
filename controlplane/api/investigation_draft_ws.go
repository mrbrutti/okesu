// WebSocket handler for war-room draft sessions. Upgrades the request,
// registers the client with the relay hub, runs read + write pumps,
// and routes inbound frames either to broadcast (most messages) or to
// snapshot capture (sync-step-2 replies to our own internal step-1).
package api

import (
	"context"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

var draftAcceptOpts = &websocket.AcceptOptions{
	InsecureSkipVerify: true, // origin check handled by cookie/token auth
}

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
