package agent

import (
	"context"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"
)

// Config holds the runtime configuration passed from the CLI.
type Config struct {
	Name              string // agent name (from agent file; used in daemon events)
	DefinitionVersion string // operator-set version label from the daimon file (e.g. "2", "v3"); reported via heartbeat
	Provider          string
	Model             string
	Prompt       string
	SystemPrompt string
	MaxTokens    int64
	APIKey       string
	// Effort controls thinking depth.
	// Claude: "low" | "medium" | "high" | "xhigh" | "max"
	// OpenAI: "low" | "medium" | "high" | "xhigh"
	Effort string
	// MaxTurns caps the number of agentic loop iterations (0 = unlimited).
	MaxTurns int
	// AllowedTools restricts which tools are exposed to the model.
	// Empty means all tools. Accepts okesu and Claude Code CLI tool names.
	AllowedTools []string
	// RBAC enforces allow/deny rules on tool calls at dispatch time.
	// Nil means no restrictions.
	RBAC *RBACPolicy
	// IsDaemon is true when running under RunDaemon. Enables daemon-specific
	// events such as EventActionTaken.
	IsDaemon bool

	// Tick is the daemon tick sequence number this invocation belongs to.
	// Stamped onto every conversation event (text/tool_call/tool_result/init/done)
	// so consumers can group them under the right tick. 0 in non-daemon mode.
	Tick int64

	// Host is the daemon hostname, copied onto every conversation event for
	// the same reason as Tick. Empty in non-daemon mode.
	Host string

	// LookupFindings, when non-nil, backs the `lookup_findings` LLM tool —
	// daemons connected to a Control Plane wire this to MgmtPlane.LookupFindings
	// so the model can ask the CP about prior findings before reporting.
	// Nil disables the tool (handler returns a tool-not-configured error,
	// the LLM learns to stop calling it).
	LookupFindings func(ctx context.Context, query string, limit int) ([]LookupResult, error)
}

// RunClaude runs a fully autonomous agentic loop against the Anthropic API using
// the SDK's built-in BetaToolRunnerStreaming, which handles the tool loop, parallel
// tool execution, and message accumulation automatically.
func RunClaude(cfg Config) error {
	client := anthropic.NewClient(
		option.WithAPIKey(cfg.APIKey),
	)

	Emit(Event{
		Type:     EventInit,
		Provider: "claude",
		Model:    cfg.Model,
		Agent:    cfg.Name,
		Host:     cfg.Host,
		Tick:     cfg.Tick,
	})

	tools, err := buildBetaTools(ActiveTools(cfg.AllowedTools), cfg.RBAC, cfg.IsDaemon, cfg.Name, cfg.Host, cfg.Tick, cfg.LookupFindings)
	if err != nil {
		return fmt.Errorf("building tools: %w", err)
	}

	msgParams := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: cfg.MaxTokens,
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(cfg.Prompt)),
		},
	}
	if cfg.SystemPrompt != "" {
		msgParams.System = []anthropic.BetaTextBlockParam{
			{Text: cfg.SystemPrompt},
		}
	}
	if cfg.Effort != "" {
		msgParams.OutputConfig = anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffort(cfg.Effort),
		}
	}

	params := anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: msgParams,
	}

	runner := client.Beta.Messages.NewToolRunnerStreaming(tools, params)

	turn := 0
	for events, err := range runner.AllStreaming(context.Background()) {
		if err != nil {
			return fmt.Errorf("runner error: %w", err)
		}
		turn++
		if cfg.MaxTurns > 0 && turn > cfg.MaxTurns {
			break
		}
		for event, err := range events {
			if err != nil {
				return fmt.Errorf("stream error: %w", err)
			}
			// Emit text deltas as they arrive.
			if delta, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
				if text, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta); ok && text.Text != "" {
					Emit(Event{
						Type:  EventText,
						Text:  text.Text,
						Turn:  turn,
						Agent: cfg.Name,
						Host:  cfg.Host,
						Tick:  cfg.Tick,
					})
				}
			}
		}
	}
	if err := runner.Err(); err != nil {
		return fmt.Errorf("runner: %w", err)
	}

	// Collect token usage from all turns.
	var totalInput, totalOutput int64
	var stopReason string
	for _, msg := range runner.Messages() {
		if msg.Role == "assistant" {
			// Messages() returns BetaMessageParam (input type), not BetaMessage (response type).
			// Token counts are only on the response type held by LastMessage().
			_ = msg
		}
	}
	if last := runner.LastMessage(); last != nil {
		totalInput = last.Usage.InputTokens
		totalOutput = last.Usage.OutputTokens
		stopReason = string(last.StopReason)
	}

	Emit(Event{
		Type:       EventDone,
		Provider:   "claude",
		Model:      cfg.Model,
		StopReason: stopReason,
		Usage:      &Usage{InputTokens: totalInput, OutputTokens: totalOutput},
		Agent:      cfg.Name,
		Host:       cfg.Host,
		Tick:       cfg.Tick,
		Turn:       turn,
	})

	return nil
}

// buildBetaTools creates a BetaTool for each entry in toolDefs.
// Each handler emits tool_call / tool_result JSONL events and delegates execution
// to ExecuteTool. Handlers run concurrently when the model issues parallel tool
// calls, so Emit uses a mutex internally.
//
// agent / host / tick are stamped onto every emitted event so the Control
// Plane can group conversation events under the right tick. They are
// captured by the closure rather than re-read from a shared cfg so tests
// that build tools without a Config still work.
func buildBetaTools(
	toolDefs []ToolDef, rbac *RBACPolicy, isDaemon bool,
	agentName, host string, tick int64,
	lookupFn func(ctx context.Context, query string, limit int) ([]LookupResult, error),
) ([]anthropic.BetaTool, error) {
	result := make([]anthropic.BetaTool, 0, len(toolDefs))
	for _, t := range toolDefs {
		schema := anthropic.BetaToolInputSchemaParam{
			Properties: t.Parameters["properties"],
			Required:   t.Required,
		}
		// Capture loop variable for the closure.
		td := t
		tool := toolrunner.NewBetaTool(
			td.Name,
			td.Description,
			schema,
			func(ctx context.Context, input map[string]interface{}) (anthropic.BetaToolResultBlockParamContentUnion, error) {
				Emit(Event{
					Type:     EventToolCall,
					ToolName: td.Name,
					Input:    input,
					Agent:    agentName, Host: host, Tick: tick,
				})

				// RBAC check before execution.
				if ok, reason := CheckRBAC(rbac, td.Name, input); !ok {
					EmitActionDenied(td.Name, input, reason, agentName, host, tick)
					denied := fmt.Sprintf("action denied: %s", reason)
					Emit(Event{
						Type: EventToolResult, ToolName: td.Name, Output: denied,
						Agent: agentName, Host: host, Tick: tick,
					})
					return anthropic.BetaToolResultBlockParamContentUnion{
						OfText: &anthropic.BetaTextBlockParam{Text: denied},
					}, nil
				}

				// Special-case lookup_findings: it talks to the management
				// plane, which the static dispatcher doesn't have access
				// to. Falls through to ExecuteTool (which returns "tool
				// not configured") when lookupFn is nil — daemons without
				// a CP shouldn't have this tool in their allowed set, but
				// we degrade gracefully if they do.
				var output string
				if td.Name == "lookup_findings" && lookupFn != nil {
					output = invokeLookupFindings(ctx, input, lookupFn)
				} else {
					output = ExecuteTool(td.Name, input)
				}

				Emit(Event{
					Type:     EventToolResult,
					ToolName: td.Name,
					Output:   output,
					Agent:    agentName, Host: host, Tick: tick,
				})

				// In daemon mode, also emit action_taken for downstream alerting.
				if isDaemon {
					Emit(Event{
						Type:     EventActionTaken,
						ToolName: td.Name,
						Input:    input,
						Output:   output,
						Agent:    agentName, Host: host, Tick: tick,
					})
				}

				return anthropic.BetaToolResultBlockParamContentUnion{
					OfText: &anthropic.BetaTextBlockParam{Text: output},
				}, nil
			},
		)
		result = append(result, tool)
	}
	return result, nil
}

