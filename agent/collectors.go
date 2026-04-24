package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"text/template"
	"time"
)

// CollectorDef describes a pre-collector shell command that runs before each
// agentic tick. The output is injected into the system prompt via Go templates.
type CollectorDef struct {
	Name    string        `yaml:"name"`              // unique identifier, used in templates
	Command string        `yaml:"command"`           // shell command to run
	Timeout time.Duration `yaml:"-"`                 // parsed from TimeoutStr
	TimeoutStr string     `yaml:"timeout,omitempty"` // e.g. "30s" — parsed at load time
}

// CollectorResult holds the output of a single pre-collector.
type CollectorResult struct {
	Name     string
	Output   string
	Error    string
	Duration time.Duration
}

// TickContext is passed to the Go template when rendering the per-tick system
// prompt. It contains metadata about the current tick and all collector outputs.
type TickContext struct {
	Tick       int64
	Time       time.Time
	Host       string
	Agent      string
	LastRunAt  time.Time
	Collectors map[string]CollectorResult
}

// RunCollectors executes all collectors in parallel and returns their results.
// Each collector times out after its configured Timeout (default 30s).
func RunCollectors(collectors []CollectorDef, cfg Config, hostname string, tickNum int64) []CollectorResult {
	if len(collectors) == 0 {
		return nil
	}

	results := make([]CollectorResult, len(collectors))
	var wg sync.WaitGroup

	for i, c := range collectors {
		wg.Add(1)
		go func(idx int, col CollectorDef) {
			defer wg.Done()
			start := time.Now()
			res := runCollector(col)
			res.Duration = time.Since(start)
			results[idx] = res

			Emit(Event{
				Type:      EventCollectorResult,
				Agent:     cfg.Name,
				Host:      hostname,
				Tick:      tickNum,
				Collector: col.Name,
				Output:    res.Output,
				Error:     res.Error,
			})
		}(i, c)
	}

	wg.Wait()
	return results
}

func runCollector(col CollectorDef) CollectorResult {
	res := CollectorResult{Name: col.Name}

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
func BuildTickContext(cfg Config, hostname string, tickNum int64, lastRunAt time.Time, results []CollectorResult) TickContext {
	colMap := make(map[string]CollectorResult, len(results))
	for _, r := range results {
		colMap[r.Name] = r
	}
	return TickContext{
		Tick:       tickNum,
		Time:       time.Now().UTC(),
		Host:       hostname,
		Agent:      cfg.Name,
		LastRunAt:  lastRunAt,
		Collectors: colMap,
	}
}

// RenderPrompt renders the agent's system prompt (a Go template) against tctx.
// If the body contains no template directives, it is returned unchanged.
// Returns an error only when the template fails to parse or execute.
func RenderPrompt(body string, tctx TickContext) (string, error) {
	if body == "" {
		return fmt.Sprintf(
			"Perform your scheduled check. Tick: %d. Time: %s.",
			tctx.Tick,
			tctx.Time.Format(time.RFC3339),
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
	// AWS
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	// GCP
	if r := os.Getenv("CLOUDSDK_COMPUTE_REGION"); r != "" {
		return r
	}
	// Azure
	if r := os.Getenv("AZURE_REGION"); r != "" {
		return r
	}
	return "unknown"
}
