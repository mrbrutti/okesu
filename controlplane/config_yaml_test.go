package controlplane

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/section9labs/okesu/controlplane/ports"
)

// fakeSecrets is a minimal ports.Secrets for unit tests.
type fakeSecrets map[string]string

func (f fakeSecrets) Get(_ context.Context, name string) ([]byte, error) {
	v, ok := f[name]
	if !ok {
		return nil, errors.New("not found: " + name)
	}
	return []byte(v), nil
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.yaml")
	if err := os.WriteFile(path, []byte(`
listen: ":7443"
db: "postgres://example"
events_store: "clickhouse"
clickhouse_addrs: ["a:9000", "b:9000"]
clickhouse_secure: true
event_ttl_days: 30
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Listen: ":8443"} // pre-existing default
	if err := LoadConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":7443" {
		t.Errorf("Listen = %q, want :7443", cfg.Listen)
	}
	if cfg.DBPath != "postgres://example" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.EventsStore != "clickhouse" {
		t.Errorf("EventsStore = %q", cfg.EventsStore)
	}
	if len(cfg.ClickHouseAddrs) != 2 || cfg.ClickHouseAddrs[0] != "a:9000" {
		t.Errorf("ClickHouseAddrs = %v", cfg.ClickHouseAddrs)
	}
	if !cfg.ClickHouseSecure {
		t.Error("ClickHouseSecure should be true")
	}
	if cfg.EventTTLDays != 30 {
		t.Errorf("EventTTLDays = %d", cfg.EventTTLDays)
	}
}

func TestLoadConfigFileMissingFieldsKeepDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.yaml")
	// Empty YAML doc — every field absent.
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Listen:        ":8443",
		AdminEmail:    "admin@local",
		EventTTLDays:  7,
	}
	before := cfg
	if err := LoadConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Errorf("empty YAML must not modify defaults\nbefore: %+v\nafter:  %+v", before, cfg)
	}
}

func TestResolveConfigSecretRefs(t *testing.T) {
	cfg := &Config{
		AdminPassword:      "${secret:cp/admin-password}",
		WebhookSecret:      "${secret:cp/webhook-secret}",
		ClickHousePassword: "static-not-a-ref",
	}
	s := fakeSecrets{
		"cp/admin-password":   "p4ssw0rd",
		"cp/webhook-secret":   "shared-1",
	}
	if err := resolveConfigSecretRefs(context.Background(), cfg, s); err != nil {
		t.Fatal(err)
	}
	if cfg.AdminPassword != "p4ssw0rd" {
		t.Errorf("AdminPassword = %q", cfg.AdminPassword)
	}
	if cfg.WebhookSecret != "shared-1" {
		t.Errorf("WebhookSecret = %q", cfg.WebhookSecret)
	}
	if cfg.ClickHousePassword != "static-not-a-ref" {
		t.Errorf("ClickHousePassword should be unchanged, got %q", cfg.ClickHousePassword)
	}
}

func TestResolveConfigSecretRefs_Missing(t *testing.T) {
	cfg := &Config{AdminPassword: "${secret:cp/missing}"}
	s := fakeSecrets{}
	if err := resolveConfigSecretRefs(context.Background(), cfg, s); err == nil {
		t.Fatal("expected error for missing secret reference")
	}
}

func TestScrubDSN(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://localhost:5432/db", "postgres://localhost:5432/db"},
		{"postgres://user@localhost/db", "postgres://user@localhost/db"},
		{"postgres://user:pass@localhost/db", "postgres://user@localhost/db"},
		{"redis://:secret@localhost:6379/0", "redis://localhost:6379/0"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := scrubDSN(tc.in); got != tc.want {
			t.Errorf("scrubDSN(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildSecrets(t *testing.T) {
	// env (default)
	if _, label, err := buildSecrets(""); err != nil || label != "env" {
		t.Errorf("empty source: label=%q err=%v", label, err)
	}
	// file://
	if _, label, err := buildSecrets("file:///tmp/secrets"); err != nil || label != "file:///tmp/secrets" {
		t.Errorf("file://: label=%q err=%v", label, err)
	}
	// oci-vault should error (not yet implemented)
	if _, _, err := buildSecrets("oci-vault://compartment.ocid"); err == nil {
		t.Error("oci-vault:// should error until Phase 8e.next")
	}
}

// Smoke check that ports.Secrets is satisfied — keeps the import warm.
var _ ports.Secrets = fakeSecrets{}
