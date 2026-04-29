// rc.d implementation of ServiceManager — covers FreeBSD and OpenBSD.
//
// Both BSDs ship a /etc/rc.subr framework that lets a service script
// declare a command + pidfile and inherit start/stop/restart for free.
// We use that — the rc.d script we write is small and largely the
// same shape on both flavours, with these differences:
//
//   FreeBSD:
//     - script path: /usr/local/etc/rc.d/<name>
//     - rc.subr provides daemon(8) for backgrounding via $command
//     - enable line: <name>_enable="YES" in /etc/rc.conf
//     - service tool: `service <name> start/stop/restart/status`
//
//   OpenBSD:
//     - script path: /etc/rc.d/<name>
//     - rc.subr emulation via /etc/rc.d/rc.subr
//     - enable line: pkg_scripts="<existing> <name>" in rc.conf.local
//     - service tool: `rcctl start|stop|restart|enable|disable`
//
// Naming: service name = systemd's name with `@` → `_` so rc-script
// filenames don't trigger shell-quoting issues. "okesu-jobs" maps
// to /usr/local/etc/rc.d/okesu_jobs (or /etc/rc.d/okesu_jobs on
// OpenBSD); "okesu-agent@edr" → okesu_agent_edr.
//
// Env files: rc.subr's flag mechanism doesn't read env files, so we
// embed `set -a; [ -r FILE ] && . FILE; set +a;` at the top of the
// generated script before invoking the binary, the same pattern the
// launchd manager uses.

package sshdeploy

import (
	"fmt"
	"strings"
)

type rcdManager struct {
	// flavour distinguishes "freebsd" vs "openbsd" — chooses script
	// path, enable-line syntax, and the service-control CLI.
	flavour string
}

func (m *rcdManager) Flavour() ServiceFlavour { return FlavourRCd }

func (m *rcdManager) Install(c *Client, sudo string, spec ServiceSpec) error {
	rcdName := rcdServiceName(spec.Name)
	scriptDir := "/usr/local/etc/rc.d"
	if m.flavour == "openbsd" {
		scriptDir = "/etc/rc.d"
	}
	scriptPath := scriptDir + "/" + rcdName
	body := renderRCDScript(m.flavour, rcdName, spec)

	if err := writeWithSudo(c, sudo, scriptPath, body, "0755"); err != nil {
		return fmt.Errorf("write %s: %w", scriptPath, err)
	}
	if err := m.enableInRCConf(c, sudo, rcdName); err != nil {
		return fmt.Errorf("enable %s in rc.conf: %w", rcdName, err)
	}
	return nil
}

func (m *rcdManager) EnableAndStart(c *Client, sudo string, name string) error {
	rcdName := rcdServiceName(name)
	cmd := "service " + rcdName + " start"
	if m.flavour == "openbsd" {
		cmd = "rcctl start " + rcdName
	}
	if out, err := runWithSudo(c, sudo, cmd); err != nil {
		return fmt.Errorf("%s: %w (%s)", cmd, err, out)
	}
	return nil
}

func (m *rcdManager) Restart(c *Client, sudo string, name string) error {
	rcdName := rcdServiceName(name)
	cmd := "service " + rcdName + " restart"
	if m.flavour == "openbsd" {
		cmd = "rcctl restart " + rcdName
	}
	if out, err := runWithSudo(c, sudo, cmd); err != nil {
		return fmt.Errorf("%s: %w (%s)", cmd, err, out)
	}
	return nil
}

func (m *rcdManager) Stop(c *Client, sudo string, name string) error {
	rcdName := rcdServiceName(name)
	cmd := "service " + rcdName + " stop"
	if m.flavour == "openbsd" {
		cmd = "rcctl stop " + rcdName
	}
	if out, err := runWithSudo(c, sudo, cmd); err != nil {
		return fmt.Errorf("%s: %w (%s)", cmd, err, out)
	}
	return nil
}

func (m *rcdManager) ListOkesuAgents(c *Client) ([]string, error) {
	scriptDir := "/usr/local/etc/rc.d"
	if m.flavour == "openbsd" {
		scriptDir = "/etc/rc.d"
	}
	out, err := c.Run("ls " + scriptDir + " 2>/dev/null | awk '/^okesu_agent_/'")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "okesu_agent_") {
			continue
		}
		agent := strings.TrimPrefix(line, "okesu_agent_")
		if agent != "" {
			names = append(names, agent)
		}
	}
	return names, nil
}

// enableInRCConf writes the appropriate "enable on boot" line for
// each flavour. Idempotent — checks for an existing entry first so
// re-running an install doesn't duplicate.
func (m *rcdManager) enableInRCConf(c *Client, sudo string, rcdName string) error {
	switch m.flavour {
	case "freebsd":
		// /etc/rc.conf — append `<name>_enable="YES"` if missing.
		check := fmt.Sprintf("grep -q '^%s_enable=' /etc/rc.conf 2>/dev/null && echo present || echo absent", rcdName)
		out, err := runWithSudo(c, sudo, check)
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) == "present" {
			return nil
		}
		add := fmt.Sprintf(`sysrc -f /etc/rc.conf %s_enable=YES`, rcdName)
		if out, err := runWithSudo(c, sudo, add); err != nil {
			return fmt.Errorf("sysrc: %w (%s)", err, out)
		}
		return nil
	case "openbsd":
		// `rcctl enable <name>` is the official knob.
		if out, err := runWithSudo(c, sudo, "rcctl enable "+rcdName); err != nil {
			return fmt.Errorf("rcctl enable: %w (%s)", err, out)
		}
		return nil
	}
	return fmt.Errorf("unsupported rc flavour %q", m.flavour)
}

// rcdServiceName converts a generic name to the rc.d-friendly form.
// systemd's `@` (template instance separator) becomes `_` because
// rc.d wants alphanumeric+underscore identifiers.
func rcdServiceName(name string) string {
	out := strings.ReplaceAll(name, "@", "_")
	out = strings.ReplaceAll(out, "-", "_")
	return out
}

// renderRCDScript produces a small rc.subr-driven service script.
// The same template works on both BSDs because rc.subr is mostly
// compatible — the differences (PROVIDE: tag handling, etc.) don't
// affect a single self-contained service.
func renderRCDScript(flavour, rcdName string, s ServiceSpec) string {
	pidfile := "/var/run/" + rcdName + ".pid"

	// Build the command preamble that sources the env file (if any)
	// and exec's the binary in the foreground — daemon(8)/rc handles
	// backgrounding + pidfile via $command_interpreter+$command_args.
	preamble := ""
	if s.EnvironmentFile != "" {
		preamble = fmt.Sprintf(`set -a; [ -r %q ] && . %q; set +a; `, s.EnvironmentFile, s.EnvironmentFile)
	}

	user := s.User
	if user == "" {
		user = "root"
	}

	// FreeBSD's rc.subr uses `command=` + `command_args=`; OpenBSD's
	// uses a `daemon=` variable. We emit a lightweight script that
	// works on both by overriding the start command directly.
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if flavour == "freebsd" {
		b.WriteString("#\n# PROVIDE: " + rcdName + "\n# REQUIRE: NETWORKING\n# KEYWORD: shutdown\n#\n")
		b.WriteString(". /etc/rc.subr\n\n")
	} else {
		b.WriteString("#\n# OpenBSD rc.d service for okesu\n#\n")
		b.WriteString("daemon=\"" + s.ExecStart + "\"\n")
		b.WriteString("daemon_user=\"" + user + "\"\n")
		b.WriteString(". /etc/rc.d/rc.subr\n\n")
		b.WriteString("rc_bg=YES\n")
		b.WriteString("rc_reload=NO\n\n")
		b.WriteString("rc_cmd $1\n")
		return b.String()
	}

	// FreeBSD branch:
	b.WriteString("name=\"" + rcdName + "\"\n")
	b.WriteString("rcvar=" + rcdName + "_enable\n")
	b.WriteString("pidfile=\"" + pidfile + "\"\n")
	b.WriteString("command=\"/usr/sbin/daemon\"\n")
	// daemon(8) wraps any arbitrary executable. We tell it to run
	// /bin/sh -c '<env preamble>exec <binary>' so the env file is
	// sourced before exec.
	b.WriteString(fmt.Sprintf("command_args=\"-S -P %s /bin/sh -c '%sexec %s'\"\n",
		pidfile, preamble, escapeSingleQuotes(s.ExecStart)))
	b.WriteString(rcdName + "_user=\"" + user + "\"\n")
	b.WriteString("\nload_rc_config $name\n")
	b.WriteString(": ${" + rcdName + "_enable:=NO}\n\n")
	b.WriteString("run_rc_command \"$1\"\n")
	return b.String()
}

// escapeSingleQuotes embeds a string inside a single-quoted shell
// literal. Replaces `'` with `'\''` per POSIX shell quoting rules.
func escapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
