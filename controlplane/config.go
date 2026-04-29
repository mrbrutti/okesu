// Package controlplane implements the Okesu Control Plane: a web server that
// receives webhook events from daemon agents, manages a fleet of agents and
// nodes, and presents an operations dashboard.
package controlplane

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds the runtime configuration for the Control Plane server.
// Values are populated from CLI flags with environment variable fallbacks.
type Config struct {
	// Listen is the HTTPS bind address (e.g. ":8443").
	Listen string

	// DBPath is the database DSN. Either a SQLite file path (legacy
	// default — created on first run if absent) or a Postgres DSN
	// (postgres://...). The store auto-detects from the prefix.
	DBPath string

	// CertFile / KeyFile are the TLS server cert and key. If both are empty,
	// the server generates a self-signed cert at startup and writes it next
	// to the DB so subsequent runs use the same cert.
	CertFile string
	KeyFile  string

	// WebhookSecret is the HMAC-SHA256 secret shared with daemon agents.
	// Webhook requests with a different signature are rejected with 401.
	// Empty disables the webhook endpoint entirely (Phase 1 always requires it).
	WebhookSecret string

	// AdminEmail is the email seeded for the default admin on first run.
	// Defaults to "admin@local".
	AdminEmail string

	// AdminPassword is the bcrypt-hashed at first run. Required on first run;
	// ignored on subsequent runs (admin already exists).
	AdminPassword string

	// SessionKey is the HMAC key used to sign session cookies. Auto-generated
	// and persisted alongside the DB if empty.
	SessionKey string

	// ── Management plane (Phase 2) ──────────────────────────────────────────

	// MgmtListen is the bind address for the mTLS-protected management plane
	// (agent register / heartbeat / config endpoints). Empty disables the
	// mgmt plane entirely.
	MgmtListen string

	// CACertFile / CAKeyFile back the mTLS CA used to verify agent client
	// certs and sign new ones. Auto-generated next to the DB if both empty.
	CACertFile string
	CAKeyFile  string

	// MgmtCertFile / MgmtKeyFile are the server cert presented to agents on
	// the mgmt-plane port. Must be signed by the CA above. Auto-generated
	// next to the DB if both empty.
	MgmtCertFile string
	MgmtKeyFile  string

	// ── OIDC / SSO (Phase 4) ────────────────────────────────────────────────

	// OIDCIssuer is the OpenID Connect provider URL. When set, OIDC sign-in
	// is enabled. For Oracle Identity Domains this is the domain root, e.g.
	// "https://idcs-abcdef0123.identity.oraclecloud.com:443".
	OIDCIssuer string

	// OIDCClientID / OIDCClientSecret identify the CP to the IDP.
	OIDCClientID     string
	OIDCClientSecret string

	// OIDCRedirectURL is the absolute callback URL the IDP will redirect to
	// after authentication. Typically <CP-public-URL>/auth/oidc/callback.
	OIDCRedirectURL string

	// OIDCGroupsClaim is the ID token claim holding the user's groups.
	// Default "groups". Oracle Identity Domains may use a different claim
	// path depending on tenant config.
	OIDCGroupsClaim string

	// OIDCRoleMap maps IDP group names to local roles, comma-separated:
	//   "okesu-admins:admin,okesu-operators:operator,okesu-viewers:viewer"
	// Users not matching any group default to "viewer".
	OIDCRoleMap string

	// OIDCLabel is the UI text on the SSO button. Defaults to "Sign in with Oracle".
	OIDCLabel string

	// ── Node deploy (Phase 5) ───────────────────────────────────────────────

	// DaemonBinaryPath is the FALLBACK single-arch daemon binary path.
	// Used when DaemonBinariesDir is empty. Maintained for compat with
	// pre-Phase-8 setups; new installs should use DaemonBinariesDir.
	DaemonBinaryPath string

	// DaemonBinariesDir is the directory holding multi-arch daemon binaries
	// uploaded via the Settings → Deploy UI. The CP picks the binary
	// matching the target host's os+arch at deploy time. Files inside are
	// also tracked in the daemon_binaries DB table.
	DaemonBinariesDir string

	// DaimonFilesDir is the directory on the CP host that holds long-form
	// *.md daimon definitions (with full frontmatter — schedule, mgmt,
	// outputs, RBAC). These are what gets installed when an operator
	// deploys to a node. Required to deploy.
	DaimonFilesDir string

	// AgentFilesDirs is the list of search directories for short-form
	// Claude/Codex agent definitions (the same .md format Claude Code +
	// Codex use natively — small frontmatter, used for one-shot runs).
	// Multiple directories are searched in order, and the first match
	// wins. By default we look in ~/.claude/agents and ~/.codex/agents;
	// operators can extend with --agent-files-dir <path> (repeatable).
	AgentFilesDirs []string

	// FleetSSHKeyPath is an optional path to a private SSH key the CP
	// uses for unattended auto-deploy of the jobs runtime when an
	// orchestration step targets a node that has neither tunnel nor
	// jobs runtime up. When empty, auto-deploy is disabled and the
	// engine fails the step with a clear "manual install required"
	// message pointing at the Nodes UI. Operators set this with
	// --fleet-ssh-key-path when they want unattended provisioning.
	FleetSSHKeyPath string

	// FleetAnthropicAPIKey / FleetOpenAIAPIKey are the API keys the
	// auto-deployer writes into /etc/okesu/jobs.env on each freshly
	// installed node, so spawned `okesu claude` / `okesu codex` jobs
	// have credentials. Empty means the runtime starts without keys —
	// agent_run jobs will fail with "no API key" until an operator
	// drops a jobs.env on the node manually.
	FleetAnthropicAPIKey string
	FleetOpenAIAPIKey    string

	// WebhookPublicURL is the absolute URL the deployed daemon should POST
	// webhook events to. Defaults to derived from Listen ("https://localhost<port>")
	// — set explicitly when the daemon reaches the CP through a different
	// hostname, e.g. behind a load balancer or via host.lima.internal.
	WebhookPublicURL string

	// MgmtPublicURL is the absolute URL of the CP's management plane,
	// reachable from the deployed daemon. Defaults to derived from MgmtListen.
	MgmtPublicURL string

	// EventTTLDays controls retention of rows in the `events` table.
	// 0 disables pruning (default — operators opt in by setting a value).
	// The retention loop runs every 6 hours and deletes events older than
	// the configured age. Findings are kept independently and are never
	// auto-pruned by this knob.
	EventTTLDays int

	// PubSubURL selects the ports.PubSub adapter. Empty (default) uses
	// the in-process adapter — fine for single-CP deployments. Set to a
	// Redis URL ("redis://host:6379/0") to switch to the redis adapter
	// so SSE fan-out works across multiple CP replicas.
	PubSubURL string

	// SecretsSource selects how the CP fetches secrets at boot. Empty
	// (or "env") falls back to OKESU_SECRET_* env vars + the legacy
	// flag/env-var path; "file:///etc/okesu/secrets" reads each secret
	// from a file in that directory (perfect for systemd
	// LoadCredential=); "oci-vault://..." pulls from OCI Vault.
	//
	// Phase 8g: every secret the CP needs flows through this adapter
	// rather than through CLI flags. Legacy flags still work for dev
	// and trigger a deprecation warning.
	SecretsSource string

	// ConfigFile is the optional YAML/TOML config file path. When set,
	// the file is loaded BEFORE flag parsing — flags then override
	// fields the file populated. This means the deployable shape is:
	//   - YAML: non-secret config + "${secret:NAME}" references
	//   - --secrets-source: where the secret references resolve from
	//   - flags: only for dev one-offs
	ConfigFile string

	// BlobStoreURL selects the ports.BlobStore adapter. Empty (default)
	// keeps storage on the local filesystem at <db dir>/blobs/. Set to
	// an s3:// URL to use S3-compatible object storage (OCI Object
	// Storage, AWS S3, MinIO, R2). The CP will lazily initialise the
	// blob store when a feature first needs one (binary uploads, cold-
	// tier event archives, exports).
	BlobStoreURL    string
	BlobAccessKey   string
	BlobSecretKey   string
	BlobBucket      string
	BlobRegion      string

	// EventsStore selects the ports.EventStore adapter:
	//   "" or "sqlite" — wrap the existing SQLite db.Store (default)
	//   "clickhouse"   — connect to the configured ClickHouse cluster
	EventsStore         string
	ClickHouseAddrs     []string // host:port pairs
	ClickHouseDatabase  string
	ClickHouseUsername  string
	ClickHousePassword  string
	ClickHouseSecure    bool

	// Queue selects the ports.Queue adapter:
	//   "" or "inprocess" — channel-backed; sync ingest stays in-CP (default)
	//   "kafka"           — async ingest via Kafka / OCI Streaming
	Queue              string
	KafkaBrokers       []string
	KafkaSASLUsername  string
	KafkaSASLPassword  string
	KafkaUseTLS        bool

	// ── Federation foundation (Phase 9) ─────────────────────────────────────
	//
	// CPRegion is an operator-set label for this CP — typically the OCI
	// region ("us-ashburn-1") or a similar geographic / business unit
	// identifier. Surfaced on the introspect endpoint so a parent CP can
	// reason about which region a finding came from.
	CPRegion string

	// CPDisplayName is a human-readable label ("Primary CP", "EU west").
	// Cosmetic only; set what reads well in the parent CP's UI.
	CPDisplayName string

	// FederationToken is a shared secret a parent CP presents on the
	// introspect endpoint via the X-Okesu-Federation-Token header. The
	// CP stores its bcrypt hash in cp_meta.federation_token_hash on
	// first boot (and rotates it whenever the operator changes the
	// flag/env). Empty disables federation auth — the introspect
	// endpoint then only accepts authenticated session callers (useful
	// for local discovery).
	FederationToken string
}

// OIDCEnabled reports whether OIDC is configured.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCRedirectURL != ""
}

// Validate returns an error if required fields are missing.
func (c Config) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen address is required")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db path is required")
	}
	return nil
}

// FromEnv returns a Config seeded with environment variable defaults.
// CLI flags should overlay these values.
func FromEnv() Config {
	return Config{
		Listen:        envOr("OKESU_CP_LISTEN", ":8443"),
		DBPath:        envOr("OKESU_CP_DB", "./cp.db"),
		CertFile:      os.Getenv("OKESU_CP_CERT"),
		KeyFile:       os.Getenv("OKESU_CP_KEY"),
		WebhookSecret: os.Getenv("OKESU_CP_WEBHOOK_SECRET"),
		AdminEmail:    envOr("OKESU_CP_ADMIN_EMAIL", "admin@local"),
		AdminPassword: os.Getenv("OKESU_CP_ADMIN_PASSWORD"),
		SessionKey:    os.Getenv("OKESU_CP_SESSION_KEY"),

		MgmtListen:   envOr("OKESU_CP_MGMT_LISTEN", ":8444"),
		CACertFile:   os.Getenv("OKESU_CP_CA_CERT"),
		CAKeyFile:    os.Getenv("OKESU_CP_CA_KEY"),
		MgmtCertFile: os.Getenv("OKESU_CP_MGMT_CERT"),
		MgmtKeyFile:  os.Getenv("OKESU_CP_MGMT_KEY"),

		OIDCIssuer:       os.Getenv("OKESU_CP_OIDC_ISSUER"),
		OIDCClientID:     os.Getenv("OKESU_CP_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("OKESU_CP_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  os.Getenv("OKESU_CP_OIDC_REDIRECT_URL"),
		OIDCGroupsClaim:  envOr("OKESU_CP_OIDC_GROUPS_CLAIM", "groups"),
		OIDCRoleMap:      os.Getenv("OKESU_CP_OIDC_ROLE_MAP"),
		OIDCLabel:        envOr("OKESU_CP_OIDC_LABEL", "Sign in with Oracle"),

		DaemonBinaryPath:  os.Getenv("OKESU_CP_DAEMON_BINARY"),
		DaemonBinariesDir: os.Getenv("OKESU_CP_DAEMON_BINARIES_DIR"),
		DaimonFilesDir:    envAny("OKESU_CP_DAIMON_FILES_DIR", "OKESU_CP_AGENT_FILES_DIR"),
		AgentFilesDirs:    defaultAgentSearchDirs(envSplitNonEmpty("OKESU_CP_AGENT_FILES_DIRS", ":")),
		WebhookPublicURL:  os.Getenv("OKESU_CP_WEBHOOK_PUBLIC_URL"),
		MgmtPublicURL:     os.Getenv("OKESU_CP_MGMT_PUBLIC_URL"),

		FleetSSHKeyPath:      os.Getenv("OKESU_CP_FLEET_SSH_KEY_PATH"),
		FleetAnthropicAPIKey: envOr("OKESU_CP_FLEET_ANTHROPIC_API_KEY", os.Getenv("ANTHROPIC_API_KEY")),
		FleetOpenAIAPIKey:    envOr("OKESU_CP_FLEET_OPENAI_API_KEY", os.Getenv("OPENAI_API_KEY")),

		EventTTLDays: envInt("OKESU_CP_EVENT_TTL_DAYS", 0),
		PubSubURL:    os.Getenv("OKESU_CP_PUBSUB_URL"),

		SecretsSource: os.Getenv("OKESU_CP_SECRETS_SOURCE"),

		BlobStoreURL:  os.Getenv("OKESU_CP_BLOB_URL"),
		BlobAccessKey: os.Getenv("OKESU_CP_BLOB_ACCESS_KEY"),
		BlobSecretKey: os.Getenv("OKESU_CP_BLOB_SECRET_KEY"),
		BlobBucket:    os.Getenv("OKESU_CP_BLOB_BUCKET"),
		BlobRegion:    os.Getenv("OKESU_CP_BLOB_REGION"),

		EventsStore:        os.Getenv("OKESU_CP_EVENTS_STORE"),
		ClickHouseAddrs:    envSplitNonEmpty("OKESU_CP_CLICKHOUSE_ADDRS", ","),
		ClickHouseDatabase: envOr("OKESU_CP_CLICKHOUSE_DATABASE", "okesu_events"),
		ClickHouseUsername: os.Getenv("OKESU_CP_CLICKHOUSE_USERNAME"),
		ClickHousePassword: os.Getenv("OKESU_CP_CLICKHOUSE_PASSWORD"),
		ClickHouseSecure:   envBool("OKESU_CP_CLICKHOUSE_SECURE", false),

		Queue:             os.Getenv("OKESU_CP_QUEUE"),
		KafkaBrokers:      envSplitNonEmpty("OKESU_CP_KAFKA_BROKERS", ","),
		KafkaSASLUsername: os.Getenv("OKESU_CP_KAFKA_SASL_USERNAME"),
		KafkaSASLPassword: os.Getenv("OKESU_CP_KAFKA_SASL_PASSWORD"),
		KafkaUseTLS:       envBool("OKESU_CP_KAFKA_USE_TLS", false),

		CPRegion:        os.Getenv("OKESU_CP_REGION"),
		CPDisplayName:   os.Getenv("OKESU_CP_DISPLAY_NAME"),
		FederationToken: os.Getenv("OKESU_CP_FEDERATION_TOKEN"),
	}
}

// envBool reads "1"/"true"/"yes" as true; anything else as false; empty as `def`.
func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// envInt returns the env var as int, falling back to def if unset/invalid.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// EffectiveWebhookURL returns WebhookPublicURL or, if empty, "https://localhost<Listen>".
func (c Config) EffectiveWebhookURL() string {
	if c.WebhookPublicURL != "" {
		return c.WebhookPublicURL
	}
	return "https://localhost" + c.Listen + "/api/webhooks/events"
}

// EffectiveMgmtURL returns MgmtPublicURL or, if empty, "https://localhost<MgmtListen>".
func (c Config) EffectiveMgmtURL() string {
	if c.MgmtPublicURL != "" {
		return c.MgmtPublicURL
	}
	return "https://localhost" + c.MgmtListen
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envAny returns the first non-empty value among the listed env vars, or "".
// Used to support a primary env var name + a legacy alias during a rename.
func envAny(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// envSplitNonEmpty reads an env var and splits on sep, dropping empty entries.
func envSplitNonEmpty(key, sep string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// defaultAgentSearchDirs returns the canonical list of search directories
// for short-form agent definitions: ~/.claude/agents, ~/.codex/agents, plus
// any extras (typically passed in via --agent-files-dir flags). Missing
// directories are kept in the list — the library page reports each one's
// status (found/missing) so operators see what's contributing.
func defaultAgentSearchDirs(extra []string) []string {
	out := []string{}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".claude", "agents"))
		out = append(out, filepath.Join(home, ".codex", "agents"))
	}
	for _, e := range extra {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}
