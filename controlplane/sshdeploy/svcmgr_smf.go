// SMF (Service Management Facility) implementation of ServiceManager
// — covers illumos / Solaris.
//
// SMF takes XML manifests describing the service then exposes a
// FMRI (Fault Management Resource Identifier) like
// `svc:/site/okesu-jobs:default` that all the lifecycle CLI tools
// reference. The flow is:
//   - write XML manifest under /lib/svc/manifest/site/<name>.xml
//   - svcadm import <manifest>   → registers the service
//   - svcadm enable <fmri>       → starts it (and on subsequent boots)
//   - svcadm restart/disable     → bounce/stop
//
// Naming: we expose all services under the `site/` category so they
// don't collide with vendor-shipped manifests under `system/` or
// `network/`. The FMRI ends with `:default` (the only instance we
// create per service).
//
// Env files: SMF supports `<envvar>` blocks, but they're static and
// can't source a file. We invoke the binary through `/bin/sh -c
// 'set -a; . FILE; set +a; exec ...'` the same way the other non-
// systemd flavours handle env files.

package sshdeploy

import (
	"fmt"
	"strings"
)

type smfManager struct{}

func (smfManager) Flavour() ServiceFlavour { return FlavourSMF }

func (smfManager) Install(c *Client, sudo string, spec ServiceSpec) error {
	manifestPath := "/lib/svc/manifest/site/" + smfBaseName(spec.Name) + ".xml"
	manifest := renderSMFManifest(spec)
	if err := writeWithSudo(c, sudo, manifestPath, manifest, "0644"); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	if out, err := runWithSudo(c, sudo, "svcadm restart manifest-import"); err != nil {
		return fmt.Errorf("svcadm restart manifest-import: %w (%s)", err, out)
	}
	// `svccfg import` is the alternative to manifest-import — running
	// it explicitly avoids waiting on the daemon's polling cycle.
	if out, err := runWithSudo(c, sudo, "svccfg import "+manifestPath); err != nil {
		return fmt.Errorf("svccfg import: %w (%s)", err, out)
	}
	return nil
}

func (smfManager) EnableAndStart(c *Client, sudo string, name string) error {
	fmri := smfFMRI(name)
	if out, err := runWithSudo(c, sudo, "svcadm enable -s "+fmri); err != nil {
		return fmt.Errorf("svcadm enable %s: %w (%s)", fmri, err, out)
	}
	return nil
}

func (smfManager) Restart(c *Client, sudo string, name string) error {
	fmri := smfFMRI(name)
	if out, err := runWithSudo(c, sudo, "svcadm restart "+fmri); err != nil {
		return fmt.Errorf("svcadm restart %s: %w (%s)", fmri, err, out)
	}
	return nil
}

func (smfManager) Stop(c *Client, sudo string, name string) error {
	fmri := smfFMRI(name)
	if out, err := runWithSudo(c, sudo, "svcadm disable -s "+fmri); err != nil {
		return fmt.Errorf("svcadm disable %s: %w (%s)", fmri, err, out)
	}
	return nil
}

func (smfManager) ListOkesuAgents(c *Client) ([]string, error) {
	// `svcs -H -o fmri` lists every imported service. Filter for
	// ours. We need both running and disabled because the binary-
	// update path bounces every registered agent.
	out, err := c.Run(`svcs -H -o fmri 'svc:/site/okesu-agent-*:default' 2>/dev/null`)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		// svc:/site/okesu-agent-<name>:default → <name>
		const prefix = "svc:/site/okesu-agent-"
		const suffix = ":default"
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
			continue
		}
		agent := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
		if agent != "" {
			names = append(names, agent)
		}
	}
	return names, nil
}

// smfBaseName produces the manifest filename component for a service.
// Examples:
//   - "okesu-jobs"        → "okesu-jobs"
//   - "okesu-agent@edr"   → "okesu-agent-edr"
//
// SMF tolerates `@` in service names but it complicates shell
// quoting in the manifest XML, so we substitute `-` to keep things
// simple. Round-trip mapping is handled by ListOkesuAgents.
func smfBaseName(name string) string {
	return strings.ReplaceAll(name, "@", "-")
}

// smfFMRI returns the full Fault-Management Resource Identifier
// (FMRI) for a service. Always uses the `site/` category and the
// `:default` instance — we don't expose multiple SMF instances.
func smfFMRI(name string) string {
	return "svc:/site/" + smfBaseName(name) + ":default"
}

// renderSMFManifest builds the XML manifest for a single SMF
// service. The shape follows Sun's `service_bundle.dtd.1` and the
// minimum required for a daemon: one service node, one instance,
// start/stop/refresh exec_methods, and a method_credential.
func renderSMFManifest(s ServiceSpec) string {
	exec := s.ExecStart
	if s.EnvironmentFile != "" {
		exec = fmt.Sprintf(`/bin/sh -c 'set -a; [ -r %q ] && . %q; set +a; exec %s'`,
			s.EnvironmentFile, s.EnvironmentFile, s.ExecStart)
	}

	user := s.User
	if user == "" {
		user = "root"
	}
	group := s.Group
	if group == "" {
		group = "root"
	}

	desc := s.Description
	if desc == "" {
		desc = s.Name
	}

	base := smfBaseName(s.Name)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?>` + "\n")
	b.WriteString(`<!DOCTYPE service_bundle SYSTEM "/usr/share/lib/xml/dtd/service_bundle.dtd.1">` + "\n")
	b.WriteString(`<service_bundle type="manifest" name="` + xmlEscape(base) + `">` + "\n")
	b.WriteString(`  <service name="site/` + xmlEscape(base) + `" type="service" version="1">` + "\n")
	b.WriteString(`    <create_default_instance enabled="false"/>` + "\n")
	b.WriteString(`    <single_instance/>` + "\n")
	b.WriteString(`    <dependency name="network" grouping="require_all" restart_on="error" type="service">` + "\n")
	b.WriteString(`      <service_fmri value="svc:/milestone/network:default"/>` + "\n")
	b.WriteString(`    </dependency>` + "\n")
	b.WriteString(`    <exec_method type="method" name="start" exec="` + xmlEscape(exec) + `" timeout_seconds="60">` + "\n")
	b.WriteString(`      <method_context working_directory="` + xmlEscape(orDefault(s.WorkingDirectory, "/")) + `">` + "\n")
	b.WriteString(`        <method_credential user="` + xmlEscape(user) + `" group="` + xmlEscape(group) + `"/>` + "\n")
	b.WriteString(`      </method_context>` + "\n")
	b.WriteString(`    </exec_method>` + "\n")
	b.WriteString(`    <exec_method type="method" name="stop" exec=":kill" timeout_seconds="30"/>` + "\n")
	b.WriteString(`    <template>` + "\n")
	b.WriteString(`      <common_name><loctext xml:lang="C">` + xmlEscape(desc) + `</loctext></common_name>` + "\n")
	b.WriteString(`    </template>` + "\n")
	b.WriteString(`  </service>` + "\n")
	b.WriteString(`</service_bundle>` + "\n")
	return b.String()
}

func orDefault(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}
