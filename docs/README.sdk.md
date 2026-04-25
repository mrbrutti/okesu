# Okesu — Go SDK Orchestrator

A single Go binary that runs a fully autonomous AI agent using either the Anthropic or OpenAI SDK. All tool calls execute without any sandbox or approval gate. Output streams as JSONL to stdout so any orchestrator — local or remote — can consume it line by line.

```
okesu claude "review ./src for vulnerabilities"
okesu codex  "find all SQL injection vectors in ./app"
```

---

## Project Structure

```
okesu/
├── main.go              CLI entry point, flag parsing, provider routing
├── go.mod
└── agent/
    ├── jsonl.go         JSONL event types and emitter
    ├── tools.go         Shared tool definitions + execution (bash, read, write, glob, grep)
    ├── claude.go        Anthropic SDK agentic loop
    └── openai.go        OpenAI SDK agentic loop
```

---

## Setup

### Install dependencies

```bash
go get github.com/anthropics/anthropic-sdk-go@latest
go get github.com/openai/openai-go@latest
```

### API keys

The app reads keys from the environment:

```bash
export ANTHROPIC_API_KEY=$(grep ANTHROPIC_API_KEY ~/.config/.env | cut -d= -f2 | tr -d '"')
export OPENAI_API_KEY=$(grep OPENAI_API_KEY ~/.config/.env | cut -d= -f2 | tr -d '"')
```

Or let your shell profile source `~/.config/.env` automatically.

### Build

```bash
go build -o okesu .

# Install globally
go install .
```

---

## Usage

```
okesu <claude|codex> [flags] "<prompt>"

Flags:
  --model <name>        Model override
  --system <path>       Path to system prompt file
  --agent <name>        Agent name — loads .claude/agents/<name>.md (claude only)
  --max-tokens <n>      Max tokens per response (default: 8192)
```

### Examples

```bash
# One-shot security review
okesu claude "review the codebase at ./src for security vulnerabilities"

# Use the security-reviewer agent
okesu claude --agent security-reviewer "audit ./api"

# Custom system prompt from a file
okesu codex --system ./my-agent.md "run all tests and fix failures"

# Specific model
okesu claude --model claude-opus-4-6 "refactor ./pkg/auth"
okesu codex  --model gpt-4o          "find hardcoded credentials in ./config"

# Stream output to a file and watch it live
okesu claude "review ./src" | tee session.jsonl | jq -r 'select(.type=="text") | .text'

# Pipe output to a remote orchestrator over SSH
okesu claude "audit ./api" | ssh orchestrator.internal "cat >> /logs/session.jsonl"
```

---

## JSONL Event Format

Every line written to stdout is a self-contained JSON object. Your orchestrator reads line by line and parses each.

### Event types

#### `init` — session started
```json
{"type":"init","provider":"claude","model":"claude-opus-4-6","ts":1745000000000}
```

#### `text` — text delta from the model
```json
{"type":"text","text":"I found a SQL injection vulnerability in","turn":1,"ts":1745000000100}
```
Text events arrive incrementally. Concatenate them per-turn to reconstruct the full response.

#### `tool_call` — model is invoking a tool
```json
{"type":"tool_call","tool_id":"toolu_01","tool_name":"bash","input":{"command":"grep -rn 'exec(' ./src"},"turn":2,"ts":1745000001000}
```

#### `tool_result` — tool execution output
```json
{"type":"tool_result","tool_id":"toolu_01","tool_name":"bash","output":"src/db.go:42:    db.Exec(query)\nsrc/db.go:87:    db.Exec(userInput)","turn":2,"ts":1745000001200}
```

#### `done` — session complete
```json
{"type":"done","provider":"claude","model":"claude-opus-4-6","stop_reason":"end_turn","usage":{"input_tokens":12400,"output_tokens":3800},"ts":1745000060000}
```

#### `error` — fatal error
```json
{"type":"error","error":"stream error: context deadline exceeded","ts":1745000005000}
```

### Consuming events in an orchestrator

```python
import json, subprocess, sys

proc = subprocess.Popen(
    ["okesu", "claude", "review ./src"],
    stdout=subprocess.PIPE,
    text=True,
)

for line in proc.stdout:
    event = json.loads(line)
    match event["type"]:
        case "text":
            sys.stdout.write(event["text"])
        case "tool_call":
            print(f"\n[tool] {event['tool_name']}: {event['input']}")
        case "tool_result":
            print(f"[result] {event['output'][:200]}")
        case "done":
            u = event.get("usage", {})
            print(f"\nDone — {u.get('input_tokens')}in / {u.get('output_tokens')}out tokens")
        case "error":
            print(f"ERROR: {event['error']}", file=sys.stderr)
```

```typescript
import { spawn } from "child_process";
import * as readline from "readline";

const proc = spawn("okesu", ["claude", "review ./src"]);
const rl = readline.createInterface({ input: proc.stdout });

rl.on("line", (line) => {
  const event = JSON.parse(line);
  if (event.type === "text") process.stdout.write(event.text);
  if (event.type === "done") console.log("\nDone", event.usage);
});
```

---

## Tools

The agent has access to five tools across both providers. All execute on the local machine with no restrictions.

| Tool | Description |
|------|-------------|
| `bash` | Execute any shell command. Returns combined stdout+stderr. |
| `read_file` | Read a file by path. |
| `write_file` | Write content to a path, creating directories as needed. |
| `list_files` | Glob pattern file listing. |
| `search` | Regex search across files (grep). |

Tool output is truncated to prevent context overflow: `bash` at 64KB, `read_file` at 128KB, `search` at 32KB.

---

## Architecture

### Agentic loop

Both providers follow the same pattern:

```
1. Send system prompt + user prompt → model
2. Stream response, emitting text deltas as EventText
3. On tool_use blocks → emit EventToolCall → execute → emit EventToolResult
4. Append tool results to message history
5. Repeat from 1 until model returns stop_reason = end_turn with no tool calls
6. Emit EventDone with token usage
```

### Anthropic SDK (`agent/claude.go`)

```go
import anthropic "github.com/anthropics/anthropic-sdk-go"

// Stream a response
stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
    Model:     anthropic.Model("claude-opus-4-6"),
    MaxTokens: 8192,
    System:    []anthropic.TextBlockParam{{Text: anthropic.String(systemPrompt)}},
    Messages:  messages,
    Tools:     tools,
})

// Accumulate while emitting text deltas
acc := anthropic.Message{}
for stream.Next() {
    event := stream.Current()
    if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
        if text, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok {
            emit(EventText, text.Text)
        }
    }
    acc.Accumulate(event)
}

// Process tool calls
for _, block := range acc.Content {
    if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
        result := ExecuteTool(tu.Name, parseInput(tu.JSON.Input.Raw()))
        toolResults = append(toolResults,
            anthropic.NewToolResultBlock(tu.ID, result, false),
        )
    }
}

// Feed results back
messages = append(messages, acc.ToParam())
messages = append(messages, anthropic.NewUserMessage(toolResults...))
```

Key types:
| Type | Purpose |
|------|---------|
| `anthropic.MessageNewParams` | Request params (model, messages, tools, system) |
| `anthropic.MessageParam` | A single turn in the conversation |
| `anthropic.ToolUnionParam` | Wraps custom tools (`OfTool`) or built-in tools (`OfBashTool20250124`) |
| `anthropic.ContentBlockDeltaEvent` | Streaming text/tool-input delta |
| `anthropic.TextDelta` | Text content delta |
| `anthropic.ToolUseBlock` | Tool call in the accumulated response |
| `anthropic.Message.Accumulate()` | Builds the full message from stream events |
| `anthropic.NewToolResultBlock()` | Creates a tool result to send back |

### OpenAI SDK (`agent/openai.go`)

```go
import "github.com/openai/openai-go"

// Stream a response
stream := client.Chat.Completions.NewStreaming(ctx,
    openai.ChatCompletionNewParams{
        Model:    openai.F(openai.ChatModel("gpt-4o")),
        Messages: openai.F(messages),
        Tools:    openai.F(tools),
    },
)

// Accumulate while emitting text deltas
acc := openai.ChatCompletionAccumulator{}
for stream.Next() {
    chunk := stream.Current()
    acc.AddChunk(chunk)
    if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
        emit(EventText, chunk.Choices[0].Delta.Content)
    }
}

// Process tool calls when finish_reason == "tool_calls"
if choice.FinishReason == "tool_calls" {
    for _, tc := range choice.Message.ToolCalls {
        result := ExecuteTool(tc.Function.Name, parseArgs(tc.Function.Arguments))
        messages = append(messages, openai.ToolMessage(tc.ID, result))
    }
}
```

Key types:
| Type | Purpose |
|------|---------|
| `openai.ChatCompletionNewParams` | Request params (model, messages, tools) |
| `openai.ChatCompletionMessageParamUnion` | A single turn (system/user/assistant/tool) |
| `openai.ChatCompletionToolParam` | Tool definition with JSON schema |
| `openai.ChatCompletionAccumulator` | Builds the full response from stream chunks |
| `openai.SystemMessage()` | Creates a system message param |
| `openai.UserMessage()` | Creates a user message param |
| `openai.ToolMessage()` | Creates a tool result message param |
| `choice.FinishReason` | `"stop"` or `"tool_calls"` |

---

## Provider Comparison

| | Claude (`claude.go`) | Codex (`openai.go`) |
|---|---|---|
| **SDK** | `github.com/anthropics/anthropic-sdk-go` | `github.com/openai/openai-go` |
| **Stream type** | `client.Messages.NewStreaming()` | `client.Chat.Completions.NewStreaming()` |
| **Accumulator** | `anthropic.Message{}` + `.Accumulate()` | `openai.ChatCompletionAccumulator{}` + `.AddChunk()` |
| **Text delta** | `ContentBlockDeltaEvent → TextDelta` | `chunk.Choices[0].Delta.Content` |
| **Tool call** | `ToolUseBlock` in `acc.Content` | `choice.Message.ToolCalls` when `finish_reason == "tool_calls"` |
| **Tool result** | `anthropic.NewToolResultBlock(id, output, false)` | `openai.ToolMessage(id, output)` |
| **System prompt** | `[]anthropic.TextBlockParam` in `MessageNewParams.System` | `openai.SystemMessage("...")` in messages slice |
| **Stop signal** | `acc.StopReason == "end_turn"` + no tool blocks | `choice.FinishReason == "stop"` |
| **Default model** | `claude-opus-4-6` | `gpt-4o` |
| **Key env var** | `ANTHROPIC_API_KEY` | `OPENAI_API_KEY` |

---

## SDK Reference Links

- Anthropic Go SDK: https://github.com/anthropics/anthropic-sdk-go
- Anthropic API docs: https://docs.anthropic.com/en/api/messages
- OpenAI Go SDK: https://github.com/openai/openai-go
- OpenAI API docs: https://platform.openai.com/docs/api-reference/chat
