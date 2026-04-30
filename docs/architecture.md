# Okesu Architecture

Okesu is a fully autonomous AI agent runner written in Go. It exposes a single binary with four subcommands (`claude`, `codex`, `auto`, `daemon`) that drive an agentic loop against either the Anthropic or OpenAI API. The agent executes tools — bash, file I/O, search — on the local machine with no sandbox and streams all activity as JSONL to stdout (or to configured sinks).

---

## Table of Contents

1. [High-Level Overview](#1-high-level-overview)
2. [Package Structure](#2-package-structure)
3. [CLI Layer — `main.go`](#3-cli-layer--maingo)
4. [Agent File System](#4-agent-file-system)
5. [Configuration Pipeline](#5-configuration-pipeline)
6. [Tool System — `agent/tools.go`](#6-tool-system--agenttoolsgo)
7. [Claude Runner — `agent/claude.go`](#7-claude-runner--agentclaudego)
8. [OpenAI Runner — `agent/openai.go`](#8-openai-runner--agentopeniago)
9. [JSONL Event Bus — `agent/jsonl.go`](#9-jsonl-event-bus--agentjsonlgo)
10. [Output Sinks — `agent/sinks.go` + `agent/buffer.go`](#10-output-sinks--agentsinksgo--agentbuffergo)
11. [Provider Inference — `auto` Command](#11-provider-inference--auto-command)
12. [Daemon Mode — `agent/daemon.go`](#12-daemon-mode--agentdaemongo)
13. [Pre-Collectors — `agent/collectors.go`](#13-pre-collectors--agentcollectorsgo)
14. [Persistent State — `agent/state.go`](#14-persistent-state--agentstatego)
15. [RBAC Action Filtering — `agent/rbac.go`](#15-rbac-action-filtering--agentrbacgo)
16. [Management Plane — `agent/mgmt.go`](#16-management-plane--agentmgmtgo)
17. [Deployment — systemd + install script](#17-deployment--systemd--install-script)
18. [End-to-End Request Flow](#18-end-to-end-request-flow)
19. [Concurrency Model](#19-concurrency-model)
20. [Data Structures Reference](#20-data-structures-reference)
21. [Control Plane Data Architecture](#21-control-plane-data-architecture)
22. [Lifecycle Management (Phase 7)](#22-lifecycle-management-phase-7)
23. [Hexagonal Architecture (Phase 8 — Ports + Adapters)](#23-hexagonal-architecture-phase-8--ports--adapters)

---

## 1. High-Level Overview

```mermaid
graph TD
    DotEnv["~/.config/.env\n(optional)"] --> |"loadDotEnv()\nat startup"| Env["process environment"]
    Env --> CLI

    CLI["okesu CLI\n(main.go)"] --> |"task mode"| Config["Config struct"]
    CLI --> |"daemon mode"| Daemon["RunDaemon()\nagent/daemon.go"]

    Config --> |"provider=claude"| RunClaude["RunClaude()\nagent/claude.go"]
    Config --> |"provider=codex"| RunOpenAI["RunOpenAI()\nagent/openai.go"]

    Daemon --> |"each tick"| Collectors["RunCollectors()\nagent/collectors.go"]
    Collectors --> |"TickContext"| Template["RenderPrompt()\nGo template"]
    Template --> |"rendered system prompt"| RunClaude
    Template --> |"rendered system prompt"| RunOpenAI

    RunClaude --> |"BetaToolRunnerStreaming"| AnthropicAPI["Anthropic API\n/v1/messages"]
    RunOpenAI --> |"Responses API"| OpenAIAPI["OpenAI API\n/v1/responses"]

    AnthropicAPI --> |"tool_call"| RBAC["CheckRBAC()\nagent/rbac.go"]
    OpenAIAPI --> |"function_call"| RBAC
    RBAC --> |"allowed"| Tools["ExecuteTool()\nagent/tools.go"]
    RBAC --> |"denied"| Denied["EventActionDenied"]

    Tools --> |"bash/read_file\nwrite_file/list_files/search"| OS["Local OS"]
    OS --> |"output"| Tools

    RunClaude --> Emit["Emit()\nagent/jsonl.go"]
    RunOpenAI --> Emit
    Tools --> Emit
    Daemon --> Emit

    Emit --> Sinks["FanoutSink\nagent/sinks.go"]
    Sinks --> Stdout["StdoutSink"]
    Sinks --> File["JSONLFileSink\n(rotating)"]
    Sinks --> Webhook["WebhookSink\n(HMAC + retry)"]

    Daemon --> State["DaemonState\nagent/state.go"]
    Daemon --> Mgmt["MgmtPlane\nagent/mgmt.go"]
```

**Key design decisions:**

- **No sandbox.** Every tool call executes directly on the host. RBAC policies (allow/deny lists) are the only execution gate.
- **JSONL protocol.** Every event — text delta, tool call, tool result, session end — is a single JSON line. All sinks receive the same stream.
- **Provider abstraction.** A single `Config` struct is handed to either `RunClaude` or `RunOpenAI`. Runners handle all SDK translation internally.
- **Agent files as configuration.** `.md` files with YAML frontmatter carry every runtime option — model, tools, schedule, collectors, RBAC, outputs, mgmt plane.
- **Daemon mode.** A persistent tick loop runs the agentic loop on a cron or interval schedule. Pre-collectors inject host data into the system prompt before each tick.

---

## 2. Package Structure

```
okesu/
├── main.go                        # CLI entry point (Cobra commands)
├── go.mod                         # Module: github.com/section9labs/okesu
│
├── agent/
│   ├── claude.go                  # Anthropic agentic loop + Config struct
│   ├── openai.go                  # OpenAI agentic loop (Responses API)
│   ├── tools.go                   # Tool definitions, execution, AgentDef parsing
│   ├── jsonl.go                   # JSONL event types and Emit()
│   ├── daemon.go                  # Daemon loop, scheduler, DaemonConfig
│   ├── collectors.go              # Pre-collector execution and template rendering
│   ├── state.go                   # Persistent daemon state, dedup cache
│   ├── buffer.go                  # MemoryBuffer (ring), FileBuffer (rotating), OutputDef
│   ├── sinks.go                   # Sink interface, StdoutSink, JSONLFileSink,
│   │                              #   WebhookSink, FilteredSink, FanoutSink, global sink
│   ├── rbac.go                    # RBACPolicy, CheckRBAC, EmitActionDenied
│   ├── mgmt.go                    # Management plane: mTLS, register, heartbeat, poll
│   ├── api_policy.go              # APIUnavailablePolicy interface (Control Plane hook)
│   └── apierr.go                  # classifyRunError — SDK/network error detection
│
├── examples/
│   └── agents/
│       └── edr.md                 # Sample EDR daemon agent file
│
├── systemd/
│   └── okesu-agent@.service       # Systemd template unit
│
├── scripts/
│   └── install.sh                 # Install binary, user, dirs, and systemd unit
│
├── Dockerfile                     # Multi-stage build: Go builder → Debian slim runtime
├── .dockerignore
│
├── .claude/
│   ├── settings.local.json
│   └── agents/
│       ├── security-reviewer.md   # Example agent file
│       └── edr.md                 # Symlink → examples/agents/edr.md
│
└── docs/
    ├── architecture.md            # This document
    └── daemon.design.md           # Daemon design discussion
```

**Dependencies:**

| Package | Version | Role |
|---|---|---|
| `github.com/anthropics/anthropic-sdk-go` | v1.37.0 | Anthropic API + BetaToolRunner |
| `github.com/openai/openai-go/v3` | v3.32.0 | OpenAI Responses API |
| `github.com/spf13/cobra` | v1.10.2 | CLI framework |
| `github.com/robfig/cron/v3` | v3.0.1 | Cron expression parsing |
| `gopkg.in/yaml.v3` | v3.0.1 | Agent file frontmatter |

---

## 3. CLI Layer — `main.go`

Okesu uses [Cobra](https://github.com/spf13/cobra) to define four subcommands. All share the same flag set and configuration pipeline.

### Startup Sequence

```mermaid
flowchart LR
    main["main()"] --> LD["loadDotEnv()\nload ~/.config/.env"]
    LD --> RC["rootCmd().Execute()"]
    RC --> Cmd["claude | codex | auto | daemon\nRunE handler"]
```

### Environment Loading — `.env` file

`loadDotEnv()` reads `~/.config/.env` at startup and populates missing env vars. Existing shell exports always take precedence.

**Supported formats:**
```bash
ANTHROPIC_API_KEY=sk-ant-abc123
OPENAI_API_KEY="sk-abc123"
export SOME_VAR=value
# comment line — ignored
```

### Command Tree

```mermaid
graph LR
    okesu --> claude["claude &lt;prompt&gt;\nAnthropic API"]
    okesu --> codex["codex &lt;prompt&gt;\nOpenAI API"]
    okesu --> auto["auto &lt;prompt&gt;\nInfers provider"]
    okesu --> daemon["daemon\n--agent required\nno prompt arg"]
```

### Shared Flags

All subcommands share:

| Flag | Default | Description |
|---|---|---|
| `--model` | | Model override |
| `--system` | | Path to system prompt file |
| `--agent` | | Agent file name (searches `.claude/agents/`, `~/.claude/agents/`, etc.) |
| `--api-key` | | API key override |
| `--max-tokens` | 8192 | Token cap per response |
| `--effort` | | Reasoning depth: `low\|medium\|high\|xhigh\|max` |
| `--max-turns` | 0 | Loop cap (0 = unlimited) |

The `daemon` subcommand adds:

| Flag | Description |
|---|---|
| `--interval` | Override tick interval (e.g. `30s`, `5m`) |
| `--cron` | Override cron schedule (e.g. `"0 * * * *"`) |

### Flag Precedence

```
CLI flag  >  agent file frontmatter  >  built-in default
```

### API Key Resolution

```mermaid
flowchart LR
    F{"--api-key\nflag?"}
    F --> |"yes"| Use["use flag"]
    F --> |"no"| E{"ANTHROPIC_API_KEY\nor OPENAI_API_KEY\n(shell or .env)"}
    E --> |"set"| Use
    E --> |"not set"| Err["error"]
```

---

## 4. Agent File System

Agent files are Markdown with YAML frontmatter. They encode both persona (body = system prompt) and full runtime configuration (frontmatter).

### File Discovery

`ParseAgentFile(name)` searches four directories, stopping at the first match:

```
1. .claude/agents/<name>.md       (project-local)
2. .codex/agents/<name>.md        (project-local)
3. ~/.claude/agents/<name>.md     (user-global)
4. ~/.codex/agents/<name>.md      (user-global)
```

### Full Frontmatter Schema

```yaml
---
name: edr-agent
description: Endpoint detection and response daemon
model: claude-opus-4-6
provider: claude               # "claude" | "codex"
tools: [bash, read_file, search]
maxTurns: 50
effort: high

# Daemon scheduling
mode: daemon
interval: 60s                  # OR use cron:
cron: "*/5 * * * *"
overlap: skip                  # "skip" | "queue"
stateDir: /var/lib/okesu/edr
dedupeTtl: 1h

# Pre-collectors (run before each tick)
collectors:
  - name: processes
    command: ps aux
    timeout: 10s
  - name: netstat
    command: ss -tnp
    timeout: 10s

# Output sinks
outputs:
  - type: stdout
  - type: file
    path: /var/log/okesu/edr.jsonl
    maxBytes: 104857600         # 100 MB rotation
  - type: webhook
    url: https://siem.example.com/ingest
    secret: hmac-signing-secret
    retries: 3

# RBAC action policy
actions:
  rbac:
    deny:
      - tool: bash
        reason: no arbitrary shell in production
    allow:
      - tool: read_file
      - tool: search

# Management plane
management:
  url: https://mgmt.example.com
  heartbeatSec: 30
  pollSec: 60
  certDir: /etc/okesu
---

You are an autonomous EDR agent. Your role is to...

<!-- Go template directives are expanded before each tick: -->
Current time: {{ .TickTime }}
Host: {{ .HostID }} | Region: {{ .CloudRegion }}
Last run: {{ .LastRunISO }}

=== Running Processes ===
{{ (index .Collectors "processes").Output }}

=== Network Connections ===
{{ (index .Collectors "connections").Output }}
```

### AgentDef Struct (full)

```go
type AgentDef struct {
    // Task mode
    Name, Description, Model, Provider string
    Tools    []string
    MaxTurns int
    Effort   string

    // Daemon scheduling
    Mode, Interval, Cron, Overlap, StateDir, DedupeTTL string

    // Pre-collectors
    Collectors []CollectorDef

    // Output sinks
    Outputs []OutputDef

    // RBAC
    Actions ActionsConfig

    // Management plane
    Mgmt MgmtConfig

    Body string  // system prompt (after frontmatter)
}
```

---

## 5. Configuration Pipeline

```mermaid
flowchart TD
    Cmd["Cobra RunE"] --> RA["resolveAgent(cmd)"]
    RA --> PAF["ParseAgentFile → *AgentDef"]
    RA --> SF["os.ReadFile(--system) → &AgentDef{Body}"]
    RA --> Nil["nil (no agent/system)"]

    PAF --> BC["buildConfig()"]
    SF --> BC
    Nil --> BC

    BC --> Config["agent.Config{\n  Name, Provider, Model, Prompt,\n  SystemPrompt, MaxTokens, Effort,\n  MaxTurns, AllowedTools, RBAC, APIKey\n}"]

    BC -.daemon only.-> BDC["buildDaemonConfig()"]
    BDC --> DConfig["agent.DaemonConfig{\n  Interval/Cron, Overlap, StateDir,\n  DedupeTTL, Collectors, Outputs, Mgmt\n}"]
```

---

## 6. Tool System — `agent/tools.go`

### The Five Built-in Tools

| Tool | Action | Truncation |
|---|---|---|
| `bash` | `exec.Command("bash", "-c", cmd)` | 64 KB |
| `read_file` | `os.ReadFile(path)` | 128 KB |
| `write_file` | `os.WriteFile(path, content, 0644)` | none |
| `list_files` | `filepath.Glob(pattern)` | none |
| `search` | `grep -rn pattern [--include glob]` | 32 KB |

### Tool Filtering via `ActiveTools`

When `AllowedTools` is non-empty, only listed tools are exposed to the model. Accepts both okesu names and Claude Code CLI names (`Bash`, `Read`, `Edit`, `Glob`, `Grep`).

```go
func ActiveTools(names []string) []ToolDef  // empty = all tools
```

---

## 7. Claude Runner — `agent/claude.go`

Uses the Anthropic SDK's `BetaToolRunnerStreaming`, which manages the multi-turn tool loop and parallel tool execution internally.

### Tool Handler with RBAC

```go
func(ctx context.Context, input map[string]interface{}) (anthropic.BetaToolResultBlockParamContentUnion, error) {
    Emit(Event{Type: EventToolCall, ToolName: td.Name, Input: input})

    // RBAC check before execution
    if ok, reason := CheckRBAC(rbac, td.Name, input); !ok {
        EmitActionDenied(td.Name, input, reason)
        denied := "action denied: " + reason
        Emit(Event{Type: EventToolResult, ToolName: td.Name, Output: denied})
        return anthropic.BetaToolResultBlockParamContentUnion{
            OfText: &anthropic.BetaTextBlockParam{Text: denied},
        }, nil
    }

    output := ExecuteTool(td.Name, input)
    Emit(Event{Type: EventToolResult, ToolName: td.Name, Output: output})
    return anthropic.BetaToolResultBlockParamContentUnion{
        OfText: &anthropic.BetaTextBlockParam{Text: output},
    }, nil
}
```

### Effort / Extended Thinking

```go
msgParams.OutputConfig = anthropic.BetaOutputConfigParam{
    Effort: anthropic.BetaOutputConfigEffort(cfg.Effort),  // "low"|"medium"|"high"|"xhigh"|"max"
}
```

---

## 8. OpenAI Runner — `agent/openai.go`

Uses the **Responses API** (`/v1/responses`). The tool loop is implemented manually; the server retains conversation state via `previous_response_id`.

### Multi-Turn Loop

```mermaid
sequenceDiagram
    participant Runner as RunOpenAI()
    participant API as /v1/responses

    Runner->>API: Turn 1: Input = prompt string
    API-->>Runner: SSE deltas + response.completed
    Runner->>Runner: extract function_calls, CheckRBAC, ExecuteTool
    Runner->>API: Turn N: PreviousResponseID + tool results
    Note over Runner,API: repeat until no function_calls
    Runner->>Runner: Emit(EventDone)
```

### Effort / Reasoning

```go
baseParams.Reasoning = shared.ReasoningParam{
    Effort: shared.ReasoningEffort(cfg.Effort),  // "low"|"medium"|"high"|"xhigh"
}
```

---

## 9. JSONL Event Bus — `agent/jsonl.go`

All activity is serialized as JSONL. `Emit()` routes every event through the active global sink (see §10).

### Event Types

| Event | When |
|---|---|
| `init` | Session started |
| `text` | Streaming text delta |
| `tool_call` | Model invoking a tool |
| `tool_result` | Tool execution completed |
| `done` | Session complete with usage |
| `error` | Fatal error |
| `daemon_start` | Daemon process started |
| `daemon_stop` | Clean shutdown |
| `tick_start` | Tick beginning |
| `tick_done` | Tick complete (`completed\|skipped\|error`) |
| `collector_result` | Pre-collector command finished |
| `finding` | Agent-reported finding |
| `action_taken` | Tool executed after RBAC allow (daemon mode) |
| `action_denied` | RBAC blocked a tool call |
| `api_unavailable` | AI provider API unreachable or errored |
| `config_reloaded` | Config reloaded via SIGHUP or management plane |

### Full Event Schema

```go
type Event struct {
    Type       EventType   `json:"type"`
    Provider   string      `json:"provider,omitempty"`
    Model      string      `json:"model,omitempty"`
    Text       string      `json:"text,omitempty"`
    ToolID     string      `json:"tool_id,omitempty"`
    ToolName   string      `json:"tool_name,omitempty"`
    Input      interface{} `json:"input,omitempty"`
    Output     string      `json:"output,omitempty"`
    Error      string      `json:"error,omitempty"`
    StopReason string      `json:"stop_reason,omitempty"`
    Usage      *Usage      `json:"usage,omitempty"`
    Turn       int         `json:"turn,omitempty"`
    Ts         int64       `json:"ts"`                      // Unix ms

    // Daemon lifecycle
    Agent        string `json:"agent,omitempty"`             // agent name
    Host         string `json:"host,omitempty"`              // hostname
    Tick         int64  `json:"tick,omitempty"`              // tick sequence number
    Result       string `json:"result,omitempty"`            // tick_done: completed|skipped|error
    Duration     string `json:"duration,omitempty"`          // tick_done: wall time e.g. "1.4s"
    Findings     int    `json:"findings,omitempty"`          // tick_done: finding count
    ActionsTaken int    `json:"actions_taken,omitempty"`     // tick_done: allowed tool call count

    // Collector
    Collector string `json:"collector,omitempty"`            // collector name
    Bytes     int    `json:"bytes,omitempty"`                // output size

    // Finding
    Severity  string `json:"severity,omitempty"`             // critical|high|medium|low|info
    Title     string `json:"title,omitempty"`                // short human-readable title
    Evidence  string `json:"evidence,omitempty"`             // raw telemetry lines
    Resource  string `json:"resource,omitempty"`             // affected resource (pid:N, path:...)
    DedupKey  string `json:"dedup_key,omitempty"`            // dedup cache key

    // RBAC / action
    Reason string `json:"reason,omitempty"`                  // action_denied: why blocked

    // API unavailability
    StatusCode int `json:"status_code,omitempty"`            // HTTP status (0 = network error)
}
```

---

## 10. Output Sinks — `agent/sinks.go` + `agent/buffer.go`

All events flow through a pluggable global sink. The default (task mode) is `StdoutSink`. Daemon mode initializes `BuildSinks(dcfg.Outputs)` at startup and replaces the global sink before the first `Emit`.

### Sink Interface

```go
type Sink interface {
    Write(line []byte) error
    Close() error
}
```

### Sink Types

```mermaid
graph LR
    FanoutSink --> FS1["FilteredSink\n(optional per-sink)"]
    FanoutSink --> FS2["FilteredSink"]
    FanoutSink --> FS3["FilteredSink"]
    FS1 --> StdoutSink["StdoutSink\nfmt.Printf + mutex"]
    FS2 --> JSONLFileSink["JSONLFileSink\nFileBuffer\n(rotating JSONL)"]
    FS3 --> WebhookSink["WebhookSink\nHMAC-SHA256\nasync queue\nexponential backoff"]
```

### FilteredSink

Each sink can be wrapped in a `FilteredSink` that only forwards events whose `type` is in an allow-list. Configured via the `events:` field on an output definition. An empty or absent `events` field means all events are forwarded.

```yaml
outputs:
  - type: stdout                           # all events
  - type: file
    path: /var/log/okesu/agent.jsonl
    events: [finding, action_taken, error]  # filtered
  - type: webhook
    url: https://siem.example.com/events
    events: [finding, action_denied]        # filtered — no tick noise
```

### WebhookSink Architecture

```mermaid
flowchart LR
    Emit["Emit()"] --> Q["channel queue\n(cap 512)"]
    Q --> D["deliver()\ngoroutine"]
    D --> |"attempt 1..N"| HTTP["POST\nX-Okesu-Signature: sha256=..."]
    Emit --> Ring["MemoryBuffer\n(ring buffer)\nreplay on reconnect"]
```

**HMAC signing:**
```go
mac := hmac.New(sha256.New, []byte(s.secret))
mac.Write(line)
req.Header.Set("X-Okesu-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
```

### FileBuffer Rotation

When the log file exceeds `maxBytes`, it is renamed to `<path>.1` and a new file is opened. Only one backup is kept.

### Agent File Configuration

```yaml
outputs:
  - type: stdout
  - type: file
    path: /var/log/okesu/agent.jsonl
    maxBytes: 104857600
  - type: webhook
    url: https://siem.example.com/events
    secret: my-hmac-secret
    retries: 3
    bufferCap: 1024
```

---

## 11. Provider Inference — `auto` Command

```mermaid
flowchart TD
    Start["inferProvider(model, def)"] --> A{"def.Provider\nin frontmatter?"}
    A --> |"claude/anthropic"| Claude["claude"]
    A --> |"codex/openai"| Codex["codex"]
    A --> |"not set"| B{"model prefix?"}
    B --> |"claude-*"| Claude
    B --> |"gpt-*/o1-*/o2-*/o3-*/o4-*"| Codex
    B --> |"unknown"| C{"API keys?"}
    C --> |"only ANTHROPIC_API_KEY"| Claude
    C --> |"only OPENAI_API_KEY"| Codex
    C --> |"both or neither"| Err["error"]
```

---

## 12. Daemon Mode — `agent/daemon.go`

Daemon mode runs an endless agentic loop on a configurable schedule. Each iteration (tick) runs the full pipeline: collect → render → run → persist.

### Daemon Lifecycle

```mermaid
flowchart TD
    Start["RunDaemon()"] --> Sinks["BuildSinks → SetGlobalSink"]
    Sinks --> State["LoadState(stateDir)"]
    State --> Mgmt["NewMgmtPlane → Register\n+ StartHeartbeat\n+ StartConfigPoller"]
    Mgmt --> Schedule["buildSchedule\n(cron or interval)"]
    Schedule --> Loop["event loop"]

    Loop --> Timer["timer fires"]
    Timer --> TryLock{"tickMu.TryLock()"}
    TryLock --> |"locked"| Goroutine["go execTick(...)"]
    TryLock --> |"busy — skip"| Emit["Emit tick_done skipped"]
    Goroutine --> Unlock["tickMu.Unlock()"]

    Loop --> Signal["signal received"]
    Signal --> |"SIGHUP"| Reload["re-parse agent file\nhot-apply MaxTurns/Effort"]
    Signal --> |"SIGTERM/SIGINT"| Drain["tickMu.Lock() — wait\nEmit daemon_stop\nreturn nil"]
```

### Tick Execution (`execTick`)

```mermaid
flowchart TD
    T["execTick()"] --> PS["state.PruneDedup()"]
    PS --> RC["RunCollectors() — parallel"]
    RC --> |"non-optional failure"| AbortErr["Emit error + tick_done error"]
    RC --> |"ok"| BTC["BuildTickContext()"]
    BTC --> RP["RenderPrompt(systemPrompt, tctx)"]
    RP --> Touch["touchLastRun(stateDir)"]
    Touch --> Run["RunClaude() or RunOpenAI()"]
    Run --> |"success"| Rec["state.RecordTick(stateDir)"]
    Run --> |"API error"| Classify["classifyRunError()"]
    Run --> |"local error"| LocalErr["EmitError()"]
    Classify --> APIUnavail["Emit api_unavailable\n+ APIPolicy.OnAPIUnavailable()"]
    APIUnavail --> Rec
    LocalErr --> Rec
    Rec --> Done["Emit tick_done"]
```

### Schedule Configuration

Priority (highest to lowest):
1. `--cron` CLI flag
2. `--interval` CLI flag
3. `cron:` in agent file
4. `interval:` in agent file
5. Default: 60 seconds

### Overlap Policy

`overlap: skip` (default) — if a tick is still running when the next fires, the new tick is skipped with a `tick_done result=skipped` event. The `sync.Mutex.TryLock()` pattern serves double duty: overlap detection on the timer path and clean-shutdown wait on the signal path.

### API Unavailability — `agent/apierr.go` + `agent/api_policy.go`

When `RunClaude()` or `RunOpenAI()` returns an error, `classifyRunError()` inspects the error chain to distinguish provider failures from local errors:

| Match | isAPIError | statusCode |
|---|---|---|
| `*anthropic.Error` | true | HTTP status (429, 5xx, etc.) |
| `*openai.Error` | true | HTTP status |
| `*url.Error` (DNS, TLS, timeout) | true | 0 |
| anything else | false | — |

When `isAPIError` is true, `execTick` emits `EventAPIUnavailable` (with `status_code` and `error` fields) and invokes `DaemonConfig.APIPolicy.OnAPIUnavailable()`. When false, the error is treated as a local error via `EmitError()`.

`APIUnavailablePolicy` is the Control Plane integration hook. The default is `NoopAPIPolicy` (do nothing). A future Control Plane implementation can supply a concrete policy that buffers prompts, switches providers, or escalates.

```go
type APIUnavailablePolicy interface {
    OnAPIUnavailable(ctx context.Context, provider, model string, statusCode int, err error)
}
```

---

## 13. Pre-Collectors — `agent/collectors.go`

Pre-collectors are shell commands that run in parallel before each agentic tick. Their output is injected into the system prompt via Go templates, giving the model fresh host context every cycle.

### Execution Flow

```mermaid
flowchart LR
    Tick["execTick"] --> RC["RunCollectors(collectors)"]
    RC --> |"goroutine 1"| C1["bash -c 'ps aux'\ntimeout 10s"]
    RC --> |"goroutine 2"| C2["bash -c 'ss -tnp'\ntimeout 10s"]
    RC --> |"goroutine N"| CN["..."]
    C1 --> Merge["[]CollectorResult"]
    C2 --> Merge
    CN --> Merge
    Merge --> BTC["BuildTickContext()"]
    BTC --> RP["RenderPrompt(body, TickContext)"]
```

### TickContext Template Data

```go
type TickContext struct {
    // Convenience string aliases (design-specified names for templates)
    HostID      string    // hostname
    CloudRegion string    // AWS/GCP/Azure region or "unknown"
    AgentName   string    // from agent file frontmatter
    TickTime    string    // RFC3339 timestamp of this tick
    LastRunISO  string    // ISO 8601 timestamp of the previous tick
    LastRunFile string    // absolute path to last_run sentinel file
    StateDir    string    // state directory path

    // Strongly-typed fields
    Tick           int64
    Time           time.Time
    Host           string
    Agent          string
    LastRunAt      time.Time
    Collectors     map[string]CollectorResult  // indexed: {{ (index .Collectors "name").Output }}
    CollectorsList []CollectorResult           // iterable: {{ range .CollectorsList }}
}
```

### Template Example

```markdown
---
collectors:
  - name: processes
    command: "ps aux --no-headers"
    timeout: 5s
  - name: connections
    command: "ss -tulnp"
    timeout: 5s
---
You are an EDR agent on host {{ .HostID }} in {{ .CloudRegion }}.
Tick: {{ .Tick }} | Time: {{ .TickTime }} | Last run: {{ .LastRunISO }}

## Telemetry

{{ range .CollectorsList }}
### {{ .Name }}{{ if .Error }} — ERROR: {{ .Error }}{{ end }}{{ if .Skipped }} (skipped){{ end }}
` ` `
{{ .Output }}
` ` `
{{ end }}
```

**Available template functions:**
- `{{ now }}` — current UTC time as RFC3339
- `{{ env "VAR_NAME" }}` — read environment variable
- `{{ cloudRegion }}` — cloud region from `AWS_DEFAULT_REGION` / `AWS_REGION` / `CLOUDSDK_COMPUTE_REGION` / `AZURE_REGION`

---

## 14. Persistent State — `agent/state.go`

`DaemonState` is persisted atomically to `stateDir/<name>/state.json` after every tick. It survives daemon restarts.

### State Contents

```go
type DaemonState struct {
    mu         sync.Mutex       `json:"-"`
    Dedup      map[string]int64 `json:"dedup,omitempty"` // hash → expiry (Unix ms)
    TickCount  int64            `json:"tick_count"`
    ErrorCount int64            `json:"error_count"`     // ticks that ended with an error
    LastTickAt time.Time        `json:"last_tick_at,omitempty"`
}
```

### Dedup Cache

Prevents re-alerting on findings that were already reported within `dedupeTtl`:

```go
func (s *DaemonState) IsDuplicate(text string, ttl time.Duration) bool
func (s *DaemonState) AddToDedup(text string, ttl time.Duration)
func (s *DaemonState) PruneDedup()  // removes expired entries
```

Uses SHA-256 content hashing (64-bit prefix for space efficiency):
```go
func contentHash(text string) string {
    h := sha256.Sum256([]byte(text))
    return fmt.Sprintf("%x", h[:8])
}
```

### Atomic Writes

State is written to `state.json.tmp` then renamed to `state.json` to prevent corruption on crash:
```go
os.WriteFile(path+".tmp", data, 0644)
os.Rename(path+".tmp", path)
```

---

## 15. RBAC Action Filtering — `agent/rbac.go`

RBAC policies enforce allow/deny rules on tool calls **before** `ExecuteTool` is called. Blocked calls return a denial message to the model (not a Go error), so the model can reason about the restriction.

### Evaluation Order

```mermaid
flowchart TD
    Check["CheckRBAC(policy, toolName, input)"] --> D{"deny list\nnon-empty?"}
    D --> |"matches a rule"| Deny["return false, reason"]
    D --> |"no match"| A{"allow list\nnon-empty?"}
    A --> |"matches a rule"| Allow["return true"]
    A --> |"no match"| Block["return false, not on allow list"]
    A --> |"allow list empty"| Allow2["return true (default)"]
```

### Agent File Configuration

```yaml
actions:
  rbac:
    deny:
      - tool: bash
        reason: arbitrary shell not permitted in production
    allow:
      - tool: read_file
      - tool: search
      - tool: list_files
```

### Integration Points

- **Claude** (`buildBetaTools`): RBAC is checked inside every tool handler closure.
- **OpenAI** (`RunOpenAI`): RBAC is checked in the tool execution loop before `ExecuteTool`.
- **Denied calls**: emit `EventActionDenied` + return `"action denied: <reason>"` to the model.

---

## 16. Management Plane — `agent/mgmt.go`

The management plane is an optional central server that daemons connect to for registration, heartbeat reporting, and remote config delivery. The connection uses mutual TLS (TLS 1.3).

### Architecture

```mermaid
sequenceDiagram
    participant Daemon as RunDaemon()
    participant Mgmt as MgmtPlane
    participant Server as Management Server

    Daemon->>Mgmt: NewMgmtPlane (loads mTLS certs)
    Mgmt->>Server: POST /api/v1/agents/register
    loop every HeartbeatSec (default 30s)
        Mgmt->>Server: POST /api/v1/agents/<name>/heartbeat\n{host, ts, tick_count}
    end
    loop every PollSec (default 60s)
        Mgmt->>Server: GET /api/v1/agents/<name>/config
        Server-->>Mgmt: {max_turns, effort, suspended}
        Mgmt->>Daemon: onReload(remoteConfig) — hot-apply
    end
    Daemon->>Daemon: SIGHUP received — re-parse agent file
```

### Certificate Layout

```
/etc/okesu/
├── client.crt    ← agent client certificate
├── client.key    ← agent private key
└── ca.crt        ← CA certificate (verifies management server)
```

`NewMgmtPlane` returns `(nil, nil)` when `management.url` is empty — daemons operate normally without a management plane. If cert loading fails, a warning is emitted and the daemon continues without management connectivity.

### Agent File Configuration

```yaml
management:
  url: https://mgmt.example.com
  heartbeatSec: 30
  pollSec: 60
  certDir: /etc/okesu
```

### SIGHUP Hot Reload

On `SIGHUP`, the daemon re-parses the agent file from disk and hot-applies `maxTurns` and `effort`. The next tick uses the updated config. No process restart is needed.

```bash
# Trigger hot reload:
systemctl kill --signal=SIGHUP okesu-agent@edr-agent
```

### Known-Issues Pull Cache (Phase 13)

`MgmtPlane.StartKnownIssuesPoller` runs alongside the config poller (60s
default). On each tick it calls
`GET /api/v1/agents/{name}/known-issues` and caches a fingerprint →
`{status, triage_note, updated_at}` map in memory. The harvester
consults this cache before emitting any finding:

| Cached status | Harvester behaviour |
|---|---|
| not present, or `open` | fall through to the local fingerprint dedup |
| `false_positive` | suppress silently forever (until reopened) |
| `acknowledged` / `investigating` / `resolved` / `wontfix` | suppress until reopened |

A suppression hit does **not** add the fingerprint to the local
`state.Dedup` cache, so an un-triage by the operator takes effect on
the very next tick rather than after `dedupeTtl` expires.

### `lookup_findings` LLM Tool (Phase 13)

When a management plane is configured, the daemon registers a built-in
tool the model can call when investigating:

```
lookup_findings(query: string, limit: int = 5)
  → [{ fingerprint, title, severity, status, host,
       resource, triage_note, last_seen, occurrence_count }]
```

The handler is `MgmtPlane.LookupFindings`, which calls
`GET /api/v1/agents/{name}/findings/search?q=...&limit=N` over the
existing mTLS client. Server-side it's a `LIKE` search across title,
resource, network endpoint, path, process name, and dedup_key, scoped
to the calling agent across all hosts. Triaged matches surface above
untriaged so the LLM sees the operator's classification first.

The tool is auto-included in the per-tick `cfg.AllowedTools` list when
mgmt is wired (operators don't need to edit each agent file). System
prompts include a small instruction block teaching the model to call
it before reporting and to honour the `status` field on results.

This forms the third dedup layer:

| Layer | When it runs | Catches |
|---|---|---|
| Local fingerprint cache | every harvested finding | repeats within `dedupeTtl` |
| Pulled known-issues cache | every harvested finding | operator-triaged, fleet-wide |
| `lookup_findings` tool | only when LLM asks | semantic neighbours, related context |

Triage notes are **not** auto-injected into the system prompt — they
surface only when the LLM explicitly looks them up, keeping daemon
prompts compact regardless of triage history size.

---

## 17. Deployment — systemd + install script

### systemd Template Unit

`systemd/okesu-agent@.service` is a template unit. One instance per agent name:

```bash
# Enable and start the "edr-agent" agent:
systemctl enable okesu-agent@edr-agent
systemctl start  okesu-agent@edr-agent

# Follow logs:
journalctl -fu okesu-agent@edr-agent

# Hot reload config:
systemctl kill --signal=SIGHUP okesu-agent@edr-agent
```

**Security hardening in the unit:**
- Runs as unprivileged `okesu` system user
- `ProtectSystem=strict` — OS directories read-only
- `NoNewPrivileges=yes`
- `PrivateTmp=yes`
- `ReadWritePaths` limited to state dir, config dir, and `/tmp`

**Per-agent secrets** go in `/etc/okesu/agents/<name>.env`:
```bash
# /etc/okesu/agents/edr-agent.env
ANTHROPIC_API_KEY=sk-ant-...
WEBHOOK_SECRET=hmac-signing-key
```

### Install Script

```bash
sudo ./scripts/install.sh --binary ./okesu --agent edr-agent
```

Steps performed:
1. Creates `okesu` system user (if absent)
2. Installs binary to `/usr/local/bin/okesu`
3. Creates `/etc/okesu/` and `/var/lib/okesu/` directories
4. Installs systemd template unit
5. `systemctl daemon-reload`
6. Enables and starts the named agent instance

### Directory Layout (production)

```
/usr/local/bin/okesu              ← binary
/etc/okesu/
├── client.crt / client.key / ca.crt ← mTLS certs
└── agents/
    ├── edr.md                        ← agent file
    └── edr.env                       ← secrets (chmod 600)
/var/lib/okesu/
└── edr-agent/
    ├── state.json                    ← dedup cache + tick count
    ├── last_run                      ← last tick timestamp
    └── ...
```

### Docker

A multi-stage `Dockerfile` is provided for container-based deployment and testing. The build stage compiles a static Go binary; the runtime stage runs on `debian:bookworm-slim` with `procps`, `iproute2`, `findutils`, and `util-linux` for the pre-collectors.

```bash
# Build
docker build -t okesu:dev .

# Run the EDR agent with a 30s tick interval
docker run --rm -it \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  okesu:dev daemon --agent edr --interval 30s

# Pipe through jq for readable output
docker run --rm -it \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  okesu:dev daemon --agent edr --interval 30s 2>/dev/null | jq .
```

The container requires only an API key at runtime. No management plane, mTLS certificates, or webhook endpoint is needed for basic testing.

---

## 18. End-to-End Request Flow

### Task Mode — Claude

```mermaid
sequenceDiagram
    participant User
    participant Main as main.go
    participant Runner as RunClaude()
    participant SDK as BetaToolRunnerStreaming
    participant API as Anthropic API

    User->>Main: okesu claude --agent security-reviewer "audit ./src"
    Main->>Main: ParseAgentFile → buildConfig
    Main->>Runner: RunClaude(cfg)
    Runner->>Runner: BuildSinks → SetGlobalSink (StdoutSink in task mode)
    Runner->>Runner: buildBetaTools(ActiveTools, rbac)
    loop until stop_reason = end_turn
        Runner->>SDK: AllStreaming()
        SDK->>API: POST /v1/messages
        API-->>SDK: stream (text + tool_use)
        SDK->>Runner: text deltas → Emit(EventText)
        SDK->>SDK: parallel tool handlers
        SDK->>SDK: CheckRBAC → ExecuteTool → Emit(EventToolCall/Result)
    end
    Runner->>Runner: Emit(EventDone)
```

### Daemon Mode — Full Tick

```mermaid
sequenceDiagram
    participant Timer
    participant Daemon as RunDaemon()
    participant Collectors as RunCollectors()
    participant Runner as RunClaude/OpenAI()
    participant Sinks

    Timer->>Daemon: tick fires
    Daemon->>Daemon: tickMu.TryLock()
    Daemon->>Daemon: state.PruneDedup()
    Daemon->>Collectors: RunCollectors() — parallel shell cmds
    Collectors-->>Daemon: []CollectorResult
    Daemon->>Daemon: BuildTickContext + RenderPrompt
    Daemon->>Daemon: touchLastRun()
    Daemon->>Runner: RunClaude(tickCfg) or RunOpenAI(tickCfg)
    Runner->>Sinks: Emit (stdout + file + webhook)
    Runner-->>Daemon: done (or error)
    alt API error
        Daemon->>Sinks: Emit api_unavailable
        Daemon->>Daemon: APIPolicy.OnAPIUnavailable()
    end
    Daemon->>Daemon: state.RecordTick()
    Daemon->>Sinks: Emit tick_done
    Daemon->>Daemon: tickMu.Unlock()
```

---

## 19. Concurrency Model

```mermaid
graph TD
    subgraph "Daemon goroutines"
        ML["main event loop\n(select on timer + signals)"]
        TG["tick goroutine\n(holds tickMu)"]
        HB["heartbeat goroutine"]
        CP["config poller goroutine"]
        WH["webhook deliver goroutine"]
        ML --> |"go func"| TG
        ML --> |"go func (StartHeartbeat)"| HB
        ML --> |"go func (StartConfigPoller)"| CP
        ML --> |"go func (NewWebhookSink)"| WH
    end

    subgraph "Inside a tick goroutine"
        TG --> Col["collector goroutines\n(one per collector, WaitGroup)"]
        TG --> SDK["Claude SDK errgroup\n(parallel tool handlers)"]
    end

    SDK --> Sink["Sink.Write() — thread-safe\n(StdoutSink has mutex;\nFileBuffer has mutex;\nWebhookSink uses channel)"]
```

**Thread safety guarantees:**
- `Emit()` → `emitToSink()` → `sink.Write()`: each sink implementation is mutex-protected.
- `tickMu sync.Mutex` serializes tick execution and provides clean-shutdown wait.
- `DaemonState.mu` protects dedup cache from concurrent reads/writes.
- `MgmtPlane.mu sync.RWMutex` protects `remote` config from concurrent reads/writes.

---

## 20. Data Structures Reference

### Config (full)

```go
type Config struct {
    Name         string      // agent name (from agent file)
    Provider     string      // "claude" | "codex"
    Model        string
    Prompt       string      // user task
    SystemPrompt string      // agent file body (may contain Go template directives)
    MaxTokens    int64
    APIKey       string
    Effort       string      // "low"|"medium"|"high"|"xhigh"|"max"
    MaxTurns     int         // 0 = unlimited
    AllowedTools []string    // empty = all tools
    RBAC         *RBACPolicy // nil = no restrictions
    IsDaemon     bool        // true when running under RunDaemon (enables action_taken events)
}
```

### DaemonConfig (full)

```go
type DaemonConfig struct {
    Interval   time.Duration
    Cron       string
    Overlap    string                // "skip" | "queue"
    StateDir   string
    DedupeTTL  time.Duration
    Collectors []CollectorDef
    Outputs    []OutputDef
    Mgmt       MgmtConfig
    APIPolicy  APIUnavailablePolicy  // nil defaults to NoopAPIPolicy
}
```

### CollectorDef

```go
type CollectorDef struct {
    Name       string        `yaml:"name"`
    Command    string        `yaml:"command"`
    Timeout    time.Duration `yaml:"-"`                 // parsed from TimeoutStr
    TimeoutStr string        `yaml:"timeout,omitempty"` // e.g. "30s"
    Optional   bool          `yaml:"optional"`          // true: failure = skip; false: failure = abort tick
}
```

### RBACPolicy

```go
type RBACPolicy struct {
    Allow []RBACRule `yaml:"allow"`
    Deny  []RBACRule `yaml:"deny"`
}

type RBACRule struct {
    Tool    string `yaml:"tool"`
    Pattern string `yaml:"pattern,omitempty"`
    Reason  string `yaml:"reason,omitempty"`
}
```

### OutputDef

```go
type OutputDef struct {
    Type      string   `yaml:"type"`               // "stdout" | "file" | "webhook"
    Path      string   `yaml:"path,omitempty"`     // file: JSONL log path
    MaxBytes  int64    `yaml:"maxBytes,omitempty"` // file: rotation threshold
    URL       string   `yaml:"url,omitempty"`      // webhook: endpoint URL
    Secret    string   `yaml:"secret,omitempty"`   // webhook: HMAC-SHA256 signing secret
    Retries   int      `yaml:"retries,omitempty"`  // webhook: max delivery attempts
    BufferCap int      `yaml:"bufferCap,omitempty"`// webhook: ring buffer size
    Events    []string `yaml:"events,omitempty"`   // event type allow-list (empty = all)
}
```

### Provider API Mapping

| Concept | Claude | OpenAI |
|---|---|---|
| Client | `anthropic.NewClient()` | `openai.NewClient()` |
| Endpoint | `/v1/messages` | `/v1/responses` |
| System prompt | `BetaMessageNewParams.System` | `ResponseNewParams.Instructions` |
| Tool definition | `BetaTool` via `toolrunner.NewBetaTool` | `ToolUnionParam{OfFunction: FunctionToolParam}` |
| Effort | `BetaOutputConfigParam{Effort}` | `shared.ReasoningParam{Effort}` |
| Loop management | SDK-managed (`BetaToolRunnerStreaming`) | Manual with `PreviousResponseID` |
| Multi-turn state | SDK in-memory | Server-side via `previous_response_id` |
| Text streaming | `BetaRawContentBlockDeltaEvent` | SSE `response.output_text.delta` |
| Tool call detection | SDK dispatches to handler | Manual: `item.Type == "function_call"` |
| Tool result | `BetaToolResultBlockParamContentUnion` | `ResponseInputItemParamOfFunctionCallOutput` |

---

## 21. Control Plane Data Architecture

Sections 1–20 describe the daemon. The **Control Plane** is a separate
Go service (`cmd/cp/`) that ingests JSONL events from daemon webhooks,
projects them into structured tables, and serves the operator UI plus
the agent-facing management plane. This section focuses on the
non-obvious data-layer choices.

### 21.1 Findings: from event to indexed issue

A finding flows through the system in three shapes:

```
┌────────────────────┐    ┌──────────────────┐    ┌────────────────────┐
│ LLM-written file   │ →  │ JSONL event line │ →  │ findings row       │
│ {stateDir}/        │    │ on the wire      │    │ + structured cols  │
│  findings/<ts>.json│    │                  │    │                    │
└────────────────────┘    └──────────────────┘    └────────────────────┘
       harvester          webhook receiver          insert + index
       (daemon)           (CP)                      (CP)
```

The harvester (Phase 12) is responsible for two kinds of normalization
before the event leaves the daemon:

1. **Title volatility stripping.** `agent.NormalizeFindingTitle()`
   removes prefixes/suffixes like `PERSISTENT (TICK 87): `,
   `[5+ ticks] `, `— 7th Consecutive Tick`. The same regex runs
   server-side as defense-in-depth for events from external producers
   via `/api/findings/ingest`.

2. **Stable fingerprint computation.** A SHA-256 prefix of:
   ```
   severity | normalized_title | resource_root |
   process_pid | path | network_endpoint | dedup_key
   ```
   where `resource_root` extracts the first stable token of `resource`
   (e.g. `host:api.example.com:443` → `host:api.example.com`, port
   stripped). This fingerprint is wired as the outbound `dedup_key`,
   so the CP's grouping query collapses LLM variants automatically.

The `findings` table after Phase 12 has eight indexed enrichment
columns (`category`, `process_pid`, `process_name`, `path`,
`network_endpoint`, `cve`, `tags`, `attributes`) plus the Phase 13
triage columns (`status`, `triage_note`, `triaged_at`, `triaged_by_*`).
Most are nullable; agents fill what they extract, the harvester infers
the rest from `resource` shapes (`pid:N` → process, `path:/...` →
file, etc.).

### 21.2 Three-layer dedup architecture

The dedup layers compose: each catches a different category of repeat,
escalating in cost.

```
┌──────────────────────────────────────────────────────────────────┐
│ Layer 1: Local fingerprint cache (state.Dedup)                   │
│   in-memory map of fingerprint → expiry_ms                       │
│   keyed by the same fingerprint that goes on the wire            │
│   PruneDedup at tick start drops entries past expiry             │
│   pure CPU, no API cost                                          │
└──────────────────────────────────────────────────────────────────┘
                            ↓ miss
┌──────────────────────────────────────────────────────────────────┐
│ Layer 2: Pulled known-issues cache                               │
│   GET /api/v1/agents/{name}/known-issues every 60s               │
│   in-memory map of fingerprint → {status, triage_note}           │
│   suppress on any non-open status                                │
│   ~one tiny GET per minute, regardless of fleet size             │
└──────────────────────────────────────────────────────────────────┘
                            ↓ miss (or LLM curiosity)
┌──────────────────────────────────────────────────────────────────┐
│ Layer 3: lookup_findings tool                                    │
│   GET /api/v1/agents/{name}/findings/search?q=...                │
│   LIKE search across title/resource/path/endpoint/process/key    │
│   one tool call per LLM-driven look-up; on-demand                │
└──────────────────────────────────────────────────────────────────┘
```

A subtle invariant: when Layer 2 suppresses a finding, the harvester
does **not** add the fingerprint to Layer 1's local cache. This means
an un-triage by the operator (status → open) takes effect on the very
next tick, instead of being shadowed by a stale Layer-1 entry until
`dedupeTtl` expires.

Triage notes are stored in the DB and surfaced via the
`lookup_findings` tool — they are intentionally **not** injected into
the LLM system prompt, which keeps the prompt compact regardless of
how many findings have been triaged. The LLM pulls notes only when it
asks.

### 21.3 Agents identity: (name, host) composite key

Pre-Phase-12, the `agents` table had `name` as the sole primary key.
Real fleets routinely run the same agent (e.g. `edr`) on dozens of
hosts; under the old schema they collapsed into one row and only the
most-recent heartbeat survived.

Migration 012 rebuilds the table with `PRIMARY KEY (name, host)`. The
existing single-name lookup path (`AgentByName`) is preserved for
code that only has the cert CN — it returns the most-recently-active
row. New code uses `AgentByNameHost(name, host)` for an exact match.

`SetDesiredConfig` is intentionally still keyed by `name` only —
operators tune *the agent*, not a specific host instance, so a
`MaxTurns` change rolls out to every host running that agent on the
next config poll.

### 21.4 Schema migration policy

Migrations are SQL files embedded via `go:embed` and applied in numeric
order. Pre-Phase-12 they were all idempotent (`CREATE TABLE IF NOT
EXISTS`, `CREATE INDEX IF NOT EXISTS`), which let the runner re-apply
them on every boot without harm.

Phase 12 introduced the first `ALTER TABLE` and `DROP TABLE`
migrations, which are NOT safely re-runnable. The runner now uses a
`schema_migrations(version PK)` table to track what's applied:

```
1. CREATE TABLE IF NOT EXISTS schema_migrations
2. If the table is empty AND `meta` exists (a pre-tracker DB):
     bootstrap by probing each migration's signature (e.g. table
     exists, column exists, agents PK is composite) and INSERT
     OR IGNORE its version. This avoids re-running schema-altering
     migrations against tables that already have the new shape.
3. For each embedded migration:
     skip if its version is in schema_migrations
     otherwise: Exec the SQL, then INSERT the version
```

Probes used during bootstrap (one per migration version):

| Version | Probe |
|---|---|
| 1–10 | `table_exists(<expected table>)` |
| 11 | `column_exists(findings, category)` |
| 12 | `agents_has_composite_pk()` (counts pk-flagged columns in `pragma_table_info('agents')`) |
| 13 | `column_exists(findings, status)` |

Going forward: any new migration is free to use destructive DDL.
Operators upgrading from older builds get the bootstrap probe; new
deployments start with empty `schema_migrations` and apply every
version in order.

### 21.5 Triage feedback loop (Phase 13)

The mgmt-plane `known-issues` endpoint is what closes the operator →
daemon feedback loop:

```mermaid
sequenceDiagram
    participant Op as Operator (UI)
    participant CP as Control Plane
    participant Daemon as Daemon
    participant LLM as LLM

    Op->>CP: POST /findings/{id}/status<br/>{status:false_positive, note}
    Note over CP: findings.status updated;<br/>audit-logged

    loop every 60s (mTLS)
        Daemon->>CP: GET /api/v1/agents/{name}/known-issues
        CP-->>Daemon: [{fingerprint, status, note, ...}]
        Note over Daemon: updates in-memory cache
    end

    Note over LLM: tick begins
    LLM->>LLM: write findings/<ts>.json
    Daemon->>Daemon: harvest + compute fingerprint
    Note over Daemon: cache hit on false_positive →<br/>silently drop, do NOT add to local dedup
    Daemon-->>CP: (no event emitted)

    Note over LLM: optionally
    LLM->>Daemon: lookup_findings(query="...")
    Daemon->>CP: GET /findings/search?q=...
    CP-->>Daemon: [{fingerprint, status, triage_note, ...}]
    Daemon-->>LLM: tool result (compact JSON)
    Note over LLM: skips emitting based on triage_note
```

The pull cache (top loop) handles the bulk-suppression case at
near-zero token cost. The tool (bottom path) handles the cases the
LLM wants nuance for — related issues, cross-host context, "is this
what we think it is?".

---

## 22. Lifecycle Management (Phase 7)

Operators routinely need to update three independent things — daimon
*definitions* (the prompt + tools), daemon *binaries*, and node
*metadata* — without taking the fleet down. Phase 7 split these into
three first-class actions so the operator UX matches the underlying
mechanics.

### 22.1 Daimon hot-reload (Phase 7b — mgmt-plane push)

Edit a daimon definition in the Daimon Library, save, every running
daemon picks up the change within ~60 seconds. No SSH, no service
restart, mid-tick conversations finish unaffected.

```mermaid
sequenceDiagram
    participant Op as Operator
    participant CP as Control Plane
    participant D1 as Daemon (host A)
    participant D2 as Daemon (host B)

    Op->>CP: PUT /api/daimons/library/{name}
    Note over CP: validates frontmatter,<br/>writes file to --daimon-files-dir,<br/>computes sha256

    loop every 60s (mTLS poll)
        D1->>CP: GET /api/v1/agents/{name}/config
        CP-->>D1: {definition_hash: "abc...", ...}
        Note over D1: local hash differs?
        D1->>CP: GET /api/v1/agents/{name}/definition
        CP-->>D1: full *.md file content + X-Definition-Hash
        Note over D1: parse → hot-reload<br/>(system prompt, tools, model, max_turns, effort)<br/>on next tick
        D1->>CP: heartbeat {definition_hash: "abc..."}
    end

    Note over CP: agents.current_definition_hash<br/>per (name, host)<br/>drives the rollout indicator
```

**What hot-reloads:** system prompt, model, allowed tools, max turns,
effort.

**What still requires a restart:** schedule (`interval`), `stateDir`.
The save-time validator flags these and warns the operator.

**Race protection:** the daemon verifies the post-fetch hash matches
what `/config` reported. If a second save lands mid-fetch, it skips
this round and retries on the next poll cycle.

### 22.2 Binary update + rollback (Phase 7c — SSH push)

Updating the `okesu` binary is structurally different from a daimon
edit because it requires a process restart. Phase 7c treats it as a
discrete operator action with explicit rollback.

```
SSH to node →  upload to /usr/local/bin/okesu.new
                                ↓
            mv okesu → okesu.previous (atomic)
            mv okesu.new → okesu     (atomic)
                                ↓
            systemctl restart okesu-agent@*
```

Rollback inverts the renames: `okesu.previous → okesu`, then the
same systemd restart. Both flows are jobs streamed through the
existing `/api/jobs/{id}/log` SSE endpoint that deploys already
use, so the UI just reuses `subscribeJobLog`.

The previous-binary slot is single-deep by design — a second update
overwrites the first rollback target. Operators rolling back to
`N - 2` need to re-deploy from the CP's `--daemon-binaries-dir`.

### 22.3 Node metadata refresh (Phase 7a — tunnel probe)

Some operator-visible fields aren't tied to the binary or the
daimon — they're just "what does this node look like". Phase 7a
adds a `MsgProbe` / `MsgProbeReply` round-trip on the existing
tunnel: the CP sends a probe, the node collects `os.Hostname()`,
`uname -m`, `/etc/os-release`, `runtime.NumCPU()`, `/proc/meminfo`,
`syscall.Statfs(/var/lib/okesu)`, returns it. The CP writes to
`nodes.kernel_release`, `nodes.os_release`, etc.

No SSH required, no service restart, sub-second per node. Operators
hit "Refresh metadata" on the NodeDetail page after the CP grows a
new column (e.g. `daemon_hostname` from migration 014) without
having to redeploy the fleet.

### 22.4 Update lifecycle table

| Artifact            | Channel         | Restart? | Operator UX                       |
|---------------------|-----------------|----------|-----------------------------------|
| Daimon definition   | mgmt-plane      | No       | Save in Library → auto-rolls in 60s |
| Daemon binary       | SSH             | Yes      | "Update binary" on Nodes page     |
| Node binary (tunnel)| SSH (same as ↑) | Yes      | Same — same Go binary             |
| Node metadata       | Tunnel probe    | No       | "Refresh metadata" on NodeDetail  |

---

## 23. Hexagonal Architecture (Phase 8 — Ports + Adapters)

Phase 8 refactored the CP to talk to every external service through
small Go interfaces ("ports") so the same code runs in dev (SQLite +
in-process queue + filesystem blobs) and production (Postgres +
ClickHouse + Kafka + Redis + S3 + OCI Vault) — adapter selection is
a config decision, not a code change.

### 23.1 The ports

| Port (`controlplane/ports/`) | Purpose                                | Adapters shipping today                             |
|------------------------------|----------------------------------------|-----------------------------------------------------|
| `Store` (the SQL DB)         | Relational state — users, daimons, findings, audit | SQLite (dev), Postgres (production via pgx)        |
| `EventStore`                 | Events firehose — append-only telemetry | sqliteevents (wraps Store), clickhouseevents       |
| `Queue`                      | Durable async ingest                   | inprocess (channel-backed), kafka (Kafka API + SASL/TLS) |
| `PubSub`                     | Fire-and-forget broadcast (SSE fan-out)| inprocess, redispubsub (Redis & compatible)        |
| `BlobStore`                  | Object storage (binaries, exports, cold-tier) | filesystem (local disk), s3blob (any S3-compat)    |
| `CertManager`                | mTLS PKI for daemons + tunnel-clients  | internalca (current self-signed), oci-certificates (planned) |
| `Secrets`                    | KMS / secret store                      | envsecrets (env + LoadCredential dir), oci-vault (planned) |

Each port is small (1–6 methods), error semantics use sentinel
errors (`ports.ErrNotFound`, `ports.ErrAlreadyExists`,
`ports.ErrNotSupported`, `ports.ErrShutdown`), and the CP code
never imports an adapter directly. Selection happens once at
`controlplane.New(cfg)`.

### 23.2 Adapter selection

```mermaid
graph LR
    Cfg[Config] --> Pick{adapter selector}
    Pick -->|--db sqlite/postgres| Store
    Pick -->|--events-store| Events[EventStore]
    Pick -->|--queue| Q[Queue]
    Pick -->|--pubsub-url| PubSub
    Pick -->|--blob-url| Blob
    Pick -->|--secrets-source| Sec[Secrets]

    Store -->|sqlite| SQLite[(SQLite file)]
    Store -->|postgres| PG[(OCI DB-PG / RDS)]
    Events -->|sqliteevents| SQLite
    Events -->|clickhouseevents| CH[(ClickHouse cluster)]
    Q -->|inprocess| Mem[in-memory channels]
    Q -->|kafka| KK[Kafka / OCI Streaming]
    PubSub -->|inprocess| Mem
    PubSub -->|redispubsub| Redis[(Redis / OCI Cache)]
    Blob -->|filesystem| Disk[local disk]
    Blob -->|s3blob| S3[S3 / OCI Object Storage]
    Sec -->|envsecrets| Env[OKESU_SECRET_*\nor /etc/okesu/secrets]
    Sec -->|oci-vault| Vault[OCI Vault]
```

### 23.3 Async event ingest pipeline

In production with `--events-store=clickhouse --queue=kafka`, the
write path becomes:

```
webhook POST → CP validates HMAC → publish to Kafka topic events.raw
                                                                ↓
                            eventpipeline worker (one per CP replica)
                                                                ↓
                                        accumulate 100ms / 1000-row batch
                                                                ↓
                                                ClickHouse.InsertBatch
```

A worker failure leaves the message uncommitted in Kafka, so a
healthy CP replica picks it up. No data loss. Findings projection
(state-mutating) stays in the synchronous CP path because it
depends on the event_id; the events stream is the only piece that
goes async.

### 23.4 Stateless CP for horizontal scale

Three pieces of in-memory state used to pin the CP to a single
process:

- `Broadcaster` (SSE fan-out for `/api/events/stream`)
- run subscriber channels (live job/run logs)
- deploy job log subscribers

Phase 8d moved these behind `ports.PubSub`. The default
in-process adapter preserves single-CP behavior; `--pubsub-url
redis://...` swaps in Redis so an event posted to replica A reaches
an SSE client on replica B in milliseconds.

### 23.5 Database dialect handling

`Store.Exec` / `Query` / `QueryRow` (and their `Context` variants)
override the embedded `*sql.DB` to apply a runtime placeholder
rewriter when `Dialect == DialectPostgres`. Every `?` placeholder
becomes `$N`; single-quoted string literals are passed through
verbatim. The CP code base stays SQLite-native; Postgres
compatibility is a single-pass transformation.

Dialect-specific helpers live in `controlplane/db/rewriter.go`:

- `tsToMillisExpr(dialect, col)` — `strftime('%s', col) * 1000` on
  SQLite, `(EXTRACT(EPOCH FROM col) * 1000)::bigint` on Postgres
- `dialectSQL(dialect)` — migration-runner check/insert SQL with
  the right placeholder style

Migrations live in `migrations/sqlite/` and `migrations/postgres/`.
The runner picks the dir from the dialect; both dirs stay in
lockstep — every migration N has a counterpart at the same N.

### 23.6 Secrets — single door (Phase 8g)

Every secret the CP needs flows through `ports.Secrets` at boot.
Three layers, applied in order in `controlplane.New`:

```
1. LoadConfigFile          — YAML config with "${secret:NAME}" refs
        ↓
2. resolveConfigSecretRefs — substitute ${secret:NAME} via adapter
        ↓
3. resolveSecrets          — for canonical names the CP needs,
                              fill the field if still empty
```

Canonical secret names (slash-separated, mirrors OCI Vault folder
hierarchy):

```
cp/admin-password         clickhouse/password
cp/webhook-secret         kafka/sasl-password
cp/session-key            blob/secret-key
cp/oidc/client-secret
```

`--secrets-source` selects the adapter:

| Source                                       | Use when                                |
|----------------------------------------------|-----------------------------------------|
| `env` (default)                              | dev — secrets in `OKESU_SECRET_*` env vars |
| `file:///etc/okesu/secrets`                  | systemd `LoadCredential=` in production |
| `oci-vault://<compartment-ocid>?region=...`  | OCI Vault (Phase 8e.next, after tenancy round-trip) |

`scrubDSN()` redacts password components from URL-style DSNs before
logging. CLI flags + raw `OKESU_CP_*` env vars for individual
secrets still work but trigger a deprecation log line at boot.

### 23.7 What still needs the OCI tenancy

- Postgres migrations applied against managed OCI Database PostgreSQL
- ClickHouse cluster on OKE — schema bootstrap + ingest under load
- OCI Streaming SASL handshake (the SASL username format is
  OCI-specific)
- OCI Object Storage S3-compat round-trip with Customer Secret Keys
- OCI Certificates adapter for `ports.CertManager` (Phase 8e.next)
- OCI Vault adapter for `ports.Secrets` (Phase 8e.next)

### 23.8 Where each adapter lives

```
controlplane/
  ports/                       interfaces (ports.{Store,EventStore,Queue,PubSub,BlobStore,CertManager,Secrets})
  adapters/
    inprocess/                 channel-backed Queue + PubSub (dev)
    sqliteevents/              EventStore wrapping db.Store
    clickhouseevents/          EventStore against ClickHouse (production)
    kafka/                     Queue against Kafka API (OCI Streaming, MSK, Confluent, Redpanda)
    redispubsub/               PubSub against Redis (OCI Cache, ElastiCache, Cloud Memorystore)
    filesystem/                BlobStore on local disk (dev)
    s3blob/                    BlobStore against S3-compat (OCI Object Storage, AWS S3, R2, MinIO)
    internalca/                CertManager around the built-in self-signed CA
    envsecrets/                Secrets from env vars + optional dir of files
  eventpipeline/               Queue → batch → EventStore worker
```

A new cloud requires writing one new file per service, not editing
the rest of the CP.

---

## 24. Multi-OS Service Manager (Phase 8)

The SSH deploy and the install paths used to shell out to
`systemctl` directly. Phase 8 introduced an abstraction so daimons +
the jobs runtime install on every Unix-family target the daemon
binary builds for.

```
controlplane/sshdeploy/
  svcmgr.go            ServiceManager interface + Detect() factory
  svcmgr_launchd.go    macOS — /Library/LaunchDaemons/<label>.plist + launchctl
  svcmgr_rcd.go        FreeBSD/OpenBSD — /usr/local/etc/rc.d or /etc/rc.d
  svcmgr_smf.go        illumos/Solaris — SMF manifests + svcadm
  bootstrap.go         OS-aware unprivileged-user creation
```

`Detect()` runs `uname -s` against the target and returns the right
manager. The deploy + install paths use `mgr.Install(spec) +
mgr.EnableAndStart(name)` — same code on every flavour. Naming
convention: a service named `okesu-agent-edr` becomes
`okesu-agent-edr.service` on systemd, `com.okesu.agent-edr.plist` on
launchd, `okesu_agent_edr` on rc.d, `svc:/site/okesu-agent-edr:default`
on SMF. The mapping is symmetric so `ListOkesuAgents` round-trips.

The Phase 8.5 migration replaced the systemd template-unit pattern
(`okesu-agent@.service`) with per-instance units; existing template
instances are stopped + disabled on the next deploy.

---

## 25. S3 Dead-Drop Transport (Phase 9)

For nodes that can't reach the CP directly (NAT, air-gap, DMZ) but
can reach an object-storage bucket. Both sides only ever read/write
objects in a shared bucket; they never connect to each other.

Wire-format spec: [docs/s3-transport.md](s3-transport.md). Layout:

```
s3://okesu-<cp-id>/
  cp/<cp-id>/
    enrollment/pubkey.pem                 fleet trust anchor
    registration/<node-uuid>.json         inbox for new nodes
    nodes/<node-id>/
      cert.pem                            CP-issued post-enrollment
      heartbeat.json                      ~30s liveness
      jobs/inbox/<job-id>.json            CP enqueues
      jobs/<job-id>/output/<seq>.txt      node-streamed chunks
      jobs/<job-id>/exit.json             terminal status
      events/YYYY-MM-DD/*.ndjson          batched daimon events
      findings/*.json                     finding-type events
      config/desired.json                 hot-reloaded from CP
```

Implementation:

```
agent/s3transport/         node-side: client, runner, eventsink, enroll, wire types
controlplane/transport/
  s3scanner/               CP-side: ingests registrations, heartbeats, events,
                           findings, job output, job exits
controlplane/packaging/    self-register package generator with pluggable
                           Formatter interface (tar.gz live; deb/rpm/pkg/msi stubs)
```

Trust: per-CP fleet keypair (`transport_configs.fleet_pubkey_pem`)
signs each generated package's signing cert. Each node generates
its own UUID + keypair on first boot, uploads a CSR signed with the
package signing cert, and the CP scanner validates against the
fleet public key before issuing the per-node mTLS cert. One package
safely registers N machines — there's no per-node identity in the
package.

Tunables (`config/desired.json`, hot-reload): jobs poll, heartbeat,
events flush, output chunk flush. Defaults documented in
`docs/s3-transport.md` §"Polling cadence".

UI: `+ Add Node` modal has an **S3 dead-drop** tab where the
operator picks a transport config, agents to install, format, and
downloads the tar.gz. Backend ready for `.deb`, `.rpm`, `.pkg`,
`.msi` — `packaging.Register()` makes adding a formatter a
single-file change.

---

## 26. Engine-Applied Action Protocol (Phase 11)

Agents emit structured intent in `orchestration_result.attributes.actions[]`;
the engine validates against the step's `actions:` allowlist and
applies via DB methods. Agents never get CP credentials. Full
protocol doc: [agents/_orchestration-actions.md](../agents/_orchestration-actions.md);
operator-facing doc: [docs/orchestrations.md §Engine-applied actions](orchestrations.md#engine-applied-actions-cp-side-mutations).

Action set:
- `update_finding_status` — open / acknowledged / investigating / resolved / false_positive / wontfix / suppressed
- `add_finding_tag` / `remove_finding_tag`
- `set_finding_severity_override`
- `link_run_to_finding`
- `escalate` (soft signal)

Schema (mig 027):
- `finding_edits` — audit log keyed off `finding_id`; one row per
  atomic change. Origin can be a user (operator triage) or an
  orchestration run + step.
- `finding_run_links` — bidirectional join so the finding-detail
  page lists "auto-handled by run #N" and the run-detail page lists
  "this run touched findings X, Y, Z".

Implementation:

```
controlplane/orchestrator/actions.go         wire types + AllowedByStep validator
controlplane/orchestrator/engine.go          applyStepActions() called post-step
controlplane/api/orchestration_actions.go    db-backed ActionApplier impl
controlplane/api/finding_history.go          read-only API: history + run links
controlplane/db/finding_edits.go             ApplyFindingStatusChange + audit row in tx
agents/_orchestration-actions.md             protocol doc agents read at runtime
```

UI surfaces the audit:
- Finding-detail drawer has a **History** section with bot-vs-user
  icons and run deep-links
  (`web/src/components/FindingHistory.tsx`).
- Run-detail expanded step shows an **Actions applied** strip — one
  green chip per action (`Orchestrations.tsx::StepActionsApplied`).

## 27. IOC entity (Phase 22.1)

Phase 22.1 promoted indicators of compromise from ad-hoc finding
attributes (`finding.attributes.sha256`, etc.) to first-class CP rows
in the `iocs` table.

### Two populations

- **Curated** (`source = "catalog"`) — declarative YAML files under
  `catalog/iocs/*.yaml`, version-controlled alongside agents and
  orchestrations. Loaded at CP boot and on `SIGHUP`. Federated parent
  ↔ child the same way agent files are.
- **Observed** (`source = "observed"`) — auto-extracted from the
  text/attributes of every finding posted to `/api/findings/ingest`.
  Hash, IPv4/v6, domain, URL, CVE, and MITRE ATT&CK identifiers are
  pulled out via regex, normalized via `controlplane/ioc/normalize`,
  and upserted via `(*db.Store).UpsertIOC`.

### Schema

Two tables (sqlite + postgres parity, migration `030_iocs.sql`):

- `iocs` — keyed by `(kind, normalized_value)`; carries `severity_floor`,
  `classification`, `confidence`, `attribution`, `observation_count`,
  `first_seen`, `last_seen`. Catalog rows additionally carry
  `definition_path`.
- `ioc_observations` — links an IOC to a `findings.id` and/or
  `orchestration_runs.id`. `ON DELETE SET NULL` on both link columns
  so observations outlive the source records that surfaced them.

### Reconciliation

A catalog upsert against an existing observed row overwrites curated
metadata (severity_floor, classification, attribution, confidence,
notes) but preserves observation history (count, last_seen). An
observed upsert against an existing catalog row only refreshes
last_seen — curated metadata is never clobbered. See
`(*db.Store).UpsertIOC` for the canonical path; the upsert is
race-safe via `INSERT … ON CONFLICT DO NOTHING + UPDATE`.

### Read path

- `GET /api/iocs` — list IOCs, filterable by `kind` or `finding_id`
  (viewer+ cookie auth).
- `iocs.lookup` orchestration data query — used by `t2-fleet-ioc-hunt`'s
  scope step; takes `kind` + `value`, normalizes via
  `normalize.NormalizeForKind`, returns `{valid, kind, normalized_value,
  attribution, severity_floor, classification, source}`. Real DB
  errors surface as step failures; genuine misses return `valid:false`
  for `when:` branching.

Cross-reference:
`docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md`
for the design.
