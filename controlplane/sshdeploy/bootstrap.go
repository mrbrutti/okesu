// OS-aware bootstrap of the unprivileged okesu user + state directories
// the daemon reads at runtime.
//
// Each ServiceManager flavour pairs with a different user-creation tool:
//
//   linux       useradd --system
//   darwin      sysadminctl -addUser  (10.13+) / dscl . -create
//   freebsd     pw useradd -s /sbin/nologin
//   openbsd     useradd -s /sbin/nologin
//   illumos     useradd -s /usr/bin/false
//
// We do the smallest amount per OS — create the user if missing, then
// `install -d` the state dirs (POSIX, identical everywhere). Failures
// surface verbatim because the message often pinpoints the exact tool
// the operator is missing (`pw: command not found`, etc.).

package sshdeploy

import (
	"fmt"
	"strings"
)

// bootstrapOkesuUser creates the okesu (or _okesu on macOS) system
// user and the standard state directories. Idempotent — re-running
// against a prepared host is a no-op.
//
// On macOS we use the underscore-prefixed convention Apple recommends
// for daemon users (`_okesu`), with UID 333 (an unused range). The
// rest of the deploy code refers to the user via the User/Group
// fields on ServiceSpec, so the rename is transparent above us.
func bootstrapOkesuUser(c *Client, sudo, targetOS string) error {
	switch strings.ToLower(targetOS) {
	case "linux":
		return bootstrapLinux(c, sudo)
	case "darwin":
		return bootstrapDarwin(c, sudo)
	case "freebsd":
		return bootstrapFreeBSD(c, sudo)
	case "openbsd":
		return bootstrapOpenBSD(c, sudo)
	case "illumos", "sunos", "solaris":
		return bootstrapIllumos(c, sudo)
	}
	return fmt.Errorf("unsupported target os %q for user bootstrap", targetOS)
}

// okesuUserGroup returns the (user, group) pair the deploy uses on
// the given OS. Linux/BSD/illumos all use "okesu"; macOS conventions
// require the "_okesu" prefix.
func okesuUserGroup(targetOS string) (string, string) {
	if strings.ToLower(targetOS) == "darwin" {
		return "_okesu", "_okesu"
	}
	return "okesu", "okesu"
}

func bootstrapLinux(c *Client, sudo string) error {
	script := `set -e
id -u okesu >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/okesu --shell /usr/sbin/nologin okesu
install -d -m 0755 -o okesu -g okesu /etc/okesu /etc/okesu/agents /var/lib/okesu /var/log/okesu
`
	if out, err := runWithSudo(c, sudo, script); err != nil {
		return fmt.Errorf("linux bootstrap: %w (output: %s)", err, out)
	}
	return nil
}

func bootstrapDarwin(c *Client, sudo string) error {
	// Look up the highest UID in the existing _okesu range (300-399)
	// and step one up. dscl is the supported way to create system
	// users on macOS; sysadminctl is friendlier but only exists on
	// 10.13+ and gates behind admin consent which doesn't fit a
	// non-interactive ssh deploy.
	script := `set -e
if ! dscl . -read /Users/_okesu UniqueID >/dev/null 2>&1; then
  next=$(dscl . -list /Users UniqueID | awk '$2 >= 300 && $2 < 400 {print $2}' | sort -n | tail -1)
  next=${next:-299}
  next=$((next+1))
  dscl . -create /Users/_okesu
  dscl . -create /Users/_okesu UniqueID $next
  dscl . -create /Users/_okesu PrimaryGroupID $next
  dscl . -create /Users/_okesu UserShell /usr/bin/false
  dscl . -create /Users/_okesu RealName "Okesu daemon"
  dscl . -create /Users/_okesu NFSHomeDirectory /var/lib/okesu
  dscl . -create /Groups/_okesu
  dscl . -create /Groups/_okesu PrimaryGroupID $next
fi
mkdir -p /etc/okesu /etc/okesu/agents /var/lib/okesu /var/log/okesu
chown -R _okesu:_okesu /var/lib/okesu /var/log/okesu
chmod 0755 /etc/okesu /etc/okesu/agents /var/lib/okesu /var/log/okesu
`
	if out, err := runWithSudo(c, sudo, script); err != nil {
		return fmt.Errorf("darwin bootstrap: %w (output: %s)", err, out)
	}
	return nil
}

func bootstrapFreeBSD(c *Client, sudo string) error {
	script := `set -e
if ! pw usershow okesu >/dev/null 2>&1; then
  pw groupadd okesu 2>/dev/null || true
  pw useradd okesu -g okesu -d /var/lib/okesu -s /usr/sbin/nologin -m -c "Okesu daemon"
fi
install -d -m 0755 -o okesu -g okesu /etc/okesu /etc/okesu/agents /var/lib/okesu /var/log/okesu
`
	if out, err := runWithSudo(c, sudo, script); err != nil {
		return fmt.Errorf("freebsd bootstrap: %w (output: %s)", err, out)
	}
	return nil
}

func bootstrapOpenBSD(c *Client, sudo string) error {
	script := `set -e
if ! id -u okesu >/dev/null 2>&1; then
  groupadd okesu 2>/dev/null || true
  useradd -g okesu -d /var/lib/okesu -s /sbin/nologin -m okesu
fi
install -d -m 0755 -o okesu -g okesu /etc/okesu /etc/okesu/agents /var/lib/okesu /var/log/okesu
`
	if out, err := runWithSudo(c, sudo, script); err != nil {
		return fmt.Errorf("openbsd bootstrap: %w (output: %s)", err, out)
	}
	return nil
}

func bootstrapIllumos(c *Client, sudo string) error {
	script := `set -e
if ! id okesu >/dev/null 2>&1; then
  groupadd okesu 2>/dev/null || true
  useradd -g okesu -d /var/lib/okesu -s /usr/bin/false -m okesu
fi
install -d -m 0755 -o okesu -g okesu /etc/okesu /etc/okesu/agents /var/lib/okesu /var/log/okesu
`
	if out, err := runWithSudo(c, sudo, script); err != nil {
		return fmt.Errorf("illumos bootstrap: %w (output: %s)", err, out)
	}
	return nil
}
