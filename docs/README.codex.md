# Okesu — Codex CLI Edition

OpenAI Codex CLI configuration for automated security code review.

## Project Structure

```
.codex/
  config.toml                    # CLI settings (model, permissions, sandbox)
  instructions.md                # System prompt / agent instructions
```

## Setup

### Prerequisites

- [Codex CLI](https://github.com/openai/codex) installed
- OpenAI API key stored in `~/.config/.env`:
  ```
  OPENAI_API_KEY="sk-..."
  ```

### Authentication

Codex supports two auth methods:

```bash
# Option 1: Environment variable
export OPENAI_API_KEY=$(grep OPENAI_API_KEY ~/.config/.env | cut -d= -f2 | tr -d '"')

# Option 2: Interactive login (stores in ~/.codex/auth.json)
codex login
```

### Configuration

Create `.codex/config.toml` in the project root:

```toml
model = "gpt-4o"
approval_policy = "on-request"
sandbox_mode = "workspace-write"
```

## Agent Instructions

Codex doesn't have a named agent system like Claude Code. Instead, it reads a project-level instructions file.

### instructions.md

Place your system prompt at `.codex/instructions.md` — Codex loads it automatically as project context. This is the equivalent of Claude Code's agent markdown files.

## Usage

### Interactive mode

```bash
codex
```

### One-shot with a prompt (exec mode)

```bash
codex exec "review the codebase for security vulnerabilities"
```

### Full auto-approve mode

```bash
codex exec --sandbox danger-full-access --ask-for-approval never "review this code"
```

This is equivalent to Claude Code's `--dangerously-skip-permissions`.

The shorthand `--yolo` flag also works:

```bash
codex --dangerously-bypass-approvals-and-sandbox "review this code"
```

### Specify a model

```bash
codex exec -m gpt-4o "review the codebase"
```

### Pipe context into the review

```bash
cat src/auth.py | codex exec "review this file for auth bypasses"
```

### Target a specific directory

```bash
codex -C /path/to/project exec "focus on the API routes in ./src/api/"
```

## CLI Reference

### Key Flags

| Flag | Description |
|------|-------------|
| `-m, --model <model>` | Override the model (`gpt-4o`, `gpt-5.4`, etc.) |
| `-s, --sandbox <mode>` | Sandbox mode: `read-only`, `workspace-write`, `danger-full-access` |
| `-a, --ask-for-approval <policy>` | Approval flow: `untrusted`, `on-request`, `never` |
| `-c, --config <key=value>` | Override config values (repeatable) |
| `-p, --profile <name>` | Select a config profile |
| `-C, --cd <path>` | Set working directory |
| `-i, --image <path>` | Attach images to the prompt |
| `--json` | JSONL output (exec mode only) |
| `-o, --output-last-message <path>` | Write final response to a file |
| `--output-schema <path>` | Validate output against a JSON Schema |
| `--color <always\|never\|auto>` | ANSI color control |
| `--oss` | Use local Ollama provider instead of OpenAI |
| `--remote` | Connect to remote app-server via WebSocket |

### Config File (`.codex/config.toml`)

```toml
model = "gpt-4o"
approval_policy = "on-request"
sandbox_mode = "workspace-write"

# Optional: MCP server integration
[mcp.servers.my_tool]
command = "node my-tool.js"
```

**Config file locations (in precedence order):**

1. `.codex/config.toml` (project-level)
2. `~/.codex/config.toml` (user-level)
3. CLI flags `-c key=value` (highest precedence)

### Permission Modes

| Sandbox Mode | Behavior |
|--------------|----------|
| `read-only` | Browse files only, approval required for any changes |
| `workspace-write` | Read/edit files and run commands in working directory (default) |
| `danger-full-access` | Unrestricted machine-wide access |

| Approval Policy | Behavior |
|-----------------|----------|
| `untrusted` | Approval required for all actions |
| `on-request` | Approval only when agent suggests actions (default) |
| `never` | Fully automatic — no prompts (CI/CD friendly) |

## Orchestration & Programmatic Output

### Output Formats

| Method | Output |
|--------|--------|
| `codex exec --json` | Real-time JSONL event stream to stdout |
| `codex exec -o output.txt` | Final response written to a file |
| `codex exec --output-schema schema.json` | Validated structured output |
| _(default)_ | Plain text |

### Streaming JSONL

```bash
# Save to a file
codex exec --json "review the codebase" > session.jsonl

# Pipe to your orchestrator
codex exec --json "review the codebase" | python my_orchestrator.py

# Watch live AND save
codex exec --json "review the codebase" | tee session.jsonl | jq '.type'
```

### Full Orchestrator Command

```bash
OPENAI_API_KEY=$(grep OPENAI_API_KEY ~/.config/.env | cut -d= -f2 | tr -d '"') \
codex exec \
  -m gpt-4o \
  --sandbox danger-full-access \
  --ask-for-approval never \
  --json \
  "review the codebase at /path/to/target for security vulnerabilities"
```

### Session Resumption

```bash
# Resume the most recent session
codex resume

# Resume a specific session by ID
codex resume SESSION_ID

# Resume the last session
codex resume --last

# Fork a session into a new thread
codex fork SESSION_ID
```

## Claude Code vs Codex CLI — Quick Comparison

| Feature | Claude Code | Codex CLI |
|---------|-------------|-----------|
| **Auth** | `ANTHROPIC_API_KEY` / `apiKeyHelper` | `OPENAI_API_KEY` / `codex login` |
| **Config format** | JSON (`settings.local.json`) | TOML (`config.toml`) |
| **Agent files** | `.claude/agents/<name>.md` with frontmatter | `.codex/instructions.md` (single file) |
| **Agent selection** | `--agent <name>` | N/A — one instructions file per project |
| **Skip permissions** | `--dangerously-skip-permissions` | `--sandbox danger-full-access -a never` |
| **Non-interactive** | `-p "prompt"` | `codex exec "prompt"` |
| **Streaming output** | `--output-format stream-json` | `--json` (exec mode) |
| **Model override** | `--model claude-mythos-preview` | `-m gpt-4o` |
| **Bare mode** | `--bare` | N/A |
| **System prompt** | `--system-prompt-file <path>` | `.codex/instructions.md` (auto-loaded) |
| **Session resume** | `--resume <session_id>` | `codex resume [SESSION_ID]` |
| **Budget limits** | `--max-budget-usd <n>` | N/A (platform billing) |
| **Turn limits** | `--max-turns <n>` | N/A (context-window based) |
| **MCP support** | Built-in | Built-in (STDIO & HTTP) |
| **Local models** | N/A | `--oss` (Ollama) |
