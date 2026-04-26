// Command okesu-cp runs the Okesu Control Plane HTTP server.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/section9labs/okesu/controlplane"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
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

	// Agent library — short-form Claude/Codex agent definitions, used for
	// one-off Runs. Repeat the flag to add more search paths; ~/.claude/agents
	// and ~/.codex/agents are always searched in addition to these.
	cmd.Flags().StringSliceVar(&cfg.AgentFilesDirs, "agent-files-dir", cfg.AgentFilesDirs, "Extra directories to search for short-form agent files (repeatable; ~/.claude/agents and ~/.codex/agents are always included)")
	cmd.Flags().StringVar(&cfg.WebhookPublicURL, "webhook-public-url", cfg.WebhookPublicURL, "URL deployed daemons should post webhook events to (default: derive from --listen)")
	cmd.Flags().StringVar(&cfg.MgmtPublicURL, "mgmt-public-url", cfg.MgmtPublicURL, "URL deployed daemons should reach the mgmt plane at (default: derive from --mgmt-listen)")

	// Persistence + retention
	cmd.Flags().IntVar(&cfg.EventTTLDays, "event-ttl-days", cfg.EventTTLDays, "Days of event history to keep (0 disables pruning)")

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
