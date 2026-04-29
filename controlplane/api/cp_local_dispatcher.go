// CP-local orchestration step dispatch.
//
// Cron-triggered orchestrations like t1-finding-batch-triage,
// t1-stuck-finding-sweep, and t1-fleet-health-sweep have no per-host
// context — their job is to read/write the CP's own API. Forcing
// them to target a fleet node is both wasteful (one extra RTT per
// API call) and brittle (breaks when no node is up).
//
// This dispatcher fills the gap: when a step has no `node:` /
// `nodes:` target, we run `okesu auto --agent <name> <prompt>` as a
// subprocess on the CP host itself, capture stdout into the same
// run_lines table the tunnel/jobs dispatchers use, and synthesize a
// DispatchResult identical in shape to the other paths so all the
// downstream `{{step.result.*}}` bindings keep working.

package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/orchestrator"
)

// CPLocalNodeName is the synthetic NodeName used for runs that
// execute on the CP host. Shows up in the Runs UI as the "node" so
// operators can see at a glance which runs were CP-local. We don't
// create a corresponding nodes row — the dispatch path doesn't
// require one.
const CPLocalNodeName = "control-plane"

type cpLocalDispatcher struct {
	reg         *RunRegistry
	store       *db.Store
	agentDirs   []string
	okesuBinary string   // resolved path to the okesu CLI
	envExtras   []string // env vars merged into the subprocess (API keys)
}

func newCPLocalDispatcher(reg *RunRegistry, store *db.Store, agentDirs []string, okesuBinary string, envExtras []string) *cpLocalDispatcher {
	return &cpLocalDispatcher{
		reg:         reg,
		store:       store,
		agentDirs:   agentDirs,
		okesuBinary: okesuBinary,
		envExtras:   envExtras,
	}
}

func (d *cpLocalDispatcher) Dispatch(ctx context.Context, req orchestrator.DispatchRequest) (orchestrator.DispatchResult, error) {
	if req.AgentName == "" {
		return orchestrator.DispatchResult{}, errors.New("cp-local dispatcher: agent name required")
	}
	if d.okesuBinary == "" {
		return orchestrator.DispatchResult{}, errors.New(
			"cp-local dispatcher: no okesu binary resolved. " +
				"Install `okesu` on the CP host (PATH) or pass --cp-local-okesu-binary.")
	}

	// Stage the agent file in a temp .claude/agents dir so the CLI's
	// per-cwd lookup finds it. Isolated per-run; cleaned up on exit
	// so concurrent runs don't collide.
	agentPath, err := findAgentFile(d.agentDirs, req.AgentName)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("agent %q not found in library", req.AgentName)
	}
	agentBytes, err := os.ReadFile(agentPath)
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("read agent file: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "okesu-cp-local-*")
	if err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	agentsDir := filepath.Join(tempDir, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("mkdir agents: %w", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, req.AgentName+".md"), agentBytes, 0o644); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("write agent file: %w", err)
	}

	runID, _ := randomID()
	if err := d.store.CreateRun(db.RunInsert{
		ID:        runID,
		NodeName:  CPLocalNodeName,
		AgentName: req.AgentName,
		Prompt:    req.Prompt,
	}); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("persist run: %w", err)
	}

	run := &Run{
		ID:       runID,
		NodeName: CPLocalNodeName,
		Status:   db.RunStatusRunning,
		subs:     map[chan string]struct{}{},
		finish:   make(chan struct{}),
	}
	d.reg.put(run)

	// Write the rendered prompt to a temp file and pass via
	// --prompt-file so the orchestration's data: block payload (which
	// can be MB-scale JSON) doesn't blow past ARG_MAX. The file lives
	// inside tempDir which gets deleted on return.
	promptPath := filepath.Join(tempDir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte(req.Prompt), 0o600); err != nil {
		return orchestrator.DispatchResult{}, fmt.Errorf("write prompt file: %w", err)
	}

	cmd := exec.CommandContext(ctx, d.okesuBinary, "auto", "--agent", req.AgentName, "--prompt-file", promptPath)
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(), d.envExtras...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.fail(run, runID, err.Error())
		return orchestrator.DispatchResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		d.fail(run, runID, err.Error())
		return orchestrator.DispatchResult{}, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		d.fail(run, runID, err.Error())
		return orchestrator.DispatchResult{}, fmt.Errorf("start subprocess: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go d.streamPipe(&wg, run, "stdout", stdout)
	go d.streamPipe(&wg, run, "stderr", stderr)
	wg.Wait()

	exitErr := cmd.Wait()
	exitCode := 0
	errMsg := ""
	finalStatus := db.RunStatusSucceeded
	if exitErr != nil {
		if ee, ok := exitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			errMsg = exitErr.Error()
		}
		finalStatus = db.RunStatusFailed
	}

	run.complete(finalStatus)
	_ = d.store.FinishRun(runID, finalStatus, exitCode, errMsg)
	d.reg.remove(runID)
	close(run.finish)

	rawLines, _ := d.store.RunLines(runID, 5000)
	result := orchestrator.DispatchResult{
		RunID:        runID,
		Findings:     parseFindingsFromLines(rawLines),
		OutputTail:   joinLineTails(rawLines, 4096),
		CPInstanceID: "local",
		HostResolved: CPLocalNodeName,
	}
	if finalStatus == db.RunStatusSucceeded {
		result.Status = orchestrator.StepStatusCompleted
	} else {
		result.Status = orchestrator.StepStatusFailed
		switch {
		case errMsg != "":
			result.Error = errMsg
		case exitCode != 0:
			result.Error = fmt.Sprintf("exit %d", exitCode)
		}
	}
	return result, nil
}

// fail wraps the bookkeeping for an early failure (before the
// subprocess actually got going). Keeps the body of Dispatch readable.
func (d *cpLocalDispatcher) fail(run *Run, runID, msg string) {
	_ = d.store.FinishRun(runID, db.RunStatusFailed, -1, msg)
	run.completeError(msg)
	d.reg.remove(runID)
	close(run.finish)
}

func (d *cpLocalDispatcher) streamPipe(wg *sync.WaitGroup, run *Run, stream string, pipe io.Reader) {
	defer wg.Done()
	br := bufio.NewReader(pipe)
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\n")
		if line != "" {
			run.appendLine(line)
			_ = d.store.AppendRunLine(run.ID, stream, line)
		}
		if err != nil {
			return
		}
	}
}

// resolveOkesuBinary picks a path to the okesu CLI for CP-local
// subprocess execution. Order:
//
//  1. Explicit path argument (set by --cp-local-okesu-binary).
//  2. exec.LookPath("okesu") — operator dropped it in PATH.
//  3. Same dir as the running CP binary — covers the dev/lab layout
//     where `okesu` lives alongside `okesu-cp`.
//
// Returns "" when none of those work. The dispatcher then surfaces a
// clear "no okesu binary resolved" error at first dispatch.
func resolveOkesuBinary(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit
		}
	}
	if p, err := exec.LookPath("okesu"); err == nil {
		return p
	}
	cpBin, err := os.Executable()
	if err != nil {
		return ""
	}
	cand := filepath.Join(filepath.Dir(cpBin), "okesu")
	if st, err := os.Stat(cand); err == nil && !st.IsDir() {
		return cand
	}
	return ""
}
