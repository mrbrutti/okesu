package agent

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	DedupeTTL  time.Duration  // window for suppressing duplicate findings
	Collectors []CollectorDef // pre-collector commands run before each tick
	Outputs    []OutputDef    // output sinks; defaults to stdout-only when empty
	Mgmt       MgmtConfig          // management plane connection config
	// APIPolicy is called when the AI provider API is unreachable or returns a
	// server-side error. Nil defaults to NoopAPIPolicy. The Control Plane will
	// supply a concrete implementation that can buffer, escalate, or switch providers.
	APIPolicy  APIUnavailablePolicy
}

// RunDaemon runs the agent in daemon mode: sleeps until the next scheduled tick,
// executes one agentic loop, then repeats indefinitely.
//
// Signal handling:
//   - SIGTERM / SIGINT — finish the current tick cleanly, then exit 0
//   - SIGHUP           — reserved for hot config reload (Phase 6)
func RunDaemon(cfg Config, dcfg DaemonConfig) error {
	hostname, _ := os.Hostname()

	// Phase 4: initialize output sinks before first Emit.
	sink, err := BuildSinks(dcfg.Outputs, cfg.Name, hostname)
	if err != nil {
		return fmt.Errorf("building output sinks: %w", err)
	}
	SetGlobalSink(sink)
	defer sink.Close()

	Emit(Event{
		Type:     EventDaemonStart,
		Agent:    cfg.Name,
		Host:     hostname,
		Provider: cfg.Provider,
		Model:    cfg.Model,
	})

	// suspended mirrors the desired_suspended flag pushed from the
	// management plane. When true, the timer-driven tick loop emits a
	// "skipped: suspended" tick_done and returns immediately without
	// spawning a tick goroutine — the daemon stays alive (heartbeats +
	// config polls keep flowing) so the operator can resume it remotely.
	var suspended atomic.Bool

	// Phase 6: connect to management plane if configured.
	mgmtCtx, mgmtCancel := context.WithCancel(context.Background())
	defer mgmtCancel()

	// Load persisted state (dedup cache, tick count, last tick time).
	state := LoadState(dcfg.StateDir, cfg.Name)

	mgmt, mgmtErr := NewMgmtPlane(dcfg.Mgmt, cfg)
	if mgmtErr != nil {
		Emit(Event{
			Type:  EventText,
			Agent: cfg.Name,
			Host:  hostname,
			Text:  fmt.Sprintf("mgmt plane init error (continuing without it): %v", mgmtErr),
		})
	} else if mgmt != nil {
		if err := mgmt.Register(); err != nil {
			Emit(Event{
				Type:  EventText,
				Agent: cfg.Name,
				Host:  hostname,
				Text:  fmt.Sprintf("mgmt registration error: %v", err),
			})
		}
		// Seed the daemon's local definition hash so the heartbeat can
		// report "what I have loaded" and the CP can detect drift. We
		// re-read the agent file from disk at startup (deploys put it at
		// /etc/okesu/agents/<name>.md). Failure here is non-fatal — the
		// daemon just won't be able to participate in the drift signal.
		if cfg.Name != "" {
			if path, content, err := readDaimonFile(cfg.Name); err == nil {
				mgmt.SetLocalDefinitionHash(HashDefinition(content))
				_ = path // path may surface in future audit events
			}
		}
		// Seed the operator-set version label too — same lifecycle as
		// the hash. cfg.DefinitionVersion was populated from the agent
		// file frontmatter at startup.
		mgmt.SetLocalDefinitionVersion(cfg.DefinitionVersion)

		mgmt.StartHeartbeat(mgmtCtx, state)
		mgmt.StartKnownIssuesPoller(mgmtCtx)
		mgmt.StartConfigPoller(mgmtCtx, func(rc remoteConfig) {
			// Hot-apply non-destructive config changes.
			if rc.MaxTurns > 0 {
				cfg.MaxTurns = rc.MaxTurns
			}
			if rc.Effort != "" {
				cfg.Effort = rc.Effort
			}
			// Suspend / resume — emit a dedicated event on transition so the
			// CP can show a clean pause/resume marker on the timeline.
			if was := suspended.Swap(rc.Suspended); was != rc.Suspended {
				transition := "agent resumed via management plane"
				if rc.Suspended {
					transition = "agent suspended via management plane — ticks will be skipped until resumed"
				}
				Emit(Event{
					Type:  EventText,
					Agent: cfg.Name,
					Host:  hostname,
					Text:  transition,
				})
			}
			Emit(Event{
				Type:  EventConfigReloaded,
				Agent: cfg.Name,
				Host:  hostname,
				Text:  "remote config applied via management plane",
			})
		}, func(newHash string) {
			// Definition drift detected — fetch the new file from the CP
			// and apply the hot-reloadable subset of frontmatter fields
			// to the live cfg. Verify the post-fetch hash matches what
			// /config reported; if not, skip and retry next poll (the
			// operator may be in the middle of saving).
			ctx, cancel := context.WithTimeout(mgmtCtx, 15*time.Second)
			defer cancel()
			body, gotHash, err := mgmt.FetchDefinition(ctx)
			if err != nil {
				Emit(Event{
					Type:  EventText,
					Agent: cfg.Name,
					Host:  hostname,
					Text:  fmt.Sprintf("definition fetch failed: %v", err),
				})
				return
			}
			actualHash := HashDefinition(body)
			if gotHash != "" && gotHash != actualHash {
				// Server-reported header doesn't match content we got.
				// Likely a network corruption; skip.
				return
			}
			if newHash != actualHash {
				// File changed between /config and /definition. Skip; we'll
				// pick up the new hash on the next poll.
				return
			}
			def, perr := parseAgentContent(string(body))
			if perr != nil {
				Emit(Event{
					Type:  EventText,
					Agent: cfg.Name,
					Host:  hostname,
					Text:  fmt.Sprintf("definition parse failed: %v", perr),
				})
				return
			}
			// Apply the hot-reloadable subset. Schedule (interval) and
			// stateDir are NOT reloaded here — those drive the tick loop
			// and require a process restart. Operators are warned about
			// this at save time.
			if def.Model != "" {
				cfg.Model = def.Model
			}
			if def.Effort != "" {
				cfg.Effort = def.Effort
			}
			if def.MaxTurns > 0 {
				cfg.MaxTurns = def.MaxTurns
			}
			if def.Body != "" {
				cfg.SystemPrompt = def.Body
			}
			if len(def.Tools) > 0 {
				cfg.AllowedTools = def.Tools
			}
			cfg.DefinitionVersion = def.Version
			mgmt.SetLocalDefinitionHash(actualHash)
			mgmt.SetLocalDefinitionVersion(def.Version)
			Emit(Event{
				Type:  EventConfigReloaded,
				Agent: cfg.Name,
				Host:  hostname,
				Text:  "definition hot-reloaded via management plane (hash=" + actualHash[:12] + ")",
			})
		})
	}

	// Create findings directory under stateDir so the AI can write structured
	// findings. The LLM's system prompt resolves `{{.StateDir}}/findings/...`
	// against the raw stateDir, so the path here MUST match — don't add an
	// extra cfg.Name segment or HarvestFindings will look in the wrong place.
	if dcfg.StateDir != "" {
		findingsDir := filepath.Join(dcfg.StateDir, "findings")
		_ = os.MkdirAll(findingsDir, 0755)
	}

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

			if suspended.Load() {
				// Operator paused us via the management plane. Skip the work
				// but still emit tick_done so the UI's tick counter advances
				// and the timeline shows a heartbeat-shaped trace instead
				// of a flatline (which would look like a dead daemon).
				Emit(Event{
					Type:   EventTickDone,
					Agent:  cfg.Name,
					Host:   hostname,
					Tick:   n,
					Result: "skipped",
					Text:   "agent suspended",
				})
				continue
			}

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
				execTick(cfg, dcfg, hostname, t, lr, state, mgmt)
				lastRunAt = time.Now()
			}(n, lastRun)

		case sig := <-sigCh:
			timer.Stop()

			if sig == syscall.SIGHUP {
				// Hot config reload — re-parse agent file from disk and apply.
				if cfg.Name != "" {
					if def, err := ParseAgentFile(cfg.Name); err == nil {
						if def.MaxTurns > 0 {
							cfg.MaxTurns = def.MaxTurns
						}
						if def.Effort != "" {
							cfg.Effort = def.Effort
						}
					}
				}
				Emit(Event{
					Type:  EventConfigReloaded,
					Agent: cfg.Name,
					Host:  hostname,
					Text:  "SIGHUP received — agent file reloaded from disk",
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
func execTick(cfg Config, dcfg DaemonConfig, hostname string, tickNum int64, lastRunAt time.Time, state *DaemonState, mgmt *MgmtPlane) {
	start := time.Now()

	Emit(Event{
		Type:  EventTickStart,
		Agent: cfg.Name,
		Host:  hostname,
		Tick:  tickNum,
	})

	// Phase 3: prune dedup cache before running the tick.
	state.PruneDedup()

	// Phase 2: run pre-collectors in parallel, then render system prompt.
	// A non-optional collector failure aborts the tick.
	results, colErr := RunCollectors(dcfg.Collectors, cfg, hostname, tickNum)
	if colErr != nil {
		EmitError(fmt.Errorf("tick %d aborted: %w", tickNum, colErr))
		state.RecordTick(dcfg.StateDir, cfg.Name, true)
		Emit(Event{
			Type:     EventTickDone,
			Agent:    cfg.Name,
			Host:     hostname,
			Tick:     tickNum,
			Result:   "error",
			Duration: time.Since(start).Round(time.Millisecond).String(),
		})
		return
	}

	tctx := BuildTickContext(cfg, hostname, tickNum, lastRunAt, dcfg.StateDir, results)

	rendered, err := RenderPrompt(cfg.SystemPrompt, tctx)
	if err != nil {
		EmitError(fmt.Errorf("tick %d prompt render: %w", tickNum, err))
		rendered = tctx.buildFallbackPrompt()
	}

	// Build a per-tick copy of cfg with rendered system prompt and default user prompt.
	// Tick + Host propagate down so RunClaude/RunOpenAI can stamp them onto every
	// emitted text/tool_call/tool_result/init/done event — required for the UI
	// to group conversation events under the right tick card.
	tickCfg := cfg
	tickCfg.SystemPrompt = rendered
	tickCfg.Prompt = fmt.Sprintf(
		"Perform your scheduled check. Tick: %d. Time: %s.",
		tickNum,
		tctx.TickTime,
	)
	tickCfg.Tick = tickNum
	tickCfg.Host = hostname
	if mgmt != nil {
		// Wire the lookup_findings tool. Without a CP this stays nil and
		// the dispatcher returns "not configured" if the LLM tries it.
		tickCfg.LookupFindings = mgmt.LookupFindings
		// Auto-include lookup_findings in the allowed tools when a CP is
		// configured. The agent file's `tools:` list is for restricting
		// expensive / state-mutating tools — `lookup_findings` is
		// read-only RAG-style context the LLM should always have access
		// to. Skip if the operator has explicitly listed it (avoid dup).
		hasLookup := false
		for _, t := range tickCfg.AllowedTools {
			if t == "lookup_findings" {
				hasLookup = true
				break
			}
		}
		if !hasLookup {
			tickCfg.AllowedTools = append(append([]string(nil), tickCfg.AllowedTools...), "lookup_findings")
		}
	}

	// Touch last-run marker so next tick can calculate elapsed time.
	touchLastRun(dcfg.StateDir, cfg.Name)

	// Phase 3: record tick in persistent state.
	var runErr error
	if cfg.Provider == "claude" {
		runErr = RunClaude(tickCfg)
	} else {
		runErr = RunOpenAI(tickCfg)
	}

	hadError := runErr != nil
	if hadError {
		if isAPI, code := classifyRunError(runErr); isAPI {
			// API-level failure: emit a dedicated event so the Control Plane and
			// downstream consumers can distinguish provider outages from local errors.
			Emit(Event{
				Type:       EventAPIUnavailable,
				Agent:      cfg.Name,
				Host:       hostname,
				Tick:       tickNum,
				Provider:   cfg.Provider,
				Model:      cfg.Model,
				StatusCode: code,
				Error:      runErr.Error(),
			})
			policy := dcfg.APIPolicy
			if policy == nil {
				policy = NoopAPIPolicy{}
			}
			policy.OnAPIUnavailable(context.Background(), cfg.Provider, cfg.Model, code, runErr)
		} else {
			EmitError(runErr)
		}
	}
	state.RecordTick(dcfg.StateDir, cfg.Name, hadError)

	// Harvest any finding JSON files the LLM wrote during this tick. The
	// agent files instruct the LLM to write findings to {stateDir}/findings/
	// — without this step they'd sit on disk forever and never reach the CP.
	// Pass the mgmt-plane known-issues cache so the harvester can suppress
	// fingerprints the operator has triaged (false_positive / wontfix /
	// resolved). Daemons without a management plane pass nil and behave
	// the same as before.
	var known KnownIssuesLookup
	if mgmt != nil {
		known = mgmt
	}
	findingsEmitted := HarvestFindings(state, dcfg.StateDir, cfg.Name, hostname, tickNum, dcfg.DedupeTTL, known)
	if findingsEmitted > 0 {
		// Persist the dedup-cache updates the harvester just made so they
		// survive a daemon restart for the rest of the TTL window.
		// RecordTick already saved the rest of the state above.
		_ = SaveState(dcfg.StateDir, cfg.Name, state)
	}

	result := "completed"
	if hadError {
		result = "error"
	}

	Emit(Event{
		Type:     EventTickDone,
		Agent:    cfg.Name,
		Host:     hostname,
		Tick:     tickNum,
		Result:   result,
		Duration: time.Since(start).Round(time.Millisecond).String(),
		Findings: findingsEmitted,
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
