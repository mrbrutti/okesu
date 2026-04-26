// SSH-driven okesu binary updates with rollback.
//
// Updating the daemon binary is a separate action from a full deploy —
// it touches only /usr/local/bin/okesu (and saves the old copy as
// /usr/local/bin/okesu.previous) before bouncing the systemd services.
// Unlike Deploy, it does NOT re-upload daimon files, certs, or env
// secrets — those stay as the operator configured them last time.
//
// Rollback flips the bytes back: rename okesu.previous → okesu and
// restart. No new binary needed; everything is already on the node.

package sshdeploy

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// UpdateBinaryRequest is the input to UpdateBinary. Mirrors DeployRequest
// where overlapping (Cred, DaemonBinaryPath/Resolver) so the existing
// SSH connection + binary resolution logic is shared.
type UpdateBinaryRequest struct {
	Cred Credential

	// DaemonBinaryPath / DaemonBinaryResolver — same semantics as
	// DeployRequest. Resolver wins when set.
	DaemonBinaryPath     string
	DaemonBinaryResolver DaemonBinaryResolver

	// AgentNames is the list of daimon services to bounce after the
	// binary swap. Typically the node's `agents_installed`. Empty list
	// means "discover from systemctl" which the routine handles via
	// `systemctl list-units 'okesu-agent@*.service'`.
	AgentNames []string
}

// UpdateBinary swaps the okesu binary on the remote host with the
// CP-side version, keeping the old copy at /usr/local/bin/okesu.previous
// for rollback. Restarts every okesu-agent@* unit. Streams progress
// through logFn.
//
// Implementation order:
//  1. SSH connect, capture os/arch (matches Deploy's preflight)
//  2. Resolve the matching binary on the CP
//  3. Upload to /usr/local/bin/okesu.new
//  4. Atomic rename: okesu → okesu.previous, okesu.new → okesu
//  5. systemctl restart okesu-agent@<each>
//
// On failure mid-flow we leave the previous binary in place — the
// rename step is the commit point. Anything before that aborts cleanly.
func UpdateBinary(ctx context.Context, req UpdateBinaryRequest, logFn LogFn) error {
	if logFn == nil {
		logFn = func(string) {}
	}
	emit := func(format string, args ...any) {
		logFn(fmt.Sprintf(format, args...))
	}
	if req.DaemonBinaryPath == "" && req.DaemonBinaryResolver == nil {
		return fmt.Errorf("daemon binary path or resolver is required")
	}

	emit("→ connecting to %s@%s:%d", req.Cred.User, req.Cred.Host, req.Cred.Port)
	c, err := Dial(req.Cred)
	if err != nil {
		return err
	}
	defer c.Close()
	emit("✓ ssh connected")

	if err := ctx.Err(); err != nil {
		return err
	}

	// Pre-flight: just os/arch. We don't repeat the full bootstrap.
	preflight, err := c.Run("uname -m && uname -s")
	if err != nil {
		return fmt.Errorf("pre-flight uname: %w (output: %s)", err, preflight)
	}
	preLines := strings.Split(strings.TrimSpace(preflight), "\n")
	var targetArch, targetOS string
	if len(preLines) >= 2 {
		targetArch = archFromUname(preLines[0])
		targetOS = strings.ToLower(strings.TrimSpace(preLines[1]))
	}
	if targetOS == "" {
		targetOS = "linux"
	}
	emit("  target: %s/%s", targetOS, targetArch)

	// Pick the binary.
	binaryPath, err := resolveDaemonBinaryPath(DeployRequest{
		DaemonBinaryPath:     req.DaemonBinaryPath,
		DaemonBinaryResolver: req.DaemonBinaryResolver,
	}, targetOS, targetArch)
	if err != nil {
		return err
	}
	emit("→ uploading new binary from %s", binaryPath)

	binFile, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open daemon binary: %w", err)
	}
	if err := c.UploadFromReader("/usr/local/bin/okesu.new", binFile, 0755); err != nil {
		_ = binFile.Close()
		return fmt.Errorf("upload binary: %w", err)
	}
	_ = binFile.Close()
	emit("✓ uploaded to /usr/local/bin/okesu.new")

	// Commit point: keep the old binary as .previous, swap the new one in.
	// The two renames are atomic individually; there's a sub-millisecond
	// window where neither okesu nor okesu.previous exists, but we don't
	// touch okesu.new until after the rename completes. Worst case if the
	// CP host is wedged: the operator can ssh in and `mv okesu.new okesu`
	// to recover.
	emit("→ swapping in new binary")
	swap := `set -e
if [ -e /usr/local/bin/okesu ]; then
  mv -f /usr/local/bin/okesu /usr/local/bin/okesu.previous
fi
mv -f /usr/local/bin/okesu.new /usr/local/bin/okesu
chmod 0755 /usr/local/bin/okesu
`
	if out, err := runWithSudo(c, swap); err != nil {
		return fmt.Errorf("binary swap: %w (%s)", err, out)
	}
	emit("✓ binary at /usr/local/bin/okesu (previous saved)")

	// Restart services.
	if len(req.AgentNames) == 0 {
		// Discover what's running.
		if out, err := c.Run(`systemctl list-units 'okesu-agent@*.service' --plain --no-legend --state=loaded | awk '{print $1}'`); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "okesu-agent@") {
					trim := strings.TrimSuffix(strings.TrimPrefix(line, "okesu-agent@"), ".service")
					if trim != "" {
						req.AgentNames = append(req.AgentNames, trim)
					}
				}
			}
		}
	}

	if len(req.AgentNames) == 0 {
		emit("  (no okesu-agent@* services running — binary in place; nothing to restart)")
		emit("✓ binary update complete")
		return nil
	}

	for _, agent := range req.AgentNames {
		emit("→ systemctl restart okesu-agent@%s", agent)
		if out, err := c.Run("systemctl restart okesu-agent@" + agent); err != nil {
			return fmt.Errorf("restart agent %s: %w (%s)", agent, err, out)
		}
		emit("  ✓ %s restarted", agent)
	}
	emit("✓ binary update complete")
	return nil
}

// RollbackBinaryRequest is the input to RollbackBinary.
type RollbackBinaryRequest struct {
	Cred       Credential
	AgentNames []string // daimon services to bounce; empty = discover
}

// RollbackBinary flips okesu and okesu.previous on the remote host and
// bounces the agent services. No new bytes are uploaded — the previous
// binary is already on the node from the prior UpdateBinary call.
//
// Returns an error if okesu.previous doesn't exist (i.e. nothing to roll
// back to).
func RollbackBinary(ctx context.Context, req RollbackBinaryRequest, logFn LogFn) error {
	if logFn == nil {
		logFn = func(string) {}
	}
	emit := func(format string, args ...any) {
		logFn(fmt.Sprintf(format, args...))
	}

	emit("→ connecting to %s@%s:%d", req.Cred.User, req.Cred.Host, req.Cred.Port)
	c, err := Dial(req.Cred)
	if err != nil {
		return err
	}
	defer c.Close()
	emit("✓ ssh connected")

	if err := ctx.Err(); err != nil {
		return err
	}

	// Sanity check: the previous binary must exist.
	if out, err := c.Run("test -e /usr/local/bin/okesu.previous && echo ok || echo missing"); err != nil || strings.TrimSpace(out) != "ok" {
		return fmt.Errorf("nothing to roll back to: /usr/local/bin/okesu.previous not found")
	}

	emit("→ swapping previous binary back into place")
	// We rename current → okesu.rollback-from (audit trail) and previous →
	// current. If a future update fires, that .rollback-from gets
	// overwritten — the previous-binary slot is single-deep by design.
	swap := `set -e
if [ -e /usr/local/bin/okesu ]; then
  mv -f /usr/local/bin/okesu /usr/local/bin/okesu.rollback-from
fi
mv -f /usr/local/bin/okesu.previous /usr/local/bin/okesu
chmod 0755 /usr/local/bin/okesu
`
	if out, err := runWithSudo(c, swap); err != nil {
		return fmt.Errorf("rollback swap: %w (%s)", err, out)
	}
	emit("✓ binary rolled back")

	if len(req.AgentNames) == 0 {
		if out, err := c.Run(`systemctl list-units 'okesu-agent@*.service' --plain --no-legend --state=loaded | awk '{print $1}'`); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "okesu-agent@") {
					trim := strings.TrimSuffix(strings.TrimPrefix(line, "okesu-agent@"), ".service")
					if trim != "" {
						req.AgentNames = append(req.AgentNames, trim)
					}
				}
			}
		}
	}
	for _, agent := range req.AgentNames {
		emit("→ systemctl restart okesu-agent@%s", agent)
		if out, err := c.Run("systemctl restart okesu-agent@" + agent); err != nil {
			return fmt.Errorf("restart agent %s: %w (%s)", agent, err, out)
		}
		emit("  ✓ %s restarted", agent)
	}
	emit("✓ rollback complete")
	return nil
}

// runWithSudo wraps a shell snippet so it's tried first with sudo and
// falls through to direct execution when sudo isn't available (root user).
// Same pattern Deploy uses for the user/dir bootstrap.
func runWithSudo(c *Client, script string) (string, error) {
	if out, err := c.Run("sudo bash -c " + shellQuote(script)); err == nil {
		return out, nil
	}
	return c.Run("bash -c " + shellQuote(script))
}
