// Runner — the S3 equivalent of agent.JobsRunner. One process per
// node, polls the bucket for queued jobs, executes them, streams
// output as numbered chunk objects, posts terminal exit objects.
//
// All shape decisions are documented in docs/s3-transport.md and
// echoed in wire.go. This file is the loop that ties them together.

package s3transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/section9labs/okesu/agent"
)

// RunnerConfig configures the S3-mode jobs runtime. Mirrors the
// HTTPS-mode JobsRunnerConfig — the daemon main picks one based on
// the --transport flag.
type RunnerConfig struct {
	Client    *Client
	CPID      string
	NodeID    int64
	NodeName  string
	OkesuBin  string

	// Initial poll cadence; CP overrides via desired.json.
	PollInterval        time.Duration
	HeartbeatInterval   time.Duration
	OutputFlushInterval time.Duration
	OutputFlushMaxBytes int
}

// Runner is the long-lived poll loop.
type Runner struct {
	cfg RunnerConfig

	// dynamic config from CP, hot-reloaded on version bump
	mu         sync.Mutex
	desiredVer int
	pollEvery  time.Duration
	hbEvery    time.Duration
	flushEvery time.Duration
	flushMax   int
	agents     []string
}

// NewRunner constructs a Runner with sensible defaults for any
// fields RunnerConfig leaves zero.
func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.OkesuBin == "" {
		cfg.OkesuBin = "/usr/local/bin/okesu"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.OutputFlushInterval == 0 {
		cfg.OutputFlushInterval = 5 * time.Second
	}
	if cfg.OutputFlushMaxBytes == 0 {
		cfg.OutputFlushMaxBytes = 4096
	}
	r := &Runner{cfg: cfg}
	r.pollEvery = cfg.PollInterval
	r.hbEvery = cfg.HeartbeatInterval
	r.flushEvery = cfg.OutputFlushInterval
	r.flushMax = cfg.OutputFlushMaxBytes
	return r
}

// Run drives the loop until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "okesu s3-jobs: "+format+"\n", args...)
	}
	logf("starting s3 jobs runtime: cp=%s node=%d bucket=%s",
		r.cfg.CPID, r.cfg.NodeID, r.cfg.Client.bucket)

	// Heartbeat goroutine — independent of the work loop so a
	// long-running job doesn't starve liveness signalling. The
	// scanner uses heartbeat.json modified-time to know the node is
	// alive; missing it for >2x interval flips status to "stale".
	go r.heartbeatLoop(ctx, logf)
	go r.configLoop(ctx, logf)

	for {
		r.mu.Lock()
		wait := r.pollEvery
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}

		jobs, err := r.listInbox(ctx)
		if err != nil {
			logf("inbox list error: %v", err)
			continue
		}
		for _, info := range jobs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.handleJob(ctx, info, logf)
		}
	}
}

// listInbox returns objects under the per-node jobs/inbox/ prefix.
func (r *Runner) listInbox(ctx context.Context) ([]ObjectInfo, error) {
	prefix := NodePrefix(r.cfg.CPID, r.cfg.NodeID) + DirJobsInbox
	return r.cfg.Client.List(ctx, prefix, 100)
}

// handleJob fetches one inbox object, claims it via PutIfAbsent,
// dispatches to the kind-specific handler, then deletes the inbox
// copy. On a claim race the handler exits early — the rival CP /
// node owns this job.
func (r *Runner) handleJob(ctx context.Context, info ObjectInfo, logf func(string, ...any)) {
	jobIDFromKey := strings.TrimSuffix(filepath.Base(info.Key), ".json")
	body, err := r.cfg.Client.GetBytes(ctx, info.Key)
	if err != nil {
		logf("read inbox %s: %v", info.Key, err)
		return
	}
	// Inbox envelope carries the same shape as the HTTPS Job —
	// {kind, run_id, payload, created_at} — so we decode it as one.
	var job agent.Job
	if err := json.Unmarshal(body, &job); err != nil {
		logf("decode inbox %s: %v", info.Key, err)
		_ = r.cfg.Client.Delete(ctx, info.Key)
		return
	}

	// Atomic claim. We intentionally key the marker on the same job
	// id the CP used in the inbox filename; PutIfAbsent prevents a
	// second claimer from racing us.
	claimKey := NodePrefix(r.cfg.CPID, r.cfg.NodeID) + DirJobs + jobIDFromKey + "/claimed.json"
	claim := ClaimMarker{
		JobID:     jobIDFromKey,
		NodeID:    r.cfg.NodeID,
		ClaimedAt: time.Now().UTC(),
	}
	cb, _ := json.Marshal(claim)
	if err := r.cfg.Client.PutIfAbsent(ctx, claimKey, cb, "application/json"); err != nil {
		if err == ErrPreconditionFailed {
			logf("job %s already claimed; skipping", jobIDFromKey)
			_ = r.cfg.Client.Delete(ctx, info.Key)
			return
		}
		logf("claim job %s: %v", jobIDFromKey, err)
		return
	}

	_ = r.cfg.Client.Delete(ctx, info.Key)
	logf("claimed job %s (kind=%s)", jobIDFromKey, job.Kind)

	switch job.Kind {
	case agent.JobKindAgentRun:
		r.runAgentJob(ctx, jobIDFromKey, job, logf)
	default:
		_ = r.postExit(ctx, jobIDFromKey, agent.JobExit{ExitCode: -1, Error: "unsupported kind for s3 transport: " + string(job.Kind)})
	}
}

// runAgentJob spawns `okesu claude` (or codex) with the supplied
// agent file content + prompt, streams stdout into chunked S3
// objects, and posts a terminal exit object.
func (r *Runner) runAgentJob(ctx context.Context, jobID string, j agent.Job, logf func(string, ...any)) {
	// Same flow as agent/jobs_runner.go's runAgentJob — we write the
	// agent file to ~/.claude/agents/<name>.md, then `okesu claude
	// --agent <name> "<prompt>"`. Mid-flight stdout streams to
	// jobs/<id>/output/<seq>.txt.
	provider := j.Payload.Provider
	if provider == "" {
		provider = "claude"
	}
	agentName := j.Payload.Agent
	if agentName == "" {
		agentName = "default"
	}

	// Drop the agent file in ~/.claude/agents on the local host.
	home, err := os.UserHomeDir()
	if err != nil {
		_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: -1, Error: "user home: " + err.Error()})
		return
	}
	dir := filepath.Join(home, ".claude", "agents")
	if provider == "codex" {
		dir = filepath.Join(home, ".codex", "agents")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: -1, Error: "mkdir agents: " + err.Error()})
		return
	}
	agentPath := filepath.Join(dir, agentName+".md")
	if err := os.WriteFile(agentPath, []byte(j.Payload.AgentContent), 0o644); err != nil {
		_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: -1, Error: "write agent: " + err.Error()})
		return
	}

	args := []string{provider, "--agent", agentName, j.Payload.Prompt}
	cmd := exec.CommandContext(ctx, r.cfg.OkesuBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: -1, Error: "stdout pipe: " + err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: -1, Error: "stderr pipe: " + err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: -1, Error: "start: " + err.Error()})
		return
	}

	// Stream both pipes through a chunk writer that flushes on
	// timer or byte budget — minimises object count without losing
	// output if a job dies mid-flight.
	var seq int
	flush := newChunkFlusher(ctx, r.cfg.Client, NodePrefix(r.cfg.CPID, r.cfg.NodeID)+DirJobs+jobID+"/output/", &seq, r.flushEvery, r.flushMax)
	var wg sync.WaitGroup
	wg.Add(2)
	go pipeIntoFlusher(stdout, flush, &wg)
	go pipeIntoFlusher(stderr, flush, &wg)

	exitErr := cmd.Wait()
	wg.Wait()
	flush.Close()

	exitCode := 0
	errStr := ""
	if exitErr != nil {
		errStr = exitErr.Error()
		if e, ok := exitErr.(*exec.ExitError); ok {
			exitCode = e.ExitCode()
		} else {
			exitCode = -1
		}
	}
	_ = r.postExit(ctx, jobID, agent.JobExit{ExitCode: exitCode, Error: errStr})
	logf("job %s exited code=%d", jobID, exitCode)
}

// postExit writes the terminal exit object. Idempotent — re-running
// posts a fresh body, which is fine because the CP scanner reads
// only the most recent.
func (r *Runner) postExit(ctx context.Context, jobID string, exit agent.JobExit) error {
	key := NodePrefix(r.cfg.CPID, r.cfg.NodeID) + DirJobs + jobID + "/exit.json"
	body, _ := json.Marshal(exit)
	return r.cfg.Client.Put(ctx, key, body, "application/json")
}

// heartbeatLoop refreshes heartbeat.json on a steady cadence.
func (r *Runner) heartbeatLoop(ctx context.Context, logf func(string, ...any)) {
	for {
		r.mu.Lock()
		wait := r.hbEvery
		r.mu.Unlock()
		hb := Heartbeat{
			NodeID:        r.cfg.NodeID,
			TS:            time.Now().UTC(),
			TunnelRunning: false,
			Version:       agent.Version(),
		}
		body, _ := json.Marshal(hb)
		if err := r.cfg.Client.Put(ctx, NodePrefix(r.cfg.CPID, r.cfg.NodeID)+FileHeartbeat, body, "application/json"); err != nil {
			logf("heartbeat: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// configLoop polls for desired config updates and applies them. The
// CP bumps version on every change; the runner only re-reads timers
// when version increased.
func (r *Runner) configLoop(ctx context.Context, logf func(string, ...any)) {
	const interval = 60 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		body, err := r.cfg.Client.GetBytes(ctx, NodePrefix(r.cfg.CPID, r.cfg.NodeID)+FileDesiredConfig)
		if err != nil {
			if err != ErrNotFound {
				logf("config get: %v", err)
			}
			continue
		}
		var d DesiredConfig
		if err := json.Unmarshal(body, &d); err != nil {
			logf("config decode: %v", err)
			continue
		}
		r.applyDesired(d, logf)
	}
}

func (r *Runner) applyDesired(d DesiredConfig, logf func(string, ...any)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d.Version <= r.desiredVer {
		return
	}
	r.desiredVer = d.Version
	if d.PollIntervalMs > 0 {
		r.pollEvery = time.Duration(d.PollIntervalMs) * time.Millisecond
	}
	if d.HeartbeatIntervalMs > 0 {
		r.hbEvery = time.Duration(d.HeartbeatIntervalMs) * time.Millisecond
	}
	if d.OutputFlushIntervalMs > 0 {
		r.flushEvery = time.Duration(d.OutputFlushIntervalMs) * time.Millisecond
	}
	if d.OutputFlushMaxBytes > 0 {
		r.flushMax = d.OutputFlushMaxBytes
	}
	if len(d.Agents) > 0 {
		r.agents = d.Agents
	}
	logf("applied desired config v%d (poll=%v, heartbeat=%v, flush=%v/%dB)",
		d.Version, r.pollEvery, r.hbEvery, r.flushEvery, r.flushMax)
}

// ───────────────────────── chunk flusher ─────────────────────────

type chunkFlusher struct {
	ctx      context.Context
	cli      *Client
	prefix   string
	seq      *int
	maxBytes int
	tick     time.Duration

	mu  sync.Mutex
	buf []byte
	t   *time.Timer
}

func newChunkFlusher(ctx context.Context, cli *Client, prefix string, seq *int, tick time.Duration, max int) *chunkFlusher {
	cf := &chunkFlusher{ctx: ctx, cli: cli, prefix: prefix, seq: seq, maxBytes: max, tick: tick}
	cf.t = time.AfterFunc(tick, cf.tickFlush)
	return cf
}

func (f *chunkFlusher) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.buf = append(f.buf, p...)
	full := len(f.buf) >= f.maxBytes
	f.mu.Unlock()
	if full {
		f.flushNow()
	}
	return len(p), nil
}

func (f *chunkFlusher) tickFlush() {
	f.flushNow()
	f.t.Reset(f.tick)
}

func (f *chunkFlusher) flushNow() {
	f.mu.Lock()
	if len(f.buf) == 0 {
		f.mu.Unlock()
		return
	}
	body := f.buf
	f.buf = nil
	*f.seq++
	seq := *f.seq
	f.mu.Unlock()
	key := fmt.Sprintf("%s%05d.txt", f.prefix, seq)
	_ = f.cli.Put(f.ctx, key, body, "text/plain; charset=utf-8")
}

func (f *chunkFlusher) Close() {
	if f.t != nil {
		f.t.Stop()
	}
	f.flushNow()
}

func pipeIntoFlusher(r io.Reader, w io.Writer, wg *sync.WaitGroup) {
	defer wg.Done()
	br := bufio.NewReader(r)
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// genObjectName produces a key suffix from a uuid — used for events
// and findings where multiple writes per second compete for unique
// names.
func genObjectName() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), shortID())
}

func shortID() string {
	id := uuid.NewString()
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
