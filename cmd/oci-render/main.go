// Command oci-render reads `terraform output -json` on stdin and writes
// the rendered cp.yaml or okesu-cp.env to stdout. Used by the Makefile.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/section9labs/okesu/deploy/oci/render"
)

func main() {
	// Exit codes: 0=success, 1=runtime/IO/render failure, 2=usage or flag-parse failure.
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	switch sub {
	case "render-cp-yaml":
		runRenderCPYAML(os.Args[2:])
	case "render-env":
		runRenderEnv(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func runRenderCPYAML(args []string) {
	fs := flag.NewFlagSet("render-cp-yaml", flag.ExitOnError)
	mode := fs.String("mode", "standalone", "standalone | parent | child")
	envFile := fs.String("env", ".env.oci", "operator env file (KEY=VALUE per line)")
	validate := fs.Bool("validate", false, "round-trip cp.yaml through controlplane.LoadConfigFile")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	in := loadInputs(*mode, *envFile)
	body, err := render.RenderCPYAML(in)
	if err != nil {
		die("render cp.yaml: %v", err)
	}
	if *validate {
		if err := render.ValidateCPYAML(body); err != nil {
			die("validate cp.yaml: %v", err)
		}
	}
	writeStdout(body)
}

func runRenderEnv(args []string) {
	fs := flag.NewFlagSet("render-env", flag.ExitOnError)
	mode := fs.String("mode", "standalone", "standalone | parent | child")
	envFile := fs.String("env", ".env.oci", "operator env file (KEY=VALUE per line)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	in := loadInputs(*mode, *envFile)
	body, err := render.RenderEnvFile(in)
	if err != nil {
		die("render env: %v", err)
	}
	writeStdout(body)
}

func loadInputs(mode, envFile string) render.Inputs {
	tfOut, err := readTerraformOutputJSON(os.Stdin)
	if err != nil {
		die("read terraform output: %v", err)
	}
	env, err := readEnvFile(envFile)
	if err != nil {
		die("read env file %s: %v", envFile, err)
	}
	return render.Inputs{Mode: mode, TerraformOut: tfOut, OperatorEnv: env}
}

func writeStdout(body []byte) {
	if _, err := os.Stdout.Write(body); err != nil {
		die("write stdout: %v", err)
	}
}

func readTerraformOutputJSON(r io.Reader) (map[string]any, error) {
	var out map[string]any
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// readEnvFile parses a .env.oci-style file: KEY=VALUE per line, '#' line
// comments, blank lines ignored, optional surrounding paired quotes
// (single or double), CRLF tolerated, leading UTF-8 BOM stripped.
//
// Out of scope (use a real .env loader if you need these): end-of-line
// comments after a value, shell-style escape sequences (\n, \t),
// 'export ' prefix, multi-line values, command substitution.
func readEnvFile(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Strip a leading UTF-8 BOM if present (e.g., from Windows editors).
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})

	m := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		// Tolerate CRLF.
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		// Only strip surrounding quotes if BOTH sides are quoted with the
		// same character (single or double). Mismatched/unpaired quotes
		// are left literal.
		if len(v) >= 2 {
			first, last := v[0], v[len(v)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		m[k] = v
	}
	return m, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  oci-render render-cp-yaml --mode=<MODE> --env=<.env.oci> [--validate] < tf.json
  oci-render render-env     --mode=<MODE> --env=<.env.oci>             < tf.json
`)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "oci-render: "+format+"\n", args...)
	os.Exit(1)
}
