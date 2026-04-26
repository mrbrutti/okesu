package tunnel

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// Server upgrades incoming HTTPS+mTLS requests to WebSocket and registers
// the node by its cert CN. Mounts at /api/tunnel/connect on the mgmt-plane
// listener (which already requires + verifies a client cert).
type Server struct {
	Reg *Registry
}

// NewServer constructs a tunnel server backed by reg.
func NewServer(reg *Registry) *Server {
	return &Server{Reg: reg}
}

// Handler returns an http.Handler that performs the WS upgrade.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client cert required", http.StatusUnauthorized)
		return
	}
	nodeName := r.TLS.PeerCertificates[0].Subject.CommonName
	if nodeName == "" {
		http.Error(w, "client cert has no CN", http.StatusForbidden)
		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same-origin defaults; the mgmt-plane is mTLS-only so CSRF is moot.
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("tunnel: ws accept from %q: %v", nodeName, err)
		return
	}
	// Disable read deadline; we use ping/pong for liveness instead.
	wsConn.SetReadLimit(64 * 1024)

	send := func(f *Frame) error {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		b, err := Marshal(f)
		if err != nil {
			return err
		}
		return wsConn.Write(ctx, websocket.MessageText, b)
	}

	conn := s.Reg.Add(nodeName, send)
	log.Printf("tunnel: node %q connected", nodeName)
	defer func() {
		s.Reg.Remove(conn)
		_ = wsConn.Close(websocket.StatusNormalClosure, "")
		log.Printf("tunnel: node %q disconnected", nodeName)
	}()

	// Read loop. Lifetime is the lifetime of the request; the http handler
	// returns when this loop exits, which closes the underlying WS.
	for {
		typ, data, err := wsConn.Read(r.Context())
		if err != nil {
			var ce websocket.CloseError
			if !errors.As(err, &ce) {
				log.Printf("tunnel: read from %q: %v", nodeName, err)
			}
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		f, err := Unmarshal(data)
		if err != nil {
			log.Printf("tunnel: bad frame from %q: %v", nodeName, err)
			continue
		}
		switch f.Type {
		case MsgHello:
			// Optional — we already know the node from the cert. Logged for visibility.
			if f.Hello != nil {
				log.Printf("tunnel: hello from %q (version=%s)", nodeName, f.Hello.Version)
			}
		case MsgPong:
			// liveness ack — nothing to do
		default:
			conn.dispatch(f)
		}
	}
}
