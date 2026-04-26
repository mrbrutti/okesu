package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ToolDef describes a tool available to both providers.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema object
	Required    []string
}

// Tools is the full shared toolset. Use ActiveTools to get a filtered subset.
var Tools = []ToolDef{
	{
		Name:        "bash",
		Description: "Execute a shell command. Returns combined stdout and stderr. Runs in the current working directory.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The bash command to execute.",
				},
				"timeout_seconds": map[string]interface{}{
					"type":        "integer",
					"description": "Optional timeout in seconds (default: 120).",
				},
			},
		},
		Required: []string{"command"},
	},
	{
		Name:        "read_file",
		Description: "Read the full contents of a file at the given path.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or relative path to the file.",
				},
			},
		},
		Required: []string{"path"},
	},
	{
		Name:        "write_file",
		Description: "Write content to a file, creating it (and any parent directories) if it does not exist.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to write to.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Content to write.",
				},
			},
		},
		Required: []string{"path", "content"},
	},
	{
		Name:        "list_files",
		Description: "List files matching a glob pattern. Returns one path per line.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Glob pattern e.g. ./src/**/*.go or ./*.md",
				},
			},
		},
		Required: []string{"pattern"},
	},
	{
		Name:        "search",
		Description: "Search for a regex pattern in files. Returns matching lines with file:line prefix.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Regex pattern to search for.",
				},
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Directory or file to search in (default: .).",
				},
				"file_glob": map[string]interface{}{
					"type":        "string",
					"description": "Optional file glob filter e.g. *.go",
				},
			},
		},
		Required: []string{"pattern"},
	},
	{
		// Daemon-mode RAG: query the Control Plane for findings this agent
		// has emitted across the fleet. Use BEFORE writing a new finding to
		// avoid re-reporting issues the operator has already triaged
		// (false_positive, wontfix, resolved, acknowledged, investigating)
		// and to enrich your own report with related history.
		Name: "lookup_findings",
		Description: "Search past findings emitted by this agent across the fleet. Use BEFORE writing a finding to (1) avoid duplicating something already triaged (status field — false_positive/wontfix/resolved/acknowledged/investigating means do NOT re-emit) and (2) calibrate severity. Each result includes `severity` (effective), `original_severity` (what the LLM previously assigned), and `suggested_severity` (server hint when operators have repeatedly overridden the LLM or set an explicit rule). When `suggested_severity` is set, prefer it over your own first instinct — it represents accumulated operator judgment for this exact fingerprint. When `original_severity` differs from `severity` across multiple results, treat that gap as a signal that your default severity calibration for this kind of finding is off and adjust accordingly.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Keywords describing the issue (process name, port, path, host, CVE, etc.). Match is case-insensitive substring across title, resource, network endpoint, path, process name, and dedup_key.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Max results to return (1..15, default 5).",
				},
			},
		},
		Required: []string{"query"},
	},
}

// ActiveTools returns the subset of Tools matching the given names.
// If names is empty, all tools are returned.
// Accepts both okesu names (bash, read_file, write_file, list_files, search)
// and Claude Code CLI names (Bash, Read, Edit, Write, Glob, Grep).
func ActiveTools(names []string) []ToolDef {
	if len(names) == 0 {
		return Tools
	}
	allowed := make(map[string]bool, len(names))
	for _, n := range names {
		if norm := normalizeToolName(n); norm != "" {
			allowed[norm] = true
		}
	}
	result := make([]ToolDef, 0, len(allowed))
	for _, t := range Tools {
		if allowed[t.Name] {
			result = append(result, t)
		}
	}
	return result
}

// normalizeToolName maps Claude Code CLI and okesu tool names to the canonical
// okesu tool name. Returns "" for unknown/unsupported tools.
func normalizeToolName(name string) string {
	switch strings.ToLower(name) {
	case "bash":
		return "bash"
	case "read", "read_file":
		return "read_file"
	case "write", "edit", "write_file":
		return "write_file"
	case "glob", "list_files":
		return "list_files"
	case "grep", "search":
		return "search"
	default:
		return ""
	}
}

// ExecuteTool dispatches a tool call by name with the parsed input map.
//
// `lookup_findings` is intentionally NOT here — it needs the management-plane
// client which is per-Config state. Callers (claude.go / openai.go) detect
// the name and route through invokeLookupFindings instead. If we end up
// here with that name, the LLM is calling a tool the daemon doesn't have a
// CP for; we return a structured error so the model stops trying.
func ExecuteTool(name string, input map[string]interface{}) string {
	switch name {
	case "bash":
		return execBash(input)
	case "read_file":
		return execReadFile(input)
	case "write_file":
		return execWriteFile(input)
	case "list_files":
		return execListFiles(input)
	case "search":
		return execSearch(input)
	case "lookup_findings":
		return "error: lookup_findings is unavailable — this daemon is not connected to a Control Plane"
	default:
		return fmt.Sprintf("error: unknown tool %q", name)
	}
}

// invokeLookupFindings is the per-Config dispatch for the lookup_findings
// tool. It's a thin wrapper around the supplied callback (typically
// MgmtPlane.LookupFindings) plus result-shape massaging so the LLM gets a
// compact, indented JSON list rather than Go structs.
func invokeLookupFindings(
	ctx context.Context,
	input map[string]interface{},
	fn func(ctx context.Context, query string, limit int) ([]LookupResult, error),
) string {
	query, _ := input["query"].(string)
	if query == "" {
		return "error: missing 'query'"
	}
	limit := 0
	switch v := input["limit"].(type) {
	case float64:
		limit = int(v)
	case int:
		limit = v
	}
	results, err := fn(ctx, query, limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if len(results) == 0 {
		return "no matching findings"
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Sprintf("error marshalling results: %v", err)
	}
	return string(b)
}

func execBash(input map[string]interface{}) string {
	command, _ := input["command"].(string)
	if command == "" {
		return "error: missing command"
	}

	var buf bytes.Buffer
	cmd := exec.Command("bash", "-c", command)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Run() //nolint — we always return output regardless of exit code

	out := buf.String()
	if len(out) > 64*1024 {
		out = out[:64*1024] + "\n... (output truncated at 64KB)"
	}
	return out
}

func execReadFile(input map[string]interface{}) string {
	path, _ := input["path"].(string)
	if path == "" {
		return "error: missing path"
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if len(content) > 128*1024 {
		return string(content[:128*1024]) + "\n... (file truncated at 128KB)"
	}
	return string(content)
}

func execWriteFile(input map[string]interface{}) string {
	path, _ := input["path"].(string)
	content, _ := input["content"].(string)
	if path == "" {
		return "error: missing path"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Sprintf("error creating directories: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return fmt.Sprintf("written %d bytes to %s", len(content), path)
}

func execListFiles(input map[string]interface{}) string {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return "error: missing pattern"
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if len(matches) == 0 {
		return "(no files matched)"
	}
	return strings.Join(matches, "\n")
}

func execSearch(input map[string]interface{}) string {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return "error: missing pattern"
	}
	searchPath, _ := input["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}
	fileGlob, _ := input["file_glob"].(string)

	args := []string{"-rn", "--color=never", pattern, searchPath}
	if fileGlob != "" {
		args = []string{"--include=" + fileGlob, "-rn", "--color=never", pattern, searchPath}
	}

	var buf bytes.Buffer
	cmd := exec.Command("grep", args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Run() //nolint

	out := buf.String()
	if out == "" {
		return "(no matches found)"
	}
	if len(out) > 32*1024 {
		out = out[:32*1024] + "\n... (truncated)"
	}
	return out
}

// AgentDef holds the parsed frontmatter and body of an agent markdown file.
type AgentDef struct {
	// Common fields
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Model       string   `yaml:"model"`
	Provider    string   `yaml:"provider"`  // "claude" | "codex" — used by okesu auto
	Tools       []string `yaml:"tools"`     // okesu or Claude Code CLI tool names
	MaxTurns    int      `yaml:"maxTurns"`
	Effort      string   `yaml:"effort"`

	// Daemon mode fields
	Mode       string         `yaml:"mode"`       // "task" (default) | "daemon"
	Interval   string         `yaml:"interval"`   // e.g. "30s", "5m", "1h"
	Cron       string         `yaml:"cron"`       // cron expression, e.g. "*/5 * * * *"
	Overlap    string         `yaml:"overlap"`    // "skip" (default) | "queue"
	StateDir   string         `yaml:"stateDir"`   // path for state files and findings
	DedupeTTL  string         `yaml:"dedupeTtl"`  // e.g. "1h" — suppress duplicate findings
	Collectors []CollectorDef `yaml:"collectors"`  // pre-collector shell commands
	Outputs    []OutputDef    `yaml:"outputs"`     // output sinks (stdout|file|webhook)
	Actions    ActionsConfig  `yaml:"actions"`     // RBAC allow/deny policy
	Mgmt       MgmtConfig     `yaml:"management"`  // management plane connection (key: management:)

	Body string // system prompt body (content after the frontmatter)
}

// readDaimonFile locates the *.md for the named daimon and returns its
// path + raw bytes. Search order matches ParseAgentFile (project-local,
// home-global, /etc/okesu/agents). Used at daemon startup to seed the
// definition-hash for the management-plane drift signal.
func readDaimonFile(name string) (string, []byte, error) {
	if filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator) {
		b, err := os.ReadFile(name)
		return name, b, err
	}
	home := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(".claude", "agents", name+".md"),
		filepath.Join(".codex", "agents", name+".md"),
		filepath.Join(home, ".claude", "agents", name+".md"),
		filepath.Join(home, ".codex", "agents", name+".md"),
		filepath.Join("/etc", "okesu", "agents", name+".md"),
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return p, b, nil
		}
	}
	return "", nil, fmt.Errorf("daimon %q not found in any standard location", name)
}

// ParseAgentFile loads and parses an agent markdown file.
//
// If `name` is an absolute path or contains a path separator, it's
// treated as a literal file path (used by the tunnel run flow, where
// the CP ships the agent file content to a temp file on the node).
// Otherwise it searches these directories in order, stopping at the
// first match:
//
//  1. .claude/agents/<name>.md       (project-local, Claude CLI convention)
//  2. .codex/agents/<name>.md        (project-local, Codex convention)
//  3. ~/.claude/agents/<name>.md     (user-global)
//  4. ~/.codex/agents/<name>.md      (user-global)
//  5. /etc/okesu/agents/<name>.md    (system-wide, production deploy convention)
func ParseAgentFile(name string) (*AgentDef, error) {
	if filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator) {
		content, err := os.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read agent file %q: %w", name, err)
		}
		return parseAgentContent(string(content))
	}
	home := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(".claude", "agents", name+".md"),
		filepath.Join(".codex", "agents", name+".md"),
		filepath.Join(home, ".claude", "agents", name+".md"),
		filepath.Join(home, ".codex", "agents", name+".md"),
		filepath.Join("/etc", "okesu", "agents", name+".md"),
	}
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return parseAgentContent(string(content))
	}
	return nil, fmt.Errorf("agent %q not found in .claude/agents/, .codex/agents/, ~/.claude/agents/, ~/.codex/agents/, or /etc/okesu/agents/", name)
}

// parseAgentContent splits YAML frontmatter from body and unmarshals the YAML.
func parseAgentContent(content string) (*AgentDef, error) {
	def := &AgentDef{}

	if !strings.HasPrefix(content, "---") {
		def.Body = strings.TrimSpace(content)
		return def, nil
	}

	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		def.Body = strings.TrimSpace(content)
		return def, nil
	}

	yamlStr := rest[:idx]
	def.Body = strings.TrimSpace(rest[idx+4:])

	if err := yaml.Unmarshal([]byte(yamlStr), def); err != nil {
		return nil, fmt.Errorf("parsing agent frontmatter: %w", err)
	}

	return def, nil
}

// LoadAgentFile is kept for compatibility. Use ParseAgentFile for full frontmatter access.
func LoadAgentFile(name string) (string, error) {
	def, err := ParseAgentFile(name)
	if err != nil {
		return "", err
	}
	return def.Body, nil
}
