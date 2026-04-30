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
	"text/template"
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
// error if any required operator env var (currently: ANTHROPIC_API_KEY)
// is missing or empty — agent jobs need it to function, and a
// silently-empty value would produce a broken-CP-at-runtime rather
// than a fail-at-render.
func RenderEnvFile(in Inputs) ([]byte, error) {
	if in.OperatorEnv["ANTHROPIC_API_KEY"] == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required in OperatorEnv")
	}
	return render("okesu-cp.env", envTmpl, in)
}
