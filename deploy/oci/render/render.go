// Package render produces /etc/okesu-cp/cp.yaml and /etc/default/okesu-cp
// for an OCI Okesu deployment, from terraform outputs + operator env.
//
// The package is pure (no I/O outside what callers pass in), which lets
// the golden tests round-trip the rendered cp.yaml through the CP's
// real LoadConfigFile loader — drift between the template and the CP
// config struct fails the test, not the production deploy.
package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/section9labs/okesu/controlplane"
)

// Inputs are everything the templates need. TerraformOut is the
// `terraform output -json` map; OperatorEnv is the parsed .env.oci.
type Inputs struct {
	Mode         string            // "standalone" | "parent" | "child"
	TerraformOut map[string]any
	OperatorEnv  map[string]string
}

//go:embed templates/cp.yaml.tmpl
var cpYAMLTmpl string

//go:embed templates/okesu-cp.env.tmpl
var envTmpl string

// funcs are template helpers shared by every renderer.
var funcs = template.FuncMap{
	// tfval extracts the .value from `terraform output -json`'s shape:
	//   { "name": { "sensitive": false, "type": "string", "value": "..." } }
	// Returns an error if the key is missing or the value is not the expected
	// wrapped shape. All fixtures must use { "value": ... } wrapping.
	"tfval": func(m map[string]any, key string) (any, error) {
		raw, ok := m[key]
		if !ok {
			return nil, fmt.Errorf("missing terraform output %q", key)
		}
		shape, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("terraform output %q: expected wrapped {\"value\":...} shape, got %T", key, raw)
		}
		v, ok := shape["value"]
		if !ok {
			return nil, fmt.Errorf("terraform output %q missing .value", key)
		}
		return v, nil
	},
	// envOr returns m[key] or fallback when m[key] is empty.
	"envOr": func(m map[string]string, key, fallback string) string {
		if v, ok := m[key]; ok && v != "" {
			return v
		}
		return fallback
	},
}

// render executes a named template with funcs attached against the given
// inputs and returns the rendered bytes. All exported renderers go through
// this helper so there is exactly one parse-and-execute path in the package.
func render(name, tmplSrc string, in Inputs) ([]byte, error) {
	t, err := template.New(name).Funcs(funcs).Parse(tmplSrc)
	if err != nil {
		return nil, fmt.Errorf("render %s: parse: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("render %s: execute: %w", name, err)
	}
	return buf.Bytes(), nil
}

// RenderCPYAML renders cp.yaml.tmpl against the inputs. Returns the YAML
// bytes ready to be written to /etc/okesu-cp/cp.yaml on the CP host.
func RenderCPYAML(in Inputs) ([]byte, error) {
	return render("cp.yaml", cpYAMLTmpl, in)
}

// RenderEnvFile renders /etc/default/okesu-cp from operator env. The
// systemd unit references this file via EnvironmentFile=. Returns an
// error if any required operator env var is missing or empty.
//
// Required for all modes: ANTHROPIC_API_KEY (agent jobs need it to
// function; a silently-empty value produces a broken CP at runtime).
//
// Mode-aware additional requirements:
//   - "parent": FEDERATION_TOKEN (the parent CP issues enrollment tokens;
//     without this the federation endpoint silently rejects all children).
//   - "child": PARENT_FEDERATION_TOKEN, PARENT_FEDERATION_BUCKET,
//     PARENT_FEDERATION_ENDPOINT, PARENT_FEDERATION_REGION,
//     PARENT_FEDERATION_ACCESS_KEY, PARENT_FEDERATION_SECRET_KEY
//     (every one of these is needed for the child to publish to the
//     parent's S3 dead-drop; any missing field breaks publishing silently).
func RenderEnvFile(in Inputs) ([]byte, error) {
	if in.OperatorEnv["ANTHROPIC_API_KEY"] == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required in OperatorEnv")
	}
	for _, k := range requiredKeysFor(in.Mode) {
		if in.OperatorEnv[k] == "" {
			return nil, fmt.Errorf("%s is required in OperatorEnv for mode=%s", k, in.Mode)
		}
	}
	return render("okesu-cp.env", envTmpl, in)
}

// requiredKeysFor returns the mode-specific operator env keys that must be
// non-empty before RenderEnvFile will produce output. The ANTHROPIC_API_KEY
// check is handled separately and is not included in this list.
func requiredKeysFor(mode string) []string {
	switch mode {
	case "parent":
		return []string{"FEDERATION_TOKEN"}
	case "child":
		return []string{
			"PARENT_FEDERATION_TOKEN",
			"PARENT_FEDERATION_BUCKET",
			"PARENT_FEDERATION_ENDPOINT",
			"PARENT_FEDERATION_REGION",
			"PARENT_FEDERATION_ACCESS_KEY",
			"PARENT_FEDERATION_SECRET_KEY",
		}
	}
	return nil
}

// ValidateCPYAML writes body to a temp file and feeds it through the
// CP's real LoadConfigFile loader. This is the drift-detector: if the
// template emits a YAML field name that no longer matches the CP's
// yamlConfig struct tags, this fails loudly during render — before
// anything is scp'd to a real VM.
//
// Note: yaml.v3 operates in non-strict mode by default, so unknown
// fields (e.g. federation fields that are env-var-only and have no
// yamlConfig entry) are silently ignored. Only field-tag mismatches
// where the loader EXPECTS a name we no longer emit cause silent data
// loss; those are surfaced by inspecting the returned Config values
// in the test, not by parse errors here.
func ValidateCPYAML(body []byte) error {
	dir, err := os.MkdirTemp("", "okesu-cp-validate-")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "cp.yaml")
	if err := os.WriteFile(path, body, 0600); err != nil {
		return fmt.Errorf("write tmp cp.yaml: %w", err)
	}

	var cfg controlplane.Config
	if err := controlplane.LoadConfigFile(path, &cfg); err != nil {
		return fmt.Errorf("controlplane.LoadConfigFile: %w", err)
	}
	return nil
}
