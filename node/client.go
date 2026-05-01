// Package node implements the `okesu node` reverse-tunnel client.
//
// The node process holds a long-lived WebSocket+mTLS connection to the CP.
// On each RunAgent request from the CP, it spawns
// `okesu claude|codex|auto [--agent NAME] <prompt>`, captures stdout, and
// streams JSONL lines back as LogLine frames.
package node

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/section9labs/okesu/controlplane/tunnel"
)

// Config tunes the node client.
type Config struct {
	// CPURL is the base URL of the Control Plane mgmt-plane listener,
	// e.g. "https://cp.example.com:8444". The /api/tunnel/connect path is
	// appended automatically.
	CPURL string

	// CertDir holds client.crt, client.key, ca.crt produced by
	// `okesu-cp issue-node-cert --out`.
	CertDir string

	// NodeName is reported in the Hello frame for logging visibility.
	// The authoritative identifier is always the cert CN.
	NodeName string

	// SelfPath is the path to the okesu binary used to spawn run children.
	// Defaults to /proc/self/exe.
	SelfPath string

	// Version reported in Hello.
	Version string
}

// Run blocks running the tunnel client until ctx is cancelled.
// On disconnect it reconnects with exponential back-off (capped at 60s).
func Run(ctx context.Context, cfg Config) error {
	if cfg.CPURL == "" {
		return errors.New("--cp-url is required")
	}
	if cfg.CertDir == "" {
		return errors.New("--cert-dir is required")
	}
	if cfg.SelfPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate self: %w", err)
		}
		cfg.SelfPath = exe
	}

	tlsCfg, err := loadTLS(cfg.CertDir)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	wsURL, err := buildWSURL(cfg.CPURL, "/api/tunnel/connect")
	if err != nil {
		return fmt.Errorf("cp url: %w", err)
	}

	backoff := time.Second
	for {
		err := connectAndServe(ctx, cfg, wsURL, tlsCfg)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("node tunnel: disconnected — %v (reconnecting in %s)", err, backoff)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}
	}
}

func connectAndServe(ctx context.Context, cfg Config, wsURL string, tlsCfg *tls.Config) error {
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: httpClient,
	})
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(64 * 1024)

	var sendMu sync.Mutex
	send := func(f *tunnel.Frame) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		b, err := tunnel.Marshal(f)
		if err != nil {
			return err
		}
		return conn.Write(writeCtx, websocket.MessageText, b)
	}

	// Send hello.
	if err := send(&tunnel.Frame{
		Type:  tunnel.MsgHello,
		Hello: &tunnel.HelloPayload{Node: cfg.NodeName, Version: cfg.Version},
	}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	log.Printf("node tunnel: connected to %s as %q", cfg.CPURL, cfg.NodeName)

	// Track active runs so we can cancel them.
	var (
		runMu sync.Mutex
		runs  = map[string]*runHandle{}
	)
	cancelAll := func() {
		runMu.Lock()
		for _, h := range runs {
			h.cancel()
		}
		runMu.Unlock()
	}
	defer cancelAll()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		f, err := tunnel.Unmarshal(data)
		if err != nil {
			log.Printf("node tunnel: bad frame: %v", err)
			continue
		}
		switch f.Type {
		case tunnel.MsgPing:
			_ = send(&tunnel.Frame{Type: tunnel.MsgPong})
		case tunnel.MsgRun:
			if f.Run == nil {
				continue
			}
			h := startRun(cfg, *f.Run, send)
			runMu.Lock()
			runs[f.Run.RunID] = h
			runMu.Unlock()
			go func(id string, h *runHandle) {
				h.wait()
				runMu.Lock()
				delete(runs, id)
				runMu.Unlock()
			}(f.Run.RunID, h)
		case tunnel.MsgCancel:
			if f.Cancel == nil {
				continue
			}
			runMu.Lock()
			h := runs[f.Cancel.RunID]
			runMu.Unlock()
			if h != nil {
				h.cancel()
			}
		case tunnel.MsgProbe:
			if f.Probe == nil {
				continue
			}
			reply := collectMetadata(cfg, f.Probe.ProbeID)
			_ = send(&tunnel.Frame{Type: tunnel.MsgProbeReply, ProbeReply: reply})
		}
	}
}

// runHandle tracks a single in-flight `okesu` child process.
type runHandle struct {
	cancel func()
	done   chan struct{}
}

func (h *runHandle) wait() { <-h.done }

// startRun spawns the child and pipes its output to the CP.
func startRun(cfg Config, p tunnel.RunPayload, send func(*tunnel.Frame) error) *runHandle {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h := &runHandle{cancel: cancel, done: done}

	go func() {
		defer close(done)

		provider := p.Provider
		if provider == "" {
			provider = "auto"
		}
		args := []string{provider}

		// If the CP shipped agent file content alongside the run request,
		// write it to a temp file and reference it via --agent <path>.
		// This makes one-shot runs work even when the node has never seen
		// the agent before — the CP's library is the source of truth.
		var agentTempPath string
		if p.AgentContent != "" {
			tmp, err := os.CreateTemp("", "okesu-agent-*.md")
			if err == nil {
				_, _ = tmp.WriteString(p.AgentContent)
				_ = tmp.Close()
				agentTempPath = tmp.Name()
				defer os.Remove(agentTempPath)
				args = append(args, "--agent", agentTempPath)
			}
		}
		if agentTempPath == "" && p.Agent != "" {
			args = append(args, "--agent", p.Agent)
		}
		if p.Model != "" {
			args = append(args, "--model", p.Model)
		}
		if p.Effort != "" {
			args = append(args, "--effort", p.Effort)
		}
		if p.MaxTurns > 0 {
			args = append(args, "--max-turns", strconv.Itoa(p.MaxTurns))
		}
		args = append(args, p.Prompt)

		cmd := exec.CommandContext(ctx, cfg.SelfPath, args...)
		// Phase 22.8 PR γ wire-through — merge per-job env from the
		// CP (selector-bound env_var secrets) on top of inherited
		// environment. Env values win on key collision so per-node
		// credentials override host defaults without the node caring
		// how. Empty/missing Env preserves legacy behaviour.
		env := os.Environ()
		for k, v := range p.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = send(&tunnel.Frame{Type: tunnel.MsgExit, Exit: &tunnel.ExitPayload{
				RunID: p.RunID, Code: -1, Error: "stdout pipe: " + err.Error(),
			}})
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			_ = send(&tunnel.Frame{Type: tunnel.MsgExit, Exit: &tunnel.ExitPayload{
				RunID: p.RunID, Code: -1, Error: "stderr pipe: " + err.Error(),
			}})
			return
		}
		if err := cmd.Start(); err != nil {
			_ = send(&tunnel.Frame{Type: tunnel.MsgExit, Exit: &tunnel.ExitPayload{
				RunID: p.RunID, Code: -1, Error: "start: " + err.Error(),
			}})
			return
		}

		var wg sync.WaitGroup
		stream := func(r io.Reader, name string) {
			defer wg.Done()
			scan := bufio.NewScanner(r)
			scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scan.Scan() {
				line := scan.Text()
				_ = send(&tunnel.Frame{Type: tunnel.MsgLine, Line: &tunnel.LinePayload{
					RunID: p.RunID, Stream: name, Data: line,
				}})
			}
		}
		wg.Add(2)
		go stream(stdout, "stdout")
		go stream(stderr, "stderr")
		wg.Wait()

		exitErr := cmd.Wait()
		code := 0
		errMsg := ""
		if exitErr != nil {
			var ee *exec.ExitError
			if errors.As(exitErr, &ee) {
				code = ee.ExitCode()
			} else {
				code = -1
				errMsg = exitErr.Error()
			}
		}
		_ = send(&tunnel.Frame{Type: tunnel.MsgExit, Exit: &tunnel.ExitPayload{
			RunID: p.RunID, Code: code, Error: errMsg,
		}})
	}()

	return h
}

func loadTLS(certDir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(
		filepath.Join(certDir, "client.crt"),
		filepath.Join(certDir, "client.key"),
	)
	if err != nil {
		return nil, fmt.Errorf("client cert: %w", err)
	}
	caBytes, err := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("ca cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("invalid ca.crt PEM")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func buildWSURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = path
	return u.String(), nil
}

// collectMetadata gathers a snapshot of the node's OS / hardware /
// runtime info for the CP's "Refresh metadata" action. Each field is
// best-effort: failures populate `Error` but don't stop the rest. Reads
// /proc files directly where possible (cheaper than shelling out).
func collectMetadata(cfg Config, probeID string) *tunnel.ProbeReplyPayload {
	r := &tunnel.ProbeReplyPayload{ProbeID: probeID}
	var errs []string

	if h, err := os.Hostname(); err == nil {
		r.Hostname = h
	} else {
		errs = append(errs, "hostname: "+err.Error())
	}

	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		r.KernelRelease = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		r.Arch = strings.TrimSpace(string(out))
	}

	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		// Pluck PRETTY_NAME from key=value lines.
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				v := strings.TrimPrefix(line, "PRETTY_NAME=")
				r.OSRelease = strings.Trim(v, `"`)
				break
			}
		}
	}

	r.CPUCount = runtime.NumCPU()

	// /proc/meminfo first line is "MemTotal:       16384 kB"
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						r.MemoryMB = kb / 1024
					}
				}
				break
			}
		}
	}

	// Disk free for the okesu state dir if it exists, otherwise /var.
	// Implementation lives in client_unix.go / client_other.go so the
	// non-Unix builds don't reach for syscall.Statfs (Windows has no
	// such syscall).
	target := "/var/lib/okesu"
	if _, err := os.Stat(target); err != nil {
		target = "/var"
	}
	if mb, ok := diskFreeMB(target); ok {
		r.DiskFreeMB = mb
	}

	r.OkesuVersion = cfg.Version

	if len(errs) > 0 {
		r.Error = strings.Join(errs, "; ")
	}
	return r
}
