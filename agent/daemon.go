package agent

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

// DaemonConfig holds scheduling and runtime configuration for daemon mode.
// Populated from agent file frontmatter; individual fields are overridable via CLI flags.
type DaemonConfig struct {
	Interval   time.Duration  // fixed interval between ticks (mutually exclusive with Cron)
	Cron       string         // cron expression, e.g. "*/5 * * * *"
	Overlap    string         // "skip" (default) | "queue"
	StateDir   string         // directory for state files, dedup cache, findings
	DedupeTTL  time.Duration  // window for suppressing duplicate findings (Phase 3)
	Collectors []CollectorDef // pre-collector commands run before each tick
}

// RunDaemon runs the agent in daemon mode: sleeps until the next scheduled tick,
// executes one agentic loop, then repeats indefinitely.
//
// Signal handling:
//   - SIGTERM / SIGINT — finish the current tick cleanly, then exit 0
//   - SIGHUP           — reserved for hot config reload (Phase 6)
func RunDaemon(cfg Config, dcfg DaemonConfig) error {
	hostname, _ := os.Hostname()

	Emit(Event{
		Type:     EventDaemonStart,
		Agent:    cfg.Name,
		Host:     hostname,
		Provider: cfg.Provider,
		Model:    cfg.Model,
	})

	nextTick, err := buildSchedule(dcfg)
	if err != nil {
		return err
	}

	// tickMu is held for the duration of each tick.
	// TryLock on the timer path detects overlap; Lock on the signal path
	// waits for the current tick to finish before exiting.
	var tickMu sync.Mutex

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	var tickNum int64
	lastRunAt := readLastRunAt(dcfg.StateDir, cfg.Name)

	for {
		next := nextTick(time.Now())
		timer := time.NewTimer(time.Until(next))

		select {

		case <-timer.C:
			tickNum++
			n := tickNum
			lastRun := lastRunAt // snapshot for the goroutine closure

			if !tickMu.TryLock() {
				// Previous tick is still running — apply overlap policy.
				Emit(Event{
					Type:   EventTickDone,
					Agent:  cfg.Name,
					Host:   hostname,
					Tick:   n,
					Result: "skipped",
					Text:   "previous tick still running",
				})
				continue
			}

			// Run the tick in a goroutine so the main loop can still handle signals.
			go func(t int64, lr time.Time) {
				defer tickMu.Unlock()
				execTick(cfg, dcfg, hostname, t, lr)
				lastRunAt = time.Now()
			}(n, lastRun)

		case sig := <-sigCh:
			timer.Stop()

			if sig == syscall.SIGHUP {
				// Phase 6: hot config reload from management plane.
				Emit(Event{
					Type:  EventText,
					Agent: cfg.Name,
					Host:  hostname,
					Text:  "SIGHUP received — hot config reload arrives in Phase 6",
				})
				continue
			}

			// SIGTERM or SIGINT: wait for the current tick, then exit cleanly.
			Emit(Event{
				Type:       EventDaemonStop,
				Agent:      cfg.Name,
				Host:       hostname,
				StopReason: sig.String(),
			})
			tickMu.Lock() // blocks until the running tick (if any) releases the lock
			tickMu.Unlock()
			return nil
		}
	}
}

// execTick runs one complete tick: emits tick_start, runs pre-collectors,
// renders the system prompt template, runs the agentic loop, emits tick_done.
func execTick(cfg Config, dcfg DaemonConfig, hostname string, tickNum int64, lastRunAt time.Time) {
	start := time.Now()

	Emit(Event{
		Type:  EventTickStart,
		Agent: cfg.Name,
		Host:  hostname,
		Tick:  tickNum,
	})

	// Phase 2: run pre-collectors in parallel, then render system prompt.
	results := RunCollectors(dcfg.Collectors, cfg, hostname, tickNum)
	tctx := BuildTickContext(cfg, hostname, tickNum, lastRunAt, results)

	rendered, err := RenderPrompt(cfg.SystemPrompt, tctx)
	if err != nil {
		EmitError(fmt.Errorf("tick %d prompt render: %w", tickNum, err))
		rendered = tctx.buildFallbackPrompt()
	}

	// Build a per-tick copy of cfg with rendered system prompt and default user prompt.
	tickCfg := cfg
	tickCfg.SystemPrompt = rendered
	tickCfg.Prompt = fmt.Sprintf(
		"Perform your scheduled check. Tick: %d. Time: %s.",
		tickNum,
		tctx.Time.Format(time.RFC3339),
	)

	// Touch last-run marker so next tick can calculate elapsed time.
	touchLastRun(dcfg.StateDir, cfg.Name)

	var runErr error
	if cfg.Provider == "claude" {
		runErr = RunClaude(tickCfg)
	} else {
		runErr = RunOpenAI(tickCfg)
	}

	result := "completed"
	if runErr != nil {
		result = "error"
		EmitError(runErr)
	}

	Emit(Event{
		Type:   EventTickDone,
		Agent:  cfg.Name,
		Host:   hostname,
		Tick:   tickNum,
		Result: result,
		Text:   time.Since(start).Round(time.Millisecond).String(),
	})
}

// buildFallbackPrompt returns a minimal prompt when template rendering fails.
func (tctx TickContext) buildFallbackPrompt() string {
	return fmt.Sprintf(
		"Perform your scheduled check. Tick: %d. Time: %s.",
		tctx.Tick,
		tctx.Time.Format(time.RFC3339),
	)
}

// touchLastRun writes the current timestamp to stateDir/<name>/last_run
// so the next tick can read it with readLastRunAt.
func touchLastRun(stateDir, name string) {
	if stateDir == "" {
		return
	}
	dir := filepath.Join(stateDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	_ = os.WriteFile(
		filepath.Join(dir, "last_run"),
		[]byte(time.Now().UTC().Format(time.RFC3339Nano)),
		0644,
	)
}

// readLastRunAt reads the timestamp written by touchLastRun and returns
// the zero time if the file is absent or unparseable.
func readLastRunAt(stateDir, name string) time.Time {
	if stateDir == "" {
		return time.Time{}
	}
	data, err := os.ReadFile(filepath.Join(stateDir, name, "last_run"))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, string(data))
	if err != nil {
		return time.Time{}
	}
	return t
}

// buildSchedule returns a function that computes the next scheduled time after t.
// Cron takes precedence over Interval when both are set. Falls back to 60s if neither.
func buildSchedule(dcfg DaemonConfig) (func(time.Time) time.Time, error) {
	if dcfg.Cron != "" {
		parser := cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		)
		sched, err := parser.Parse(dcfg.Cron)
		if err != nil {
			return nil, fmt.Errorf("invalid cron expression %q: %w", dcfg.Cron, err)
		}
		return sched.Next, nil
	}

	interval := dcfg.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return func(t time.Time) time.Time {
		return t.Add(interval)
	}, nil
}
