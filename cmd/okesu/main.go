package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/agent/s3transport"
	"github.com/section9labs/okesu/node"
	"github.com/spf13/cobra"
)

func main() {
	loadDotEnv()
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// loadDotEnv reads ~/.config/.env and exports any variables not already set
// in the process environment. Variables already set take precedence.
// The file is silently skipped if it does not exist or cannot be read.
//
// Supported formats per line:
//
//	KEY=value
//	KEY="value with spaces"
//	KEY='value'
//	export KEY=value
//	# comment
func loadDotEnv() {
	home := os.Getenv("HOME")
	if home == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", ".env"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// strip optional "export " prefix
		line = strings.TrimPrefix(line, "export ")

		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		// strip surrounding quotes (" or ')
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		// existing env vars always win
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "okesu",
		Short: "Autonomous AI agent runner",
		Long: `okesu runs a fully autonomous AI agent using either the Anthropic or OpenAI SDK.
All tool calls execute without any sandbox or approval gate.
Output streams as JSONL to stdout.`,
	}
	root.AddCommand(claudeCmd(), codexCmd(), autoCmd(), daemonCmd(), nodeCmd(), jobsCmd(), enrollCmd(), s3JobsCmd())
	return root
}

// enrollCmd reads the bootstrap.json an operator's package dropped
// on disk, runs the S3 dead-drop enrollment flow, and persists the
// per-node identity to /etc/okesu/node-certs/. Idempotent — re-runs
// no-op once the cached identity is in place.
func enrollCmd() *cobra.Command {
	var (
		bootstrapPath string
		certDir       string
	)
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll this host against a Control Plane via the S3 dead-drop transport",
		Long: `Reads /etc/okesu/bootstrap.json (produced by the CP's package
generator), uploads a registration request to the bucket, and waits
for the CP scanner to issue the per-node mTLS cert.

The flow is offline-friendly: the host needs reachability to the
configured bucket only, never to the CP itself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			body, err := os.ReadFile(bootstrapPath)
			if err != nil {
				return fmt.Errorf("read bootstrap: %w", err)
			}
			var b struct {
				PackageID      int64    `json:"package_id"`
				CPID           string   `json:"cp_id"`
				Bucket         string   `json:"bucket"`
				Endpoint       string   `json:"endpoint"`
				Region         string   `json:"region"`
				UseSSL         bool     `json:"use_ssl"`
				AccessKey      string   `json:"access_key"`
				SecretKey      string   `json:"secret_key"`
				PackageCertPEM string   `json:"package_cert_pem"`
				PackageKeyPEM  string   `json:"package_key_pem"`
				Defaults       struct {
					Agents []string `json:"agents,omitempty"`
				} `json:"defaults"`
			}
			if err := json.Unmarshal(body, &b); err != nil {
				return fmt.Errorf("decode bootstrap: %w", err)
			}
			cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
				Endpoint:  b.Endpoint,
				Region:    b.Region,
				Bucket:    b.Bucket,
				AccessKey: b.AccessKey,
				SecretKey: b.SecretKey,
				UseSSL:    b.UseSSL,
			})
			if err != nil {
				return fmt.Errorf("connect bucket: %w", err)
			}
			res, err := s3transport.Enroll(ctx, s3transport.EnrollRequest{
				Client:          cli,
				CPID:            b.CPID,
				PackageID:       b.PackageID,
				PackageCertPEM:  b.PackageCertPEM,
				PackageKeyPEM:   b.PackageKeyPEM,
				AgentsRequested: b.Defaults.Agents,
				CertDir:         certDir,
			})
			if err != nil {
				return err
			}
			fmt.Printf("enrolled: node_id=%d uuid=%s\n", res.NodeID, res.NodeUUID)
			return nil
		},
	}
	cmd.Flags().StringVar(&bootstrapPath, "bootstrap", "/etc/okesu/bootstrap.json", "Path to the bootstrap.json file the package dropped on disk")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/etc/okesu/node-certs", "Directory to persist the issued cert + key")
	return cmd
}

// s3JobsCmd runs the S3-mode jobs runtime — the dead-drop dual of
// the existing `okesu jobs` HTTPS pull loop. Reads the bootstrap.json
// + node identity from disk and starts the poll loop.
//
// Wired as a subcommand of the main `jobs` group via a flag in v1
// to keep the existing `okesu jobs --cp-url ...` shape backward
// compatible.
func s3JobsCmd() *cobra.Command {
	var (
		bootstrapPath string
		certDir       string
	)
	cmd := &cobra.Command{
		Use:    "s3-jobs",
		Short:  "Run the pull-mode jobs runtime over S3 (dead-drop transport)",
		Hidden: true, // surfaced via `okesu jobs --transport=s3` in a follow-up
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			body, err := os.ReadFile(bootstrapPath)
			if err != nil {
				return fmt.Errorf("read bootstrap: %w", err)
			}
			var b struct {
				CPID      string `json:"cp_id"`
				Bucket    string `json:"bucket"`
				Endpoint  string `json:"endpoint"`
				Region    string `json:"region"`
				UseSSL    bool   `json:"use_ssl"`
				AccessKey string `json:"access_key"`
				SecretKey string `json:"secret_key"`
			}
			if err := json.Unmarshal(body, &b); err != nil {
				return fmt.Errorf("decode bootstrap: %w", err)
			}
			cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
				Endpoint:  b.Endpoint,
				Region:    b.Region,
				Bucket:    b.Bucket,
				AccessKey: b.AccessKey,
				SecretKey: b.SecretKey,
				UseSSL:    b.UseSSL,
			})
			if err != nil {
				return fmt.Errorf("connect bucket: %w", err)
			}
			// Read the persisted node-id (set by enroll). Without it
			// the runner has nowhere to read jobs from.
			idStr, err := os.ReadFile(certDir + "/node-id")
			if err != nil {
				return fmt.Errorf("read node-id: %w (run `okesu enroll` first)", err)
			}
			var nodeID int64
			fmt.Sscanf(string(idStr), "%d", &nodeID)
			if nodeID == 0 {
				return fmt.Errorf("invalid node-id at %s/node-id", certDir)
			}
			r := s3transport.NewRunner(s3transport.RunnerConfig{
				Client:   cli,
				CPID:     b.CPID,
				NodeID:   nodeID,
				NodeName: hostname(),
			})
			return r.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&bootstrapPath, "bootstrap", "/etc/okesu/bootstrap.json", "Path to bootstrap.json")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/etc/okesu/node-certs", "Directory holding the persisted node-id")
	return cmd
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

// jobsCmd runs the pull-mode jobs runtime — the host-side worker the
// orchestrator dispatches agent runs through when the tunnel isn't
// installed (which is the new default). One process per host;
// authenticates with a node-level mTLS cert in --cert-dir.
//
// This is the cleaner long-term path than `okesu node`: no persistent
// reverse-tunnel, no separate connection-tracking on the CP, and the
// same trust path the daimon mgmt-plane already uses. Tunnels stay
// available on demand — the jobs runtime supervises an `okesu node`
// child whenever the engine asks for one via a start_tunnel job.
func jobsCmd() *cobra.Command {
	var (
		cpMgmtURL string
		certDir   string
		nodeName  string
	)
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Run the pull-mode jobs runtime",
		Long: `Polls the Control Plane for orchestration jobs and executes them locally.

Same mTLS cert layout as ` + "`okesu node`" + `:

  okesu-cp issue-node-cert --node <name> --out <dir>

Then on the target host:

  okesu jobs --cp-url https://cp.example.com:8444 --cert-dir /etc/okesu/node-certs --name prod-web-01`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			r, err := agent.NewJobsRunner(agent.JobsRunnerConfig{
				CPMgmtURL: cpMgmtURL,
				CertDir:   certDir,
				NodeName:  nodeName,
			})
			if err != nil {
				return err
			}
			return r.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&cpMgmtURL, "cp-url", "", "Control Plane mgmt-plane URL (https://cp.example.com:8444)")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/etc/okesu/node-certs", "Directory holding client.crt, client.key, ca.crt")
	cmd.Flags().StringVar(&nodeName, "name", "", "Node name reported on each poll (defaults to hostname)")
	return cmd
}

// nodeCmd runs the reverse-tunnel client (Phase 6). Connects to the CP via
// mTLS WebSocket and waits for ad-hoc agent run requests; spawns
// `okesu claude|codex|auto` for each request and streams output back.
func nodeCmd() *cobra.Command {
	var (
		cpURL    string
		certDir  string
		nodeName string
	)
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Run the reverse-tunnel client to a Control Plane",
		Long: `Connects to the Control Plane's mgmt-plane endpoint over mTLS and holds
a long-lived WebSocket open. The CP can then spawn ad-hoc claude/codex/auto
runs on this host and stream the JSONL output back to the operator's browser.

Provision the cert bundle on the CP host with:

  okesu-cp issue-node-cert --node <name> --out <dir>

Then drop the three files (client.crt, client.key, ca.crt) onto this host
and run:

  okesu node --cp-url https://cp.example.com:8444 --cert-dir /etc/okesu/node-certs --name prod-web-01`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeName == "" {
				h, _ := os.Hostname()
				nodeName = h
			}
			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			return node.Run(ctx, node.Config{
				CPURL:    cpURL,
				CertDir:  certDir,
				NodeName: nodeName,
				Version:  agent.Version(),
			})
		},
	}
	cmd.Flags().StringVar(&cpURL, "cp-url", "", "Control Plane mgmt-plane URL (https://cp.example.com:8444)")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/etc/okesu/node-certs", "Directory holding client.crt, client.key, ca.crt")
	cmd.Flags().StringVar(&nodeName, "name", "", "Node name reported in Hello (defaults to hostname)")
	return cmd
}

// sharedFlags adds the flags that all subcommands accept.
func sharedFlags(cmd *cobra.Command) {
	cmd.Flags().String("model", "", "Model override")
	cmd.Flags().String("system", "", "Path to system prompt file")
	cmd.Flags().String("agent", "", "Agent name — loads from .claude/agents/ or .codex/agents/")
	cmd.Flags().String("api-key", "", "API key (overrides env var)")
	cmd.Flags().Int64("max-tokens", 8192, "Maximum tokens per response")
	cmd.Flags().String("effort", "", "Thinking depth — claude: low|medium|high|xhigh|max  codex: low|medium|high|xhigh")
	cmd.Flags().Int("max-turns", 0, "Maximum agentic loop iterations (0 = unlimited; agent file value used if not set)")
	cmd.Flags().String("prompt-file", "", "Read the user prompt from this file instead of <prompt> argv. Lets callers pass prompts that exceed ARG_MAX (e.g. orchestration data: blocks).")
}

// resolveAgent loads the agent file (if --agent is set) or wraps --system as a bare AgentDef.
// Returns nil with no error when neither flag is provided.
func resolveAgent(cmd *cobra.Command) (*agent.AgentDef, error) {
	agentName, _ := cmd.Flags().GetString("agent")
	systemFile, _ := cmd.Flags().GetString("system")

	if agentName != "" {
		return agent.ParseAgentFile(agentName)
	}
	if systemFile != "" {
		content, err := os.ReadFile(systemFile)
		if err != nil {
			return nil, fmt.Errorf("reading system file: %w", err)
		}
		return &agent.AgentDef{Body: string(content)}, nil
	}
	return nil, nil
}

// buildConfig assembles a Config from CLI flags and agent frontmatter.
// CLI flags always take precedence over agent file values.
func buildConfig(cmd *cobra.Command, args []string, provider string, def *agent.AgentDef) (agent.Config, error) {
	model, _ := cmd.Flags().GetString("model")
	effort, _ := cmd.Flags().GetString("effort")
	maxTokens, _ := cmd.Flags().GetInt64("max-tokens")
	maxTurns, _ := cmd.Flags().GetInt("max-turns")

	// Apply agent frontmatter defaults — only when CLI flag was not explicitly set.
	if def != nil {
		if model == "" {
			model = def.Model
		}
		if effort == "" {
			effort = def.Effort
		}
		if maxTurns == 0 && def.MaxTurns > 0 {
			maxTurns = def.MaxTurns
		}
	}

	// Provider-specific model defaults.
	if model == "" {
		if provider == "claude" {
			model = "claude-opus-4-6"
		} else {
			model = "gpt-4o"
		}
	}

	name := ""
	systemPrompt := ""
	var allowedTools []string
	var rbac *agent.RBACPolicy
	var definitionVersion string
	if def != nil {
		name = def.Name
		systemPrompt = def.Body
		allowedTools = def.Tools
		definitionVersion = def.Version
		if len(def.Actions.RBAC.Allow) > 0 || len(def.Actions.RBAC.Deny) > 0 {
			p := def.Actions.RBAC
			rbac = &p
		}
	}

	prompt := ""
	if len(args) > 0 {
		prompt = args[0]
	}
	// --prompt-file overrides the positional. Callers (notably the
	// CP's cp-local dispatcher) use this when the prompt embeds large
	// data: payloads that would blow past the OS argv limit.
	if pf, _ := cmd.Flags().GetString("prompt-file"); pf != "" {
		b, err := os.ReadFile(pf)
		if err != nil {
			return agent.Config{}, fmt.Errorf("read --prompt-file: %w", err)
		}
		prompt = string(b)
	}

	return agent.Config{
		Name:              name,
		DefinitionVersion: definitionVersion,
		Provider:          provider,
		Model:             model,
		Prompt:            prompt,
		SystemPrompt:      systemPrompt,
		MaxTokens:         maxTokens,
		Effort:            effort,
		MaxTurns:          maxTurns,
		AllowedTools:      allowedTools,
		RBAC:              rbac,
	}, nil
}

// resolveAPIKey resolves --api-key flag → env var → error.
func resolveAPIKey(cmd *cobra.Command, envVar string) (string, error) {
	key, _ := cmd.Flags().GetString("api-key")
	if key == "" {
		key = os.Getenv(envVar)
	}
	if key == "" {
		return "", fmt.Errorf("no API key — set %s or use --api-key", envVar)
	}
	return key, nil
}

// inferProvider determines the provider from, in order:
//  1. agent file `provider:` field
//  2. model name prefix (claude- → claude, gpt-/o1-/o3-/o4- → codex)
//  3. which API keys are present in the environment
func inferProvider(model string, def *agent.AgentDef) (string, error) {
	if def != nil && def.Provider != "" {
		switch strings.ToLower(def.Provider) {
		case "claude", "anthropic":
			return "claude", nil
		case "codex", "openai":
			return "codex", nil
		default:
			return "", fmt.Errorf("unknown provider %q in agent file — use claude or codex", def.Provider)
		}
	}

	if model != "" {
		lower := strings.ToLower(model)
		if strings.HasPrefix(lower, "claude") {
			return "claude", nil
		}
		if strings.HasPrefix(lower, "gpt") ||
			strings.HasPrefix(lower, "o1") ||
			strings.HasPrefix(lower, "o2") ||
			strings.HasPrefix(lower, "o3") ||
			strings.HasPrefix(lower, "o4") {
			return "codex", nil
		}
	}

	hasAnthropic := os.Getenv("ANTHROPIC_API_KEY") != ""
	hasOpenAI := os.Getenv("OPENAI_API_KEY") != ""
	if hasAnthropic && !hasOpenAI {
		return "claude", nil
	}
	if hasOpenAI && !hasAnthropic {
		return "codex", nil
	}

	return "", fmt.Errorf(
		"cannot determine provider — set provider: in agent file, " +
			"use --model with a recognisable prefix (claude-* / gpt-* / o1-* / o3-*), " +
			"or export only one of ANTHROPIC_API_KEY / OPENAI_API_KEY",
	)
}

func claudeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude <prompt>",
		Short: "Run an autonomous agent using the Anthropic API",
		Long: `Run a fully autonomous agentic loop against the Anthropic API.
Streams JSONL events to stdout until the model stops issuing tool calls.

API key precedence:
  1. --api-key flag
  2. ANTHROPIC_API_KEY environment variable`,
		Example: `  okesu claude "review ./src for security vulnerabilities"
  okesu claude --agent security-reviewer "audit ./api"
  okesu claude --model claude-opus-4-6 --system ./agent.md "fix failing tests"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := resolveAgent(cmd)
			if err != nil {
				return err
			}
			apiKey, err := resolveAPIKey(cmd, "ANTHROPIC_API_KEY")
			if err != nil {
				return err
			}
			cfg, err := buildConfig(cmd, args, "claude", def)
			if err != nil {
				return err
			}
			cfg.APIKey = apiKey
			return agent.RunClaude(cfg)
		},
	}
	sharedFlags(cmd)
	return cmd
}

func codexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex <prompt>",
		Short: "Run an autonomous agent using the OpenAI API",
		Long: `Run a fully autonomous agentic loop against the OpenAI Responses API.
Streams JSONL events to stdout until the model stops issuing tool calls.

API key precedence:
  1. --api-key flag
  2. OPENAI_API_KEY environment variable`,
		Example: `  okesu codex "review ./src for security vulnerabilities"
  okesu codex --agent security-reviewer "audit ./api"
  okesu codex --model gpt-4o "find SQL injection vectors in ./app"
  okesu codex --model gpt-5.4-cyber --effort xhigh "full security audit"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := resolveAgent(cmd)
			if err != nil {
				return err
			}
			apiKey, err := resolveAPIKey(cmd, "OPENAI_API_KEY")
			if err != nil {
				return err
			}
			cfg, err := buildConfig(cmd, args, "codex", def)
			if err != nil {
				return err
			}
			cfg.APIKey = apiKey
			return agent.RunOpenAI(cfg)
		},
	}
	sharedFlags(cmd)
	return cmd
}

func autoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto <prompt>",
		Short: "Run an autonomous agent, inferring the provider automatically",
		Long: `Run a fully autonomous agentic loop, picking the provider from:

  1. provider: field in the agent file (claude | codex)
  2. Model name prefix  (claude-* → Anthropic,  gpt-*/o1-*/o3-* → OpenAI)
  3. Which API key is set in the environment

API key precedence:
  1. --api-key flag
  2. ANTHROPIC_API_KEY or OPENAI_API_KEY (whichever matches the inferred provider)`,
		Example: `  okesu auto --agent security-reviewer "audit ./api"
  okesu auto --model claude-opus-4-6 "review this code"
  okesu auto --model gpt-4o --effort high "find bugs in ./src"
  okesu auto --agent investigator --prompt-file /tmp/prompt.txt`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := resolveAgent(cmd)
			if err != nil {
				return err
			}

			// Peek at --model so inferProvider can use it.
			model, _ := cmd.Flags().GetString("model")
			if model == "" && def != nil {
				model = def.Model
			}

			provider, err := inferProvider(model, def)
			if err != nil {
				return err
			}

			var envVar string
			if provider == "claude" {
				envVar = "ANTHROPIC_API_KEY"
			} else {
				envVar = "OPENAI_API_KEY"
			}
			apiKey, err := resolveAPIKey(cmd, envVar)
			if err != nil {
				return err
			}

			cfg, err := buildConfig(cmd, args, provider, def)
			if err != nil {
				return err
			}
			cfg.APIKey = apiKey

			if provider == "claude" {
				return agent.RunClaude(cfg)
			}
			return agent.RunOpenAI(cfg)
		},
	}
	sharedFlags(cmd)
	return cmd
}

func daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run an agent as a persistent scheduled daemon",
		Long: `Run an agent in daemon mode: wake on schedule, execute one agentic tick,
sleep, and repeat indefinitely. The agent file must specify mode: daemon.

The prompt is assembled automatically each tick (from pre-collectors in Phase 2).
No positional argument is accepted — use --agent to load the agent file.

Signal handling:
  SIGTERM / SIGINT — finish the current tick cleanly, then exit 0
  SIGHUP           — hot config reload (Phase 6)

Schedule precedence (highest to lowest):
  1. --cron flag
  2. --interval flag
  3. cron: field in agent file
  4. interval: field in agent file
  5. default: 60s`,
		Example: `  okesu daemon --agent edr-agent
  okesu daemon --agent edr-agent --interval 30s
  okesu daemon --agent report-builder --cron "0 8 * * 1-5"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, _ := cmd.Flags().GetString("agent")
			if agentName == "" {
				return fmt.Errorf("--agent is required for daemon mode")
			}

			def, err := resolveAgent(cmd)
			if err != nil {
				return err
			}

			model, _ := cmd.Flags().GetString("model")
			if model == "" && def != nil {
				model = def.Model
			}

			provider, err := inferProvider(model, def)
			if err != nil {
				return err
			}

			// In daemon mode a missing API key is non-fatal: we still want to
			// connect to the management plane and emit heartbeats so the
			// operator can see the agent on the Agents page. Each tick that
			// tries to call the LLM will just emit an api_unavailable event.
			apiKey, _ := resolveAPIKey(cmd, apiKeyEnvVar(provider))
			if apiKey == "" {
				fmt.Fprintf(os.Stderr,
					"warning: no %s set — daemon will register but LLM calls will fail until the key is provided\n",
					apiKeyEnvVar(provider))
			}

			cfg, err := buildConfig(cmd, nil, provider, def)
			if err != nil {
				return err
			}
			cfg.APIKey = apiKey
			cfg.IsDaemon = true

			dcfg, err := buildDaemonConfig(cmd, def)
			if err != nil {
				return err
			}

			return agent.RunDaemon(cfg, dcfg)
		},
	}
	sharedFlags(cmd)
	cmd.Flags().String("interval", "", "Override tick interval (e.g. 30s, 5m, 1h)")
	cmd.Flags().String("cron", "", "Override cron schedule (e.g. '0 * * * *')")
	return cmd
}

// buildDaemonConfig assembles a DaemonConfig from agent file frontmatter and CLI overrides.
// CLI flags always take precedence over agent file values.
func buildDaemonConfig(cmd *cobra.Command, def *agent.AgentDef) (agent.DaemonConfig, error) {
	dcfg := agent.DaemonConfig{
		Overlap:  "skip",
		Interval: 60 * time.Second,
	}

	if def != nil {
		if def.Interval != "" {
			d, err := time.ParseDuration(def.Interval)
			if err != nil {
				return dcfg, fmt.Errorf("invalid interval %q in agent file: %w", def.Interval, err)
			}
			dcfg.Interval = d
			// Default IntervalMin = IntervalMax = interval — adaptive
			// scheduler degenerates to a fixed interval for backward
			// compatibility with single-`interval:` agent files.
			dcfg.IntervalMin = d
			dcfg.IntervalMax = d
		}
		if def.IntervalMin != "" {
			d, err := time.ParseDuration(def.IntervalMin)
			if err != nil {
				return dcfg, fmt.Errorf("invalid interval_min %q in agent file: %w", def.IntervalMin, err)
			}
			dcfg.IntervalMin = d
			// If interval_max isn't set, the scheduler stays at the floor
			// (fixed-interval behavior). interval_min also seeds Interval
			// so the legacy fixed-interval path keeps working if someone
			// disables adaptive scheduling later.
			if dcfg.IntervalMax < d {
				dcfg.IntervalMax = d
			}
			if dcfg.Interval <= 0 {
				dcfg.Interval = d
			}
		}
		if def.IntervalMax != "" {
			d, err := time.ParseDuration(def.IntervalMax)
			if err != nil {
				return dcfg, fmt.Errorf("invalid interval_max %q in agent file: %w", def.IntervalMax, err)
			}
			dcfg.IntervalMax = d
		}
		if def.Cron != "" {
			dcfg.Cron = def.Cron
			dcfg.Interval = 0
			dcfg.IntervalMin = 0
			dcfg.IntervalMax = 0
		}
		if def.Overlap != "" {
			dcfg.Overlap = def.Overlap
		}
		if def.StateDir != "" {
			dcfg.StateDir = def.StateDir
		} else if def.Name != "" {
			dcfg.StateDir = "/var/lib/okesu/" + def.Name
		}
		if def.DedupeTTL != "" {
			d, err := time.ParseDuration(def.DedupeTTL)
			if err != nil {
				return dcfg, fmt.Errorf("invalid dedupeTtl %q in agent file: %w", def.DedupeTTL, err)
			}
			dcfg.DedupeTTL = d
		}
		// Parse collector timeouts from their string representation.
		for i, c := range def.Collectors {
			if c.TimeoutStr != "" {
				d, err := time.ParseDuration(c.TimeoutStr)
				if err != nil {
					return dcfg, fmt.Errorf("invalid collector %q timeout %q: %w", c.Name, c.TimeoutStr, err)
				}
				def.Collectors[i].Timeout = d
			}
		}
		dcfg.Collectors = def.Collectors
		dcfg.Outputs = def.Outputs
		dcfg.Mgmt = def.Mgmt
	}

	// CLI overrides — highest precedence.
	if intervalStr, _ := cmd.Flags().GetString("interval"); intervalStr != "" {
		d, err := time.ParseDuration(intervalStr)
		if err != nil {
			return dcfg, fmt.Errorf("invalid --interval %q: %w", intervalStr, err)
		}
		dcfg.Interval = d
		// CLI --interval forces fixed-interval behavior — pin min == max == d.
		dcfg.IntervalMin = d
		dcfg.IntervalMax = d
		dcfg.Cron = ""
	}
	if cronStr, _ := cmd.Flags().GetString("cron"); cronStr != "" {
		dcfg.Cron = cronStr
		dcfg.Interval = 0
		dcfg.IntervalMin = 0
		dcfg.IntervalMax = 0
	}

	return dcfg, nil
}

// apiKeyEnvVar returns the env var name for the given provider's API key.
func apiKeyEnvVar(provider string) string {
	if provider == "claude" {
		return "ANTHROPIC_API_KEY"
	}
	return "OPENAI_API_KEY"
}
