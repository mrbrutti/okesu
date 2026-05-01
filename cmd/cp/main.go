// Command okesu-cp runs the Okesu Control Plane HTTP server.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/section9labs/okesu/controlplane"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// earlyConfigPath scans args for "--config <path>", "-c <path>", or
// "--config=path" and returns the value, BEFORE cobra parses flags.
// Used so the YAML file can populate defaults and CLI flags can
// override them rather than the other way around. Returns "" when
// the operator didn't pass --config.
func earlyConfigPath(args []string) string {
	for i, a := range args {
		switch {
		case a == "--config" || a == "-c":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-c="):
			return strings.TrimPrefix(a, "-c=")
		}
	}
	// Env var fallback for systemd unit files etc.
	return os.Getenv("OKESU_CP_CONFIG")
}

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "okesu-cp",
		Short: "Okesu Control Plane",
		Long: `okesu-cp runs the Okesu Control Plane: a web server that receives
webhook events from daemon agents, manages the agent fleet, and presents an
operations dashboard.`,
	}
	cmd.AddCommand(serveCmd(), issueCertCmd(), issueNodeCertCmd())
	return cmd
}

func serveCmd() *cobra.Command {
	cfg := controlplane.FromEnv()

	// Phase 8g: scan for --config / -c BEFORE cobra binds flags, so
	// the YAML file populates defaults and CLI flags can override
	// them in turn. Without this two-step, flags would silently lose
	// to whatever the YAML contains (or vice versa) — neither is what
	// the operator expects.
	if path := earlyConfigPath(os.Args[1:]); path != "" {
		if err := controlplane.LoadConfigFile(path, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "config file: %v\n", err)
			os.Exit(1)
		}
		cfg.ConfigFile = path
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Control Plane HTTPS server",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := controlplane.New(cfg)
			if err != nil {
				return fmt.Errorf("init: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			if err := srv.Run(ctx); err != nil {
				return fmt.Errorf("serve: %w", err)
			}
			log.Println("okesu-cp stopped")
			return nil
		},
	}

	cmd.Flags().StringVar(&cfg.Listen, "listen", cfg.Listen, "User-facing HTTPS bind (UI + webhooks)")
	cmd.Flags().StringVar(&cfg.MgmtListen, "mgmt-listen", cfg.MgmtListen, "Management plane HTTPS bind (mTLS, agent traffic)")
	cmd.Flags().StringVar(&cfg.DBPath, "db", cfg.DBPath, "Database DSN — sqlite path (./cp.db) or postgres://user:pass@host:port/db")
	cmd.Flags().StringVar(&cfg.CertFile, "cert", cfg.CertFile, "User-facing TLS cert (auto-generated if empty)")
	cmd.Flags().StringVar(&cfg.KeyFile, "key", cfg.KeyFile, "User-facing TLS key (auto-generated if empty)")
	cmd.Flags().StringVar(&cfg.CACertFile, "ca-cert", cfg.CACertFile, "mTLS CA cert (auto-generated if empty)")
	cmd.Flags().StringVar(&cfg.CAKeyFile, "ca-key", cfg.CAKeyFile, "mTLS CA private key (auto-generated if empty)")
	cmd.Flags().StringVar(&cfg.MgmtCertFile, "mgmt-cert", cfg.MgmtCertFile, "Mgmt plane server cert (auto-generated, signed by CA)")
	cmd.Flags().StringVar(&cfg.MgmtKeyFile, "mgmt-key", cfg.MgmtKeyFile, "Mgmt plane server key (auto-generated)")
	cmd.Flags().StringVar(&cfg.WebhookSecret, "webhook-secret", cfg.WebhookSecret, "HMAC-SHA256 secret shared with daemon agents")
	cmd.Flags().StringVar(&cfg.AdminEmail, "admin-email", cfg.AdminEmail, "Default admin email (seeded on first run)")
	cmd.Flags().StringVar(&cfg.AdminPassword, "admin-password", cfg.AdminPassword, "Default admin password (required on first run)")

	// OIDC / SSO (optional)
	cmd.Flags().StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL (e.g. Oracle Identity Domain root)")
	cmd.Flags().StringVar(&cfg.OIDCClientID, "oidc-client-id", cfg.OIDCClientID, "OIDC client ID")
	cmd.Flags().StringVar(&cfg.OIDCClientSecret, "oidc-client-secret", cfg.OIDCClientSecret, "OIDC client secret")
	cmd.Flags().StringVar(&cfg.OIDCRedirectURL, "oidc-redirect-url", cfg.OIDCRedirectURL, "OIDC callback URL (must match IDP config)")
	cmd.Flags().StringVar(&cfg.OIDCGroupsClaim, "oidc-groups-claim", cfg.OIDCGroupsClaim, "ID token claim with the user's groups")
	cmd.Flags().StringVar(&cfg.OIDCRoleMap, "oidc-role-map", cfg.OIDCRoleMap, "group:role,group:role mapping (e.g. \"okesu-admins:admin,ops:operator\")")
	cmd.Flags().StringVar(&cfg.OIDCLabel, "oidc-label", cfg.OIDCLabel, "Label for the SSO sign-in button")

	// Node deploy
	cmd.Flags().StringVar(&cfg.DaemonBinaryPath, "daemon-binary", cfg.DaemonBinaryPath, "Single-arch fallback daemon binary path (used when --daemon-binaries-dir is empty)")
	cmd.Flags().StringVar(&cfg.DaemonBinariesDir, "daemon-binaries-dir", cfg.DaemonBinariesDir, "Directory of per-arch daemon binaries (admins upload via Settings → Deploy)")
	// Daimon library — long-form *.md daimon definitions (with full
	// frontmatter — schedule, mgmt, outputs). What gets deployed to nodes.
	cmd.Flags().StringVar(&cfg.DaimonFilesDir, "daimon-files-dir", cfg.DaimonFilesDir, "Directory holding *.md daimon files available to deploy")

	// Orchestration seed — optional directory of orchestration *.md files
	// the CP installs on boot if not already present.
	cmd.Flags().StringVar(&cfg.OrchestrationSeedDir, "orchestration-seed-dir", cfg.OrchestrationSeedDir, "Optional directory of orchestration *.md files to install on boot if not already present")

	// Agent library — short-form Claude/Codex agent definitions, used for
	// one-off Runs. Repeat the flag to add more search paths; ~/.claude/agents
	// and ~/.codex/agents are always searched in addition to these.
	cmd.Flags().StringSliceVar(&cfg.AgentFilesDirs, "agent-files-dir", cfg.AgentFilesDirs, "Extra directories to search for short-form agent files (repeatable; ~/.claude/agents and ~/.codex/agents are always included)")
	cmd.Flags().StringSliceVar(&cfg.IOCCatalogDirs, "ioc-catalog-dir", cfg.IOCCatalogDirs, "Directory holding YAML IOC catalog files (repeatable; loaded on boot + SIGHUP). Defaults to catalog/iocs.")
	cmd.Flags().StringVar(&cfg.FleetSSHKeyPath, "fleet-ssh-key-path", cfg.FleetSSHKeyPath, "Path to an SSH private key the CP uses for unattended auto-deploy of the jobs runtime to nodes that have neither tunnel nor jobs runtime up. Empty disables auto-deploy.")
	cmd.Flags().StringVar(&cfg.FleetAnthropicAPIKey, "fleet-anthropic-api-key", cfg.FleetAnthropicAPIKey, "Anthropic API key written to /etc/okesu/jobs.env on auto-deployed nodes so spawned `okesu claude` jobs can authenticate. Falls back to ANTHROPIC_API_KEY env var.")
	cmd.Flags().StringVar(&cfg.FleetOpenAIAPIKey, "fleet-openai-api-key", cfg.FleetOpenAIAPIKey, "OpenAI API key written to /etc/okesu/jobs.env on auto-deployed nodes. Falls back to OPENAI_API_KEY env var.")
	cmd.Flags().StringVar(&cfg.WebhookPublicURL, "webhook-public-url", cfg.WebhookPublicURL, "URL deployed daemons should post webhook events to (default: derive from --listen)")
	cmd.Flags().StringVar(&cfg.MgmtPublicURL, "mgmt-public-url", cfg.MgmtPublicURL, "URL deployed daemons should reach the mgmt plane at (default: derive from --mgmt-listen)")

	// Persistence + retention
	cmd.Flags().IntVar(&cfg.EventTTLDays, "event-ttl-days", cfg.EventTTLDays, "Days of event history to keep (0 disables pruning)")

	// Pub/sub (SSE fan-out, run subscribers)
	cmd.Flags().StringVar(&cfg.PubSubURL, "pubsub-url", cfg.PubSubURL, "PubSub URL — empty=inprocess (single CP); redis://host:6379/0 for multi-replica deployments")

	// Secrets source — see controlplane/secrets.go for canonical names + supported schemes.
	cmd.Flags().StringVar(&cfg.SecretsSource, "secrets-source", cfg.SecretsSource, "Secrets source — env (default; OKESU_SECRET_*), file:///etc/okesu/secrets (systemd LoadCredential), oci-vault://<compartment-ocid>?region=...")
	cmd.Flags().StringVar(&cfg.ConfigFile, "config", cfg.ConfigFile, "Path to YAML config file. Non-secret config + ${secret:NAME} references that resolve via --secrets-source")

	// Events store + queue (async event ingest pipeline)
	cmd.Flags().StringVar(&cfg.EventsStore, "events-store", cfg.EventsStore, "Events backend — empty/sqlite (default) or clickhouse")
	cmd.Flags().StringSliceVar(&cfg.ClickHouseAddrs, "clickhouse-addr", cfg.ClickHouseAddrs, "ClickHouse host:port (repeatable for cluster)")
	cmd.Flags().StringVar(&cfg.ClickHouseDatabase, "clickhouse-database", cfg.ClickHouseDatabase, "ClickHouse database name")
	cmd.Flags().StringVar(&cfg.ClickHouseUsername, "clickhouse-username", cfg.ClickHouseUsername, "ClickHouse username")
	cmd.Flags().StringVar(&cfg.ClickHousePassword, "clickhouse-password", cfg.ClickHousePassword, "ClickHouse password")
	cmd.Flags().BoolVar(&cfg.ClickHouseSecure, "clickhouse-secure", cfg.ClickHouseSecure, "Enable TLS for ClickHouse")

	cmd.Flags().StringVar(&cfg.Queue, "queue", cfg.Queue, "Queue backend — empty/inprocess (default) or kafka")
	cmd.Flags().StringSliceVar(&cfg.KafkaBrokers, "kafka-broker", cfg.KafkaBrokers, "Kafka broker host:port (repeatable for cluster)")
	cmd.Flags().StringVar(&cfg.KafkaSASLUsername, "kafka-sasl-username", cfg.KafkaSASLUsername, "Kafka SASL username (for OCI Streaming: tenancy/username/stream-pool-ocid)")
	cmd.Flags().StringVar(&cfg.KafkaSASLPassword, "kafka-sasl-password", cfg.KafkaSASLPassword, "Kafka SASL password / auth token")
	cmd.Flags().BoolVar(&cfg.KafkaUseTLS, "kafka-tls", cfg.KafkaUseTLS, "Enable TLS for Kafka")

	// Blob store (binaries, exports, cold-tier event archives)
	cmd.Flags().StringVar(&cfg.BlobStoreURL, "blob-url", cfg.BlobStoreURL, "Blob store URL — empty=local filesystem; s3://endpoint or https://<namespace>.compat.objectstorage.<region>.oraclecloud.com for OCI/S3")
	cmd.Flags().StringVar(&cfg.BlobAccessKey, "blob-access-key", cfg.BlobAccessKey, "S3 access key (OCI customer secret key, AWS access-key-id, MinIO root user)")
	cmd.Flags().StringVar(&cfg.BlobSecretKey, "blob-secret-key", cfg.BlobSecretKey, "S3 secret key — paste from OCI Profile → Customer Secret Keys")
	cmd.Flags().StringVar(&cfg.BlobBucket, "blob-bucket", cfg.BlobBucket, "S3 bucket name")
	cmd.Flags().StringVar(&cfg.BlobRegion, "blob-region", cfg.BlobRegion, "S3 region (e.g. us-ashburn-1 for OCI, us-east-1 for AWS)")

	// Federation (Phase 9) — labels surfaced on /api/v1/cp/introspect.
	cmd.Flags().StringVar(&cfg.CPRegion, "cp-region", cfg.CPRegion, "Region label for this CP (e.g. us-ashburn-1) — surfaced on the federation introspect endpoint")
	cmd.Flags().StringVar(&cfg.CPDisplayName, "cp-display-name", cfg.CPDisplayName, "Human-readable name for this CP (cosmetic; shown to a parent CP in federated views)")
	cmd.Flags().StringVar(&cfg.FederationToken, "federation-token", cfg.FederationToken, "Shared secret a parent CP presents on /api/v1/cp/introspect (X-Okesu-Federation-Token). Empty disables federation auth.")
	cmd.Flags().StringVar(&cfg.CPBootstrapBinaryPath, "cp-bootstrap-binary", cfg.CPBootstrapBinaryPath, "Linux build of okesu-cp embedded in dockerfile-format CP bootstrap bundles. Empty disables that format.")
	cmd.Flags().StringVar(&cfg.CPBootstrapImageTarPath, "cp-bootstrap-image-tar", cfg.CPBootstrapImageTarPath, "`docker save`-format tarball of the okesu-cp image embedded in compose-format CP bootstrap bundles. Empty disables that format.")
	cmd.Flags().StringVar(&cfg.FederationS3PublishPrefix, "federation-s3-publish-prefix", cfg.FederationS3PublishPrefix, "Bucket prefix this child CP writes its introspect manifest to, e.g. 'cp/<self>/outbound/<parent>/'. Empty disables S3-dead-drop publishing.")
	cmd.Flags().Int64Var(&cfg.FederationS3PublishConfigID, "federation-s3-publish-config-id", cfg.FederationS3PublishConfigID, "transport_configs.id whose bucket coords the publisher uses. 0 disables.")
	cmd.Flags().StringVar(&cfg.FederationS3PublishBucket, "federation-s3-publish-bucket", cfg.FederationS3PublishBucket, "Inline bucket name for the publisher (alternative to --federation-s3-publish-config-id). Set with the other --federation-s3-publish-* flags by the enrollment bundle.")
	cmd.Flags().StringVar(&cfg.FederationS3PublishEndpoint, "federation-s3-publish-endpoint", cfg.FederationS3PublishEndpoint, "Inline bucket endpoint (host:port).")
	cmd.Flags().StringVar(&cfg.FederationS3PublishRegion, "federation-s3-publish-region", cfg.FederationS3PublishRegion, "Inline bucket region.")
	cmd.Flags().BoolVar(&cfg.FederationS3PublishUseSSL, "federation-s3-publish-use-ssl", cfg.FederationS3PublishUseSSL, "Whether to talk to the bucket over TLS.")
	cmd.Flags().StringVar(&cfg.FederationS3PublishAccessKey, "federation-s3-publish-access-key", cfg.FederationS3PublishAccessKey, "Inline bucket access key.")
	cmd.Flags().StringVar(&cfg.FederationS3PublishSecretKey, "federation-s3-publish-secret-key", cfg.FederationS3PublishSecretKey, "Inline bucket secret key.")
	cmd.Flags().StringVar(&cfg.CPInstanceID, "cp-instance-id", cfg.CPInstanceID, "Pre-assigned CP instance UUID. Honored only on a fresh DB; existing rows keep their generated id. Set by the federation enrollment bundle.")

	return cmd
}

func issueCertCmd() *cobra.Command {
	var (
		agentName string
		outDir    string
		dbPath    string
		caCert    string
		caKey     string
	)
	cmd := &cobra.Command{
		Use:   "issue-cert",
		Short: "Issue an mTLS client certificate for a daemon agent",
		Long: `Generate a client certificate (signed by the CP CA) that an agent can use
to authenticate to the management plane. Writes three files to --out:

  client.crt, client.key, ca.crt

These map directly to the layout the daemon expects under /etc/okesu/.`,
		Example: `  okesu-cp issue-cert --agent edr --out /etc/okesu/`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if agentName == "" {
				return fmt.Errorf("--agent is required")
			}
			if outDir == "" {
				return fmt.Errorf("--out is required")
			}
			cfg := controlplane.FromEnv()
			if dbPath != "" {
				cfg.DBPath = dbPath
			}
			if caCert != "" {
				cfg.CACertFile = caCert
			}
			if caKey != "" {
				cfg.CAKeyFile = caKey
			}
			ca, err := controlplane.EnsureCA(cfg)
			if err != nil {
				return fmt.Errorf("load CA: %w", err)
			}
			cert, key, err := ca.IssueClientCert(agentName)
			if err != nil {
				return fmt.Errorf("issue cert: %w", err)
			}
			if err := os.MkdirAll(outDir, 0755); err != nil {
				return err
			}
			files := map[string][]byte{
				"client.crt": cert,
				"client.key": key,
				"ca.crt":     ca.CertPEM,
			}
			modes := map[string]os.FileMode{
				"client.crt": 0644,
				"client.key": 0600,
				"ca.crt":     0644,
			}
			for name, data := range files {
				path := outDir + "/" + name
				if err := os.WriteFile(path, data, modes[name]); err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
				log.Printf("wrote %s", path)
			}
			log.Printf("issued cert for agent %q (CN=%s)", agentName, agentName)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "Agent name (used as cert CN)")
	cmd.Flags().StringVar(&outDir, "out", "", "Output directory")
	cmd.Flags().StringVar(&dbPath, "db", "", "DB path (for locating the auto-generated CA; defaults to OKESU_CP_DB or ./cp.db)")
	cmd.Flags().StringVar(&caCert, "ca-cert", "", "CA cert path override")
	cmd.Flags().StringVar(&caKey, "ca-key", "", "CA key path override")
	return cmd
}

// issueNodeCertCmd is the same as issueCertCmd but the CN identifies a Node
// running `okesu node` (the reverse-tunnel client) rather than a daemon agent.
// Same CA, same wire layout — distinct command for clarity.
func issueNodeCertCmd() *cobra.Command {
	var (
		nodeName string
		outDir   string
		dbPath   string
		caCert   string
		caKey    string
	)
	cmd := &cobra.Command{
		Use:   "issue-node-cert",
		Short: "Issue an mTLS client certificate for an `okesu node` reverse tunnel client",
		Long: `Generate a client certificate (signed by the CP CA) that an okesu node
process can use to authenticate to the reverse tunnel endpoint. Writes
client.crt, client.key, and ca.crt to --out.`,
		Example: `  okesu-cp issue-node-cert --node prod-web-01 --out /etc/okesu/node-certs`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if nodeName == "" {
				return fmt.Errorf("--node is required")
			}
			if outDir == "" {
				return fmt.Errorf("--out is required")
			}
			cfg := controlplane.FromEnv()
			if dbPath != "" {
				cfg.DBPath = dbPath
			}
			if caCert != "" {
				cfg.CACertFile = caCert
			}
			if caKey != "" {
				cfg.CAKeyFile = caKey
			}
			ca, err := controlplane.EnsureCA(cfg)
			if err != nil {
				return fmt.Errorf("load CA: %w", err)
			}
			cert, key, err := ca.IssueClientCert(nodeName)
			if err != nil {
				return fmt.Errorf("issue cert: %w", err)
			}
			if err := os.MkdirAll(outDir, 0755); err != nil {
				return err
			}
			files := map[string][]byte{
				"client.crt": cert,
				"client.key": key,
				"ca.crt":     ca.CertPEM,
			}
			modes := map[string]os.FileMode{
				"client.crt": 0644,
				"client.key": 0600,
				"ca.crt":     0644,
			}
			for name, data := range files {
				path := outDir + "/" + name
				if err := os.WriteFile(path, data, modes[name]); err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
				log.Printf("wrote %s", path)
			}
			log.Printf("issued node cert %q (CN=%s)", nodeName, nodeName)
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeName, "node", "", "Node name (used as cert CN; must match the name passed to `okesu node`)")
	cmd.Flags().StringVar(&outDir, "out", "", "Output directory")
	cmd.Flags().StringVar(&dbPath, "db", "", "DB path (for locating the auto-generated CA)")
	cmd.Flags().StringVar(&caCert, "ca-cert", "", "CA cert path override")
	cmd.Flags().StringVar(&caKey, "ca-key", "", "CA key path override")
	return cmd
}
