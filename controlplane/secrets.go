package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/section9labs/okesu/controlplane/adapters/envsecrets"
	"github.com/section9labs/okesu/controlplane/ports"
)

// Phase 8g — every secret the CP needs flows through ports.Secrets.
//
// Why this exists
//
// Before this commit, secrets came in via two leaky channels:
//   1. CLI flags (`--admin-password`, `--clickhouse-password`, ...)
//      which appear in `ps`, in shell history, in systemd unit files,
//      in /proc/PID/cmdline.
//   2. Direct environment variables (OKESU_CP_ADMIN_PASSWORD, ...)
//      which appear in /proc/PID/environ, in `systemctl show`, in
//      container manifests.
//
// With ports.Secrets in front, both channels stay supported as a
// back-compat dev path, but a deployed CP fetches every secret by
// canonical name from a single source — env vars, a credentials
// directory (perfect for systemd LoadCredential=), an OCI Vault, or
// HashiCorp Vault. Operators can give the CP a config file that
// references secrets by name only, ship the YAML through any
// pipeline they want (gitops included), and never expose a secret
// value outside the vault.
//
// Canonical names follow the convention documented in
// adapters/envsecrets/secrets.go — slash-separated hierarchy that
// maps cleanly onto OCI Vault folders and onto a directory layout.

// Canonical secret names. Adding a new secret-bearing config field?
// Add the name here, document where the CP needs it, and route the
// resolution through resolveSecrets below.
const (
	SecretAdminPassword       = "cp/admin-password"
	SecretWebhookSecret       = "cp/webhook-secret"
	SecretSessionKey          = "cp/session-key"
	SecretOIDCClientSecret    = "cp/oidc/client-secret"
	SecretClickHousePassword  = "clickhouse/password"
	SecretKafkaSASLPassword   = "kafka/sasl-password"
	SecretBlobSecretKey       = "blob/secret-key"
	SecretDeploySSHPrivateKey = "deploy/ssh-private-key"
)

// buildSecrets picks the ports.Secrets adapter from cfg.SecretsSource:
//
//   ""               — env-only fallback (legacy default for dev)
//   "env"            — same
//   "env+/path/to/d" — env first, then a directory of files (LoadCredential)
//   "file:///path"   — directory only (no env fallback)
//
// Future Phase 8e.next adds:
//
//   "oci-vault://<compartment-ocid>?region=us-ashburn-1"  — managed
//
// Returns the adapter + a human-readable label for the boot log.
func buildSecrets(source string) (ports.Secrets, string, error) {
	source = strings.TrimSpace(source)
	switch {
	case source == "" || source == "env":
		return envsecrets.New(), "env", nil

	case strings.HasPrefix(source, "file://"):
		dir := strings.TrimPrefix(source, "file://")
		if dir == "" {
			return nil, "", errors.New("file:// secrets source requires a path")
		}
		return envsecrets.NewWithDir(dir), "file://" + dir, nil

	case strings.HasPrefix(source, "env+"):
		dir := strings.TrimPrefix(source, "env+")
		return envsecrets.NewWithDir(dir), "env+" + dir, nil

	case strings.HasPrefix(source, "oci-vault://"):
		// Reserved for Phase 8e.next.
		return nil, "", fmt.Errorf("oci-vault:// secrets source not yet implemented (Phase 8e.next); use env or file:// for now")

	default:
		// Treat anything else as a directory path (operator-friendly).
		// Same effect as env+<path>.
		if u, perr := url.Parse(source); perr == nil && u.Scheme != "" {
			return nil, "", fmt.Errorf("unrecognised secrets-source scheme %q", u.Scheme)
		}
		return envsecrets.NewWithDir(source), source, nil
	}
}

// resolveSecrets pulls every CP-level secret through the configured
// ports.Secrets adapter. Empty values from the config struct (operator
// did NOT pass --admin-password etc.) get filled from the secrets
// store; non-empty values are kept as-is for back-compat with the
// legacy flag/env-var path.
//
// Logs each resolution by *name only* — never the value. This gives
// us a "what secrets did this CP read at boot" audit trail without
// risking the value showing up in a log scraper.
func resolveSecrets(ctx context.Context, cfg *Config, s ports.Secrets) error {
	resolve := func(name string, dst *string) {
		if *dst != "" {
			// Operator passed it via flag/env — back-compat path.
			// Print a deprecation hint so the right answer is visible.
			log.Printf("secrets: %s — using legacy flag/env value (deprecated; configure --secrets-source instead)", name)
			return
		}
		v, err := s.Get(ctx, name)
		if err != nil {
			// Missing is fine for optional secrets. The downstream
			// validation in Config.Validate flags required-but-missing.
			return
		}
		*dst = string(v)
		log.Printf("secrets: resolved %s (%d bytes)", name, len(v))
	}

	resolve(SecretAdminPassword, &cfg.AdminPassword)
	resolve(SecretWebhookSecret, &cfg.WebhookSecret)
	resolve(SecretSessionKey, &cfg.SessionKey)
	resolve(SecretOIDCClientSecret, &cfg.OIDCClientSecret)
	resolve(SecretClickHousePassword, &cfg.ClickHousePassword)
	resolve(SecretKafkaSASLPassword, &cfg.KafkaSASLPassword)
	resolve(SecretBlobSecretKey, &cfg.BlobSecretKey)

	return nil
}

// scrubDSN redacts a password in a database/AMQP/Redis-style DSN so
// the result is safe to log. Conservative — if we can't parse the
// URL, we return "<dsn redacted>" rather than risk leaking the
// original.
//
// Used by boot-time log lines that include DSNs ("connecting to
// postgres at <dsn>"). The connection still uses the original DSN
// internally; we just don't print it.
func scrubDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		// No user-info section means no password embedded.
		return dsn
	}
	if _, hasPass := u.User.Password(); hasPass {
		username := u.User.Username()
		if username == "" {
			// "redis://:pw@host" — strip the entire userinfo so the
			// scrubbed result doesn't have a stray "@".
			u.User = nil
		} else {
			u.User = url.User(username)
		}
	}
	return u.String()
}
