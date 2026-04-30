package federation

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

// fakeParent stands up an httptest server that imitates a parent CP
// exposing BOTH /api/v1/cp/introspect AND /api/v1/federation/fleet-env.
// The fleet-env response can be flipped per-test via setFleetEnv.
type fakeParent struct {
	*httptest.Server
	expectToken string
	instanceID  string

	mu       *sync.Mutex
	feStatus int
	feBody   string
}

func newFakeParent(t *testing.T, token, instanceID string) *fakeParent {
	t.Helper()
	c := &fakeParent{
		expectToken: token,
		instanceID:  instanceID,
		mu:          &sync.Mutex{},
		feStatus:    http.StatusOK,
		feBody:      `{"anthropic_api_key":"sk-ant-parent","openai_api_key":"sk-openai-parent","version":3}`,
	}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Okesu-Federation-Token")
		if got != c.expectToken {
			http.Error(w, "federation token invalid or not configured", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/cp/introspect"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"instance_id":"` + c.instanceID + `","region":"us-east","display_name":"Parent","role":"standalone","version":"test","go_version":"go1.0","os":"linux","arch":"amd64","features":{},"counts":{}}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/federation/fleet-env"):
			c.mu.Lock()
			status := c.feStatus
			body := c.feBody
			c.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	return c
}

func (c *fakeParent) setFleetEnv(status int, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.feStatus = status
	c.feBody = body
}

// seedMasterKey writes a 32-byte zero master key into cp_meta so
// MasterKeyFromMeta succeeds. Mirrors api/buckets_test.go's helper.
func seedMasterKey(t *testing.T, store *db.Store) {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := store.MetaSet("session_hmac_key", key); err != nil {
		t.Fatalf("seed session_hmac_key: %v", err)
	}
}

func TestPoller_PollOnce_AppliesFleetEnv(t *testing.T) {
	parent := newFakeParent(t, "shared-1", "cp-parent-1")
	defer parent.Close()
	store := openTempStore(t)
	seedMasterKey(t, store)
	// Default source is 'local'; flip so the federation update is
	// allowed. Operators do this from the LLM Keys settings page when
	// a child CP is bootstrapped to take its keys from a parent.
	if err := store.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("set source federated: %v", err)
	}
	peer, err := store.AddFederationPeer(parent.URL, "Parent", "shared-1")
	if err != nil {
		t.Fatalf("add peer: %v", err)
	}
	p := NewPoller(store, nil)
	// pollOne runs the full path: introspect + fleet-env mirror.
	p.pollOne(context.Background(), peer)

	fe, err := store.GetFleetEnv()
	if err != nil {
		t.Fatalf("GetFleetEnv: %v", err)
	}
	if fe.Source != "federated_from_parent" {
		t.Errorf("source = %q, want federated_from_parent", fe.Source)
	}
	if fe.Version != 3 {
		t.Errorf("version = %d, want 3", fe.Version)
	}
	if !fe.ParentCPID.Valid || fe.ParentCPID.String != "cp-parent-1" {
		t.Errorf("parent_cp_id = %+v, want cp-parent-1", fe.ParentCPID)
	}
	if !fe.HasAnthropic || !fe.HasOpenAI {
		t.Errorf("expected both keys present, got anthropic=%v openai=%v", fe.HasAnthropic, fe.HasOpenAI)
	}
}

func TestPoller_PollOnce_FleetEnvIdempotent(t *testing.T) {
	parent := newFakeParent(t, "shared-1", "cp-parent-1")
	defer parent.Close()
	store := openTempStore(t)
	seedMasterKey(t, store)
	if err := store.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("set source federated: %v", err)
	}
	peer, _ := store.AddFederationPeer(parent.URL, "Parent", "shared-1")
	p := NewPoller(store, nil)

	// First poll lands version=3.
	p.pollOne(context.Background(), peer)
	first, err := store.GetFleetEnv()
	if err != nil {
		t.Fatalf("GetFleetEnv: %v", err)
	}
	if first.Version != 3 {
		t.Fatalf("first version = %d, want 3", first.Version)
	}
	firstUpdatedAt := first.UpdatedAt

	// Second poll: same parent version; SetFleetEnvFromFederation
	// must report applied=false and the row stays put.
	p.pollOne(context.Background(), peer)
	second, _ := store.GetFleetEnv()
	if second.Version != 3 {
		t.Errorf("second poll mutated version: got %d, want 3", second.Version)
	}
	if !second.UpdatedAt.Equal(firstUpdatedAt) {
		t.Errorf("second poll touched updated_at: %v -> %v", firstUpdatedAt, second.UpdatedAt)
	}
}

func TestPoller_PollOnce_FleetEnvSkipsWhenLocal(t *testing.T) {
	parent := newFakeParent(t, "shared-1", "cp-parent-1")
	defer parent.Close()
	store := openTempStore(t)
	seedMasterKey(t, store)
	peer, _ := store.AddFederationPeer(parent.URL, "Parent", "shared-1")

	// Operator has set local override. Federation must not clobber it.
	mk, err := store.MasterKeyFromMeta()
	if err != nil {
		t.Fatalf("master key: %v", err)
	}
	localKey := "sk-ant-local"
	if _, err := store.UpsertFleetEnv(mk, db.FleetEnvUpdate{
		AnthropicAPIKey:    &localKey,
		UpdatedByUserEmail: "admin@example.com",
	}); err != nil {
		t.Fatalf("upsert local: %v", err)
	}
	preLocal, _ := store.GetFleetEnv()
	if preLocal.Source != "local" {
		t.Fatalf("setup: source = %q, want local", preLocal.Source)
	}
	preVersion := preLocal.Version

	p := NewPoller(store, nil)
	p.pollOne(context.Background(), peer)

	got, _ := store.GetFleetEnv()
	if got.Source != "local" {
		t.Errorf("source flipped: got %q, want local", got.Source)
	}
	if got.Version != preVersion {
		t.Errorf("version mutated: got %d, want %d", got.Version, preVersion)
	}
}

func TestPoller_PollOnce_FleetEnvNoApplyOnEmptyUpstream(t *testing.T) {
	parent := newFakeParent(t, "shared-1", "cp-parent-1")
	parent.setFleetEnv(http.StatusOK, `{"anthropic_api_key":"","openai_api_key":"","version":0}`)
	defer parent.Close()
	store := openTempStore(t)
	seedMasterKey(t, store)
	if err := store.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("set source federated: %v", err)
	}
	peer, _ := store.AddFederationPeer(parent.URL, "Parent", "shared-1")

	p := NewPoller(store, nil)
	p.pollOne(context.Background(), peer)

	fe, _ := store.GetFleetEnv()
	if fe.Version != 0 {
		t.Errorf("empty upstream bumped version: %d, want 0", fe.Version)
	}
	if fe.HasAnthropic || fe.HasOpenAI {
		t.Errorf("empty upstream populated keys; HasAnthropic=%v HasOpenAI=%v", fe.HasAnthropic, fe.HasOpenAI)
	}
}

func TestPoller_PollOnce_FleetEnv404Tolerated(t *testing.T) {
	parent := newFakeParent(t, "shared-1", "cp-parent-1")
	parent.setFleetEnv(http.StatusNotFound, "not found")
	defer parent.Close()
	store := openTempStore(t)
	seedMasterKey(t, store)
	if err := store.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("set source federated: %v", err)
	}
	peer, _ := store.AddFederationPeer(parent.URL, "Parent", "shared-1")

	p := NewPoller(store, nil)
	// pollOne must not panic / error / mark peer unhealthy on 404.
	p.pollOne(context.Background(), peer)

	got, err := store.FederationPeer(peer.ID)
	if err != nil {
		t.Fatalf("get peer: %v", err)
	}
	if !got.LastSeenAt.Valid {
		t.Error("introspect should still mark peer healthy even on fleet-env 404")
	}
	if got.LastError != "" {
		t.Errorf("fleet-env 404 must not set last_error, got %q", got.LastError)
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
