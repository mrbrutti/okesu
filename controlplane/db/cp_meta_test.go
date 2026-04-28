package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// openTempStore opens a fresh sqlite store in a t.TempDir, applying
// migrations. Used by the cp_meta tests; tests in other files that
// want a real store can adopt the same pattern.
func openTempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open temp store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCPMeta_BootstrapsOnFirstRead(t *testing.T) {
	s := openTempStore(t)

	m, err := s.CPMeta()
	if err != nil {
		t.Fatalf("CPMeta: %v", err)
	}
	if m.InstanceID == "" {
		t.Fatal("expected fresh instance_id, got empty")
	}
	if !strings.Contains(m.InstanceID, "-") {
		t.Errorf("instance_id should look like UUID-shape: %q", m.InstanceID)
	}
	if m.Role != CPRoleStandalone {
		t.Errorf("default role = %q, want %q", m.Role, CPRoleStandalone)
	}
	if m.FederationTokenHash != "" {
		t.Errorf("default federation hash should be empty, got %q", m.FederationTokenHash)
	}

	// Second read returns the same row — InstanceID is stable across
	// reads (a parent CP relies on it).
	m2, err := s.CPMeta()
	if err != nil {
		t.Fatalf("second CPMeta: %v", err)
	}
	if m2.InstanceID != m.InstanceID {
		t.Errorf("instance_id drifted across reads: %q -> %q", m.InstanceID, m2.InstanceID)
	}
}

func TestUpdateCPMeta_OnlyWritesNonEmpty(t *testing.T) {
	s := openTempStore(t)
	orig, _ := s.CPMeta()

	updated, err := s.UpdateCPMeta("us-ashburn-1", "Primary", CPRoleParent, "")
	if err != nil {
		t.Fatalf("UpdateCPMeta: %v", err)
	}
	if updated.Region != "us-ashburn-1" {
		t.Errorf("region = %q, want us-ashburn-1", updated.Region)
	}
	if updated.DisplayName != "Primary" {
		t.Errorf("display_name = %q, want Primary", updated.DisplayName)
	}
	if updated.Role != CPRoleParent {
		t.Errorf("role = %q, want parent", updated.Role)
	}
	if updated.InstanceID != orig.InstanceID {
		t.Errorf("instance_id changed: %q -> %q", orig.InstanceID, updated.InstanceID)
	}

	// Empty strings should leave fields alone — operators set what they
	// want to change and skip the rest.
	preserved, err := s.UpdateCPMeta("", "", "", "")
	if err != nil {
		t.Fatalf("UpdateCPMeta empty: %v", err)
	}
	if preserved.Region != "us-ashburn-1" {
		t.Errorf("region wiped by empty update: %q", preserved.Region)
	}
}

func TestUpdateCPMeta_RejectsBadRole(t *testing.T) {
	s := openTempStore(t)
	if _, err := s.UpdateCPMeta("", "", "warlord", ""); err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestVerifyFederationToken_RoundTrip(t *testing.T) {
	s := openTempStore(t)
	if _, err := s.UpdateCPMeta("", "", "", "shared-secret-1"); err != nil {
		t.Fatalf("set token: %v", err)
	}
	m, _ := s.CPMeta()

	if !m.VerifyFederationToken("shared-secret-1") {
		t.Error("correct token rejected")
	}
	if m.VerifyFederationToken("wrong") {
		t.Error("wrong token accepted")
	}
	if m.VerifyFederationToken("") {
		t.Error("empty token accepted")
	}

	// Sentinel "-" clears the token hash, federation auth then disabled.
	if _, err := s.UpdateCPMeta("", "", "", "-"); err != nil {
		t.Fatalf("clear token: %v", err)
	}
	m2, _ := s.CPMeta()
	if m2.FederationTokenHash != "" {
		t.Errorf("hash should be cleared, got %q", m2.FederationTokenHash)
	}
	if m2.VerifyFederationToken("shared-secret-1") {
		t.Error("cleared hash should reject all tokens")
	}
}

func TestNewInstanceID_Shape(t *testing.T) {
	id, err := newInstanceID()
	if err != nil {
		t.Fatalf("newInstanceID: %v", err)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Errorf("expected 5 hyphen-separated parts, got %d in %q", len(parts), id)
	}
	wantLen := []int{8, 4, 4, 4, 12}
	for i, p := range parts {
		if len(p) != wantLen[i] {
			t.Errorf("part %d len = %d, want %d (%q)", i, len(p), wantLen[i], p)
		}
	}
}
