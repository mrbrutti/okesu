// Command oci-render reads `terraform output -json` on stdin and writes
// the rendered cp.yaml or okesu-cp.env to stdout. Used by the Makefile.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/section9labs/okesu/deploy/oci/render"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	mode := fs.String("mode", "standalone", "standalone | parent | child")
	envFile := fs.String("env", ".env.oci", "operator env file (KEY=VALUE per line)")
	validate := fs.Bool("validate", false, "round-trip cp.yaml through controlplane.LoadConfigFile")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	tfOut, err := readTerraformOutputJSON(os.Stdin)
	if err != nil {
		die("read terraform output: %v", err)
	}
	env, err := readEnvFile(*envFile)
	if err != nil {
		die("read env file %s: %v", *envFile, err)
	}
	in := render.Inputs{Mode: *mode, TerraformOut: tfOut, OperatorEnv: env}

	switch sub {
	case "render-cp-yaml":
		body, err := render.RenderCPYAML(in)
		if err != nil {
			die("render cp.yaml: %v", err)
		}
		if *validate {
			if err := render.ValidateCPYAML(body); err != nil {
				die("validate cp.yaml: %v", err)
			}
		}
		os.Stdout.Write(body)
	case "render-env":
		body, err := render.RenderEnvFile(in)
		if err != nil {
			die("render env: %v", err)
		}
		os.Stdout.Write(body)
	default:
		usage()
		os.Exit(2)
	}
}

func readTerraformOutputJSON(r io.Reader) (map[string]any, error) {
	var out map[string]any
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func readEnvFile(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
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
		v = strings.TrimPrefix(v, `"`)
		v = strings.TrimSuffix(v, `"`)
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
