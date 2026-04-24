package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"text/template"
	"time"
)

// CollectorDef describes a pre-collector shell command that runs before each
// agentic tick. The output is injected into the system prompt via Go templates.
type CollectorDef struct {
	Name       string        `yaml:"name"`              // unique identifier, used in templates
	Command    string        `yaml:"command"`           // shell command to run
	Timeout    time.Duration `yaml:"-"`                 // parsed from TimeoutStr
	TimeoutStr string        `yaml:"timeout,omitempty"` // e.g. "30s" — parsed at load time
	Optional   bool          `yaml:"optional"`          // true: failure is skipped silently; false (default): failure aborts tick
}

// CollectorResult holds the output of a single pre-collector.
type CollectorResult struct {
	Name     string
	Command  string        // shell command that was executed
	Output   string
	Error    string
	Skipped  bool          // true when an optional collector failed and was skipped
	Duration time.Duration
}

// TickContext is passed to the Go template when rendering the per-tick system
// prompt. It contains metadata about the current tick and all collector outputs.
//
// Template variables (design §3.1):
//
//	{{.HostID}}       — hostname (alias for .Host)
//	{{.Host}}         — hostname
//	{{.CloudRegion}}  — cloud region (AWS/GCP/Azure env vars, or "unknown")
//	{{.AgentName}}    — agent name from frontmatter (alias for .Agent)
//	{{.Agent}}        — agent name
//	{{.TickTime}}     — RFC3339 timestamp of this tick (string, alias for .Time)
//	{{.Time}}         — tick start time (time.Time)
//	{{.LastRunISO}}   — ISO 8601 timestamp of the previous tick (string)
//	{{.LastRunAt}}    — previous tick time (time.Time)
//	{{.LastRunFile}}  — absolute path to the last_run sentinel file
//	{{.StateDir}}     — state directory path
//	{{.Collectors}}   — map[name]CollectorResult for indexed access
//	{{.CollectorsList}} — []CollectorResult for range iteration
type TickContext struct {
	// Convenience string aliases (design-specified names)
	HostID      string
	CloudRegion string
	AgentName   string
	TickTime    string
	LastRunISO  string
	LastRunFile string
	StateDir    string

	// Strongly-typed fields
	Tick          int64
	Time          time.Time
	Host          string
	Agent         string
	LastRunAt     time.Time
	Collectors    map[string]CollectorResult // indexed access: index .Collectors "name"
	CollectorsList []CollectorResult         // slice for {{range .CollectorsList}}
}

// RunCollectors executes all collectors in parallel and returns their results.
// Each collector times out after its configured Timeout (default 30s).
//
// Returns an error if any non-optional collector fails — the caller should
// abort the tick in that case. Optional collectors that fail are included in
// the results with Skipped=true and are never fatal.
func RunCollectors(collectors []CollectorDef, cfg Config, hostname string, tickNum int64) ([]CollectorResult, error) {
	if len(collectors) == 0 {
		return nil, nil
	}

	results := make([]CollectorResult, len(collectors))
	errs := make([]error, len(collectors))
	var wg sync.WaitGroup

	for i, c := range collectors {
		wg.Add(1)
		go func(idx int, col CollectorDef) {
			defer wg.Done()
			start := time.Now()
			res := runCollector(col)
			res.Duration = time.Since(start)

			if res.Error != "" && col.Optional {
				res.Skipped = true
				res.Error = fmt.Sprintf("skipped (optional): %s", res.Error)
			} else if res.Error != "" && !col.Optional {
				errs[idx] = fmt.Errorf("collector %q failed: %s", col.Name, res.Error)
			}

			results[idx] = res

			Emit(Event{
				Type:      EventCollectorResult,
				Agent:     cfg.Name,
				Host:      hostname,
				Tick:      tickNum,
				Collector: col.Name,
				Output:    res.Output,
				Error:     res.Error,
				Bytes:     len(res.Output),
			})
		}(i, c)
	}

	wg.Wait()

	// Return the first non-optional failure as an error.
	for _, err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func runCollector(col CollectorDef) CollectorResult {
	res := CollectorResult{Name: col.Name, Command: col.Command}

	timeout := col.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "bash", "-c", col.Command)
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := buf.String()
	if len(out) > 32*1024 {
		out = out[:32*1024] + "\n... (truncated at 32KB)"
	}
	res.Output = out

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			res.Error = fmt.Sprintf("timeout after %s", timeout)
		} else {
			res.Error = err.Error()
		}
	}

	return res
}

// BuildTickContext constructs the template data passed to RenderPrompt.
func BuildTickContext(cfg Config, hostname string, tickNum int64, lastRunAt time.Time, stateDir string, results []CollectorResult) TickContext {
	now := time.Now().UTC()
	colMap := make(map[string]CollectorResult, len(results))
	for _, r := range results {
		colMap[r.Name] = r
	}

	lastRunISO := ""
	if !lastRunAt.IsZero() {
		lastRunISO = lastRunAt.UTC().Format(time.RFC3339)
	}

	lastRunFile := ""
	if stateDir != "" && cfg.Name != "" {
		lastRunFile = filepath.Join(stateDir, cfg.Name, "last_run")
	}

	return TickContext{
		// Design-specified convenience names
		HostID:      hostname,
		CloudRegion: envCloudRegion(),
		AgentName:   cfg.Name,
		TickTime:    now.Format(time.RFC3339),
		LastRunISO:  lastRunISO,
		LastRunFile: lastRunFile,
		StateDir:    stateDir,

		// Strongly-typed fields
		Tick:          tickNum,
		Time:          now,
		Host:          hostname,
		Agent:         cfg.Name,
		LastRunAt:     lastRunAt,
		Collectors:    colMap,
		CollectorsList: results,
	}
}

// RenderPrompt renders the agent's system prompt (a Go template) against tctx.
// If the body is empty, a sensible default prompt is returned.
// Returns an error only when the template fails to parse or execute.
func RenderPrompt(body string, tctx TickContext) (string, error) {
	if body == "" {
		return fmt.Sprintf(
			"Perform your scheduled check. Tick: %d. Time: %s.",
			tctx.Tick,
			tctx.TickTime,
		), nil
	}

	funcMap := template.FuncMap{
		"env":         os.Getenv,
		"cloudRegion": envCloudRegion,
		"now": func() string {
			return time.Now().UTC().Format(time.RFC3339)
		},
	}

	tmpl, err := template.New("system").Funcs(funcMap).Parse(body)
	if err != nil {
		return "", fmt.Errorf("parsing system prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tctx); err != nil {
		return "", fmt.Errorf("rendering system prompt template: %w", err)
	}

	return buf.String(), nil
}

// envCloudRegion returns the cloud region from well-known environment variables.
// Supports AWS, GCP, and Azure conventions, falling back to "unknown".
func envCloudRegion() string {
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("CLOUDSDK_COMPUTE_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("AZURE_REGION"); r != "" {
		return r
	}
	return "unknown"
}
