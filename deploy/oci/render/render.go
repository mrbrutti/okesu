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

// render is an unexported helper that executes a named template against
// the given inputs and returns the rendered bytes. It exists to keep
// bytes, fmt, and text/template in use at the scaffold stage so the
// compiler does not reject the file before Tasks 2-5 fill in the real
// exported functions.
func render(name, tmplSrc string, in Inputs) ([]byte, error) {
	t, err := template.New(name).Parse(tmplSrc)
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
	t, err := template.New("cp.yaml").Funcs(funcs).Parse(cpYAMLTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse cp.yaml.tmpl: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("execute cp.yaml.tmpl: %w", err)
	}
	return buf.Bytes(), nil
}

// funcs are template helpers shared by every renderer.
var funcs = template.FuncMap{
	// tfval extracts the .value from `terraform output -json`'s shape:
	//   { "name": { "sensitive": false, "type": "string", "value": "..." } }
	"tfval": func(m map[string]any, key string) (any, error) {
		raw, ok := m[key]
		if !ok {
			return nil, fmt.Errorf("missing terraform output %q", key)
		}
		shape, ok := raw.(map[string]any)
		if !ok {
			return raw, nil // already unwrapped (e.g. test fixture)
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

// RenderEnvFile renders the okesu-cp env file from the embedded template
// and the given inputs. The function body is a stub; full implementation
// comes in Task 3.
func RenderEnvFile(in Inputs) ([]byte, error) {
	return render("okesu-cp.env", envTmpl, in)
}
