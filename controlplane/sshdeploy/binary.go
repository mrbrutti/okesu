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
	"strconv"
	"strings"
	"time"
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
	// SCP/SFTP uploads run as the SSH user without going through a
	// shell, so we can't sudo the upload itself. Stage it under the
	// user's HOME (always writable) and move it into the install dir
	// with sudo as the next step. Avoids "permission denied" on
	// /usr/local/bin for non-root SSH users (Mac developer machines,
	// homebrew installs, etc.).
	stagePath := stagePath(c, "okesu.new")
	if err := c.UploadFromReader(stagePath, binFile, 0755); err != nil {
		_ = binFile.Close()
		return fmt.Errorf("upload binary to %s: %w", stagePath, err)
	}
	_ = binFile.Close()
	emit("✓ uploaded to %s (staging)", stagePath)

	// Commit point: move staged file into /usr/local/bin/okesu.new,
	// keep the old binary as .previous, swap the new one in. The two
	// renames are atomic individually; there's a sub-millisecond
	// window where neither okesu nor okesu.previous exists, but we
	// don't touch okesu.new until after the rename completes. Worst
	// case if the CP host is wedged mid-swap: the operator can ssh in
	// and `mv okesu.new okesu` to recover.
	emit("→ swapping in new binary")
	swap := `set -e
mkdir -p /usr/local/bin
mv -f ` + shellQuote(stagePath) + ` /usr/local/bin/okesu.new
if [ -e /usr/local/bin/okesu ]; then
  mv -f /usr/local/bin/okesu /usr/local/bin/okesu.previous
fi
mv -f /usr/local/bin/okesu.new /usr/local/bin/okesu
chmod 0755 /usr/local/bin/okesu
`
	if out, err := runWithSudo(c, req.Cred.SudoPassword, swap); err != nil {
		// Best-effort cleanup of the staged file when the swap fails.
		_, _ = c.Run("rm -f " + shellQuote(stagePath))
		return fmt.Errorf("binary swap: %w (%s)", err, out)
	}
	emit("✓ binary at /usr/local/bin/okesu (previous saved)")

	// Restart services via the flavour-appropriate manager.
	mgr, err := Detect(c, targetOS)
	if err != nil {
		emit("  ! detect service manager: %v — binary in place, restart agents manually", err)
		emit("✓ binary update complete")
		return nil
	}

	if len(req.AgentNames) == 0 {
		req.AgentNames, _ = mgr.ListOkesuAgents(c)
	}

	if len(req.AgentNames) == 0 {
		emit("  (no daimon services running — binary in place; nothing to restart)")
		emit("✓ binary update complete")
		return nil
	}

	for _, agent := range req.AgentNames {
		svcName := "okesu-agent-" + agent
		emit("→ %s: restart %s", mgr.Flavour(), svcName)
		if err := mgr.Restart(c, req.Cred.SudoPassword, svcName); err != nil {
			return fmt.Errorf("restart agent %s: %w", agent, err)
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

	// Detect target OS so we can skip systemctl when it isn't there.
	targetOS := "linux"
	if out, err := c.Run("uname -s"); err == nil {
		targetOS = strings.ToLower(strings.TrimSpace(out))
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
	if out, err := runWithSudo(c, req.Cred.SudoPassword, swap); err != nil {
		return fmt.Errorf("rollback swap: %w (%s)", err, out)
	}
	emit("✓ binary rolled back")

	mgr, err := Detect(c, targetOS)
	if err != nil {
		emit("  ! detect service manager: %v — binary rolled back; restart agents manually", err)
		emit("✓ rollback complete")
		return nil
	}

	if len(req.AgentNames) == 0 {
		req.AgentNames, _ = mgr.ListOkesuAgents(c)
	}
	for _, agent := range req.AgentNames {
		svcName := "okesu-agent-" + agent
		emit("→ %s: restart %s", mgr.Flavour(), svcName)
		if err := mgr.Restart(c, req.Cred.SudoPassword, svcName); err != nil {
			return fmt.Errorf("restart agent %s: %w", agent, err)
		}
		emit("  ✓ %s restarted", agent)
	}
	emit("✓ rollback complete")
	return nil
}

// runWithSudo wraps a shell snippet so it executes with whatever
// privilege the target's sudo policy allows.
//
// Order of attempts:
//
//  0. If the SSH session is already root, skip sudo entirely. Saves
//     two RTTs and avoids targets where root's `sudo` errors out
//     because of a broken PAM stack (notably the Fedora test images).
//  1. If sudoPassword is non-empty, run `sudo -S -p '' bash -c …`
//     and feed `sudoPassword + \n` to stdin. Mac developer machines
//     and other targets without passwordless sudo land here. A bad
//     password produces a clear error message instead of falling
//     through to non-sudo.
//  2. Try `sudo bash -c …` with no password (NOPASSWD or root login).
//  3. Fall through to plain `bash -c …` (root SSH user — sudo isn't
//     available but isn't needed either).
func runWithSudo(c *Client, sudoPassword, script string) (string, error) {
	if c.IsRoot() {
		return c.Run("bash -c " + shellQuote(script))
	}
	if sudoPassword != "" {
		// `-p ''` suppresses sudo's "[sudo] password for X:" prompt
		// from going to stderr; we already know it wants a password.
		out, err := c.RunWithStdin(
			"sudo -S -p '' bash -c "+shellQuote(script),
			sudoPassword+"\n",
		)
		if err == nil {
			return out, nil
		}
		// Detect bad password specifically so the operator gets a
		// clear message rather than a generic "exit status 1."
		lo := strings.ToLower(out)
		if strings.Contains(lo, "sorry, try again") ||
			strings.Contains(lo, "incorrect password") ||
			strings.Contains(lo, "authentication failure") {
			return out, fmt.Errorf("sudo password rejected by remote: %w", err)
		}
		// Otherwise fall through — operator may have set NOPASSWD
		// after configuring this node and the password is now stale.
	}
	if out, err := c.Run("sudo bash -c " + shellQuote(script)); err == nil {
		return out, nil
	}
	return c.Run("bash -c " + shellQuote(script))
}

// stagePath picks a writable directory the SSH user owns (HOME, or
// /tmp as fallback) and returns a per-deploy unique upload target
// inside it. SCP/SFTP uploads can't sudo, so a non-root SSH user
// can't write directly to /usr/local/bin — the binary lands here
// first and gets sudo-moved into place during the swap step.
func stagePath(c *Client, basename string) string {
	// Probe the SSH user's HOME via a single shell command so we don't
	// depend on the SFTP server's notion of "current dir." Ignore
	// errors — we have a /tmp fallback.
	out, err := c.Run(`echo -n "$HOME"`)
	home := strings.TrimSpace(out)
	if err != nil || home == "" || home == "/" {
		home = "/tmp"
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	return home + "/." + basename + "." + suffix
}
