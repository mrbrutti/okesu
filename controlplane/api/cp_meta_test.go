package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cp.db")
	s, err := db.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newIntrospectHandler(t *testing.T, s *db.Store) http.HandlerFunc {
	t.Helper()
	return CPIntrospect(CPIntrospectDepsValue{
		Store:           s,
		Version:         "test-1.0",
		DaemonVersionFn: func() string { return "test-daemon-1.0" },
		Features:        AboutFeatures{MgmtPlane: true, WebhookIngest: true},
		WebhookURL:      "https://cp.example/api/webhooks/events",
		MgmtURL:         "https://cp.example:8444",
	})
}

func TestCPIntrospect_RejectsWithoutToken(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpdateCPMeta("us-ashburn-1", "Primary", "", "shared-secret-1"); err != nil {
		t.Fatalf("set token: %v", err)
	}
	h := newIntrospectHandler(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cp/introspect", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no-token request: got %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate challenge on 401")
	}
}

func TestCPIntrospect_RejectsWrongToken(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpdateCPMeta("", "", "", "shared-secret-1"); err != nil {
		t.Fatalf("set token: %v", err)
	}
	h := newIntrospectHandler(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cp/introspect", nil)
	req.Header.Set("X-Okesu-Federation-Token", "guess")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong-token request: got %d, want 401", rec.Code)
	}
}

func TestCPIntrospect_RejectsWhenFederationDisabled(t *testing.T) {
	// Fresh store, no token configured. Even a request with some header
	// value should 401 — the empty stored hash means federation is off.
	s := newTestStore(t)
	h := newIntrospectHandler(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cp/introspect", nil)
	req.Header.Set("X-Okesu-Federation-Token", "anything")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("federation-disabled: got %d, want 401", rec.Code)
	}
}

func TestCPIntrospect_AcceptsCorrectToken(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpdateCPMeta("us-ashburn-1", "Primary CP", db.CPRoleStandalone, "shared-secret-1"); err != nil {
		t.Fatalf("set token: %v", err)
	}
	h := newIntrospectHandler(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cp/introspect", nil)
	req.Header.Set("X-Okesu-Federation-Token", "shared-secret-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp IntrospectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.InstanceID == "" {
		t.Error("response missing instance_id")
	}
	if resp.Region != "us-ashburn-1" {
		t.Errorf("region = %q, want us-ashburn-1", resp.Region)
	}
	if resp.DisplayName != "Primary CP" {
		t.Errorf("display_name = %q, want Primary CP", resp.DisplayName)
	}
	if resp.Role != db.CPRoleStandalone {
		t.Errorf("role = %q, want standalone", resp.Role)
	}
	if resp.Version != "test-1.0" {
		t.Errorf("version = %q, want test-1.0", resp.Version)
	}
	if resp.WebhookPublicURL != "https://cp.example/api/webhooks/events" {
		t.Errorf("webhook_public_url = %q", resp.WebhookPublicURL)
	}
	if !resp.Features.MgmtPlane {
		t.Error("features.mgmt_plane should be true")
	}
	// Counts default to zero on a fresh store — no agents/nodes.
	if resp.Counts.Daimons != 0 || resp.Counts.Nodes != 0 || resp.Counts.OpenFindings != 0 {
		t.Errorf("expected zero counts, got %+v", resp.Counts)
	}
}
