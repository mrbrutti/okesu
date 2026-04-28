package federation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// fakeChild stands up an httptest server that imitates a child CP's
// /api/v1/cp/introspect endpoint. Returns the server (caller closes
// it) and a function to flip the auth/error mode for individual tests.
type fakeChild struct {
	*httptest.Server
	expectToken string
	respond     func() (status int, body string)
}

func newFakeChild(t *testing.T, token string) *fakeChild {
	t.Helper()
	c := &fakeChild{expectToken: token}
	c.respond = func() (int, string) {
		return http.StatusOK, `{"instance_id":"abcd-1234","region":"us-east","display_name":"East","role":"standalone","version":"test","go_version":"go1.0","os":"linux","arch":"amd64","features":{},"counts":{"daimons":7,"daimons_healthy":7,"nodes":3,"open_findings":0}}`
	}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/cp/introspect") {
			http.NotFound(w, r)
			return
		}
		got := r.Header.Get("X-Okesu-Federation-Token")
		if got != c.expectToken {
			http.Error(w, "federation token invalid or not configured", http.StatusUnauthorized)
			return
		}
		status, body := c.respond()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return c
}

func openTempStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cp.db")
	s, err := db.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPoller_PollOnce_Success(t *testing.T) {
	child := newFakeChild(t, "shared-1")
	defer child.Close()
	store := openTempStore(t)
	peer, err := store.AddFederationPeer(child.URL, "East", "shared-1")
	if err != nil {
		t.Fatalf("add peer: %v", err)
	}
	p := NewPoller(store, nil)
	body, err := p.PollOnce(context.Background(), peer, false)
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if !strings.Contains(body, "abcd-1234") {
		t.Errorf("body missing instance_id: %s", body)
	}
	got, _ := store.FederationPeer(peer.ID)
	if !got.LastSeenAt.Valid {
		t.Error("last_seen_at not recorded")
	}
	if got.LastError != "" {
		t.Errorf("last_error should be cleared on success, got %q", got.LastError)
	}
}

func TestPoller_PollOnce_WrongToken(t *testing.T) {
	child := newFakeChild(t, "real-secret")
	defer child.Close()
	store := openTempStore(t)
	peer, _ := store.AddFederationPeer(child.URL, "", "wrong-token")
	p := NewPoller(store, nil)
	_, err := p.PollOnce(context.Background(), peer, false)
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got %v", err)
	}
	got, _ := store.FederationPeer(peer.ID)
	if got.LastError == "" {
		t.Error("last_error should be set on failure")
	}
	if got.LastSeenAt.Valid {
		t.Error("last_seen_at should NOT update on failure")
	}
}

func TestPoller_PollOnce_VerifyOnlyDoesNotPersist(t *testing.T) {
	child := newFakeChild(t, "shared-1")
	defer child.Close()
	store := openTempStore(t)
	peer := &db.FederationPeer{URL: child.URL, Token: "shared-1"}

	p := NewPoller(store, nil)
	_, err := p.PollOnce(context.Background(), peer, true)
	if err != nil {
		t.Fatalf("verify probe: %v", err)
	}
	// VerifyOnly=true means no row was created.
	peers, _ := store.ListFederationPeers()
	if len(peers) != 0 {
		t.Errorf("verify-only should not persist, got %d rows", len(peers))
	}
}

func TestPoller_RejectsNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><html><body>Hello</body></html>"))
	}))
	defer srv.Close()
	store := openTempStore(t)
	peer := &db.FederationPeer{URL: srv.URL, Token: "x"}
	p := NewPoller(store, nil)
	_, err := p.PollOnce(context.Background(), peer, true)
	if err == nil {
		t.Fatal("expected error on non-JSON response")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error should mention JSON, got %v", err)
	}
}

func TestPoller_OnUpdateFires(t *testing.T) {
	child := newFakeChild(t, "shared-1")
	defer child.Close()
	store := openTempStore(t)
	peer, _ := store.AddFederationPeer(child.URL, "", "shared-1")

	called := make(chan int64, 1)
	p := NewPoller(store, func(id int64) { called <- id })
	if _, err := p.PollOnce(context.Background(), peer, false); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	select {
	case id := <-called:
		if id != peer.ID {
			t.Errorf("onUpdate id = %d, want %d", id, peer.ID)
		}
	case <-time.After(time.Second):
		t.Error("onUpdate never fired")
	}
}
