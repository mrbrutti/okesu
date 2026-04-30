# Okesu — End-to-end OCI build / configure / deploy

**Status:** approved design, ready for implementation plan
**Date:** 2026-04-30
**Owner:** infra
**Related:** `deploy/oci/terraform/` (existing), `deploy/helm/okesu-cp/` (kept but unused
by this flow), `docs/oci-validation.md` (becomes checklist that maps to `make` targets)

---

## 1. Goal

A single Makefile target — `make oci-deploy MODE=<standalone|parent|child>` — takes a
clean checkout to a running Okesu Control Plane on Oracle Cloud Infrastructure: builds
the binaries, applies terraform, generates `cp.yaml`, scps the artifact, and starts the
service. Iteration after a code change collapses to `make oci-build && make oci-redeploy`.

Three deployment modes, all sharing the same OCI footprint and differing only in
`cp.yaml` content:

- **standalone** — single CP, no federation
- **parent** ("global") — CP that other CPs federate into; mints a federation token
- **child** — CP that publishes federated catalog/IOC data into a parent's S3 bucket

Production multi-CP rollouts will be UI-driven via the existing S3 dead-drop bootstrap
flow once a global CP exists. The `MODE=child` target stays in the Makefile primarily
for dev/test of the federation pipe.

---

## 2. Non-goals

- Multi-AZ / HA. Single-VM CP, single-VM ClickHouse.
- Zero-downtime upgrades. `make oci-redeploy` is restart-with-brief-downtime.
- TLS-cert provisioning for a real DNS name. Self-signed by default; bring-your-own via
  `.env.oci` paths.
- Backups orchestration. `scripts/cp-backup.sh` stays separate.
- A live-OCI test in CI. Static validation + a render-time golden test only; real
  provisioning is operator-driven.
- An OKE/Helm path. The existing `deploy/helm/okesu-cp/` chart is preserved for
  k8s-native customers but is not the primary OCI deploy.

---

## 3. Architecture

### 3.1 Topology

Each `make oci-deploy` provisions one self-contained CP installation in one OCI
compartment, one VCN.

```
                           ┌──────────────────────────────────────┐
                           │  OCI compartment                     │
                           │                                      │
        operator's laptop  │  ┌─ VCN ────────────────────────┐    │
            │              │  │                              │    │
     make oci-deploy ─SSH──┼──┼─→ public subnet              │    │
            │              │  │   ├─ cp-vm (okesu-cp + UI)   │    │
            │              │  │   └─ ch-vm (clickhouse)      │    │
            │              │  │                              │    │
            │              │  │   private subnet             │    │
            │              │  │   ├─ OCI Postgres            │    │
            │              │  │   └─ OCI Cache (Redis)       │    │
            │              │  └──────────────────────────────┘    │
            │              │                                      │
            │              │  OCI Streaming (Kafka)  ─ regional   │
            │              │  OCI Object Storage     ─ regional   │
            │              └──────────────────────────────────────┘
            │
            └── (child mode only) parent CP's federation bucket — pasted into child.tfvars
```

OKE is removed. ClickHouse runs on its own VM (`ch-vm`) installed via the upstream dnf
package (`dnf install clickhouse-server-<pinned-version>`) and managed by systemd. The
CP runs on `cp-vm` as a systemd service. Postgres, Streaming (Kafka), Cache (Redis),
and Object Storage remain as today — managed services, addressable over the VCN.

### 3.2 Mode differences — purely in `cp.yaml`

| Field | standalone | parent | child |
|---|---|---|---|
| `FederationToken` | unset | random, written to `~/.okesu-secrets/federation/token` | parent's value (paste) |
| `FederationS3PublishBucket` | unset | unset | parent's bucket |
| `FederationS3PublishEndpoint` | unset | unset | parent's endpoint |
| `FederationS3PublishAccessKey` | unset | unset | parent's CSK access ID |
| `FederationS3PublishSecretKey` | unset | unset | parent's CSK secret (paste) |
| `CPInstanceID` | auto | seeded (`cp-parent-001` default) | seeded, distinct |

Same infra in all three modes. Only the rendered config file differs.

### 3.3 Network changes vs current terraform

- ClickHouse VM lives in the **private** subnet, not public, since the operator never
  reaches it directly — only the CP VM does.
- The existing private security list permits all intra-VCN traffic from `10.0.0.0/16`,
  which already allows the CP VM in the public subnet to reach ClickHouse on 9000. We
  add an explicit TCP 9000 ingress rule on the private SL anyway, for auditability —
  it is functionally redundant but documents intent.
- No new public ingress is needed: only 22 (SSH) and 8443/8444 (CP UI / mgmt) are
  exposed to the internet, all on cp-vm. ClickHouse 9000 is never internet-reachable.

---

## 4. Repo layout

```
deploy/oci/
├── README.md                          # updated: removes "Phase 8f not here yet"
├── cp.example.yaml                    # unchanged
├── cp.yaml.tmpl                       # NEW. Go template, rendered per mode
├── jobs.env.tmpl                      # NEW. Anthropic/OpenAI key delivery
├── .env.oci.example                   # NEW. Operator secrets template
├── modes/
│   ├── standalone.tfvars.example      # NEW
│   ├── parent.tfvars.example          # NEW
│   └── child.tfvars.example           # NEW. Includes parent_federation_* fields
├── render/                            # NEW. Go package that renders the templates
│   ├── render.go
│   ├── render_test.go
│   └── testdata/
│       ├── cp.yaml.standalone.golden
│       ├── cp.yaml.parent.golden
│       └── cp.yaml.child.golden
└── terraform/
    ├── main.tf                        # reworked: drop OKE, add cp_vm + clickhouse_vm
    ├── variables.tf                   # + mode + parent_federation_* (child-only)
    ├── outputs.tf                     # + cp_public_ip, ch_private_ip,
    │                                  #   federation_outputs (parent-only, prints
    │                                  #   what to paste into child.tfvars)
    ├── versions.tf                    # unchanged
    └── modules/
        ├── network/                   # tighten ch SL ingress to 9000-only
        ├── db/                        # unchanged
        ├── streaming/                 # unchanged
        ├── cache/                     # unchanged
        ├── objectstorage/             # parent mode also generates federation_token
        ├── cp_vm/                     # NEW. oci_core_instance + cloud-init
        └── clickhouse_vm/             # NEW. oci_core_instance + cloud-init
                                       # (dnf install clickhouse-server-<pinned>)
```

The existing `modules/oke/` is **deleted**. The existing `deploy/helm/okesu-cp/`
chart is left in place — it's still a useful artifact for k8s-native operators — but
this Makefile path does not invoke it.

---

## 5. Makefile additions

The existing top-level `Makefile` keeps `daemon`, `daemons`, `cp`, `cp-all`, `release`,
`ui`, `deposit`, `clean`, `version` unchanged. The `oci-*` targets layer on top.

```
oci-build                # cross-compile cp + UI for linux/amd64 + linux/arm64
                         # alias: invokes existing `make ui cp-all`

oci-plan      MODE=...   # terraform plan with the right tfvars
oci-apply     MODE=...   # terraform apply
oci-render    MODE=...   # render cp.yaml + jobs.env from tf outputs + .env.oci
oci-install   MODE=...   # scp + systemctl start (assumes apply + render done)
oci-deploy    MODE=...   # apply + render + install (the headline target)
oci-redeploy  MODE=...   # render + install only — no terraform — for fast iteration
oci-destroy   MODE=...   # terraform destroy + wipe ~/.okesu-secrets/ (typed confirm)
oci-print     MODE=...   # print operator-facing values (federation outputs, IPs)

oci-test                 # run render package's golden tests + terraform validate
```

`MODE` defaults to `standalone`. The Makefile sources `.env.oci` (gitignored) for
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY` (optional), `OIDC_CLIENT_SECRET` (optional). It
refuses to run `oci-deploy` if `ANTHROPIC_API_KEY` is missing.

---

## 6. Operator workflow

### 6.1 Prerequisites

Documented in `deploy/oci/README.md`:

- `oci` CLI authenticated (`oci session authenticate` for SSO, or API key)
- `terraform >= 1.6`
- An SSH keypair
- An Anthropic API key
- A target OCI compartment OCID + region

### 6.2 Standalone

```
$ cp deploy/oci/modes/standalone.tfvars.example deploy/oci/terraform/standalone.tfvars
$ $EDITOR deploy/oci/terraform/standalone.tfvars   # compartment, region, ssh key
$ cp deploy/oci/.env.oci.example .env.oci
$ $EDITOR .env.oci                                 # ANTHROPIC_API_KEY=…

$ make oci-build
$ make oci-deploy MODE=standalone
   ⋮
   ✔ CP up at https://132.226.x.y:8443
   ✔ Admin password written to ~/.okesu-secrets/cp/admin-password
```

### 6.3 Parent + Child

Each deployment owns its own terraform state. The simplest convention is two working
directories — one per CP — each holding its own `terraform/` copy with its own
`.terraform/` and `terraform.tfstate`. The Makefile's `OCI_DIR` variable points at
which terraform directory to use; it defaults to `$(REPO_ROOT)/deploy/oci/terraform/`.

```
# 1) Parent — uses the in-repo terraform dir
$ make oci-deploy MODE=parent
   ⋮
   ✔ CP up at https://132.226.x.y:8443
   ✔ Federation outputs (paste into child.tfvars):
       parent_federation_bucket      = "okesu-parent-bucket"
       parent_federation_endpoint    = "<ns>.compat.objectstorage.us-ashburn-1.oraclecloud.com"
       parent_federation_access_key  = "<csk-id>"
       parent_federation_secret_key  = "<sensitive — saved to ~/.okesu-secrets/parent/secret-key>"
       parent_federation_token       = "<sensitive — saved to ~/.okesu-secrets/parent/federation-token>"
       parent_cp_instance_id         = "cp-parent-001"
       parent_url                    = "https://132.226.x.y:8443"

# 2) Child — separate working dir with its own state
$ mkdir -p ~/okesu-child && cd ~/okesu-child
$ cp -r ~/okesu/deploy/oci/terraform .
$ cp ~/okesu/deploy/oci/modes/child.tfvars.example terraform/child.tfvars
$ $EDITOR terraform/child.tfvars                    # paste the values printed above
$ make -C ~/okesu oci-deploy MODE=child OCI_DIR=$PWD/terraform
```

If parent and child are deployed from the same machine, they MUST use distinct
`OCI_DIR` paths (or distinct `terraform workspace` names) to avoid one's apply
clobbering the other's state.

### 6.4 Iteration after a code change

```
$ make oci-build
$ make oci-redeploy MODE=standalone     # scp + systemctl restart, ~10s
```

### 6.5 Tearing down

```
$ make oci-destroy MODE=standalone
   This will delete VMs, DBs, buckets in compartment <ocid>.
   Type 'destroy' to confirm:
```

---

## 7. Data flow

```
.env.oci                  $MODE.tfvars                  terraform state
   │                            │                              │
   ├─ANTHROPIC_API_KEY          ├─compartment_ocid             ├─generated:
   ├─OPENAI_API_KEY (opt)       ├─region                       │  cp/admin-password
   ├─OIDC_CLIENT_SECRET (opt)   ├─ssh_public_key               │  cp/session-key
   │                            ├─cp_image_version             │  cp/webhook-secret
   │                            ├─clickhouse_version           │  db-admin-password
   │                            └─name_prefix                  │  kafka/sasl-password
   │                                                           │  redis/auth-token
   │                                                           │  blob/secret-key
   │                                                           │  clickhouse/password
   │                                                           │  (parent only) federation/token
   │                                                           │
   │                                                           ├─outputs:
   │                                                           │  db_dsn (no pwd)
   │                                                           │  kafka_brokers, kafka_username
   │                                                           │  redis_url (no pwd)
   │                                                           │  blob_endpoint, blob_bucket, blob_access_key
   │                                                           │  ch_private_ip
   │                                                           │  cp_public_ip
   │                                                           │  (parent only) federation_outputs.json
   │
   ▼
┌──────────────────── make oci-deploy ────────────────────────┐
│ 1. source .env.oci                                          │
│ 2. terraform apply -var-file=$MODE.tfvars                   │
│ 3. terraform output -json  →  $.tf_out                      │
│ 4. render cp.yaml.tmpl   ($.tf_out ⨁ .env.oci ⨁ MODE)        │
│ 5. render jobs.env.tmpl  ($.tf_out ⨁ .env.oci)              │
│ 6. PRE-FLIGHT: feed rendered cp.yaml through                │
│    controlplane.LoadConfigFile on the operator's laptop     │
│    (catches drift before scp)                               │
│ 7. scp dist/cp/okesu-cp-linux-amd64       → /usr/local/bin/ │
│    scp dist/oci/cp.yaml                   → /etc/okesu/     │
│    scp dist/oci/jobs.env                  → /etc/okesu/     │
│    scp ~/.okesu-secrets/                  → /etc/okesu/secrets/ (mode 0600, owner=okesu) │
│    scp systemd/okesu-cp.service           → /etc/systemd/system/ │
│ 8. ssh: daemon-reload + enable --now okesu-cp.service       │
│ 9. healthcheck: curl --insecure -fsS /health (12 retries)   │
└─────────────────────────────────────────────────────────────┘
```

The CP boots, reads `/etc/okesu/cp.yaml`, resolves `${secret:NAME}` from
`file:///etc/okesu/secrets/`, dials Postgres/Kafka/Redis/Object Storage, and starts
`okesu-jobs.service` which reads `/etc/okesu/jobs.env` for `ANTHROPIC_API_KEY`.

### 7.1 Idempotence

- **Terraform:** stock apply behavior; re-runs are no-ops if nothing changed.
- **Render:** pure function of (tf outputs ⨁ .env.oci ⨁ tmpl ⨁ MODE) — same inputs
  produce byte-identical output.
- **Install:** writes to a temp file, atomically `mv`s into place; `systemctl restart`
  fires only if the binary's sha256 or cp.yaml content changed (Makefile compares with
  `sha256sum` over ssh).

---

## 8. Components

### 8.1 `cp.yaml.tmpl`

Go `text/template` source. Every value is a template ref. The mode-conditional
federation block uses `{{ if eq .Mode "child" }}…{{ end }}` so child mode produces the
`FederationS3Publish*` lines, parent produces `FederationToken: ${secret:federation/token}`,
standalone produces neither.

### 8.2 `deploy/oci/render/`

Small Go package that owns rendering. Public surface:

```go
type Inputs struct {
    Mode           string             // "standalone" | "parent" | "child"
    TerraformOut   map[string]any     // from `terraform output -json`
    OperatorEnv    map[string]string  // from .env.oci
}

func RenderCPYAML(in Inputs) ([]byte, error)
func RenderJobsEnv(in Inputs) ([]byte, error)
func ValidateCPYAML(rendered []byte) error  // calls controlplane.LoadConfigFile
```

The Makefile shells out to a tiny `cmd/oci-render` CLI that wraps this package:
`cmd/oci-render render --mode=$MODE --tf-out=- --env=.env.oci > dist/oci/cp.yaml`.

### 8.3 Cloud-init scripts

- `cp_vm`: install systemd unit *file* (no binary yet), `useradd okesu`,
  `mkdir -p /etc/okesu/secrets /var/lib/okesu`, `chown` paths, open firewalld for
  8443/8444. Make scp + systemctl-start happen post-apply.
- `clickhouse_vm`: add upstream ClickHouse yum repo, `dnf install clickhouse-server-<pinned>`,
  write `/etc/clickhouse-server/users.d/okesu.xml` with the password from secrets,
  `systemctl enable --now clickhouse-server`. ClickHouse VM is fully self-contained at
  apply time — no post-apply step needed for it.

### 8.4 Terraform module additions

- `modules/cp_vm/`: one `oci_core_instance` in the public subnet with cloud-init.
  Outputs: `public_ip`, `private_ip`, `instance_id`.
- `modules/clickhouse_vm/`: one `oci_core_instance` in the private subnet.
  Inputs: `clickhouse_password` (read from secrets dir), `clickhouse_version`.
  Outputs: `private_ip`.

### 8.5 Federation token

A `random_password` resource in the `objectstorage` module is **always** generated
(it's free) and written to `~/.okesu-secrets/federation/token`. Whether it's *used*
is a config-time decision: only `mode = "parent"` causes `cp.yaml.tmpl` to emit the
`FederationToken` line referencing it. This keeps the terraform graph free of
mode-conditional branching while still letting the parent surface a stable token.

The parent's terraform `outputs.tf` exposes a `federation_outputs` map (the bundle
the operator pastes into child.tfvars). Child mode reads no terraform state from the
parent; the operator copies the values manually.

---

## 9. Error handling

| Step | Failure mode | Recovery |
|---|---|---|
| `.env.oci` missing or `ANTHROPIC_API_KEY` unset | Makefile aborts before terraform with a clear error | Edit and re-run |
| `oci session` expired | Provider auth error | `oci session refresh -p $PROFILE`; re-run |
| Terraform apply mid-stream (quota exceeded) | Partial state | Re-run; terraform converges. If a resource is unfixable, `terraform state rm` + manual cleanup |
| Postgres takes 15 min to come up | apply waits | Documented; `tail -f .terraform.log` |
| scp fails (cp-vm not yet sshable) | cloud-init still running | Makefile retries 12× with 10s backoff (~2 min total) |
| Binary scp succeeds, `systemctl start` fails | unit fails; healthcheck times out | Makefile prints last 50 lines of `journalctl -u okesu-cp` over ssh |
| Healthcheck never passes | CP up but can't reach DB/Kafka (SL misconfig) | Makefile prints redacted cp.yaml + journalctl tail; usually `oci-apply` re-converges |
| Wrong tfvars (e.g., child without parent token) | Pre-flight rejects before terraform | Operator pastes missing values, re-runs |

### 9.1 Pre-flight checks

Before any `terraform apply` or `scp`:

1. `.env.oci` exists, required vars set
2. `MODE`-specific tfvars exist and have all required fields populated (greps for
   non-empty values; for `child`, all `parent_federation_*` fields must be set)
3. `oci-build` artifacts present in `dist/cp/`
4. `oci` CLI authenticated (`oci iam region list` — fast, fails on expired session)
5. SSH agent has the key referenced in tfvars (warns, doesn't fail)

### 9.2 Logging convention

Every `oci-*` target prepends step labels (`▶ terraform apply`, `▶ render cp.yaml`,
`▶ scp binary`) so an operator can paste failure context with one screenshot.

---

## 10. Testing

### 10.1 Static — runs in CI

- `terraform fmt -check -recursive deploy/oci/terraform/`
- `terraform validate` per module via `terraform init -backend=false` (no OCI creds
  needed)
- `go test ./deploy/oci/render/...` — golden tests for all three modes; the killer
  assertion is that **the rendered cp.yaml round-trips through `controlplane.LoadConfigFile`**.
  This catches drift between the template and the CP's config struct, which was the
  motivating concern of this whole work item.

### 10.2 Pre-flight — runs in `make oci-deploy`

`render.ValidateCPYAML` runs on the operator's laptop, against real terraform outputs,
before any scp. Catches misnamed secrets, broken `${secret:...}` refs, mismatched
secrets-dir paths.

### 10.3 Smoke — operator-driven

Update `docs/oci-validation.md` to a checklist that maps to `make` targets:

```
□ make oci-build
□ make oci-deploy MODE=standalone
□ Open https://<cp_public_ip>:8443, log in with admin password
□ Add a daemon node (UI), confirm it heartbeats
□ Trigger an investigation, confirm Anthropic key works (jobs.env)
□ make oci-destroy MODE=standalone
```

A second smoke section covers parent+child: deploy parent, paste outputs into
`child.tfvars`, deploy child, confirm a federated catalog request lands in the parent's
bucket.

### 10.4 What's intentionally not tested

- Real OCI provisioning in CI — too expensive, too flaky.
- Cloud-init success — only the smoke runbook validates this.
- Federation S3 RPC end-to-end — only the parent+child smoke validates this.

---

## 11. Migration

Operators with an existing `terraform apply` from the old (OKE-bearing) state need to
`terraform destroy` the old stack before running this new one. There is no in-place
migration — the old stack and this new one are not compatible. The smoke runbook
historically warns to use a dedicated compartment for this exact reason; the new
README will be more emphatic.

---

## 12. Open questions

None remaining at design time. Implementation may surface details around:

- Exact ClickHouse package version to pin (latest LTS as of implementation)
- Healthcheck timeout tuning (start with 90s; revise if first smokes fail flakily)
- Whether `cmd/oci-render` should live under `cmd/` (Go convention) or under
  `deploy/oci/render/cmd/` (proximity to its templates). Default to `cmd/oci-render`
  unless an existing `cmd/` convention disagrees.
