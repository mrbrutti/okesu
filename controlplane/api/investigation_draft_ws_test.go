package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/section9labs/okesu/controlplane/db"
)

func TestDraftWS_Unauthenticated401(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/draft/ws", GetInvestigationDraftWSHandler(store, hub))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	id := mustCreateInvestigationForRelayTest(t, store, "case")
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/investigations/" + strconv.FormatInt(id, 10) + "/draft/ws"
	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Fatalf("expected error from unauthenticated dial")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %v, want 401", resp)
	}
}

func TestDraftWS_Unknown404(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	auth := authStubMiddleware()
	router := chi.NewRouter()
	router.Use(auth)
	router.Get("/api/investigations/{id}/draft/ws", GetInvestigationDraftWSHandler(store, hub))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/investigations/99999/draft/ws?u=alice@x"
	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Fatalf("expected error from unknown-id dial")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %v, want 404", resp)
	}
}

func TestDraftWS_TwoClientsRelay(t *testing.T) {
	store := newSeededTestStore(t)
	hub := NewRelayHub(store)
	t.Cleanup(hub.Shutdown)

	auth := authStubMiddleware()
	router := chi.NewRouter()
	router.Use(auth)
	router.Get("/api/investigations/{id}/draft/ws", GetInvestigationDraftWSHandler(store, hub))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	id := mustCreateInvestigationForRelayTest(t, store, "case")
	dial := func(email string) *websocket.Conn {
		u := strings.Replace(srv.URL, "http://", "ws://", 1) +
			"/api/investigations/" + strconv.FormatInt(id, 10) + "/draft/ws?u=" + email
		c, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{
			HTTPClient: srv.Client(),
		})
		if err != nil {
			t.Fatalf("dial %s: %v", email, err)
		}
		return c
	}

	a := dial("alice@x")
	defer a.CloseNow()
	b := dial("bob@x")
	defer b.CloseNow()

	// Give the second join a moment to register.
	time.Sleep(20 * time.Millisecond)

	// alice sends a sync-update frame; bob should receive it.
	frame := []byte{0, 1, 0xab, 0xcd}
	ctx := context.Background()
	if err := a.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("alice write: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	mt, got, err := b.Read(readCtx)
	if err != nil {
		t.Fatalf("bob read: %v", err)
	}
	if mt != websocket.MessageBinary {
		t.Errorf("bob received non-binary message type: %v", mt)
	}
	if string(got) != string(frame) {
		t.Errorf("bob received %v, want %v", got, frame)
	}
}

// authStubMiddleware injects a test user from a `?u=email` query
// param. Mirrors the production cookie-session middleware contract:
// after the middleware runs, userIdentityFromContext reads the email
// out of the request context.
func authStubMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			email := r.URL.Query().Get("u")
			if email != "" {
				r = r.WithContext(withTestUser(r.Context(), email))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func withTestUser(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, testUserKey{}, email)
}

type testUserKey struct{}

// Compile-time silencer for unused imports if a package alias slips.
var _ = db.InvestigationInsert{}
