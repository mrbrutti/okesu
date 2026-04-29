// JobsRunner — the host-side pull loop for the orchestration jobs
// queue.
//
// One process per host (systemd unit `okesu-jobs.service`). It uses
// the same mTLS cert layout as `okesu node` (the tunnel runtime), so
// operators can deploy either or both without a separate trust path.
//
// The loop:
//   1. POST a "hello" or just rely on the first poll bumping
//      jobs_runtime_seen_at on the CP.
//   2. GET /api/v1/agents/jobs every NextPollMs (defaults to 5s, CP
//      can throttle via the response).
//   3. For each claimed job, dispatch to a kind-specific handler:
//      - agent_run: spawn `okesu claude/codex/auto`, stream stdout
//        via /output, POST /exit on terminate.
//      - start_tunnel / stop_tunnel: manage an `okesu node` child
//        process, report status via /exit.
//
// The runner is intentionally process-isolated from the daimon
// runtimes — operators can stop the jobs unit independently of any
// daimons running on the same host.

package agent

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// JobsRunnerConfig configures the jobs poll loop. Mirrors MgmtConfig
// but kept separate because the jobs runtime is per-node, not
// per-daimon — the cert dir holds a node-level cert (CN = node name)
// rather than a daimon-level one.
type JobsRunnerConfig struct {
	// CPMgmtURL is the mgmt-plane URL — same as MgmtConfig.URL the
	// daimons use, e.g. https://cp.example.com:8444.
	CPMgmtURL string
	// CertDir holds client.crt, client.key, ca.crt for mTLS. The
	// CN of client.crt identifies the node. Defaults to
	// /etc/okesu/node-certs.
	CertDir string
	// NodeName must match the cert CN; reported to the CP for
	// audit / debug. Defaults to the host's hostname.
	NodeName string
	// OkesuBinary is the path to the okesu binary used to spawn
	// agent runs. Defaults to /usr/local/bin/okesu.
	OkesuBinary string
}

// JobsRunner holds the long-lived state for the poll loop. One
// instance per process; constructed in cmd/okesu/main.go's `jobs`
// subcommand and Run() until the process is signalled.
type JobsRunner struct {
	cfg    JobsRunnerConfig
	client *http.Client

	// tunnel child supervision. Set when a start_tunnel job spawns
	// `okesu node`; cleared when it exits (the supervising goroutine
	// writes a heartbeat-extension flag back to the runner).
	tunnelMu     sync.Mutex
	tunnelChild  *exec.Cmd
	tunnelStopCh chan struct{} // closed by stop_tunnel handler

	// liveness — atomic for cheap reads from the heartbeat reporter.
	tunnelRunning atomic.Bool
}

// NewJobsRunner builds a runner. Errors out if the cert bundle
// can't be loaded — the runtime is useless without mTLS.
func NewJobsRunner(cfg JobsRunnerConfig) (*JobsRunner, error) {
	if cfg.CertDir == "" {
		cfg.CertDir = "/etc/okesu/node-certs"
	}
	if cfg.OkesuBinary == "" {
		cfg.OkesuBinary = "/usr/local/bin/okesu"
	}
	if cfg.NodeName == "" {
		h, _ := os.Hostname()
		cfg.NodeName = h
	}
	tlsCfg, err := loadJobsTLS(cfg.CertDir)
	if err != nil {
		return nil, fmt.Errorf("jobs runtime mTLS: %w", err)
	}
	return &JobsRunner{
		cfg: cfg,
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

// Run drives the poll loop. Blocks until ctx is cancelled.
// Errors during poll are logged but don't abort the loop — the
// runtime self-heals when the CP comes back.
func (r *JobsRunner) Run(ctx context.Context) error {
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "okesu jobs: "+format+"\n", args...)
	}
	logf("starting jobs runtime: cp=%s node=%s cert-dir=%s", r.cfg.CPMgmtURL, r.cfg.NodeName, r.cfg.CertDir)

	pollMs := 5000
	for {
		select {
		case <-ctx.Done():
			r.killTunnel("shutdown")
			return ctx.Err()
		case <-time.After(time.Duration(pollMs) * time.Millisecond):
		}
		jobs, next, err := r.poll(ctx)
		if err != nil {
			logf("poll error: %v", err)
			pollMs = 10000 // back off briefly on error
			continue
		}
		if next > 0 {
			pollMs = next
		}
		for _, j := range jobs {
			r.dispatchJob(ctx, j, logf)
		}
	}
}

// poll runs one GET against /api/v1/agents/jobs. The query string
// carries the live tunnel state so the CP can update its column
// without needing a separate heartbeat round-trip.
func (r *JobsRunner) poll(ctx context.Context) ([]Job, int, error) {
	state := "stopped"
	if r.tunnelRunning.Load() {
		state = "running"
	}
	url := strings.TrimRight(r.cfg.CPMgmtURL, "/") + "/api/v1/agents/jobs?tunnel=" + state
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("jobs poll: %d: %s", resp.StatusCode, string(body))
	}
	var out JobsPollResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	return out.Jobs, out.NextPollMs, nil
}

// dispatchJob picks a kind-specific handler. Each handler reports
// terminal status via postExit; output streaming is the agent_run
// handler's job. Errors here are reported as job failures, not
// runtime errors — a bad job shouldn't take down the loop.
func (r *JobsRunner) dispatchJob(ctx context.Context, j Job, logf func(string, ...any)) {
	logf("claimed job %d (kind=%s)", j.ID, j.Kind)
	switch j.Kind {
	case JobKindAgentRun:
		r.runAgentJob(ctx, j, logf)
	case JobKindStartTunnel:
		r.runStartTunnelJob(ctx, j, logf)
	case JobKindStopTunnel:
		r.runStopTunnelJob(ctx, j, logf)
	default:
		_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "unknown job kind: " + string(j.Kind)})
	}
}

// runAgentJob spawns `okesu claude` (or whichever provider the
// payload requests) with the supplied agent definition + prompt.
// Stdout streams back via /output one line at a time; exit reports
// once the process terminates.
func (r *JobsRunner) runAgentJob(ctx context.Context, j Job, logf func(string, ...any)) {
	args := []string{}
	provider := j.Payload.Provider
	if provider == "" {
		provider = "claude"
	}
	args = append(args, provider)
	if j.Payload.Model != "" {
		args = append(args, "--model", j.Payload.Model)
	}
	if j.Payload.Effort != "" {
		args = append(args, "--effort", j.Payload.Effort)
	}
	if j.Payload.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", j.Payload.MaxTurns))
	}
	// Agent definition shipped from the CP — write to the standard
	// agent-library location so the spawned process's `--agent <name>`
	// resolver finds it. Using `--agent` (not `--system`) means the
	// existing agent frontmatter (provider, model, tools, max_turns)
	// is honoured; using `--system` would treat the whole file as a
	// raw system prompt and discard the frontmatter directives.
	if j.Payload.AgentContent != "" && j.Payload.Agent != "" {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".claude", "agents")
		if err := os.MkdirAll(dir, 0755); err != nil {
			_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "mkdir agent dir: " + err.Error()})
			return
		}
		agentPath := filepath.Join(dir, j.Payload.Agent+".md")
		if err := os.WriteFile(agentPath, []byte(j.Payload.AgentContent), 0644); err != nil {
			_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "write agent file: " + err.Error()})
			return
		}
		args = append(args, "--agent", j.Payload.Agent)
	} else if j.Payload.Agent != "" {
		// Agent referenced by name only — relies on the host having
		// the file pre-staged in ~/.claude/agents (e.g. from a deploy
		// that included this agent file).
		args = append(args, "--agent", j.Payload.Agent)
	}
	args = append(args, j.Payload.Prompt)

	cmd := exec.CommandContext(ctx, r.cfg.OkesuBinary, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "stdout pipe: " + err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "stderr pipe: " + err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "start: " + err.Error()})
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go r.streamPipe(ctx, j.ID, "stdout", stdout, &wg, logf)
	go r.streamPipe(ctx, j.ID, "stderr", stderr, &wg, logf)
	wg.Wait()

	exitErr := cmd.Wait()
	exitCode := 0
	errMsg := ""
	if exitErr != nil {
		if ee, ok := exitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			errMsg = exitErr.Error()
		}
	}
	if err := r.postExit(ctx, j.ID, JobExit{ExitCode: exitCode, Error: errMsg}); err != nil {
		logf("post exit for job %d: %v", j.ID, err)
	}
}

// streamPipe reads lines from the agent's stdout/stderr and POSTs
// them as they arrive. Per-line POSTs keep the latency-to-UI low at
// the cost of more requests; for typical agent output (a few lines
// per second) that's the right trade.
func (r *JobsRunner) streamPipe(ctx context.Context, jobID int64, stream string, pipe io.Reader, wg *sync.WaitGroup, logf func(string, ...any)) {
	defer wg.Done()
	br := bufio.NewReader(pipe)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if perr := r.postOutput(ctx, jobID, JobOutputChunk{Stream: stream, Data: strings.TrimRight(line, "\n")}); perr != nil {
				logf("post output for job %d: %v", jobID, perr)
			}
		}
		if err != nil {
			return
		}
	}
}

// runStartTunnelJob spawns `okesu node` as a managed child. The
// tunnel registers itself with the CP via the existing reverse-mTLS
// flow; we just supervise the lifetime.
func (r *JobsRunner) runStartTunnelJob(ctx context.Context, j Job, logf func(string, ...any)) {
	r.tunnelMu.Lock()
	if r.tunnelChild != nil && r.tunnelChild.ProcessState == nil {
		// Already running; treat as success.
		r.tunnelMu.Unlock()
		_ = r.postExit(ctx, j.ID, JobExit{ExitCode: 0, TunnelStarted: true})
		return
	}
	cpURL := j.Payload.CPMgmtURL
	if cpURL == "" {
		cpURL = r.cfg.CPMgmtURL
	}
	certDir := j.Payload.CertDir
	if certDir == "" {
		certDir = r.cfg.CertDir
	}
	nodeName := j.Payload.NodeName
	if nodeName == "" {
		nodeName = r.cfg.NodeName
	}
	args := []string{
		"node",
		"--cp-url", cpURL,
		"--cert-dir", certDir,
		"--name", nodeName,
	}
	cmd := exec.Command(r.cfg.OkesuBinary, args...)
	cmd.Stdout = os.Stderr // surface in journalctl alongside the runner's own logs
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		r.tunnelMu.Unlock()
		_ = r.postExit(ctx, j.ID, JobExit{ExitCode: -1, Error: "start tunnel: " + err.Error()})
		return
	}
	r.tunnelChild = cmd
	r.tunnelStopCh = make(chan struct{})
	stopCh := r.tunnelStopCh
	r.tunnelRunning.Store(true)
	r.tunnelMu.Unlock()
	logf("tunnel child started pid=%d", cmd.Process.Pid)
	_ = r.postExit(ctx, j.ID, JobExit{ExitCode: 0, TunnelStarted: true})

	// Supervise — when the child exits, clear the running flag so
	// the next heartbeat tells the CP the tunnel is gone.
	go func() {
		_ = cmd.Wait()
		r.tunnelMu.Lock()
		if r.tunnelChild == cmd {
			r.tunnelChild = nil
			r.tunnelStopCh = nil
		}
		r.tunnelRunning.Store(false)
		r.tunnelMu.Unlock()
		logf("tunnel child exited pid=%d", cmd.Process.Pid)
		select {
		case <-stopCh: // expected: operator stopped it
		default:
			// Unexpected exit — leave the running flag false; the
			// engine can request another start_tunnel job if it
			// still wants the tunnel up.
		}
	}()
}

func (r *JobsRunner) runStopTunnelJob(ctx context.Context, j Job, logf func(string, ...any)) {
	r.killTunnel("stop_tunnel job")
	_ = r.postExit(ctx, j.ID, JobExit{ExitCode: 0})
	_ = logf
}

func (r *JobsRunner) killTunnel(reason string) {
	r.tunnelMu.Lock()
	defer r.tunnelMu.Unlock()
	if r.tunnelChild == nil || r.tunnelChild.Process == nil {
		return
	}
	_ = r.tunnelChild.Process.Signal(os.Interrupt)
	if r.tunnelStopCh != nil {
		close(r.tunnelStopCh)
	}
	_ = reason
}

// postOutput POSTs one chunk of stdout/stderr to the CP. Errors are
// swallowed by the caller — a missed chunk during a network blip is
// acceptable; the agent's output is also written to the local
// daemon journal via the spawned process's own logging.
func (r *JobsRunner) postOutput(ctx context.Context, jobID int64, chunk JobOutputChunk) error {
	url := fmt.Sprintf("%s/api/v1/agents/jobs/%d/output", strings.TrimRight(r.cfg.CPMgmtURL, "/"), jobID)
	body, _ := json.Marshal(chunk)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post output %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (r *JobsRunner) postExit(ctx context.Context, jobID int64, payload JobExit) error {
	url := fmt.Sprintf("%s/api/v1/agents/jobs/%d/exit", strings.TrimRight(r.cfg.CPMgmtURL, "/"), jobID)
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post exit %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// loadJobsTLS reads the three-file cert bundle from certDir and
// returns a tls.Config suitable for an outbound mTLS client. Same
// layout `okesu node` already uses, which is also the layout the
// CP's `issue-node-cert` command emits — so an operator who's
// already running the tunnel can drop the jobs runtime in without
// re-provisioning.
func loadJobsTLS(certDir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(certDir, "client.crt"), filepath.Join(certDir, "client.key"))
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	caBytes, err := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("read ca: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("parse ca cert from %s", certDir)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
