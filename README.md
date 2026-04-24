# Okesu

Claude Code configuration and custom agents for automated security code review.

## Project Structure

```
.claude/
  settings.local.json          # CLI settings (API key, permissions)
  agents/
    security-reviewer.md       # Security code review agent
```

## Setup

### Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed
- Anthropic API key stored in `~/.config/.env`:
  ```
  ANTHROPIC_API_KEY="sk-ant-..."
  ```

### Configuration

The settings file at `.claude/settings.local.json` handles:

- **API key**: Automatically extracted from `~/.config/.env` via `apiKeyHelper`
- **Permissions**: All tools (Bash, Read, Edit, Write) pre-approved

## Agents

### security-reviewer

A security-focused code review agent that performs deep vulnerability analysis across any language or framework.

**Capabilities:**
- Injection (SQLi, command injection, template injection, XSS, etc.)
- Authentication & session management flaws
- Authorization bugs (IDOR, privilege escalation, mass assignment)
- Cryptographic misuse and hardcoded secrets
- Data exposure and over-fetching
- SSRF, CSRF, open redirects, path traversal
- Language-specific pitfalls (C/C++, Python, Java, JS/TS, Go, Rust, PHP, Ruby)
- Business logic flaws and race conditions

**Output:** Structured findings with severity, CWE, proof of concept, impact, and remediation.

## Usage

### Interactive mode

```bash
claude --agent security-reviewer
```

### One-shot with a prompt

```bash
claude --agent security-reviewer -p "review the codebase in /path/to/project"
```

### Bare mode (no hooks, no CLAUDE.md, no plugins)

```bash
claude --bare --dangerously-skip-permissions --model claude-opus-4-1 -p "review this code"
```

### Pipe context into the review

```bash
cat src/auth.py | claude --agent security-reviewer -p "review this file for auth bypasses"
```

### Target a specific directory

```bash
claude --agent security-reviewer -p "focus on the API routes in ./src/api/"
```

## CLI Reference

### Key Flags

| Flag | Description |
|------|-------------|
| `--agent <name>` | Load a named agent from `.claude/agents/` or `~/.claude/agents/` |
| `--bare` | Minimal mode — skips hooks, skills, plugins, MCP servers, CLAUDE.md |
| `--dangerously-skip-permissions` | Auto-approve all tool calls without prompting |
| `--model <model>` | Override the model (`claude-opus-4-1`, `sonnet`, `haiku`) |
| `-p "prompt"` | Non-interactive single-prompt mode |
| `--system-prompt-file <path>` | Replace system prompt with a file (arbitrary path) |
| `--append-system-prompt-file <path>` | Append to default system prompt from a file |
| `--settings <path>` | Load settings from a specific file |
| `--max-turns <n>` | Limit agentic loop iterations |

### Settings File (`settings.local.json`)

```json
{
  "apiKeyHelper": "grep ANTHROPIC_API_KEY ~/.config/.env | cut -d= -f2 | tr -d '\"'",
  "permissions": {
    "allow": [
      "Bash(*)",
      "Read(*)",
      "Edit(*)",
      "Write(*)"
    ]
  }
}
```

### Agent File Format (`.claude/agents/<name>.md`)

```markdown
---
name: agent-name
description: When to use this agent
model: claude-opus-4-1
tools: Bash, Read, Edit, Grep, Glob
permissionMode: bypassPermissions
maxTurns: 100
---

System prompt content goes here.
```

**Available frontmatter fields:**

| Field | Values |
|-------|--------|
| `name` | Lowercase, hyphens only |
| `description` | When Claude should delegate to this agent |
| `model` | `opus`, `sonnet`, `haiku`, full model ID, or `inherit` |
| `tools` | Comma-separated allowlist |
| `permissionMode` | `default`, `acceptEdits`, `auto`, `bypassPermissions`, `plan` |
| `maxTurns` | Integer — max agentic iterations |
| `isolation` | `worktree` for isolated git worktree |

## Orchestration & Programmatic Output

### Output Formats

| Flag | Output |
|------|--------|
| `--output-format stream-json` | Real-time JSONL event stream to stdout |
| `--output-format json` | Single JSON result object at the end |
| _(default)_ | Plain text |

### Streaming JSONL (`stream-json`)

Writes one JSON object per line to **stdout** — it does not save to a file. Each line represents an event: text deltas, tool calls, tool results, errors, or the final result.

```bash
# Save to a file
claude --agent security-reviewer --output-format stream-json -p "review" > session.jsonl

# Pipe to your orchestrator
claude --agent security-reviewer --output-format stream-json -p "review" | python my_orchestrator.py

# Watch live AND save
claude --agent security-reviewer --output-format stream-json -p "review" \
  | tee session.jsonl \
  | jq -rj 'select(.event.delta.type? == "text_delta") | .event.delta.text'
```

Example JSONL output:

```json
{"type":"system","subtype":"init","session_id":"abc-123"}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"The"}}}
{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"Read"}}}
{"type":"result","result":"final text here","usage":{...}}
```

### Streaming Flags

| Flag | Purpose |
|------|---------|
| `--verbose` | Include tool calls and results in the stream |
| `--include-partial-messages` | Include token-by-token text deltas |
| `--include-hook-events` | Include hook lifecycle events |

### Filtering with jq

```bash
# Extract text as it's generated
... | jq -rj 'select(.event.delta.type? == "text_delta") | .event.delta.text'

# Watch tool calls
... | jq 'select(.event.content_block.type? == "tool_use")'
```

### Full Orchestrator Command

```bash
claude --bare \
  --dangerously-skip-permissions \
  --agent security-reviewer \
  --output-format stream-json \
  --verbose \
  --include-partial-messages \
  --max-turns 50 \
  --max-budget-usd 10.00 \
  -p "review the codebase at /path/to/target"
```

### Session Resumption

```bash
# Capture session ID from the first run
sid=$(claude --agent security-reviewer --output-format json -p "start review" | jq -r '.session_id')

# Continue the conversation later
claude -p "now focus on auth" --resume "$sid" --output-format json
```

### Budget & Turn Limits

| Flag | Purpose |
|------|---------|
| `--max-turns <n>` | Cap agentic iterations |
| `--max-budget-usd <n>` | Cap spend per invocation |

## Agent Configuration Reference

### Agent Resolution Order

When using `--agent <name>`, Claude Code searches:

1. `.claude/agents/<name>.md` (project)
2. `~/.claude/agents/<name>.md` (user global)

The flag takes a **name**, not a file path. Use symlinks to reference agents stored elsewhere:

```bash
ln -s /path/to/my-agent.md ~/.claude/agents/my-agent.md
```
