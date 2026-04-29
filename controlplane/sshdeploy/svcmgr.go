// Service-manager abstraction for the SSH deploy paths.
//
// Why this exists: the original install paths shelled out to
// `systemctl` directly, which only works on Linux+systemd. To support
// macOS (launchd), FreeBSD/OpenBSD (rc.d), and illumos/Solaris (SMF)
// we route every "write a service file, enable + start it, restart it,
// stop it, list okesu services" call through this interface.
//
// One implementation per service-manager flavour. Detect() picks the
// right one based on the preflight scan of the SSH target.
//
// Phase 8.1 lands the systemd implementation only — it produces
// byte-identical output to the inline systemctl calls it replaces, so
// existing nodes are unaffected. Phases 8.2–8.4 add the other
// implementations.

package sshdeploy

import (
	"fmt"
	"strings"
)

// ServiceFlavour identifies which service-manager implementation a
// target uses. Returned by Detect().
type ServiceFlavour string

const (
	FlavourSystemd  ServiceFlavour = "systemd"  // linux
	FlavourLaunchd  ServiceFlavour = "launchd"  // darwin
	FlavourRCd      ServiceFlavour = "rcd"      // freebsd, openbsd
	FlavourSMF      ServiceFlavour = "smf"      // illumos, solaris
	FlavourUnknown  ServiceFlavour = "unknown"
)

// ServiceSpec is the union of fields a service-unit needs across all
// supported service managers. Each manager picks the fields relevant
// to its format and writes the appropriate file.
//
//   - Name is the user-visible service name (no extension or scheme
//     prefix). Examples: "okesu-jobs", "okesu-agent@edr".
//   - ExecStart is the absolute command line the service runs.
//   - User / Group is the unprivileged identity to drop to.
//   - EnvironmentFile is an optional path the manager should source
//     before exec; managers that don't natively support env files
//     prefix the ExecStart with `env -F <file>` or similar.
//   - WorkingDirectory is set when the agent unit needs a
//     per-instance state dir; empty otherwise.
//   - Restart is "on-failure" or "always". Defaults to "on-failure".
//   - RestartSec is the back-off between restart attempts.
type ServiceSpec struct {
	Name             string
	Description      string
	ExecStart        string
	User             string
	Group            string
	EnvironmentFile  string
	WorkingDirectory string
	Restart          string
	RestartSec       string
}

// ServiceManager is the small surface every deploy path uses. Each
// flavour implementation routes the calls to the right CLI tools and
// the right file-system locations.
type ServiceManager interface {
	// Flavour returns the constant identifying this implementation.
	Flavour() ServiceFlavour

	// Install writes the service unit to its canonical path with the
	// right permissions, performs whatever "register the unit" step
	// the manager requires (systemd: daemon-reload; launchd: nothing;
	// rc.d: rc.conf entry; SMF: manifest import), and enables the
	// service so it starts on boot.
	Install(c *Client, sudo string, spec ServiceSpec) error

	// EnableAndStart starts the service after Install. systemd folds
	// this into one `systemctl enable --now` call; the others split
	// install vs start. Idempotent: re-running is safe.
	EnableAndStart(c *Client, sudo string, name string) error

	// Restart bounces a running service. Used by binary-update flow.
	Restart(c *Client, sudo string, name string) error

	// Stop halts a service without removing it from boot.
	Stop(c *Client, sudo string, name string) error

	// ListOkesuAgents returns the list of `okesu-agent@<NAME>` (or
	// flavour-equivalent) services currently registered. Used by the
	// binary-update flow to know which agents to bounce.
	ListOkesuAgents(c *Client) ([]string, error)
}

// Detect picks a ServiceManager based on a preflight scan of the
// target. Falls back to systemd on linux when the marker file is
// present, returns FlavourUnknown otherwise.
//
// `osName` is the lowercased `uname -s` value (linux, darwin,
// freebsd, openbsd, illumos, sunos). We trust the caller's parsing
// — preflight already normalised it.
func Detect(c *Client, osName string) (ServiceManager, error) {
	switch strings.ToLower(osName) {
	case "linux":
		// Confirm systemd is actually the active init.
		out, err := c.Run("test -d /run/systemd/system && echo yes || echo no")
		if err == nil && strings.TrimSpace(out) == "yes" {
			return &systemdManager{}, nil
		}
		return nil, fmt.Errorf("linux target without systemd is not supported")
	case "darwin":
		return &launchdManager{}, nil
	case "freebsd":
		return &rcdManager{flavour: "freebsd"}, nil
	case "openbsd":
		return &rcdManager{flavour: "openbsd"}, nil
	case "illumos", "sunos", "solaris":
		return &smfManager{}, nil
	}
	return nil, fmt.Errorf("unsupported target os %q", osName)
}

// ───────────────────────── systemd implementation ─────────────────────────

type systemdManager struct{}

func (systemdManager) Flavour() ServiceFlavour { return FlavourSystemd }

// Install writes the unit file under /etc/systemd/system, performs a
// daemon-reload so systemd picks it up, and enables it for boot. The
// caller follows up with EnableAndStart to actually start it — keeps
// the surface symmetrical with the non-systemd flavours where the
// "register" and "start" steps are physically separate commands.
func (systemdManager) Install(c *Client, sudo string, spec ServiceSpec) error {
	unit := renderSystemdUnit(spec)
	path := "/etc/systemd/system/" + spec.Name + ".service"
	if err := writeWithSudo(c, sudo, path, unit, "0644"); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if out, err := runWithSudo(c, sudo, "systemctl daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w (%s)", err, out)
	}
	return nil
}

func (systemdManager) EnableAndStart(c *Client, sudo string, name string) error {
	if out, err := runWithSudo(c, sudo, "systemctl enable --now "+name+".service"); err != nil {
		return fmt.Errorf("systemctl enable --now %s: %w (%s)", name, err, out)
	}
	return nil
}

func (systemdManager) Restart(c *Client, sudo string, name string) error {
	if out, err := runWithSudo(c, sudo, "systemctl restart "+name+".service"); err != nil {
		return fmt.Errorf("systemctl restart %s: %w (%s)", name, err, out)
	}
	return nil
}

func (systemdManager) Stop(c *Client, sudo string, name string) error {
	if out, err := runWithSudo(c, sudo, "systemctl stop "+name+".service"); err != nil {
		return fmt.Errorf("systemctl stop %s: %w (%s)", name, err, out)
	}
	return nil
}

func (systemdManager) ListOkesuAgents(c *Client) ([]string, error) {
	// Match both the new per-instance form (okesu-agent-<NAME>.service)
	// and the legacy template-instance form (okesu-agent@<NAME>.service)
	// during the migration window. Returned names dedupe so the binary-
	// update path doesn't restart twice.
	out, err := c.Run(`systemctl list-units 'okesu-agent-*.service' 'okesu-agent@*.service' --plain --no-legend --state=loaded | awk '{print $1}'`)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		var trim string
		switch {
		case strings.HasPrefix(line, "okesu-agent-"):
			trim = strings.TrimSuffix(strings.TrimPrefix(line, "okesu-agent-"), ".service")
		case strings.HasPrefix(line, "okesu-agent@"):
			trim = strings.TrimSuffix(strings.TrimPrefix(line, "okesu-agent@"), ".service")
		default:
			continue
		}
		if trim == "" {
			continue
		}
		if _, dup := seen[trim]; dup {
			continue
		}
		seen[trim] = struct{}{}
		names = append(names, trim)
	}
	return names, nil
}

// renderSystemdUnit produces a unit body from a ServiceSpec. The
// string layout matches what the old hard-coded `defaultSystemdUnit`
// and `renderJobsUnit` produced — operators upgrading get an
// equivalent file, not a re-written one.
func renderSystemdUnit(s ServiceSpec) string {
	restart := s.Restart
	if restart == "" {
		restart = "on-failure"
	}
	restartSec := s.RestartSec
	if restartSec == "" {
		restartSec = "10s"
	}
	desc := s.Description
	if desc == "" {
		desc = s.Name
	}

	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=" + desc + "\n")
	b.WriteString("Documentation=https://github.com/section9labs/okesu\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("StartLimitIntervalSec=300\n")
	b.WriteString("StartLimitBurst=5\n\n")

	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("ExecStart=" + s.ExecStart + "\n")
	b.WriteString("Restart=" + restart + "\n")
	b.WriteString("RestartSec=" + restartSec + "\n\n")

	if s.User != "" {
		b.WriteString("User=" + s.User + "\n")
	}
	if s.Group != "" {
		b.WriteString("Group=" + s.Group + "\n")
	}
	if s.WorkingDirectory != "" {
		b.WriteString("WorkingDirectory=" + s.WorkingDirectory + "\n")
	}
	b.WriteString("ReadWritePaths=/var/lib/okesu /var/log/okesu /etc/okesu /tmp\n")
	b.WriteString("ProtectSystem=strict\n")
	b.WriteString("ProtectHome=yes\n")
	b.WriteString("PrivateTmp=yes\n")
	b.WriteString("NoNewPrivileges=yes\n")
	if s.EnvironmentFile != "" {
		b.WriteString("EnvironmentFile=-" + s.EnvironmentFile + "\n")
	}
	b.WriteString("\nStandardOutput=journal\n")
	b.WriteString("StandardError=journal\n\n")

	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}
