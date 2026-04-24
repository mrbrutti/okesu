package agent

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

// DaemonConfig holds scheduling and runtime configuration for daemon mode.
// Populated from agent file frontmatter; individual fields are overridable via CLI flags.
type DaemonConfig struct {
	Interval  time.Duration // fixed interval between ticks (mutually exclusive with Cron)
	Cron      string        // cron expression, e.g. "*/5 * * * *"
	Overlap   string        // "skip" (default) | "queue"
	StateDir  string        // directory for state files, dedup cache, findings
	DedupeTTL time.Duration // window for suppressing duplicate findings (Phase 3)
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

	for {
		next := nextTick(time.Now())
		timer := time.NewTimer(time.Until(next))

		select {

		case <-timer.C:
			tickNum++
			n := tickNum

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
			go func(t int64) {
				defer tickMu.Unlock()
				execTick(cfg, hostname, t)
			}(n)

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

// execTick runs one complete tick: emits tick_start, runs the agentic loop,
// emits tick_done. In Phase 1 the prompt is a fixed message assembled here.
// Phase 2 replaces this with pre-collector output rendered through the agent template.
func execTick(cfg Config, hostname string, tickNum int64) {
	start := time.Now()

	Emit(Event{
		Type:  EventTickStart,
		Agent: cfg.Name,
		Host:  hostname,
		Tick:  tickNum,
	})

	// Build a per-tick copy of cfg with the tick prompt injected.
	// Phase 2 will render the agent file's Go template here instead.
	tickCfg := cfg
	tickCfg.Prompt = fmt.Sprintf(
		"Perform your scheduled check. Tick: %d. Time: %s.",
		tickNum,
		time.Now().UTC().Format(time.RFC3339),
	)

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
