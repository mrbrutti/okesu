// Package sshdeploy bootstraps okesu daemons on remote hosts via one-shot SSH.
//
// Design notes:
//
//   - The operator-supplied SSH credential (private key) is used immediately
//     and never persisted. Long-term node management runs over the daemon's
//     own mTLS management plane (controlplane/api/mgmt.go).
//   - All steps are idempotent — a deploy can be re-run after a partial failure.
//   - The orchestrator streams progress as line-oriented log entries through
//     a channel so the UI can show live deploy logs.
package sshdeploy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Credential carries the SSH authentication material for a deploy.
// PrivateKey is required (PEM-encoded). Passphrase is optional.
type Credential struct {
	User       string
	Host       string
	Port       int
	PrivateKey []byte
	Passphrase string

	// SudoPassword is fed to `sudo -S` when the SSH user isn't root and
	// doesn't have passwordless sudo. Empty string means try sudo
	// without a password (NOPASSWD or root login). Mac developer
	// machines and other "regular user" SSH targets need this.
	SudoPassword string

	// HostKeyCallback receives the target's offered host key. The deploy
	// orchestrator wraps a TOFU/known-hosts policy here. If nil, the SSH
	// client uses InsecureIgnoreHostKey() (only acceptable for tests).
	HostKeyCallback ssh.HostKeyCallback
}

// Client is a simple wrapper around an SSH session + SFTP client.
type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client

	// rootProbed / isRoot cache one-shot `id -u` so writeWithSudo +
	// runWithSudo can skip the `sudo --` prefix when we're already
	// connected as root. Some target images (notably the Fedora test
	// containers, but also bare-bones cloud-init AMIs) ship a broken
	// PAM config that makes even passwordless `sudo` fail when the
	// process is already privileged — skipping the wrapper avoids it.
	rootMu     sync.Mutex
	rootProbed bool
	isRoot     bool
}

// IsRoot reports whether the SSH session is connected as uid 0.
// First call probes via `id -u`; result is cached for the lifetime
// of the Client. Errors during the probe are treated as "unknown,
// not root" to keep the conservative path (sudo) when we can't tell.
func (c *Client) IsRoot() bool {
	c.rootMu.Lock()
	defer c.rootMu.Unlock()
	if c.rootProbed {
		return c.isRoot
	}
	c.rootProbed = true
	out, err := c.Run("id -u")
	if err != nil {
		return false
	}
	c.isRoot = strings.TrimSpace(out) == "0"
	return c.isRoot
}

// Dial opens an SSH connection using the supplied credential.
func Dial(cred Credential) (*Client, error) {
	if cred.User == "" {
		cred.User = "root"
	}
	if cred.Port == 0 {
		cred.Port = 22
	}

	signer, err := parseSigner(cred.PrivateKey, cred.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	hostKeyCallback := cred.HostKeyCallback
	if hostKeyCallback == nil {
		hostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec
	}

	cfg := &ssh.ClientConfig{
		User:            cred.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(cred.Host, strconv.Itoa(cred.Port))
	sshConn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	sftpConn, err := sftp.NewClient(sshConn)
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("sftp: %w", err)
	}
	return &Client{ssh: sshConn, sftp: sftpConn}, nil
}

// Close releases the SFTP and SSH resources.
func (c *Client) Close() error {
	var firstErr error
	if c.sftp != nil {
		if err := c.sftp.Close(); err != nil {
			firstErr = err
		}
	}
	if c.ssh != nil {
		if err := c.ssh.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Run executes a command and returns combined stdout+stderr along with
// the remote exit status. A non-zero exit status is returned via err
// (typed *ssh.ExitError) but the output is always returned.
func (c *Client) Run(cmd string) (string, error) {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// RunWithStdin executes cmd and writes stdinData to its stdin before
// closing it. Used by `sudo -S` for password-via-stdin flows on Mac
// developer machines and other non-passwordless-sudo targets.
//
// stdinData should include any trailing newlines the command expects
// (e.g. "password\n"). The pipe is closed once stdinData is written
// so the remote process sees EOF and proceeds.
func (c *Client) RunWithStdin(cmd, stdinData string) (string, error) {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}
	go func() {
		_, _ = stdin.Write([]byte(stdinData))
		_ = stdin.Close()
	}()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// WriteFile uploads content to the remote path with the given mode.
// Parent directories are created via SFTP MkdirAll.
func (c *Client) WriteFile(path string, content []byte, mode os.FileMode) error {
	if dir := dirOf(path); dir != "" {
		if err := c.sftp.MkdirAll(dir); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp := path + ".okesu-tmp"
	f, err := c.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := c.sftp.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := c.sftp.PosixRename(tmp, path); err != nil {
		// Some SFTP servers don't support posix-rename@openssh.com; fall back.
		_ = c.sftp.Remove(path)
		if rErr := c.sftp.Rename(tmp, path); rErr != nil {
			return fmt.Errorf("rename %s -> %s: %w", tmp, path, rErr)
		}
	}
	return nil
}

// UploadFromReader streams a reader into a remote file.
// Used for the okesu binary, which can be tens of MB.
func (c *Client) UploadFromReader(path string, r io.Reader, mode os.FileMode) error {
	if dir := dirOf(path); dir != "" {
		if err := c.sftp.MkdirAll(dir); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp := path + ".okesu-tmp"
	f, err := c.sftp.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return fmt.Errorf("copy %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := c.sftp.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := c.sftp.PosixRename(tmp, path); err != nil {
		_ = c.sftp.Remove(path)
		if rErr := c.sftp.Rename(tmp, path); rErr != nil {
			return fmt.Errorf("rename %s -> %s: %w", tmp, path, rErr)
		}
	}
	return nil
}

func parseSigner(pem []byte, passphrase string) (ssh.Signer, error) {
	if len(pem) == 0 {
		return nil, errors.New("private key is empty")
	}
	if passphrase == "" {
		return ssh.ParsePrivateKey(pem)
	}
	return ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}
