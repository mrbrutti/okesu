# Okesu OCI End-to-End Deploy — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land `make oci-deploy MODE=<standalone|parent|child>` that takes a clean checkout to a running Okesu CP on OCI in one command — no kubectl, no helm, just terraform + scp + systemd.

**Architecture:** Single terraform stack provisioning VCN + Postgres/Streaming/Cache/ObjectStorage + two VMs (cp-vm, clickhouse-vm). Mode is purely a config-file decision: a Go template renders `cp.yaml` from terraform outputs and operator env, and the Makefile orchestrates apply → render → scp → systemctl. Same infra in all three modes; only the rendered config differs.

**Tech Stack:** Terraform (oracle/oci provider), Go (`text/template` + golden tests), GNU Make, bash for pre-flight checks, cloud-init for VM bootstrap, systemd, dnf (ClickHouse upstream package).

**Spec:** `docs/superpowers/specs/2026-04-30-okesu-oci-end-to-end-deploy.md`

**Naming notes — read before starting:**
- The systemd unit (`systemd/okesu-cp.service`) runs as user **`okesu-cp`** (not `okesu`) with state at **`/var/lib/okesu-cp/`** and config at **`/etc/default/okesu-cp`** + **`/etc/okesu-cp/cp.yaml`**. The plan uses these paths consistently.
- `/etc/okesu/jobs.env` is a *different* file used on fleet daemon nodes, written by `controlplane/sshdeploy/install_jobs.go`. **Do not overload that name.** The CP's env file is `/etc/default/okesu-cp`.
- `OKESU_CP_FLEET_ANTHROPIC_API_KEY` and `OKESU_CP_FLEET_OPENAI_API_KEY` are env-var-only (no YAML mirror at `controlplane/config_yaml.go`). They go into `/etc/default/okesu-cp`, not `cp.yaml`.

**Repo root in this plan = `/Users/matt/Code/Oracle/Okesu`**. All paths are relative to it unless absolute.

---

## File Structure

**New files:**
- `deploy/oci/render/render.go` — pure Go template renderer
- `deploy/oci/render/render_test.go` — golden tests
- `deploy/oci/render/testdata/inputs.standalone.json` — fixture inputs
- `deploy/oci/render/testdata/inputs.parent.json`
- `deploy/oci/render/testdata/inputs.child.json`
- `deploy/oci/render/testdata/cp.yaml.standalone.golden`
- `deploy/oci/render/testdata/cp.yaml.parent.golden`
- `deploy/oci/render/testdata/cp.yaml.child.golden`
- `deploy/oci/render/testdata/okesu-cp.env.golden`
- `cmd/oci-render/main.go` — CLI wrapper used by Makefile
- `deploy/oci/cp.yaml.tmpl` — Go template for the rendered config
- `deploy/oci/okesu-cp.env.tmpl` — Go template for `/etc/default/okesu-cp`
- `deploy/oci/.env.oci.example` — operator secrets template
- `deploy/oci/modes/standalone.tfvars.example`
- `deploy/oci/modes/parent.tfvars.example`
- `deploy/oci/modes/child.tfvars.example`
- `deploy/oci/terraform/secrets.tf` — root-level CP secret generation
- `deploy/oci/terraform/modules/cp_vm/main.tf`
- `deploy/oci/terraform/modules/cp_vm/versions.tf`
- `deploy/oci/terraform/modules/cp_vm/cloudinit.sh.tftpl`
- `deploy/oci/terraform/modules/clickhouse_vm/main.tf`
- `deploy/oci/terraform/modules/clickhouse_vm/versions.tf`
- `deploy/oci/terraform/modules/clickhouse_vm/cloudinit.sh.tftpl`

**Modified files:**
- `Makefile` (add `oci-*` targets, keep existing targets unchanged)
- `deploy/oci/README.md` (rewrite for the new flow)
- `docs/oci-validation.md` (rewrite as a checklist mapped to `make` targets)
- `deploy/oci/terraform/main.tf` (drop oke module, add cp_vm + clickhouse_vm)
- `deploy/oci/terraform/variables.tf` (add `mode`, `cp_*`, `clickhouse_*`, `parent_federation_*` vars)
- `deploy/oci/terraform/outputs.tf` (add `cp_public_ip`, `ch_private_ip`, `federation_outputs`)
- `deploy/oci/terraform/modules/network/main.tf` (add explicit CH 9000 SL rule)
- `deploy/oci/terraform/modules/objectstorage/main.tf` (always-generate federation token)

**Deleted:**
- `deploy/oci/terraform/modules/oke/` (entire directory)

---

## Phase 1: Render package (Go, TDD)

The render package is the keystone. It's pure Go, golden-tested, and the killer assertion is that `RenderCPYAML` output round-trips through the CP's actual `LoadConfigFile`. That single test catches drift between this template and the CP's config struct, which was the original problem.

---

### Task 1: Scaffold render package + Inputs type

**Files:**
- Create: `deploy/oci/render/render.go`
- Create: `deploy/oci/render/render_test.go`

**Steps:**

- [ ] **Step 1: Write the package and `Inputs` type**

`deploy/oci/render/render.go`:

```go
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
```

- [ ] **Step 2: Move templates under the package's embed dir**

Templates need to be `//go:embed`-able. Create `deploy/oci/render/templates/` and tell the engineer the actual templates land here in later tasks; for now create empty placeholders so the package compiles:

```bash
mkdir -p deploy/oci/render/templates
: > deploy/oci/render/templates/cp.yaml.tmpl
: > deploy/oci/render/templates/okesu-cp.env.tmpl
```

- [ ] **Step 3: Write a placeholder test that asserts the package compiles**

`deploy/oci/render/render_test.go`:

```go
package render

import "testing"

func TestPackageCompiles(t *testing.T) {
	in := Inputs{Mode: "standalone", TerraformOut: map[string]any{}, OperatorEnv: map[string]string{}}
	_ = in
}
```

- [ ] **Step 4: Run the test**

```
go test ./deploy/oci/render/...
```

Expected: PASS (one test, trivial).

- [ ] **Step 5: Commit**

```
git add deploy/oci/render/ cmd/oci-render
git commit -m "deploy/oci: scaffold render package"
```

---

### Task 2: RenderCPYAML — standalone mode (TDD with golden file)

**Files:**
- Modify: `deploy/oci/render/render.go`
- Modify: `deploy/oci/render/render_test.go`
- Create: `deploy/oci/render/testdata/inputs.standalone.json`
- Create: `deploy/oci/render/testdata/cp.yaml.standalone.golden`
- Modify: `deploy/oci/render/templates/cp.yaml.tmpl`

**Steps:**

- [ ] **Step 1: Write the failing golden test**

`deploy/oci/render/render_test.go`:

```go
package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from current output")

func loadInputs(t *testing.T, name string) Inputs {
	t.Helper()
	path := filepath.Join("testdata", "inputs."+name+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw struct {
		Mode         string
		TerraformOut map[string]any
		OperatorEnv  map[string]string
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return Inputs(raw)
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("output drift vs %s\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func TestRenderCPYAML_Standalone(t *testing.T) {
	got, err := RenderCPYAML(loadInputs(t, "standalone"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.standalone", got)
}
```

- [ ] **Step 2: Author the standalone fixture inputs**

`deploy/oci/render/testdata/inputs.standalone.json`:

```json
{
  "Mode": "standalone",
  "TerraformOut": {
    "db_dsn":          { "value": "postgres://okesu@db.adb.us-ashburn-1.oraclecloud.com:5432/cpdb?sslmode=require" },
    "kafka_brokers":   { "value": "streampool-x.streaming.us-ashburn-1.oci.oraclecloud.com:9092" },
    "kafka_username":  { "value": "ocid1.tenancy/ocid1.user/ocid1.streampool" },
    "redis_host":      { "value": "redis-x.us-ashburn-1.oci.oraclecloud.com" },
    "redis_port":      { "value": 6379 },
    "blob_endpoint":   { "value": "ns.compat.objectstorage.us-ashburn-1.oraclecloud.com" },
    "blob_bucket":     { "value": "okesu-smoke-bucket" },
    "blob_access_key": { "value": "AKID-EXAMPLE" },
    "blob_region":     { "value": "us-ashburn-1" },
    "ch_private_ip":   { "value": "10.0.1.42" }
  },
  "OperatorEnv": {
    "ADMIN_EMAIL": "admin@example.com"
  }
}
```

- [ ] **Step 3: Author `templates/cp.yaml.tmpl`**

`deploy/oci/render/templates/cp.yaml.tmpl`:

```yaml
# Rendered by deploy/oci/render — do not edit by hand.
# Inputs: terraform output -json + .env.oci

listen: ":8443"
mgmt_listen: ":8444"

db: "{{ tfval .TerraformOut "db_dsn" }}"

admin_email: "{{ envOr .OperatorEnv "ADMIN_EMAIL" "admin@example.com" }}"
admin_password: "${secret:cp/admin-password}"
session_key:    "${secret:cp/session-key}"
webhook_secret: "${secret:cp/webhook-secret}"

events_store: "clickhouse"
clickhouse_addrs:
  - "{{ tfval .TerraformOut "ch_private_ip" }}:9000"
clickhouse_database: "okesu_events"
clickhouse_username: "default"
clickhouse_password: "${secret:clickhouse/password}"
clickhouse_secure:   false

queue: "kafka"
kafka_brokers:
  - "{{ tfval .TerraformOut "kafka_brokers" }}"
kafka_sasl_username: "{{ tfval .TerraformOut "kafka_username" }}"
kafka_sasl_password: "${secret:kafka/sasl-password}"
kafka_use_tls:       true

pubsub_url: "redis://:${secret:redis/auth-token}@{{ tfval .TerraformOut "redis_host" }}:{{ tfval .TerraformOut "redis_port" }}/0"

blob_url:        "{{ tfval .TerraformOut "blob_endpoint" }}"
blob_access_key: "{{ tfval .TerraformOut "blob_access_key" }}"
blob_secret_key: "${secret:blob/secret-key}"
blob_bucket:     "{{ tfval .TerraformOut "blob_bucket" }}"
blob_region:     "{{ tfval .TerraformOut "blob_region" }}"

event_ttl_days: 30
{{- if eq .Mode "parent" }}

federation_token: "${secret:federation/token}"
cp_instance_id:   "cp-parent-001"
{{- end }}
{{- if eq .Mode "child" }}

federation_token:                "{{ envOr .OperatorEnv "PARENT_FEDERATION_TOKEN" "" }}"
cp_instance_id:                  "{{ envOr .OperatorEnv "CP_INSTANCE_ID" "cp-child-001" }}"
federation_s3_publish_bucket:    "{{ envOr .OperatorEnv "PARENT_FEDERATION_BUCKET" "" }}"
federation_s3_publish_endpoint:  "{{ envOr .OperatorEnv "PARENT_FEDERATION_ENDPOINT" "" }}"
federation_s3_publish_region:    "{{ envOr .OperatorEnv "PARENT_FEDERATION_REGION" "" }}"
federation_s3_publish_use_ssl:   true
federation_s3_publish_access_key: "{{ envOr .OperatorEnv "PARENT_FEDERATION_ACCESS_KEY" "" }}"
federation_s3_publish_secret_key: "${secret:parent/secret-key}"
{{- end }}
```

> **Note for the engineer:** the YAML field names like `federation_s3_publish_bucket` must match `controlplane/config_yaml.go`'s field tags. Task 6 (round-trip through `LoadConfigFile`) will fail loudly if they don't — let it. If it fails because the YAML names differ from what's in `config_yaml.go`, fix the template to match the loader, NOT the loader to match the template.

- [ ] **Step 4: Implement `RenderCPYAML`**

`deploy/oci/render/render.go`, append:

```go
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
```

- [ ] **Step 5: Generate the golden file**

```
go test -run TestRenderCPYAML_Standalone ./deploy/oci/render/... -update
cat deploy/oci/render/testdata/cp.yaml.standalone.golden
```

Inspect the golden — confirm there's no `federation_*` block (standalone mode) and that all `{{ tfval ... }}` substitutions resolved.

- [ ] **Step 6: Run the test without `-update` to confirm it passes**

```
go test ./deploy/oci/render/... -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```
git add deploy/oci/render/
git commit -m "deploy/oci: render package — RenderCPYAML standalone path"
```

---

### Task 3: RenderCPYAML — parent mode

**Files:**
- Modify: `deploy/oci/render/render_test.go`
- Create: `deploy/oci/render/testdata/inputs.parent.json`
- Create: `deploy/oci/render/testdata/cp.yaml.parent.golden`

**Steps:**

- [ ] **Step 1: Add the parent test**

Append to `render_test.go`:

```go
func TestRenderCPYAML_Parent(t *testing.T) {
	got, err := RenderCPYAML(loadInputs(t, "parent"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.parent", got)
}
```

- [ ] **Step 2: Author the parent fixture**

`deploy/oci/render/testdata/inputs.parent.json` — start by copying `inputs.standalone.json` and changing only `"Mode": "parent"`. Same TerraformOut and OperatorEnv. The federation token comes from a `${secret:federation/token}` reference, so it doesn't appear in the inputs.

- [ ] **Step 3: Verify it fails (no golden yet)**

```
go test -run TestRenderCPYAML_Parent ./deploy/oci/render/...
```

Expected: FAIL with "read testdata/cp.yaml.parent.golden".

- [ ] **Step 4: Generate the golden**

```
go test -run TestRenderCPYAML_Parent ./deploy/oci/render/... -update
```

Inspect `cp.yaml.parent.golden`: should be identical to standalone *plus* a `federation_token` and `cp_instance_id` block.

- [ ] **Step 5: Re-run without `-update`**

```
go test ./deploy/oci/render/... -v
```

Expected: PASS for both Standalone and Parent.

- [ ] **Step 6: Commit**

```
git add deploy/oci/render/
git commit -m "deploy/oci: render — parent mode adds federation_token block"
```

---

### Task 4: RenderCPYAML — child mode

**Files:**
- Modify: `deploy/oci/render/render_test.go`
- Create: `deploy/oci/render/testdata/inputs.child.json`
- Create: `deploy/oci/render/testdata/cp.yaml.child.golden`

**Steps:**

- [ ] **Step 1: Add the child test**

Append:

```go
func TestRenderCPYAML_Child(t *testing.T) {
	got, err := RenderCPYAML(loadInputs(t, "child"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.child", got)
}
```

- [ ] **Step 2: Author the child fixture**

`deploy/oci/render/testdata/inputs.child.json` — copy `inputs.standalone.json`, set `"Mode": "child"`, and add to `OperatorEnv`:

```json
{
  "ADMIN_EMAIL": "admin@example.com",
  "PARENT_FEDERATION_BUCKET":     "okesu-parent-bucket",
  "PARENT_FEDERATION_ENDPOINT":   "ns.compat.objectstorage.us-ashburn-1.oraclecloud.com",
  "PARENT_FEDERATION_REGION":     "us-ashburn-1",
  "PARENT_FEDERATION_ACCESS_KEY": "PARENT-AKID-EXAMPLE",
  "PARENT_FEDERATION_TOKEN":      "ftoken-example-32chars",
  "CP_INSTANCE_ID":               "cp-child-001"
}
```

- [ ] **Step 3: Generate and verify the golden**

```
go test -run TestRenderCPYAML_Child ./deploy/oci/render/... -update
go test ./deploy/oci/render/... -v
```

Expected: PASS for all three.

- [ ] **Step 4: Commit**

```
git add deploy/oci/render/
git commit -m "deploy/oci: render — child mode emits federation_s3_publish_* block"
```

---

### Task 5: RenderEnvFile — operator env vars to /etc/default/okesu-cp

**Files:**
- Modify: `deploy/oci/render/render.go`
- Modify: `deploy/oci/render/templates/okesu-cp.env.tmpl`
- Modify: `deploy/oci/render/render_test.go`
- Create: `deploy/oci/render/testdata/okesu-cp.env.golden`

**Steps:**

- [ ] **Step 1: Author the env template**

`deploy/oci/render/templates/okesu-cp.env.tmpl`:

```
# Rendered by deploy/oci/render — read by systemd via EnvironmentFile=
# in /etc/systemd/system/okesu-cp.service. Do not edit by hand.
OKESU_CP_FLEET_ANTHROPIC_API_KEY={{ envOr .OperatorEnv "ANTHROPIC_API_KEY" "" }}
{{- if index .OperatorEnv "OPENAI_API_KEY" }}
OKESU_CP_FLEET_OPENAI_API_KEY={{ index .OperatorEnv "OPENAI_API_KEY" }}
{{- end }}
{{- if index .OperatorEnv "OIDC_CLIENT_SECRET" }}
OKESU_CP_OIDC_CLIENT_SECRET={{ index .OperatorEnv "OIDC_CLIENT_SECRET" }}
{{- end }}
```

- [ ] **Step 2: Implement `RenderEnvFile`**

Append to `render.go`:

```go
// RenderEnvFile renders /etc/default/okesu-cp from operator env. The
// systemd unit references this file via EnvironmentFile=.
func RenderEnvFile(in Inputs) ([]byte, error) {
	t, err := template.New("okesu-cp.env").Funcs(funcs).Parse(envTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse okesu-cp.env.tmpl: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("execute okesu-cp.env.tmpl: %w", err)
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 3: Add a test against the standalone fixture**

Add a test:

```go
func TestRenderEnvFile(t *testing.T) {
	in := loadInputs(t, "standalone")
	in.OperatorEnv["ANTHROPIC_API_KEY"] = "sk-ant-test"
	in.OperatorEnv["OPENAI_API_KEY"] = "sk-openai-test"
	got, err := RenderEnvFile(in)
	if err != nil {
		t.Fatalf("RenderEnvFile: %v", err)
	}
	assertGolden(t, "okesu-cp.env", got)
}
```

- [ ] **Step 4: Generate golden**

```
go test -run TestRenderEnvFile ./deploy/oci/render/... -update
go test ./deploy/oci/render/... -v
```

Expected: golden contains `OKESU_CP_FLEET_ANTHROPIC_API_KEY=sk-ant-test` and `OKESU_CP_FLEET_OPENAI_API_KEY=sk-openai-test`, no OIDC line.

- [ ] **Step 5: Commit**

```
git add deploy/oci/render/
git commit -m "deploy/oci: render — env file for systemd EnvironmentFile"
```

---

### Task 6: ValidateCPYAML — round-trip through controlplane.LoadConfigFile

This is the test that catches drift between the template and the CP's config struct.

**Files:**
- Modify: `deploy/oci/render/render.go`
- Modify: `deploy/oci/render/render_test.go`

**Steps:**

- [ ] **Step 1: Implement `ValidateCPYAML`**

Append to `render.go`:

```go
import (
	// ... existing imports
	"os"
	"path/filepath"

	"github.com/section9labs/okesu/controlplane"
)

// ValidateCPYAML writes `body` to a temp file and feeds it through the
// CP's real LoadConfigFile loader. This is the drift-detector: if the
// template emits a YAML field name that no longer exists in the CP's
// yamlConfig struct, this fails loudly during `make oci-deploy` —
// before anything is scp'd to a real VM.
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
```

- [ ] **Step 2: Add tests for all three modes**

```go
func TestValidateCPYAML_AllModes(t *testing.T) {
	for _, mode := range []string{"standalone", "parent", "child"} {
		t.Run(mode, func(t *testing.T) {
			body, err := RenderCPYAML(loadInputs(t, mode))
			if err != nil {
				t.Fatalf("RenderCPYAML: %v", err)
			}
			if err := ValidateCPYAML(body); err != nil {
				t.Fatalf("ValidateCPYAML(%s): %v\n--- yaml\n%s", mode, err, body)
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests**

```
go test ./deploy/oci/render/... -v
```

Expected: PASS. **If this fails** with something like "unknown field 'federation_s3_publish_bucket'", the YAML field names in `controlplane/config_yaml.go` differ from what the template emits. Read `controlplane/config_yaml.go` and adjust **the template** to match — not the loader.

- [ ] **Step 4: Commit**

```
git add deploy/oci/render/
git commit -m "deploy/oci: render — round-trip cp.yaml through CP loader to catch drift"
```

---

### Task 7: cmd/oci-render CLI

The Makefile shells out to this. Two subcommands: `render-cp-yaml` and `render-env`.

**Files:**
- Create: `cmd/oci-render/main.go`

**Steps:**

- [ ] **Step 1: Implement the CLI**

`cmd/oci-render/main.go`:

```go
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
```

- [ ] **Step 2: Smoke-test against the standalone fixture**

```
go build -o /tmp/oci-render ./cmd/oci-render

# Wrap fixture in the shape `terraform output -json` produces (it already is).
/tmp/oci-render render-cp-yaml --mode=standalone --env=/dev/null \
  < deploy/oci/render/testdata/inputs.standalone.json | head -20
```

Wait — the fixture's `TerraformOut` is the inner shape; `terraform output -json` writes the same shape at the top level. Confirm the CLI handles both by testing with a minimal env file:

```
echo "ADMIN_EMAIL=admin@example.com" > /tmp/env.test
jq '.TerraformOut' deploy/oci/render/testdata/inputs.standalone.json \
  | /tmp/oci-render render-cp-yaml --mode=standalone --env=/tmp/env.test --validate
```

Expected: identical to `cp.yaml.standalone.golden`.

- [ ] **Step 3: Commit**

```
git add cmd/oci-render/
git commit -m "deploy/oci: oci-render CLI — used by Makefile to render cp.yaml + env"
```

---

## Phase 2: Terraform

---

### Task 8: Generate CP-side secrets at terraform root

Today terraform generates db/kafka/redis/blob/clickhouse passwords but not `cp/admin-password`, `cp/session-key`, `cp/webhook-secret`. After this task it does.

**Files:**
- Create: `deploy/oci/terraform/secrets.tf`

**Steps:**

- [ ] **Step 1: Author the secrets file**

`deploy/oci/terraform/secrets.tf`:

```hcl
# CP-side secrets generated at apply time and written to secrets_dir
# where the CP's file:// secrets adapter reads them.

resource "random_password" "admin" {
  length  = 32
  special = true
}

resource "random_password" "session_key" {
  length  = 64
  special = false
}

resource "random_password" "webhook" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "admin" {
  filename        = "${pathexpand(var.secrets_dir)}/cp/admin-password"
  content         = random_password.admin.result
  file_permission = "0600"
}

resource "local_sensitive_file" "session_key" {
  filename        = "${pathexpand(var.secrets_dir)}/cp/session-key"
  content         = random_password.session_key.result
  file_permission = "0600"
}

resource "local_sensitive_file" "webhook" {
  filename        = "${pathexpand(var.secrets_dir)}/cp/webhook-secret"
  content         = random_password.webhook.result
  file_permission = "0600"
}
```

- [ ] **Step 2: Validate**

```
cd deploy/oci/terraform
terraform init -backend=false
terraform validate
```

Expected: "Success! The configuration is valid."

- [ ] **Step 3: Commit**

```
git add deploy/oci/terraform/secrets.tf
git commit -m "deploy/oci/terraform: generate cp/admin-password, session-key, webhook-secret"
```

---

### Task 9: Generate federation_token in objectstorage module (always)

**Files:**
- Modify: `deploy/oci/terraform/modules/objectstorage/main.tf`

**Steps:**

- [ ] **Step 1: Add the federation token resource**

Append to `deploy/oci/terraform/modules/objectstorage/main.tf`:

```hcl
# Always-generated federation token. Whether the CP USES it is a
# config-time decision in cp.yaml.tmpl (only mode=parent emits the
# federation_token line referencing it); generating it unconditionally
# keeps the terraform graph free of mode-conditional branching.
resource "random_password" "federation_token" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "federation_token" {
  filename        = "${var.secrets_dir}/federation/token"
  content         = random_password.federation_token.result
  file_permission = "0600"
}

output "federation_token_path" {
  description = "Local file holding the federation token (for parent mode operators)."
  value       = local_sensitive_file.federation_token.filename
}

output "federation_outputs" {
  description = "Bundle parent-mode operators paste into a child's tfvars."
  value = {
    parent_federation_bucket     = oci_objectstorage_bucket.main.name
    parent_federation_endpoint   = "${data.oci_objectstorage_namespace.ns.namespace}.compat.objectstorage.${var.region}.oraclecloud.com"
    parent_federation_region     = var.region
    parent_federation_access_key = oci_identity_customer_secret_key.main.id
    parent_federation_token      = random_password.federation_token.result
  }
  sensitive = true
}
```

- [ ] **Step 2: Validate**

```
cd deploy/oci/terraform
terraform validate
```

- [ ] **Step 3: Commit**

```
git add deploy/oci/terraform/modules/objectstorage/main.tf
git commit -m "deploy/oci/objectstorage: always-generate federation token + parent outputs"
```

---

### Task 10: Network module — explicit ClickHouse 9000 SL rule

**Files:**
- Modify: `deploy/oci/terraform/modules/network/main.tf`

**Steps:**

- [ ] **Step 1: Add the explicit 9000 ingress to the private SL**

In `modules/network/main.tf`, add to `oci_core_security_list "private"`'s `ingress_security_rules`:

```hcl
  # ClickHouse — explicit rule (functionally redundant given the
  # all-protocols intra-VCN rule on the public SL, but documents intent
  # and lets us tighten 5432/6379-only later if we ever drop the wildcard).
  ingress_security_rules {
    protocol = "6"
    source   = "10.0.0.0/16"
    tcp_options {
      min = 9000
      max = 9000
    }
  }
```

- [ ] **Step 2: Validate**

```
cd deploy/oci/terraform
terraform validate
```

- [ ] **Step 3: Commit**

```
git add deploy/oci/terraform/modules/network/main.tf
git commit -m "deploy/oci/network: explicit ClickHouse 9000 ingress on private SL"
```

---

### Task 11: Create modules/cp_vm/

Provisions one VM in the public subnet that will run okesu-cp via systemd. Cloud-init handles OS prep only — binary + config arrive post-apply via scp.

**Files:**
- Create: `deploy/oci/terraform/modules/cp_vm/main.tf`
- Create: `deploy/oci/terraform/modules/cp_vm/versions.tf`
- Create: `deploy/oci/terraform/modules/cp_vm/cloudinit.sh.tftpl`

**Steps:**

- [ ] **Step 1: Author `versions.tf`**

```hcl
terraform {
  required_providers {
    oci = { source = "oracle/oci", version = ">= 5.30.0" }
  }
}
```

- [ ] **Step 2: Author `cloudinit.sh.tftpl`**

`modules/cp_vm/cloudinit.sh.tftpl`:

```sh
#!/bin/bash
# Cloud-init for the okesu-cp VM. Runs once at first boot.
# Binary + cp.yaml are scp'd in by `make oci-install` after this.
set -euo pipefail

# Dedicated low-privilege user, matching scripts/install-cp.sh.
groupadd --system okesu-cp || true
useradd  --system --gid okesu-cp --home-dir /var/lib/okesu-cp --shell /sbin/nologin okesu-cp || true

mkdir -p /var/lib/okesu-cp /var/log/okesu-cp /etc/okesu-cp /etc/okesu-cp/secrets
chown -R okesu-cp:okesu-cp /var/lib/okesu-cp /var/log/okesu-cp /etc/okesu-cp
chmod 0750 /etc/okesu-cp /etc/okesu-cp/secrets

# Open the CP UI / mgmt ports through firewalld.
if command -v firewall-cmd >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port=8443/tcp
  firewall-cmd --permanent --add-port=8444/tcp
  firewall-cmd --reload
fi

touch /var/log/okesu-cp/cloudinit.done
```

- [ ] **Step 3: Author `main.tf`**

```hcl
variable "compartment_ocid" { type = string }
variable "name_prefix"      { type = string }
variable "subnet_ocid"      { type = string }
variable "shape"            { type = string }
variable "image_ocid"       { type = string }
variable "ssh_public_key"   { type = string }

data "oci_identity_availability_domains" "ads" {
  compartment_id = var.compartment_ocid
}

data "oci_core_images" "ol9" {
  count                    = var.image_ocid == "" ? 1 : 0
  compartment_id           = var.compartment_ocid
  operating_system         = "Oracle Linux"
  operating_system_version = "9"
  shape                    = var.shape
  state                    = "AVAILABLE"
  filter {
    name   = "display_name"
    values = ["^Oracle-Linux-9.*"]
    regex  = true
  }
}

locals {
  resolved_image_ocid = var.image_ocid != "" ? var.image_ocid : data.oci_core_images.ol9[0].images[0].id
  cloudinit           = templatefile("${path.module}/cloudinit.sh.tftpl", {})
}

resource "oci_core_instance" "cp" {
  compartment_id      = var.compartment_ocid
  display_name        = "${var.name_prefix}-cp"
  shape               = var.shape
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name

  source_details {
    source_type = "image"
    source_id   = local.resolved_image_ocid
  }

  create_vnic_details {
    subnet_id        = var.subnet_ocid
    assign_public_ip = true
  }

  metadata = {
    ssh_authorized_keys = var.ssh_public_key
    user_data           = base64encode(local.cloudinit)
  }
}

output "public_ip"   { value = oci_core_instance.cp.public_ip }
output "private_ip"  { value = oci_core_instance.cp.private_ip }
output "instance_id" { value = oci_core_instance.cp.id }
```

- [ ] **Step 4: Validate**

```
cd deploy/oci/terraform/modules/cp_vm
terraform init -backend=false
terraform validate
```

- [ ] **Step 5: Commit**

```
git add deploy/oci/terraform/modules/cp_vm/
git commit -m "deploy/oci/terraform: add cp_vm module — Oracle Linux 9 + cloud-init"
```

---

### Task 12: Create modules/clickhouse_vm/

Provisions one VM in the **private** subnet running `clickhouse-server` from the upstream dnf repo. Cloud-init does the full install — no post-apply step needed.

**Files:**
- Create: `deploy/oci/terraform/modules/clickhouse_vm/main.tf`
- Create: `deploy/oci/terraform/modules/clickhouse_vm/versions.tf`
- Create: `deploy/oci/terraform/modules/clickhouse_vm/cloudinit.sh.tftpl`

**Steps:**

- [ ] **Step 1: Author `versions.tf`**

```hcl
terraform {
  required_providers {
    oci = { source = "oracle/oci", version = ">= 5.30.0" }
  }
}
```

- [ ] **Step 2: Author `cloudinit.sh.tftpl`**

```sh
#!/bin/bash
# Cloud-init for the ClickHouse VM. Installs clickhouse-server from
# the upstream Yandex repo, pinned to ${clickhouse_version}, and seeds
# the default user's password from the value generated by terraform.
set -euo pipefail

cat > /etc/yum.repos.d/clickhouse.repo <<'EOF'
[clickhouse-stable]
name=ClickHouse - Stable
baseurl=https://packages.clickhouse.com/rpm/stable/
enabled=1
gpgcheck=1
gpgkey=https://packages.clickhouse.com/rpm/stable/repodata/repomd.xml.key
EOF

dnf install -y clickhouse-server-${clickhouse_version} clickhouse-client-${clickhouse_version}

mkdir -p /etc/clickhouse-server/users.d
cat > /etc/clickhouse-server/users.d/okesu.xml <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<clickhouse>
  <users>
    <default>
      <password>${clickhouse_password}</password>
      <networks><ip>::/0</ip></networks>
      <profile>default</profile>
      <quota>default</quota>
      <access_management>1</access_management>
    </default>
  </users>
</clickhouse>
EOF
chmod 0640 /etc/clickhouse-server/users.d/okesu.xml
chown root:clickhouse /etc/clickhouse-server/users.d/okesu.xml

# Allow inbound 9000 inside the VCN.
if command -v firewall-cmd >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port=9000/tcp
  firewall-cmd --permanent --add-port=9009/tcp
  firewall-cmd --reload
fi

systemctl enable --now clickhouse-server
```

- [ ] **Step 3: Author `main.tf`**

```hcl
variable "compartment_ocid"     { type = string }
variable "name_prefix"          { type = string }
variable "subnet_ocid"          { type = string }
variable "shape"                { type = string }
variable "image_ocid"           { type = string }
variable "ssh_public_key"       { type = string }
variable "clickhouse_version"   { type = string }
variable "clickhouse_password"  { type = string, sensitive = true }

data "oci_identity_availability_domains" "ads" {
  compartment_id = var.compartment_ocid
}

data "oci_core_images" "ol9" {
  count                    = var.image_ocid == "" ? 1 : 0
  compartment_id           = var.compartment_ocid
  operating_system         = "Oracle Linux"
  operating_system_version = "9"
  shape                    = var.shape
  state                    = "AVAILABLE"
  filter {
    name   = "display_name"
    values = ["^Oracle-Linux-9.*"]
    regex  = true
  }
}

locals {
  resolved_image_ocid = var.image_ocid != "" ? var.image_ocid : data.oci_core_images.ol9[0].images[0].id
  cloudinit = templatefile("${path.module}/cloudinit.sh.tftpl", {
    clickhouse_version  = var.clickhouse_version
    clickhouse_password = var.clickhouse_password
  })
}

resource "oci_core_instance" "ch" {
  compartment_id      = var.compartment_ocid
  display_name        = "${var.name_prefix}-ch"
  shape               = var.shape
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name

  source_details {
    source_type = "image"
    source_id   = local.resolved_image_ocid
  }

  create_vnic_details {
    subnet_id                  = var.subnet_ocid
    assign_public_ip           = false
  }

  metadata = {
    ssh_authorized_keys = var.ssh_public_key
    user_data           = base64encode(local.cloudinit)
  }
}

output "private_ip" { value = oci_core_instance.ch.private_ip }
```

> **Note:** `clickhouse_password` flows in from the existing `modules/cache` write — no, that's redis. Re-check: ClickHouse password is generated in **the existing OKE module** today (`modules/oke/main.tf:99-103`). Since we're deleting the OKE module, the password generation must move. **Do this generation at the terraform root** (similar to Task 8), and pass the result into `clickhouse_vm` via `var.clickhouse_password`. See Task 13 for the wiring.

- [ ] **Step 4: Validate**

```
cd deploy/oci/terraform/modules/clickhouse_vm
terraform init -backend=false
terraform validate
```

- [ ] **Step 5: Commit**

```
git add deploy/oci/terraform/modules/clickhouse_vm/
git commit -m "deploy/oci/terraform: add clickhouse_vm module — dnf-installed, single-node"
```

---

### Task 13: Rewire root main.tf — drop oke, add cp_vm + clickhouse_vm

**Files:**
- Modify: `deploy/oci/terraform/main.tf`
- Modify: `deploy/oci/terraform/variables.tf`
- Modify: `deploy/oci/terraform/secrets.tf` (add ClickHouse password generation)

**Steps:**

- [ ] **Step 1: Add ClickHouse password to root secrets**

Append to `deploy/oci/terraform/secrets.tf`:

```hcl
resource "random_password" "clickhouse" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "clickhouse_password" {
  filename        = "${pathexpand(var.secrets_dir)}/clickhouse/password"
  content         = random_password.clickhouse.result
  file_permission = "0600"
}
```

- [ ] **Step 2: Add new variables**

Append to `deploy/oci/terraform/variables.tf`:

```hcl
variable "mode" {
  description = "Deployment mode — used only by the Makefile to pick a tfvars file. Validated for sanity, ignored at the terraform layer."
  type        = string
  default     = "standalone"
  validation {
    condition     = contains(["standalone", "parent", "child"], var.mode)
    error_message = "mode must be standalone, parent, or child."
  }
}

variable "cp_shape" {
  description = "Compute shape for the CP VM."
  type        = string
  default     = "VM.Standard.E4.Flex.1.16GB"
}

variable "cp_image_ocid" {
  description = "Image OCID for the CP VM (Oracle Linux 9 amd64 by default in us-ashburn-1)."
  type        = string
  default     = ""
}

variable "clickhouse_shape" {
  description = "Compute shape for the ClickHouse VM."
  type        = string
  default     = "VM.Standard.E4.Flex.1.16GB"
}

variable "clickhouse_image_ocid" {
  description = "Image OCID for the ClickHouse VM."
  type        = string
  default     = ""
}

variable "clickhouse_version" {
  description = "Pinned ClickHouse package version (no -<arch> suffix)."
  type        = string
  default     = "24.8.4.13"
}

# Child-mode only — informational; the Makefile validates these.
variable "parent_federation_bucket"     { type = string, default = "" }
variable "parent_federation_endpoint"   { type = string, default = "" }
variable "parent_federation_region"     { type = string, default = "" }
variable "parent_federation_access_key" { type = string, default = "" }
```

- [ ] **Step 3: Rewrite `deploy/oci/terraform/main.tf`**

Replace the `module "oke" { ... }` block with the new modules, and keep network/db/streaming/cache/objectstorage as-is. The new `main.tf` ends like this (showing the changed bottom half — `module "fleet"` is unchanged from today):

```hcl
# ── CP VM ────────────────────────────────────────────────────────────
module "cp_vm" {
  source = "./modules/cp_vm"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.public_subnet_ocid
  shape            = var.cp_shape
  image_ocid       = var.cp_image_ocid
  ssh_public_key   = var.ssh_public_key
}

# ── ClickHouse VM ────────────────────────────────────────────────────
module "clickhouse_vm" {
  source = "./modules/clickhouse_vm"

  compartment_ocid    = var.compartment_ocid
  name_prefix         = var.name_prefix
  subnet_ocid         = module.network.private_subnet_ocid
  shape               = var.clickhouse_shape
  image_ocid          = var.clickhouse_image_ocid
  ssh_public_key      = var.ssh_public_key
  clickhouse_version  = var.clickhouse_version
  clickhouse_password = random_password.clickhouse.result
}
```

Delete the entire `module "oke" { ... }` block.

- [ ] **Step 4: Validate**

```
cd deploy/oci/terraform
rm -rf .terraform
terraform init -backend=false
terraform validate
```

Expected: "Success! The configuration is valid."

- [ ] **Step 5: Commit**

```
git add deploy/oci/terraform/{main.tf,variables.tf,secrets.tf}
git commit -m "deploy/oci/terraform: drop oke module; wire cp_vm + clickhouse_vm"
```

---

### Task 14: Update outputs.tf

**Files:**
- Modify: `deploy/oci/terraform/outputs.tf`

**Steps:**

- [ ] **Step 1: Replace OKE outputs with VM outputs**

Open `deploy/oci/terraform/outputs.tf`. Delete:
- `output "oke_kubeconfig_path"`
- `output "oke_cluster_id"`

Add:

```hcl
# ── CP VM ────────────────────────────────────────────────────────────
output "cp_public_ip" {
  description = "Public IP for SSH + the CP UI (https://<ip>:8443)."
  value       = module.cp_vm.public_ip
}

output "cp_private_ip" {
  description = "Private IP for intra-VCN access."
  value       = module.cp_vm.private_ip
}

# ── ClickHouse VM ────────────────────────────────────────────────────
output "ch_private_ip" {
  description = "Private IP — fed into cp.yaml's clickhouse_addrs."
  value       = module.clickhouse_vm.private_ip
}

# ── ClickHouse password (sensitive) ──────────────────────────────────
output "clickhouse_password_path" {
  description = "Local path where the password file lives."
  value       = local_sensitive_file.clickhouse_password.filename
}

# ── Federation outputs (parent mode operators paste these into a child's tfvars) ───
output "federation_outputs" {
  description = "Parent-mode bundle for child enrollment."
  value       = module.objectstorage.federation_outputs
  sensitive   = true
}

output "redis_host" {
  description = "Redis FQDN (no scheme, no auth)."
  value       = module.cache.endpoint
}

output "redis_port" {
  description = "Redis port."
  value       = 6379
}

output "blob_region" {
  description = "Blob region (mirrors var.region)."
  value       = var.region
}
```

> **Note for the engineer:** `module.cache.endpoint` doesn't exist yet — `modules/cache/main.tf` only outputs `url` and `url_no_password`. Add an `endpoint` output to the cache module:
>
> ```hcl
> output "endpoint" {
>   description = "Bare FQDN — Makefile/render package combine with port + auth themselves."
>   value       = local.endpoint
> }
> ```

- [ ] **Step 2: Validate**

```
cd deploy/oci/terraform
terraform validate
```

- [ ] **Step 3: Commit**

```
git add deploy/oci/terraform/outputs.tf deploy/oci/terraform/modules/cache/main.tf
git commit -m "deploy/oci/terraform: outputs — cp_public_ip, ch_private_ip, federation bundle"
```

---

### Task 15: Delete modules/oke/

**Steps:**

- [ ] **Step 1: Delete the directory**

```
git rm -r deploy/oci/terraform/modules/oke
```

- [ ] **Step 2: Confirm `terraform init -backend=false` still works**

```
cd deploy/oci/terraform
rm -rf .terraform
terraform init -backend=false
terraform validate
```

- [ ] **Step 3: Commit**

```
git commit -m "deploy/oci/terraform: drop oke module — CP runs on a VM, not k8s"
```

---

### Task 16: Final terraform validation across all modules

Run `terraform validate` on every module path to confirm nothing slipped.

**Steps:**

- [ ] **Step 1: Validate per-module**

```
for d in \
  deploy/oci/terraform \
  deploy/oci/terraform/modules/network \
  deploy/oci/terraform/modules/db \
  deploy/oci/terraform/modules/streaming \
  deploy/oci/terraform/modules/cache \
  deploy/oci/terraform/modules/objectstorage \
  deploy/oci/terraform/modules/cp_vm \
  deploy/oci/terraform/modules/clickhouse_vm \
  deploy/oci/terraform/modules/fleet ; do
  echo "=== $d ==="
  (cd "$d" && rm -rf .terraform && terraform init -backend=false -no-color >/dev/null && terraform validate -no-color)
done
```

Expected: every directory prints "Success!".

- [ ] **Step 2: `terraform fmt -check -recursive deploy/oci/terraform/`**

Fix any formatting drift with `terraform fmt -recursive deploy/oci/terraform/`.

- [ ] **Step 3: Commit (only if fmt changed anything)**

```
git add deploy/oci/terraform/
git commit -m "deploy/oci/terraform: terraform fmt"
```

---

## Phase 3: Templates and tfvars examples

---

### Task 17: Author `.env.oci.example` and three tfvars examples

**Files:**
- Create: `deploy/oci/.env.oci.example`
- Create: `deploy/oci/modes/standalone.tfvars.example`
- Create: `deploy/oci/modes/parent.tfvars.example`
- Create: `deploy/oci/modes/child.tfvars.example`

**Steps:**

- [ ] **Step 1: `.env.oci.example`**

```
# Operator-supplied secrets — gitignored as `.env.oci`. Copy this file
# and fill in. The Makefile sources `.env.oci` and refuses to deploy
# without ANTHROPIC_API_KEY set.
#
# Required:
ANTHROPIC_API_KEY=

# Optional:
# OPENAI_API_KEY=
# OIDC_CLIENT_SECRET=
# ADMIN_EMAIL=admin@example.com

# Child-mode only — paste from `make oci-print MODE=parent` output
# on the parent's working dir:
# PARENT_FEDERATION_BUCKET=
# PARENT_FEDERATION_ENDPOINT=
# PARENT_FEDERATION_REGION=
# PARENT_FEDERATION_ACCESS_KEY=
# PARENT_FEDERATION_TOKEN=
# CP_INSTANCE_ID=cp-child-001
```

- [ ] **Step 2: `standalone.tfvars.example`**

```hcl
# Standalone CP — no federation. Copy to `standalone.tfvars` and fill in.

mode             = "standalone"
tenancy_ocid     = ""   # `oci iam tenancy get`
user_ocid        = ""   # the user owning the API key (~/.oci/config)
compartment_ocid = ""   # dedicated compartment recommended
region           = "us-ashburn-1"
ssh_public_key   = ""   # cat ~/.ssh/id_ed25519.pub

name_prefix      = "okesu-standalone"
fleet_size       = 2
secrets_dir      = "~/.okesu-secrets"
```

- [ ] **Step 3: `parent.tfvars.example`**

Same as standalone but `mode = "parent"`, `name_prefix = "okesu-parent"`, and a banner comment:

```hcl
# Parent ("global") CP. After `make oci-deploy MODE=parent`, run
#   make oci-print MODE=parent
# and paste the federation_outputs block into the child's
# `.env.oci` and tfvars.

mode             = "parent"
# (rest identical to standalone.tfvars.example)
```

- [ ] **Step 4: `child.tfvars.example`**

```hcl
# Child CP — federates into a parent. Fill in BOTH:
#   1) the standard infra fields (tenancy, region, ssh key, ...)
#   2) the parent_federation_* fields, pasted from the parent's
#      `make oci-print MODE=parent` output.
# Operator MUST also set PARENT_FEDERATION_TOKEN in `.env.oci`
# (it's in the secrets dir on the parent's machine; copy by hand).

mode             = "child"
tenancy_ocid     = ""
user_ocid        = ""
compartment_ocid = ""
region           = "us-ashburn-1"
ssh_public_key   = ""
name_prefix      = "okesu-child"
fleet_size       = 1
secrets_dir      = "~/.okesu-child-secrets"

parent_federation_bucket     = ""
parent_federation_endpoint   = ""
parent_federation_region     = ""
parent_federation_access_key = ""
```

- [ ] **Step 5: Add `.env.oci` to `.gitignore`**

```
echo ".env.oci" >> .gitignore
```

- [ ] **Step 6: Commit**

```
git add deploy/oci/.env.oci.example deploy/oci/modes/ .gitignore
git commit -m "deploy/oci: .env.oci.example + tfvars examples for all three modes"
```

---

## Phase 4: Makefile

The Makefile is the operator-facing surface. Tasks 18-25 each add a self-contained block of targets. Helpers go at the top of the new section.

The new section appends to the existing `Makefile`. Use a clearly-marked header so the existing release/build targets stay untouched.

---

### Task 18: Makefile — header + pre-flight check helpers

**Files:**
- Modify: `Makefile`

**Steps:**

- [ ] **Step 1: Append the OCI section header and helpers**

Append to `Makefile`:

```makefile
# ─────────────────────────────────────────────────────────────────────
# OCI end-to-end deploy
# ─────────────────────────────────────────────────────────────────────
#
# `make oci-deploy MODE=<standalone|parent|child>` takes a clean checkout
# to a running Okesu CP on Oracle Cloud. See deploy/oci/README.md.

OCI_MODE ?= $(or $(MODE),standalone)
OCI_DIR  ?= deploy/oci/terraform
OCI_TFVARS ?= deploy/oci/terraform/$(OCI_MODE).tfvars
OCI_ENV  ?= .env.oci

# Default ssh user for Oracle Linux on OCI.
OCI_SSH_USER ?= opc

# Render CLI built into dist/.
OCI_RENDER ?= dist/oci-render

.PHONY: oci-build oci-plan oci-apply oci-render oci-install oci-deploy \
        oci-redeploy oci-destroy oci-print oci-test \
        _oci-preflight _oci-render-cli

# ── Pre-flight: hard-fail before terraform/scp if anything's missing.
_oci-preflight:
	@if [ ! -f "$(OCI_ENV)" ]; then \
	  echo "✖ missing $(OCI_ENV) — copy deploy/oci/.env.oci.example and fill it in" >&2; exit 1; fi
	@. "$(OCI_ENV)"; \
	  if [ -z "$$ANTHROPIC_API_KEY" ]; then \
	    echo "✖ ANTHROPIC_API_KEY unset in $(OCI_ENV)" >&2; exit 1; fi
	@if [ ! -f "$(OCI_TFVARS)" ]; then \
	  echo "✖ missing $(OCI_TFVARS) — copy deploy/oci/modes/$(OCI_MODE).tfvars.example" >&2; exit 1; fi
	@if [ "$(OCI_MODE)" = "child" ]; then \
	  for k in parent_federation_bucket parent_federation_endpoint parent_federation_region parent_federation_access_key; do \
	    grep -E "^[[:space:]]*$$k[[:space:]]*=[[:space:]]*\"[^\"]+\"" $(OCI_TFVARS) >/dev/null || { \
	      echo "✖ child mode: $$k not set in $(OCI_TFVARS)" >&2; exit 1; }; \
	  done; \
	  . "$(OCI_ENV)"; \
	  for k in PARENT_FEDERATION_TOKEN PARENT_FEDERATION_ACCESS_KEY; do \
	    eval "v=\$$$$k"; \
	    [ -n "$$v" ] || { echo "✖ child mode: $$k unset in $(OCI_ENV)" >&2; exit 1; }; \
	  done; \
	fi
	@command -v oci >/dev/null   || { echo "✖ oci CLI not in PATH" >&2; exit 1; }
	@command -v terraform >/dev/null || { echo "✖ terraform not in PATH" >&2; exit 1; }
	@oci iam region list >/dev/null 2>&1 || { echo "✖ oci CLI not authenticated — run 'oci session refresh -p $$OCI_CLI_PROFILE' (or oci session authenticate)" >&2; exit 1; }
	@echo "▶ pre-flight ok (mode=$(OCI_MODE), dir=$(OCI_DIR))"

_oci-render-cli: $(OCI_RENDER)
$(OCI_RENDER): cmd/oci-render/main.go deploy/oci/render/render.go deploy/oci/render/templates/cp.yaml.tmpl deploy/oci/render/templates/okesu-cp.env.tmpl
	@mkdir -p $(@D)
	go build -o $@ ./cmd/oci-render
```

- [ ] **Step 2: Smoke-test the helpers**

```
make _oci-preflight MODE=standalone OCI_ENV=/dev/null OCI_TFVARS=/dev/null 2>&1 | head -5
```

Expected: fails fast with a clear "✖ missing /dev/null" or similar.

- [ ] **Step 3: Commit**

```
git add Makefile
git commit -m "make: oci section header + pre-flight + render-cli helpers"
```

---

### Task 19: Makefile — oci-build, oci-plan, oci-apply

**Steps:**

- [ ] **Step 1: Append the targets**

```makefile
# ── oci-build: cross-compile cp + UI bundle (alias for ui + cp-all).
oci-build: ui cp-all $(OCI_RENDER)
	@echo "▶ oci-build done — binaries in $(CP_DIR)/, render in $(OCI_RENDER)"

# ── oci-plan: terraform plan in $(OCI_DIR) with the right tfvars.
oci-plan: _oci-preflight
	cd $(OCI_DIR) && terraform init -input=false
	cd $(OCI_DIR) && terraform plan -var-file=$(abspath $(OCI_TFVARS))

# ── oci-apply: terraform apply.
oci-apply: _oci-preflight
	cd $(OCI_DIR) && terraform init -input=false
	cd $(OCI_DIR) && terraform apply -auto-approve -var-file=$(abspath $(OCI_TFVARS))
	@echo "▶ terraform apply done"
```

- [ ] **Step 2: Sanity-check (no real terraform run yet)**

```
make oci-plan MODE=standalone OCI_TFVARS=/dev/null 2>&1 | head
```

Expected: pre-flight rejects /dev/null. Confirms the dependency chain works.

- [ ] **Step 3: Commit**

```
git add Makefile
git commit -m "make: oci-build, oci-plan, oci-apply targets"
```

---

### Task 20: Makefile — oci-render

Renders cp.yaml + /etc/default/okesu-cp from terraform outputs into `dist/oci/<mode>/`.

**Steps:**

- [ ] **Step 1: Append the target**

```makefile
oci-render: _oci-preflight $(OCI_RENDER)
	@mkdir -p dist/oci/$(OCI_MODE)
	cd $(OCI_DIR) && terraform output -json > $(abspath dist/oci/$(OCI_MODE))/tf.json
	$(OCI_RENDER) render-cp-yaml --mode=$(OCI_MODE) --env=$(OCI_ENV) --validate \
	  < dist/oci/$(OCI_MODE)/tf.json > dist/oci/$(OCI_MODE)/cp.yaml
	$(OCI_RENDER) render-env --mode=$(OCI_MODE) --env=$(OCI_ENV) \
	  < dist/oci/$(OCI_MODE)/tf.json > dist/oci/$(OCI_MODE)/okesu-cp.env
	@echo "▶ rendered dist/oci/$(OCI_MODE)/{cp.yaml,okesu-cp.env}"
```

- [ ] **Step 2: Commit**

```
git add Makefile
git commit -m "make: oci-render — render cp.yaml + okesu-cp.env from terraform output"
```

---

### Task 21: Makefile — oci-install

Copies binary + cp.yaml + secrets + systemd unit to the CP VM, daemon-reloads, starts, healthchecks.

**Steps:**

- [ ] **Step 1: Append the target**

```makefile
# Number of retries waiting for cp-vm to be sshable (cloud-init may
# still be running) and waiting for the CP /health endpoint to come
# up after start.
OCI_SSH_RETRIES   ?= 12
OCI_HEALTH_RETRIES ?= 18

# arch of the binary to upload — match cp-vm's shape.
OCI_CP_ARCH ?= amd64
OCI_CP_BIN  ?= $(CP_DIR)/okesu-cp-linux-$(OCI_CP_ARCH)

oci-install: oci-render
	@CP_IP=$$(cd $(OCI_DIR) && terraform output -raw cp_public_ip); \
	  echo "▶ cp-vm = $$CP_IP"; \
	  for i in $$(seq 1 $(OCI_SSH_RETRIES)); do \
	    ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 $(OCI_SSH_USER)@$$CP_IP true 2>/dev/null && break; \
	    echo "  waiting for ssh ($$i/$(OCI_SSH_RETRIES))…"; sleep 10; \
	  done; \
	  echo "▶ uploading binary + config"; \
	  scp -q -o StrictHostKeyChecking=no $(OCI_CP_BIN) $(OCI_SSH_USER)@$$CP_IP:/tmp/okesu-cp.new; \
	  scp -q -o StrictHostKeyChecking=no dist/oci/$(OCI_MODE)/cp.yaml      $(OCI_SSH_USER)@$$CP_IP:/tmp/cp.yaml.new; \
	  scp -q -o StrictHostKeyChecking=no dist/oci/$(OCI_MODE)/okesu-cp.env $(OCI_SSH_USER)@$$CP_IP:/tmp/okesu-cp.env.new; \
	  scp -q -o StrictHostKeyChecking=no systemd/okesu-cp.service          $(OCI_SSH_USER)@$$CP_IP:/tmp/okesu-cp.service.new; \
	  echo "▶ uploading secrets dir"; \
	  rsync -aq --delete -e "ssh -o StrictHostKeyChecking=no" \
	    $$(grep '^secrets_dir' $(OCI_TFVARS) | sed -E 's/^.*=[[:space:]]*"([^"]+)"/\1/')/ \
	    $(OCI_SSH_USER)@$$CP_IP:/tmp/secrets/; \
	  echo "▶ atomically installing + reload + restart"; \
	  ssh -o StrictHostKeyChecking=no $(OCI_SSH_USER)@$$CP_IP 'sudo bash -s' <<-'BASH' ; \
	    set -euo pipefail \
	    install -m 0755 -o root -g root /tmp/okesu-cp.new /usr/local/bin/okesu-cp \
	    install -m 0640 -o root -g okesu-cp /tmp/cp.yaml.new /etc/okesu-cp/cp.yaml \
	    install -m 0640 -o root -g okesu-cp /tmp/okesu-cp.env.new /etc/default/okesu-cp \
	    install -m 0644 -o root -g root /tmp/okesu-cp.service.new /etc/systemd/system/okesu-cp.service \
	    rm -rf /etc/okesu-cp/secrets \
	    mv /tmp/secrets /etc/okesu-cp/secrets \
	    chown -R okesu-cp:okesu-cp /etc/okesu-cp/secrets \
	    chmod -R go-rwx /etc/okesu-cp/secrets \
	    systemctl daemon-reload \
	    systemctl enable --now okesu-cp \
	    systemctl restart okesu-cp \
	BASH \
	  echo "▶ waiting for /health"; \
	  for i in $$(seq 1 $(OCI_HEALTH_RETRIES)); do \
	    if curl -ksS --max-time 5 https://$$CP_IP:8443/health >/dev/null; then \
	      echo "✔ CP up at https://$$CP_IP:8443"; exit 0; \
	    fi; \
	    echo "  health probe $$i/$(OCI_HEALTH_RETRIES)…"; sleep 5; \
	  done; \
	  echo "✖ healthcheck failed; tail of journalctl:"; \
	  ssh -o StrictHostKeyChecking=no $(OCI_SSH_USER)@$$CP_IP 'sudo journalctl -u okesu-cp --no-pager -n 50'; \
	  exit 1
```

> **Note for the engineer:** the heredoc inside Make is finicky. The `<<-'BASH'` form is required (tabs only, no spaces, single-quoted to suppress make-side expansion). If your tab/space settings break it, switch to a per-line `ssh ... 'cmd'` chain — uglier but bulletproof. The end result must be: install all four files atomically, then `daemon-reload`, then `enable --now`, then a final `restart` (so a re-run picks up new cp.yaml).

- [ ] **Step 2: Commit**

```
git add Makefile
git commit -m "make: oci-install — scp binary/config/secrets, systemd reload + restart, healthcheck"
```

---

### Task 22: Makefile — oci-deploy + oci-redeploy

**Steps:**

- [ ] **Step 1: Append the targets**

```makefile
# The headline target: terraform apply + render + scp + start.
oci-deploy: oci-build oci-apply oci-install
	@echo "▶ deploy complete (mode=$(OCI_MODE))"

# Iteration target: just re-render + reinstall. No terraform.
oci-redeploy: oci-build oci-install
	@echo "▶ redeploy complete (mode=$(OCI_MODE))"
```

- [ ] **Step 2: Commit**

```
git add Makefile
git commit -m "make: oci-deploy + oci-redeploy compose the pipeline"
```

---

### Task 23: Makefile — oci-destroy with typed confirmation

**Steps:**

- [ ] **Step 1: Append the target**

```makefile
oci-destroy: _oci-preflight
	@echo "▶ this will DELETE all VMs, DBs, buckets in compartment for mode=$(OCI_MODE)"
	@read -p "type 'destroy' to confirm: " ans; \
	  [ "$$ans" = "destroy" ] || { echo "aborted"; exit 1; }
	cd $(OCI_DIR) && terraform destroy -auto-approve -var-file=$(abspath $(OCI_TFVARS))
	@SECRETS=$$(grep '^secrets_dir' $(OCI_TFVARS) | sed -E 's/^.*=[[:space:]]*"([^"]+)"/\1/'); \
	  [ -d "$$SECRETS" ] && rm -rf "$$SECRETS" || true
	@echo "▶ destroyed (mode=$(OCI_MODE))"
```

- [ ] **Step 2: Commit**

```
git add Makefile
git commit -m "make: oci-destroy — typed-confirmation guard, wipes secrets dir"
```

---

### Task 24: Makefile — oci-print + oci-test

**Steps:**

- [ ] **Step 1: Append the targets**

```makefile
# oci-print: human-readable dump of the operator-relevant outputs.
# Parent mode prints the federation bundle for child enrollment.
oci-print:
	@cd $(OCI_DIR) && terraform output cp_public_ip ch_private_ip 2>/dev/null || true
	@if [ "$(OCI_MODE)" = "parent" ]; then \
	  echo; echo "── federation_outputs (paste into child's tfvars + .env.oci) ──"; \
	  cd $(OCI_DIR) && terraform output -json federation_outputs | jq -r '.[] | to_entries[] | "  \(.key) = \"\(.value)\""' 2>/dev/null || \
	  cd $(OCI_DIR) && terraform output federation_outputs; \
	fi

# oci-test: render-package goldens + terraform validate across all modules.
oci-test: $(OCI_RENDER)
	go test ./deploy/oci/render/...
	@for d in deploy/oci/terraform deploy/oci/terraform/modules/*; do \
	  [ -f "$$d/main.tf" ] || continue; \
	  echo "=== $$d ==="; \
	  (cd "$$d" && rm -rf .terraform && terraform init -backend=false -no-color >/dev/null && terraform validate -no-color); \
	done
	terraform fmt -check -recursive deploy/oci/terraform/
```

- [ ] **Step 2: Smoke-test**

```
make oci-test
```

Expected: PASS for all three render goldens, "Success!" per module, no fmt drift.

- [ ] **Step 3: Commit**

```
git add Makefile
git commit -m "make: oci-print (federation bundle) + oci-test (render goldens + tf validate)"
```

---

## Phase 5: Documentation

---

### Task 25: Update `deploy/oci/README.md`

**Files:**
- Modify: `deploy/oci/README.md`

**Steps:**

- [ ] **Step 1: Replace contents**

The new README has these sections:

1. **What this is** — a `make oci-deploy MODE=…` flow that takes a clean checkout to a running CP on OCI in one command. Three modes: standalone, parent, child.
2. **Topology** — copy the §3.1 diagram from the spec.
3. **Services used** — the same table from the current README, with the row for *Compute* changed from "OKE" to "OCI Compute (VM)" and a row for *ClickHouse* changed from "ClickHouse on OKE" to "ClickHouse on OCI Compute (VM)".
4. **Prerequisites** — `oci` CLI authenticated, terraform ≥ 1.6, SSH keypair, Anthropic API key, target compartment + region.
5. **Quick start (standalone)** — copy from spec §6.2.
6. **Parent + Child** — copy from spec §6.3, including the warning about distinct `OCI_DIR` paths.
7. **Iterating** — `make oci-redeploy MODE=…`.
8. **Tearing down** — `make oci-destroy MODE=…`.
9. **What this directory contains** — list `cp.example.yaml`, `cp.yaml.tmpl`, `okesu-cp.env.tmpl`, `modes/`, `terraform/`, `render/`.
10. **What's not here** — kept minimal: HA / backups / OCI-Vault adapter (Phase 8e.next).

Delete the old "What's not here yet → Helm chart for the CP itself + tunnel-server StatefulSet — Phase 8f" line; the chart exists, and this flow no longer uses it.

- [ ] **Step 2: Commit**

```
git add deploy/oci/README.md
git commit -m "deploy/oci: README — rewrite for VM-based make oci-deploy flow"
```

---

### Task 26: Rewrite `docs/oci-validation.md` as a checklist

**Files:**
- Modify: `docs/oci-validation.md`

**Steps:**

- [ ] **Step 1: Restructure as a runbook checklist**

Replace contents with:

```markdown
# OCI smoke validation

A two-pass checklist that exercises `make oci-deploy` end-to-end against a real OCI tenancy. Run in a dedicated, throwaway compartment.

## Pass 1 — Standalone

- [ ] `cp deploy/oci/.env.oci.example .env.oci` and set `ANTHROPIC_API_KEY`
- [ ] `cp deploy/oci/modes/standalone.tfvars.example deploy/oci/terraform/standalone.tfvars` and fill in
- [ ] `make oci-test` — render goldens + terraform validate pass
- [ ] `make oci-build`
- [ ] `make oci-deploy MODE=standalone`
- [ ] Open `https://<cp_public_ip>:8443` (printed at the end of deploy)
- [ ] Log in with the admin password from `~/.okesu-secrets/cp/admin-password`
- [ ] Settings → Deploy → Add a daemon node, confirm it heartbeats
- [ ] Run an investigation; confirm the agent's Anthropic call succeeds (i.e., `ANTHROPIC_API_KEY` reached the CP)
- [ ] `make oci-destroy MODE=standalone`

## Pass 2 — Parent + Child federation

- [ ] In `~/okesu-parent/`, deploy parent: `make oci-deploy MODE=parent`
- [ ] `make oci-print MODE=parent` — copy the federation outputs
- [ ] In `~/okesu-child/`, prepare: copy `deploy/oci/terraform/`, set `OCI_DIR`, fill `child.tfvars` + `.env.oci`
- [ ] `make oci-deploy MODE=child OCI_DIR=$PWD/terraform`
- [ ] On the parent CP, confirm a federated catalog request from the child arrives in the parent's bucket (UI → Catalog → Federated CPs)
- [ ] `make oci-destroy MODE=child OCI_DIR=$PWD/terraform`
- [ ] `make oci-destroy MODE=parent`

## Notes

- A full pass takes ~30 minutes (Postgres provisioning is the long pole at ~15 min).
- Use a dedicated compartment so `terraform destroy` is unambiguous.
- If a step fails, capture `journalctl -u okesu-cp --no-pager -n 200` from the CP VM and the failing step's Make output.
```

- [ ] **Step 2: Commit**

```
git add docs/oci-validation.md
git commit -m "docs/oci-validation: rewrite as a make-target checklist"
```

---

## Final task: green build

- [ ] Run the full test/validate sweep:

```
make oci-test
go vet ./...
go build ./...
```

Expected: all green.

- [ ] If anything fails, fix it before declaring done. Common failures and where to look:
  - YAML field-name drift → `controlplane/config_yaml.go` vs `deploy/oci/render/templates/cp.yaml.tmpl`
  - terraform validate error → re-read the latest module Task; missing variable wiring is the #1 cause
  - Makefile heredoc breakage → see the note in Task 21

- [ ] **Final commit (if any cleanup):**

```
git commit -m "deploy/oci: green build across go test, go vet, terraform validate"
```

---

## Self-review (filled out by the plan author after writing)

**Spec coverage:**

- §3.1 topology → Tasks 11, 12, 13, 14
- §3.2 mode-as-config → Tasks 17, 19, 9 (`Mode` is a tfvars field; render package gates federation block per mode)
- §3.3 network changes → Task 10
- §4 repo layout → Tasks 1-7 (render), 8-15 (terraform), 17 (templates+tfvars)
- §5 Makefile targets — every target named in the spec has a task: oci-build (19), oci-plan (19), oci-apply (19), oci-render (20), oci-install (21), oci-deploy (22), oci-redeploy (22), oci-destroy (23), oci-print (24), oci-test (24)
- §6 operator workflow → Task 25 (README)
- §7 data flow → distributed across Tasks 8-15 (terraform writes secrets), 17 (.env.oci shape), 18-22 (Makefile orchestration)
- §8.1 cp.yaml.tmpl → Task 2 (and refined in 3, 4)
- §8.2 render package → Tasks 1-7
- §8.3 cloud-init → Tasks 11, 12
- §8.4 cp_vm + clickhouse_vm modules → Tasks 11, 12
- §8.5 federation_token → Task 9
- §9 error handling → Task 18 (pre-flight), 21 (retries + journalctl tail)
- §10 testing → Task 6 (round-trip), 24 (oci-test), 26 (smoke runbook)
- §11 migration — covered in README rewrite (Task 25)

**Placeholder scan:** none — every task has full code or clear instruction. The only "judgment call" left to the engineer is exact ClickHouse version pin (defaulted to `24.8.4.13`, override at `var.clickhouse_version`).

**Type consistency:** `RenderCPYAML`, `RenderEnvFile`, `ValidateCPYAML` consistent across Tasks 2-7 and the CLI in Task 7. `Inputs` struct field set defined in Task 1 and never broadened. Terraform variable names (`mode`, `cp_shape`, `clickhouse_password`, etc.) consistent across Tasks 11-14.
