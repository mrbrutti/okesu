package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/section9labs/okesu/agent"
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
	root.AddCommand(claudeCmd(), codexCmd(), autoCmd())
	return root
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

	systemPrompt := ""
	var allowedTools []string
	if def != nil {
		systemPrompt = def.Body
		allowedTools = def.Tools
	}

	return agent.Config{
		Provider:     provider,
		Model:        model,
		Prompt:       args[0],
		SystemPrompt: systemPrompt,
		MaxTokens:    maxTokens,
		Effort:       effort,
		MaxTurns:     maxTurns,
		AllowedTools: allowedTools,
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
  okesu auto --model gpt-4o --effort high "find bugs in ./src"`,
		Args: cobra.ExactArgs(1),
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
