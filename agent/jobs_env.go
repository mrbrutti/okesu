package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// jobsEnvPath is the on-disk path the pull-mode jobs runtime
// (okesu-jobs.service) reads ANTHROPIC_API_KEY / OPENAI_API_KEY from
// via EnvironmentFile=-/etc/okesu/jobs.env. Package-private var so
// tests can swap it for a temp file.
var jobsEnvPath = "/etc/okesu/jobs.env"

// writeJobsEnvAtomic writes the configured jobsEnvPath via temp+rename
// so a concurrent reader (okesu-jobs.service starting) never sees a
// half-written file. Mode 0600 — the file holds plaintext API keys.
//
// Empty keys produce an empty file (mode 0600 still). The systemd
// unit uses EnvironmentFile=-/etc/okesu/jobs.env so a missing or
// empty file is fine; agent_run jobs without keys will fail with a
// clear message at exec time, which is the right behavior.
func writeJobsEnvAtomic(anthropic, openai string) error {
	return writeJobsEnvAtomicTo(jobsEnvPath, anthropic, openai)
}

// writeJobsEnvAtomicTo is the parameterized implementation; the
// production caller uses the package-private jobsEnvPath, tests pass
// a temp directory.
func writeJobsEnvAtomicTo(path, anthropic, openai string) error {
	dir := filepath.Dir(path)
	// Ensure parent dir exists. Mode 0700 — the dir holds secrets too.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	var lines []string
	if anthropic != "" {
		lines = append(lines, "ANTHROPIC_API_KEY="+anthropic)
	}
	if openai != "" {
		lines = append(lines, "OPENAI_API_KEY="+openai)
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	tmp, err := os.CreateTemp(dir, ".jobs.env.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// restartJobsService runs `systemctl restart okesu-jobs.service`.
// Quiet on success. Errors propagate to the caller (typically logged
// via Emit). On systems without systemctl, returns the exec.LookPath
// "executable not found" error — caller treats that the same as a
// permission failure: jobs.env is updated, the unit just won't pick
// up the new env until the operator restarts manually.
func restartJobsService() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not available: %w", err)
	}
	out, err := exec.Command("systemctl", "restart", "okesu-jobs.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart okesu-jobs.service: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
