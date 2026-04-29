package sshdeploy

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// DeployRequest describes the artifacts and settings for one node bootstrap.
type DeployRequest struct {
	Cred Credential

	// AgentsToInstall lists agent names whose files exist in DaimonFilesDir.
	AgentsToInstall []string

	// DaemonBinaryPath is the FALLBACK single-arch daemon binary path on the
	// CP host. Used only when DaemonBinaryResolver is nil. Uploaded to
	// /usr/local/bin/okesu.
	DaemonBinaryPath string

	// DaemonBinaryResolver is the multi-arch lookup. When non-nil, the
	// orchestrator runs `uname -m` on the target, maps the result to a Go
	// arch ("amd64", "arm64"), and asks the resolver for the matching binary.
	// If the resolver returns an error the deploy aborts with a clear message.
	DaemonBinaryResolver DaemonBinaryResolver

	// DaimonFilesDir on the CP host — *.md files matching AgentsToInstall are uploaded.
	DaimonFilesDir string

	// Mgmt-plane bundle for the daemon. If non-empty, written under /etc/okesu/<agent>/.
	// Map of agent name → {client.crt, client.key, ca.crt} bytes.
	MgmtCerts map[string]MgmtCertBundle

	// Webhook secret to drop in /etc/okesu/agents/<agent>.env. Empty disables.
	WebhookSecret string

	// WebhookURL is the absolute URL daemons should POST events to. Required
	// when WebhookSecret is set. Templated into the agent file's outputs section.
	WebhookURL string

	// MgmtURL is the absolute URL of the CP's management plane. Required
	// when MgmtCerts has entries. Templated into the agent file's management section.
	MgmtURL string

	// AnthropicAPIKey, OpenAIAPIKey for /etc/okesu/agents/<agent>.env. Empty disables.
	AnthropicAPIKey string
	OpenAIAPIKey    string

	// JobsRuntime: when set, deploy also installs and starts
	// okesu-jobs.service alongside the daimons. The cert bundle
	// authenticates the host's pull-mode jobs runtime when polling
	// the CP's mgmt-plane jobs endpoints. Phase 6 default: deploy
	// always sets this so a fresh node lands a fully-functional
	// orchestration target out of the box.
	JobsRuntime *MgmtCertBundle
	// NodeName is what `okesu jobs --name` reports — should match
	// the cert CN. Defaults to the operator's `nodes.name`.
	NodeName string
}

// MgmtCertBundle holds the three PEM artifacts an agent needs for mTLS.
type MgmtCertBundle struct {
	ClientCert []byte
	ClientKey  []byte
	CACert     []byte
}

// DaemonBinaryResolver looks up the daemon binary path for a target's
// reported os/arch. Implementations are backed by the `daemon_binaries`
// table or by a static directory scan.
type DaemonBinaryResolver interface {
	Resolve(osName, arch string) (path string, err error)
}

// unameSToOS maps `uname -s` output to the lowercase identifiers the
// rest of the package uses (linux, darwin, freebsd, openbsd, illumos,
// sunos). Unrecognised values pass through lowercased.
func unameSToOS(unameS string) string {
	v := strings.ToLower(strings.TrimSpace(unameS))
	switch v {
	case "linux", "darwin", "freebsd", "openbsd", "illumos":
		return v
	case "sunos":
		// SunOS reports as "SunOS" on both Solaris and illumos; treat
		// as illumos by convention since the open-source distros
		// (OmniOS, OpenIndiana) are now the dominant deploy target.
		return "illumos"
	}
	return v
}

// archFromUname maps `uname -m` output to a Go GOARCH value. Returns ""
// when the output is unfamiliar — the deploy then surfaces a clear error
// rather than guessing.
func archFromUname(unameM string) string {
	switch strings.TrimSpace(unameM) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv6l":
		return "arm"
	case "i386", "i486", "i586", "i686":
		return "386"
	case "ppc64le":
		return "ppc64le"
	case "s390x":
		return "s390x"
	case "riscv64":
		return "riscv64"
	default:
		return ""
	}
}

// LogFn receives a single human-readable progress line.
type LogFn func(line string)

// DeployResult is metadata captured during a successful deploy that the
// caller may want to persist (or surface in audit logs).
type DeployResult struct {
	// DaemonHostname is the value of `hostname` on the remote host —
	// captured so the CP can correlate inbound daemon events (which carry
	// the daemon's `os.Hostname()`) back to this node, even when the SSH
	// target hostname differs (typical for VMs / containers).
	DaemonHostname string
}

// Deploy runs the full bootstrap flow against a single host, streaming progress
// to logFn. The function is intended to be called from a goroutine; ctx is
// honored between steps but most steps are short.
//
// Returns a DeployResult on success (also returned partially-populated on
// failure when meaningful, e.g. when the hostname was captured before a
// later step failed) — callers may persist its fields.
func Deploy(ctx context.Context, req DeployRequest, logFn LogFn) (DeployResult, error) {
	var result DeployResult
	if logFn == nil {
		logFn = func(string) {}
	}
	emit := func(format string, args ...any) {
		logFn(fmt.Sprintf(format, args...))
	}

	if len(req.AgentsToInstall) == 0 {
		return result, fmt.Errorf("no agents to install")
	}
	if req.DaemonBinaryPath == "" {
		return result, fmt.Errorf("daemon binary path is required")
	}
	if req.DaimonFilesDir == "" {
		return result, fmt.Errorf("agent files dir is required")
	}

	emit("→ connecting to %s@%s:%d", req.Cred.User, req.Cred.Host, req.Cred.Port)
	c, err := Dial(req.Cred)
	if err != nil {
		return result, err
	}
	defer c.Close()
	emit("✓ ssh connected")

	if err := ctx.Err(); err != nil {
		return result, err
	}

	// 1. Pre-flight: capture remote hostname, arch, OS. The service-
	//    manager probe happens later via Detect(); we no longer reject
	//    non-systemd targets — Phase 8.1–8.5 added launchd / rc.d / SMF
	//    support for both daimons and the jobs runtime.
	emit("→ pre-flight checks")
	preflight, err := c.Run("hostname && uname -m && uname -s && cat /etc/os-release 2>/dev/null | head -3")
	if err != nil {
		return result, fmt.Errorf("pre-flight: %w (output: %s)", err, preflight)
	}
	preflight = strings.TrimSpace(preflight)
	for _, line := range strings.Split(preflight, "\n") {
		if line != "" {
			emit("  %s", line)
		}
	}
	// Layout: [hostname, uname-m, uname-s, ...os-release lines]
	preLines := strings.Split(preflight, "\n")
	var targetArch, targetOS string
	if len(preLines) >= 1 {
		result.DaemonHostname = strings.TrimSpace(preLines[0])
	}
	if len(preLines) >= 3 {
		targetArch = archFromUname(preLines[1])
		targetOS = unameSToOS(preLines[2])
	}
	if targetOS == "" {
		targetOS = "linux"
	}
	if targetArch == "" && len(preLines) >= 2 {
		emit("  ! could not classify target arch from uname output %q", preLines[1])
	}

	// 2. Pick the service manager — single source of truth for the
	//    rest of the deploy. Both the daimon install loop below and
	//    the Phase-6 jobs-runtime install reuse this manager.
	mgr, err := Detect(c, targetOS)
	if err != nil {
		return result, fmt.Errorf("detect service manager: %w", err)
	}
	emit("  service manager: %s", mgr.Flavour())

	// 3. Create okesu (or _okesu on macOS) system user + state dirs.
	user, group := okesuUserGroup(targetOS)
	emit("→ ensuring %s system user + directories", user)
	if err := bootstrapOkesuUser(c, req.Cred.SudoPassword, targetOS); err != nil {
		return result, err
	}
	emit("✓ user + dirs ready")

	// 3. Pick the daemon binary matching the target's os+arch and upload.
	binaryPath, err := resolveDaemonBinaryPath(req, targetOS, targetArch)
	if err != nil {
		return result, err
	}
	emit("→ uploading daemon binary (%s/%s) from %s", targetOS, targetArch, binaryPath)
	binFile, err := os.Open(binaryPath)
	if err != nil {
		return result, fmt.Errorf("open daemon binary: %w", err)
	}
	if err := c.UploadFromReader("/usr/local/bin/okesu", binFile, 0755); err != nil {
		_ = binFile.Close()
		return result, fmt.Errorf("upload binary: %w", err)
	}
	_ = binFile.Close()
	emit("✓ binary at /usr/local/bin/okesu")

	// 4. (systemd-only) Stop any legacy `okesu-agent@<name>.service`
	//    template-instance services left over from the pre-Phase-8.5
	//    deploy code. The new code writes per-instance units named
	//    `okesu-agent-<name>.service`; running both in parallel would
	//    double-spawn each daimon. Tolerated to fail — `systemctl stop`
	//    on a missing unit is a no-op.
	if mgr.Flavour() == FlavourSystemd {
		emit("→ migrating any legacy okesu-agent@*.service template instances")
		_, _ = runWithSudo(c, req.Cred.SudoPassword,
			`for u in $(systemctl list-units 'okesu-agent@*.service' --plain --no-legend --state=loaded | awk '{print $1}'); do systemctl disable --now "$u" 2>/dev/null || true; done`)
	}

	// 5. For each agent: upload agent file, optional env file, optional mgmt certs, start service.
	for _, agent := range req.AgentsToInstall {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		emit("→ deploying agent %q", agent)

		// Agent file. If the operator asked to wire up webhook output or
		// the mgmt plane, inject those YAML stanzas before the closing `---`
		// — yaml.v3 last-key-wins semantics mean these override any commented
		// or stub blocks earlier in the file.
		src := req.DaimonFilesDir + "/" + agent + ".md"
		body, err := os.ReadFile(src)
		if err != nil {
			return result, fmt.Errorf("read agent %s: %w", agent, err)
		}
		hasMgmt := false
		if _, ok := req.MgmtCerts[agent]; ok {
			hasMgmt = true
		}
		body = injectAgentConfig(body, agentInjection{
			AgentName:     agent,
			WebhookURL:    req.WebhookURL,
			WebhookSecret: req.WebhookSecret,
			MgmtURL:       req.MgmtURL,
			IncludeMgmt:   hasMgmt,
		})
		dest := "/etc/okesu/agents/" + agent + ".md"
		if err := c.WriteFile(dest, body, 0644); err != nil {
			return result, fmt.Errorf("upload agent file: %w", err)
		}
		emit("  ✓ %s", dest)

		// Per-agent env file with secrets. chown to the OS-specific
		// daemon user so the service can read it post-drop-privileges.
		envContent := buildEnvFile(req.AnthropicAPIKey, req.OpenAIAPIKey, req.WebhookSecret)
		if envContent != "" {
			envPath := "/etc/okesu/agents/" + agent + ".env"
			if err := c.WriteFile(envPath, []byte(envContent), 0600); err != nil {
				return result, fmt.Errorf("upload env file: %w", err)
			}
			if _, err := runWithSudo(c, req.Cred.SudoPassword, fmt.Sprintf("chown %s:%s %s", user, group, envPath)); err != nil {
				// Non-fatal — env file is still readable by root.
				emit("  ! could not chown %s: %v", envPath, err)
			}
			emit("  ✓ %s (mode 0600)", envPath)
		}

		// mTLS cert bundle.
		if bundle, ok := req.MgmtCerts[agent]; ok && len(bundle.ClientCert) > 0 {
			certDir := "/etc/okesu/" + agent + "-mgmt-certs"
			if _, err := runWithSudo(c, req.Cred.SudoPassword, fmt.Sprintf("install -d -m 0750 -o %s -g %s %s", user, group, certDir)); err != nil {
				return result, fmt.Errorf("mkdir cert dir: %w", err)
			}
			if err := c.WriteFile(certDir+"/ca.crt", bundle.CACert, 0644); err != nil {
				return result, fmt.Errorf("write ca.crt: %w", err)
			}
			if err := c.WriteFile(certDir+"/client.crt", bundle.ClientCert, 0644); err != nil {
				return result, fmt.Errorf("write client.crt: %w", err)
			}
			if err := c.WriteFile(certDir+"/client.key", bundle.ClientKey, 0600); err != nil {
				return result, fmt.Errorf("write client.key: %w", err)
			}
			if _, err := runWithSudo(c, req.Cred.SudoPassword, fmt.Sprintf("chown -R %s:%s %s", user, group, certDir)); err != nil {
				emit("  ! could not chown %s: %v", certDir, err)
			}
			emit("  ✓ mTLS certs in %s", certDir)
		}

		// Service unit + start, via the flavour-appropriate manager.
		// The svcmgr name-mapping handles the per-OS naming convention
		// (systemd: okesu-agent-<name>.service; launchd:
		// com.okesu.agent-<name>; rc.d: okesu_agent_<name>; SMF:
		// svc:/site/okesu-agent-<name>:default).
		svcName := "okesu-agent-" + agent
		spec := ServiceSpec{
			Name:             svcName,
			Description:      "Okesu daimon: " + agent,
			ExecStart:        "/usr/local/bin/okesu daemon --agent " + agent,
			User:             user,
			Group:            group,
			EnvironmentFile:  "/etc/okesu/agents/" + agent + ".env",
			WorkingDirectory: "/var/lib/okesu/" + agent,
		}
		emit("  → %s: install %s", mgr.Flavour(), svcName)
		if err := mgr.Install(c, req.Cred.SudoPassword, spec); err != nil {
			return result, fmt.Errorf("install service for agent %s: %w", agent, err)
		}
		if err := mgr.EnableAndStart(c, req.Cred.SudoPassword, svcName); err != nil {
			return result, fmt.Errorf("start agent %s: %w", agent, err)
		}
		emit("  ✓ agent %q started", agent)
	}

	// Phase 6: install the pull-mode jobs runtime alongside the
	// daimons. Same binary, same trust path, just a different
	// systemd unit. Nodes deployed before this change get the
	// jobs runtime on their next deploy or via the manual install
	// button on the Nodes page.
	if req.JobsRuntime != nil && len(req.JobsRuntime.ClientCert) > 0 {
		emit("→ installing pull-mode jobs runtime (okesu-jobs)")
		if err := installNodeCerts(c, req.JobsRuntime, user, group, req.Cred.SudoPassword); err != nil {
			emit("  ! jobs runtime install failed: %v (daimons unaffected)", err)
		} else {
			// Reuse the daimon-deploy keys for the jobs runtime so
			// spawned `okesu claude` jobs have credentials. Same env
			// shape as /etc/okesu/agents/<agent>.env.
			if envContent := buildJobsEnvFile(req.AnthropicAPIKey, req.OpenAIAPIKey); envContent != "" {
				if err := c.WriteFile("/etc/okesu/jobs.env", []byte(envContent), 0600); err != nil {
					emit("  ! write /etc/okesu/jobs.env: %v", err)
				} else {
					_, _ = runWithSudo(c, req.Cred.SudoPassword, fmt.Sprintf("chown %s:%s /etc/okesu/jobs.env", user, group))
					emit("  ✓ /etc/okesu/jobs.env written")
				}
			}
			spec := ServiceSpec{
				Name:            "okesu-jobs",
				Description:     "Okesu pull-mode jobs runtime",
				ExecStart:       fmt.Sprintf("/usr/local/bin/okesu jobs --cp-url %q --name %q", req.MgmtURL, req.NodeName),
				User:            user,
				Group:           group,
				EnvironmentFile: "/etc/okesu/jobs.env",
			}
			if err := mgr.Install(c, req.Cred.SudoPassword, spec); err != nil {
				emit("  ! install okesu-jobs unit: %v (daimons unaffected)", err)
			} else if err := mgr.EnableAndStart(c, req.Cred.SudoPassword, "okesu-jobs"); err != nil {
				emit("  ! start okesu-jobs: %v (daimons unaffected)", err)
			} else {
				emit("  ✓ okesu-jobs running — node accepts pull-mode dispatch")
			}
		}
	}

	emit("✓ deploy complete")
	return result, nil
}

// installNodeCerts drops the three-file mTLS bundle into the
// canonical /etc/okesu/node-certs directory. The jobs runtime + the
// future tunnel runtime both look here, so they share one cert
// (the CN identifies the node either way).
func installNodeCerts(c *Client, bundle *MgmtCertBundle, user, group, sudo string) error {
	dir := "/etc/okesu/node-certs"
	if _, err := runWithSudo(c, sudo, fmt.Sprintf("install -d -m 0750 -o %s -g %s %s", user, group, dir)); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := c.WriteFile(dir+"/ca.crt", bundle.CACert, 0644); err != nil {
		return fmt.Errorf("write ca.crt: %w", err)
	}
	if err := c.WriteFile(dir+"/client.crt", bundle.ClientCert, 0644); err != nil {
		return fmt.Errorf("write client.crt: %w", err)
	}
	if err := c.WriteFile(dir+"/client.key", bundle.ClientKey, 0600); err != nil {
		return fmt.Errorf("write client.key: %w", err)
	}
	if _, err := runWithSudo(c, sudo, fmt.Sprintf("chown -R %s:%s %s", user, group, dir)); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}
	return nil
}

// buildEnvFile assembles the contents of /etc/okesu/agents/<agent>.env.
// All keys are optional — empty entries are omitted.
func buildEnvFile(anthropic, openai, webhookSecret string) string {
	var b strings.Builder
	if anthropic != "" {
		b.WriteString("ANTHROPIC_API_KEY=")
		b.WriteString(anthropic)
		b.WriteString("\n")
	}
	if openai != "" {
		b.WriteString("OPENAI_API_KEY=")
		b.WriteString(openai)
		b.WriteString("\n")
	}
	if webhookSecret != "" {
		b.WriteString("OKESU_WEBHOOK_SECRET=")
		b.WriteString(webhookSecret)
		b.WriteString("\n")
	}
	return b.String()
}

// agentInjection captures the parameters for injectAgentConfig.
type agentInjection struct {
	AgentName     string
	WebhookURL    string
	WebhookSecret string
	MgmtURL       string
	IncludeMgmt   bool
}

// injectAgentConfig rewrites an agent file's frontmatter to wire up the
// webhook output and the management plane.
//
// Strategy: strip any existing top-level `outputs:` or `management:` blocks
// from the frontmatter (yaml.v3 errors on duplicate keys), then splice
// fresh stanzas in just before the closing `---`. This works regardless of
// whether the source file had stub blocks or none.
//
// If neither block is requested, the body is returned unchanged.
func injectAgentConfig(body []byte, in agentInjection) []byte {
	wantWebhook := in.WebhookURL != "" && in.WebhookSecret != ""
	wantMgmt := in.IncludeMgmt && in.MgmtURL != ""
	if !wantWebhook && !wantMgmt {
		return body
	}

	s := string(body)
	if !strings.HasPrefix(s, "---") {
		return body
	}

	// Split into frontmatter / body around the closing `---`.
	rest := s[3:]
	closeIdx := strings.Index(rest, "\n---")
	if closeIdx < 0 {
		return body
	}
	frontmatter := s[3 : 3+closeIdx] // between the two fences (excludes both)
	suffix := s[3+closeIdx:]         // starts with "\n---" and includes everything after

	// Remove any existing top-level outputs: / management: sections so we
	// don't end up with duplicate keys.
	if wantWebhook {
		frontmatter = stripTopLevelKey(frontmatter, "outputs")
	}
	if wantMgmt {
		frontmatter = stripTopLevelKey(frontmatter, "management")
	}
	frontmatter = strings.TrimRight(frontmatter, "\n")

	var out strings.Builder
	out.WriteString("---")
	out.WriteString(frontmatter)
	out.WriteString("\n\n# ── Injected by Control Plane deploy ────────────────────────────────────────\n")
	if wantWebhook {
		// The events filter is the union of:
		//   - lifecycle (daemon_start/stop, tick_start/done, config_reloaded)
		//   - findings + action audit (finding, action_taken, action_denied)
		//   - error reporting (error, api_unavailable)
		//   - LLM loop visibility (init, tool_call, tool_result, done)
		//
		// `text` is INTENTIONALLY OMITTED: a single LLM response can produce
		// dozens of small text deltas; piping all of them through the webhook
		// can swamp the receiver. Operators who want full streaming output
		// should remove the events: filter or use the SSE endpoint on the CP.
		fmt.Fprintf(&out, `outputs:
  - type: stdout
  - type: file
    path: /var/log/okesu/%s.jsonl
    maxBytes: 104857600
  - type: webhook
    url: %q
    secret: %q
    retries: 3
    bufferCap: 512
    insecureSkipVerify: true   # CP user-facing cert is self-signed in dev
    events:
      - daemon_start
      - daemon_stop
      - tick_start
      - tick_done
      - collector_result
      - config_reloaded
      - finding
      - action_taken
      - action_denied
      - error
      - api_unavailable
      - init
      - tool_call
      - tool_result
      - done
`, in.AgentName, in.WebhookURL, in.WebhookSecret)
	}
	if wantMgmt {
		fmt.Fprintf(&out, `management:
  url: %q
  certDir: /etc/okesu/%s-mgmt-certs
  heartbeatSec: 30
  pollSec: 30
`, in.MgmtURL, in.AgentName)
	}
	out.WriteString(suffix)
	return []byte(out.String())
}

// stripTopLevelKey removes a top-level key and its value block from a YAML
// snippet. A line starts a new top-level key when it begins at column 0 with
// a non-whitespace, non-comment character followed (eventually) by a colon.
// Commented lines and indented lines belong to the prior section.
func stripTopLevelKey(yamlText, key string) string {
	lines := strings.Split(yamlText, "\n")
	out := make([]string, 0, len(lines))
	inTarget := false
	for _, line := range lines {
		if !inTarget {
			if isTopLevelKey(line, key) {
				inTarget = true
				continue
			}
			out = append(out, line)
			continue
		}
		// Inside target section — keep skipping until we see a different
		// top-level key.
		if isAnyTopLevelKey(line) && !isTopLevelKey(line, key) {
			inTarget = false
			out = append(out, line)
		}
		// otherwise: drop the line
	}
	return strings.Join(out, "\n")
}

func isTopLevelKey(line, key string) bool {
	prefix := key + ":"
	return strings.HasPrefix(line, prefix) &&
		(len(line) == len(prefix) || line[len(prefix)] == ' ' || line[len(prefix)] == '\t' || line[len(prefix)] == '\n' || line[len(prefix)] == '\r')
}

func isAnyTopLevelKey(line string) bool {
	if len(line) == 0 {
		return false
	}
	c := line[0]
	if c == ' ' || c == '\t' || c == '#' || c == '-' {
		return false
	}
	// Top-level keys contain a colon and are valid identifiers (loose check).
	idx := strings.Index(line, ":")
	return idx > 0
}

// resolveDaemonBinaryPath picks the binary to upload. Prefers the multi-arch
// resolver when present; falls back to the single-arch DaemonBinaryPath.
func resolveDaemonBinaryPath(req DeployRequest, targetOS, targetArch string) (string, error) {
	if req.DaemonBinaryResolver != nil {
		if targetArch == "" {
			return "", fmt.Errorf("could not detect target arch — cannot pick a daemon binary")
		}
		path, err := req.DaemonBinaryResolver.Resolve(targetOS, targetArch)
		if err != nil {
			return "", fmt.Errorf("no daemon binary registered for %s/%s — upload one in Settings → Deploy: %w",
				targetOS, targetArch, err)
		}
		return path, nil
	}
	if req.DaemonBinaryPath != "" {
		return req.DaemonBinaryPath, nil
	}
	return "", fmt.Errorf("no daemon binary configured (set --daemon-binary or --daemon-binaries-dir on the CP)")
}

// shellQuote returns a single-quoted string suitable for inline embedding in
// `bash -c '...'`. Single quotes inside the string are escaped via the
// idiomatic close-escape-reopen pattern.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

