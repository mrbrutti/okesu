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
	Name         string // agent name (from agent file; used in daemon events)
	Provider     string
	Model        string
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
	})

	tools, err := buildBetaTools(ActiveTools(cfg.AllowedTools), cfg.RBAC)
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
					Emit(Event{Type: EventText, Text: text.Text, Turn: turn})
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
	})

	return nil
}

// buildBetaTools creates a BetaTool for each entry in toolDefs.
// Each handler emits tool_call / tool_result JSONL events and delegates execution
// to ExecuteTool. Handlers run concurrently when the model issues parallel tool
// calls, so Emit uses a mutex internally.
func buildBetaTools(toolDefs []ToolDef, rbac *RBACPolicy) ([]anthropic.BetaTool, error) {
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
				})

				// RBAC check before execution.
				if ok, reason := CheckRBAC(rbac, td.Name, input); !ok {
					EmitActionDenied(td.Name, input, reason)
					denied := fmt.Sprintf("action denied: %s", reason)
					Emit(Event{Type: EventToolResult, ToolName: td.Name, Output: denied})
					return anthropic.BetaToolResultBlockParamContentUnion{
						OfText: &anthropic.BetaTextBlockParam{Text: denied},
					}, nil
				}

				output := ExecuteTool(td.Name, input)

				Emit(Event{
					Type:     EventToolResult,
					ToolName: td.Name,
					Output:   output,
				})

				return anthropic.BetaToolResultBlockParamContentUnion{
					OfText: &anthropic.BetaTextBlockParam{Text: output},
				}, nil
			},
		)
		result = append(result, tool)
	}
	return result, nil
}

