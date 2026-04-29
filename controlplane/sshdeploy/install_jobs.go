// Install the host-side jobs runtime (`okesu jobs`) on a target via
// SSH. Same dependency stack as the daimon deploy path — Linux host,
// systemd, the operator's SSH credential — but writes a single new
// systemd unit and starts the runtime as a stand-alone service
// rather than per-daimon.
//
// The runtime authenticates with a node-level mTLS cert (CN = node
// name) we issue on the CP side and ship over SSH alongside the
// binary. Same cert layout `okesu node` already uses, so an operator
// who later wants the tunnel can drop in `okesu node` without
// re-provisioning trust.

package sshdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func readBinary(p string) ([]byte, error) { return os.ReadFile(p) }

// InstallJobsRequest carries everything InstallJobsRuntime needs.
// Most fields mirror DeployRequest — same SSH credentials, same
// binary resolver — with the addition of the freshly-issued node
// cert bundle.
type InstallJobsRequest struct {
	Cred Credential

	// NodeName is the cert CN; the daemon reports it on every poll.
	// Should match the row in `nodes`.
	NodeName string

	// CPMgmtURL is the URL the daemon polls — same value the daimon
	// units use, e.g. https://cp.example.com:8444.
	CPMgmtURL string

	// DaemonBinaryResolver is the shared multi-arch resolver. Falls
	// back to DaemonBinaryPath when no per-arch resolution is set.
	DaemonBinaryResolver DaemonBinaryResolver
	DaemonBinaryPath     string

	// Cert bundle: PEM-encoded client cert, key, and CA. Issued on
	// the CP side from the same CA the mgmt-plane uses, so the
	// daemon's mTLS handshake against /api/v1/agents/jobs succeeds.
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	CACertPEM     []byte

	// API keys forwarded to /etc/okesu/jobs.env so spawned `okesu
	// claude` / `okesu codex` processes have the credentials the
	// orchestrator's agent_run jobs require. Empty values are omitted.
	AnthropicAPIKey string
	OpenAIAPIKey    string
}

// InstallJobsResult mirrors DeployResult — captured target metadata
// + a multi-line install log so the UI can show what happened.
type InstallJobsResult struct {
	TargetOS   string
	TargetArch string
	Hostname   string
}

// InstallJobsRuntime opens the SSH connection, installs the binary
// and cert bundle, writes the systemd unit, and starts the service.
// Idempotent: re-running the install on a host that already has it
// just refreshes the binary + certs and bounces the unit.
func InstallJobsRuntime(ctx context.Context, req InstallJobsRequest, logFn LogFn) (InstallJobsResult, error) {
	emit := func(format string, a ...any) {
		if logFn != nil {
			logFn(fmt.Sprintf(format, a...))
		}
	}
	var result InstallJobsResult

	c, err := Dial(req.Cred)
	if err != nil {
		return result, fmt.Errorf("ssh dial: %w", err)
	}
	defer c.Close()

	// Pre-flight: same checks as the daimon Deploy — capture
	// os/arch for binary resolution + service-manager dispatch.
	emit("→ pre-flight: arch, hostname, os")
	preflight, err := c.Run("hostname && uname -m && uname -s && cat /etc/os-release 2>/dev/null | head -3")
	if err != nil {
		return result, fmt.Errorf("pre-flight: %w (output: %s)", err, preflight)
	}
	hostname, targetArch, targetOS := parsePreflight(preflight)
	result.Hostname = hostname
	result.TargetArch = targetArch
	result.TargetOS = targetOS
	emit("✓ %s · %s/%s", hostname, targetOS, targetArch)

	// Ensure the unprivileged daemon user + state dirs exist. The
	// install endpoint is the lightweight "I just want pull-mode
	// dispatch on this host" entry point — operators can run it on a
	// host that's never seen Deploy(), so we can't assume the user
	// is already present.
	user, group := okesuUserGroup(targetOS)
	if err := bootstrapOkesuUser(c, req.Cred.SudoPassword, targetOS); err != nil {
		return result, err
	}

	// 1. Stage the binary if not already present at the canonical
	// path, or if its hash differs. We trust the daemon Deploy
	// flow's binary placement and just verify here — a fresh
	// install rather than a daimon-deploy host gets the binary now.
	binPath, err := resolveDaemonBinaryPathSimple(req, targetOS, targetArch)
	if err != nil {
		return result, fmt.Errorf("resolve daemon binary: %w", err)
	}
	emit("→ uploading binary (target /usr/local/bin/okesu)")
	if err := uploadAndInstallBinary(c, req.Cred.SudoPassword, binPath); err != nil {
		return result, fmt.Errorf("install binary: %w", err)
	}
	emit("✓ binary in place")

	// 2. Cert bundle.
	emit("→ writing node cert bundle to /etc/okesu/node-certs")
	if err := writeCertBundle(c, req.Cred.SudoPassword, req.ClientCertPEM, req.ClientKeyPEM, req.CACertPEM, user, group); err != nil {
		return result, fmt.Errorf("write certs: %w", err)
	}
	emit("✓ certs installed")

	// 3. Env file with API keys for spawned agent_run jobs.
	if envContent := buildJobsEnvFile(req.AnthropicAPIKey, req.OpenAIAPIKey); envContent != "" {
		emit("→ writing /etc/okesu/jobs.env")
		if err := writeWithSudo(c, req.Cred.SudoPassword, "/etc/okesu/jobs.env", envContent, "0600"); err != nil {
			return result, fmt.Errorf("write jobs.env: %w", err)
		}
		if _, err := runWithSudo(c, req.Cred.SudoPassword, fmt.Sprintf("chown %s:%s /etc/okesu/jobs.env", user, group)); err != nil {
			emit("  ! could not chown /etc/okesu/jobs.env: %v", err)
		}
		emit("✓ env file installed")
	}

	// 4. Service unit + start, via the flavour-appropriate manager.
	mgr, err := Detect(c, targetOS)
	if err != nil {
		return result, fmt.Errorf("detect service manager: %w", err)
	}
	emit("→ %s: installing okesu-jobs", mgr.Flavour())
	spec := ServiceSpec{
		Name:            "okesu-jobs",
		Description:     "Okesu pull-mode jobs runtime",
		ExecStart:       fmt.Sprintf("/usr/local/bin/okesu jobs --cp-url %q --name %q", req.CPMgmtURL, req.NodeName),
		User:            user,
		Group:           group,
		EnvironmentFile: "/etc/okesu/jobs.env",
	}
	if err := mgr.Install(c, req.Cred.SudoPassword, spec); err != nil {
		return result, fmt.Errorf("install unit: %w", err)
	}
	if err := mgr.EnableAndStart(c, req.Cred.SudoPassword, "okesu-jobs"); err != nil {
		return result, fmt.Errorf("start unit: %w", err)
	}
	emit("✓ okesu-jobs running")
	return result, nil
}

// uploadAndInstallBinary stages the binary at /tmp and moves it into
// /usr/local/bin via sudo. Mirrors the daimon Deploy flow's binary
// step but standalone so a fresh-install host doesn't need a daimon
// to also be installed.
func uploadAndInstallBinary(c *Client, sudoPassword, localPath string) error {
	data, err := osReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local binary %q: %w", localPath, err)
	}
	staged := stagePath(c, "okesu-jobs-install.bin")
	if err := c.WriteFile(staged, data, 0755); err != nil {
		return err
	}
	script := fmt.Sprintf(`
set -e
install -m 0755 %s /usr/local/bin/okesu
rm -f %s
`, staged, staged)
	_, err = runWithSudo(c, sudoPassword, script)
	return err
}

// osReadFile is a thin wrapper so the install path doesn't need a
// direct os import — keeps `os` confined to test setup if we add
// any.
func osReadFile(path string) ([]byte, error) {
	return readBinary(path)
}

// writeCertBundle drops the three cert files into /etc/okesu/node-certs
// with the right modes. Same layout `okesu node` uses. The dir is
// chowned to the OS-specific daemon user so the runtime can read the
// key after dropping privileges.
func writeCertBundle(c *Client, sudoPassword string, clientCert, clientKey, caCert []byte, user, group string) error {
	dir := "/etc/okesu/node-certs"
	if _, err := runWithSudo(c, sudoPassword, "mkdir -p "+dir+" && chmod 0755 "+dir); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
		mode string
	}{
		{"client.crt", clientCert, "0644"},
		{"client.key", clientKey, "0600"},
		{"ca.crt", caCert, "0644"},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := writeWithSudo(c, sudoPassword, path, string(f.data), f.mode); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if _, err := runWithSudo(c, sudoPassword, fmt.Sprintf("chown -R %s:%s %s", user, group, dir)); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}
	return nil
}

// writeWithSudo writes a file with sudo by piping through a heredoc
// to `tee` — keeps the data off the SSH command line so binaries +
// keys with awkward characters don't have to be shell-escaped.
//
// When the SSH session is already connected as root (Client.IsRoot)
// the sudo wrapper is skipped entirely. Some target images ship a
// broken PAM stack that fails even passwordless sudo for root, so
// invoking the install binary directly is both faster and more
// robust.
func writeWithSudo(c *Client, sudoPassword, path, content, mode string) error {
	cmd := fmt.Sprintf("install -m %s /dev/stdin %s", mode, path)
	switch {
	case c.IsRoot():
		// already root — no need (and no benefit) to wrap in sudo
	case sudoPassword != "":
		cmd = "sudo -S -- " + cmd
	default:
		cmd = "sudo -- " + cmd
	}
	_, err := c.RunWithStdin(cmd, content)
	return err
}

// parsePreflight extracts hostname / arch / os from the multi-line
// preflight output. Same parsing the daimon Deploy uses — kept here
// rather than re-exported because both paths read it identically.
func parsePreflight(out string) (hostname, arch, os string) {
	// Output: hostname\n<arch>\n<os>\n[os-release lines...]
	// We only need the first three lines.
	lines := splitLines(out)
	if len(lines) >= 1 {
		hostname = lines[0]
	}
	if len(lines) >= 2 {
		arch = archFromUname(lines[1])
	}
	if len(lines) >= 3 {
		switch lines[2] {
		case "Linux":
			os = "linux"
		case "FreeBSD":
			os = "freebsd"
		case "Darwin":
			os = "darwin"
		default:
			os = lines[2]
		}
	}
	return
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// resolveDaemonBinaryPathSimple is a trimmed-down copy of the
// resolveDaemonBinaryPath function used by Deploy — the install
// path doesn't need the per-agent path overrides, so we keep it
// inline rather than refactor the existing function.
func resolveDaemonBinaryPathSimple(req InstallJobsRequest, targetOS, targetArch string) (string, error) {
	if req.DaemonBinaryResolver != nil {
		if p, err := req.DaemonBinaryResolver.Resolve(targetOS, targetArch); err == nil && p != "" {
			return p, nil
		}
	}
	if req.DaemonBinaryPath != "" {
		return req.DaemonBinaryPath, nil
	}
	return "", fmt.Errorf("no daemon binary configured for %s/%s", targetOS, targetArch)
}

// buildJobsEnvFile renders /etc/okesu/jobs.env. Empty keys are
// omitted so an install on a CP without the optional secret doesn't
// produce a half-populated file.
func buildJobsEnvFile(anthropic, openai string) string {
	var out string
	if anthropic != "" {
		out += "ANTHROPIC_API_KEY=" + anthropic + "\n"
	}
	if openai != "" {
		out += "OPENAI_API_KEY=" + openai + "\n"
	}
	return out
}

