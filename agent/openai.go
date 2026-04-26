package agent

import (
	"context"
	"encoding/json"
	"fmt"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// RunOpenAI runs a fully autonomous agentic loop against the OpenAI Responses API.
// It streams text deltas to stdout as JSONL and loops until the model stops issuing
// tool calls.
func RunOpenAI(cfg Config) error {
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
	)

	Emit(Event{
		Type:     EventInit,
		Provider: "codex",
		Model:    cfg.Model,
		Agent:    cfg.Name,
		Host:     cfg.Host,
		Tick:     cfg.Tick,
	})

	tools := buildResponsesTools(ActiveTools(cfg.AllowedTools))

	// Build base request params (reused each turn).
	baseParams := responses.ResponseNewParams{
		Model: shared.ResponsesModel(cfg.Model),
		Tools: tools,
	}
	if cfg.SystemPrompt != "" {
		baseParams.Instructions = param.NewOpt(cfg.SystemPrompt)
	}
	if cfg.MaxTokens > 0 {
		baseParams.MaxOutputTokens = param.NewOpt(cfg.MaxTokens)
	}
	if cfg.Effort != "" {
		baseParams.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(cfg.Effort),
		}
	}

	// Seed the first request with the user prompt as a string input.
	firstParams := baseParams
	firstParams.Input = responses.ResponseNewParamsInputUnion{
		OfString: param.NewOpt(cfg.Prompt),
	}

	var totalInput, totalOutput int64
	turn := 0
	var prevResponseID string
	var completedResp responses.Response

	currentParams := firstParams

	for {
		turn++

		// Stream the response so text deltas reach the caller in real time.
		stream := client.Responses.NewStreaming(context.Background(), currentParams)

		completedResp = responses.Response{}
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.output_text.delta":
				delta := event.AsResponseOutputTextDelta()
				if delta.Delta != "" {
					Emit(Event{
						Type: EventText, Text: delta.Delta, Turn: turn,
						Agent: cfg.Name, Host: cfg.Host, Tick: cfg.Tick,
					})
				}
			case "response.completed":
				completedResp = event.AsResponseCompleted().Response
			}
		}
		if err := stream.Err(); err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		prevResponseID = completedResp.ID
		if completedResp.Usage.InputTokens > 0 {
			totalInput += completedResp.Usage.InputTokens
			totalOutput += completedResp.Usage.OutputTokens
		}

		// Collect any tool calls from output.
		var toolCalls []responses.ResponseFunctionToolCall
		for _, item := range completedResp.Output {
			if item.Type == "function_call" {
				toolCalls = append(toolCalls, item.AsFunctionCall())
			}
		}

		// No tool calls — the model is done.
		if len(toolCalls) == 0 {
			break
		}

		// MaxTurns cap reached — stop before executing more tools.
		if cfg.MaxTurns > 0 && turn >= cfg.MaxTurns {
			break
		}

		// Execute each tool and build the result input items.
		resultItems := make([]responses.ResponseInputItemUnionParam, 0, len(toolCalls))
		for _, tc := range toolCalls {
			var input map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
				input = map[string]interface{}{"raw": tc.Arguments}
			}

			Emit(Event{
				Type:     EventToolCall,
				ToolID:   tc.CallID,
				ToolName: tc.Name,
				Input:    input,
				Turn:     turn,
				Agent:    cfg.Name, Host: cfg.Host, Tick: cfg.Tick,
			})

			var output string
			if ok, reason := CheckRBAC(cfg.RBAC, tc.Name, input); !ok {
				EmitActionDenied(tc.Name, input, reason, cfg.Name, cfg.Host, cfg.Tick)
				output = fmt.Sprintf("action denied: %s", reason)
			} else {
				if tc.Name == "lookup_findings" && cfg.LookupFindings != nil {
					output = invokeLookupFindings(context.Background(), input, cfg.LookupFindings)
				} else {
					output = ExecuteTool(tc.Name, input)
				}
				// In daemon mode, also emit action_taken for downstream alerting.
				if cfg.IsDaemon {
					Emit(Event{
						Type:     EventActionTaken,
						ToolName: tc.Name,
						Input:    input,
						Output:   output,
						Turn:     turn,
						Agent:    cfg.Name, Host: cfg.Host, Tick: cfg.Tick,
					})
				}
			}

			Emit(Event{
				Type:     EventToolResult,
				ToolID:   tc.CallID,
				ToolName: tc.Name,
				Output:   output,
				Turn:     turn,
				Agent:    cfg.Name, Host: cfg.Host, Tick: cfg.Tick,
			})

			resultItems = append(resultItems, responses.ResponseInputItemParamOfFunctionCallOutput(
				tc.CallID, output,
			))
		}

		// Next turn: attach tool results and reference the previous response.
		nextParams := baseParams
		nextParams.PreviousResponseID = param.NewOpt(prevResponseID)
		nextParams.Input = responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam(resultItems),
		}
		currentParams = nextParams
	}

	stopReason := string(completedResp.Status)

	Emit(Event{
		Type:       EventDone,
		Provider:   "codex",
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

// buildResponsesTools converts a ToolDef slice into Responses API tool params.
func buildResponsesTools(toolDefs []ToolDef) []responses.ToolUnionParam {
	params := make([]responses.ToolUnionParam, len(toolDefs))
	for i, t := range toolDefs {
		schema := buildResponsesSchema(t)
		desc := param.NewOpt(t.Description)
		params[i] = responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        t.Name,
				Description: desc,
				Parameters:  schema,
			},
		}
	}
	return params
}

// buildResponsesSchema constructs a JSON Schema map from a ToolDef.
func buildResponsesSchema(t ToolDef) map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": t.Parameters["properties"],
	}
	if len(t.Required) > 0 {
		schema["required"] = t.Required
	}
	return schema
}
