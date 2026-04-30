# OCI deployment shape

Reference architecture for running the Okesu Control Plane on Oracle
Cloud Infrastructure. The CP itself is cloud-agnostic — every external
dependency goes through a port (see `controlplane/ports/`) and selecting
an OCI-native adapter is a config decision. This directory is the
operator-facing side of that story.

## Quick start

```bash
# 1. Configure operator secrets (required: ANTHROPIC_API_KEY)
cp deploy/oci/.env.oci.example .env.oci
$EDITOR .env.oci

# 2. Configure infra (compartment, region, ssh key)
cp deploy/oci/modes/standalone.tfvars.example deploy/oci/terraform/standalone.tfvars
$EDITOR deploy/oci/terraform/standalone.tfvars

# 3. Authenticate with OCI
oci session authenticate    # or `oci session refresh -p <profile>`

# 4. Build, plan, deploy in one command
make oci-deploy MODE=standalone
# → CP up at https://<cp_public_ip>:8443
```

## Modes

`make oci-deploy MODE=<standalone|parent|child>` provisions one
self-contained CP installation. Same OCI footprint in all three modes;
only the rendered config (`/etc/okesu-cp/cp.yaml` + `/etc/default/okesu-cp`)
differs.

| Mode | Use for |
|---|---|
| `standalone` | Single CP, no federation. Default. |
| `parent` | "Global" CP that other CPs federate into. Generates a federation token. |
| `child` | CP that publishes catalog/IOC data into a parent's S3 bucket. |

For parent + child, deploy each in a separate working dir / state. After
`make oci-deploy MODE=parent`, run `make oci-print MODE=parent` and paste
the federation outputs into the child's `child.tfvars` + `.env.oci`.

## Topology

Each deployment provisions one VCN containing:

```
                           ┌──────────────────────────────────────┐
                           │  OCI compartment                     │
                           │                                      │
        operator's laptop  │  ┌─ VCN ────────────────────────┐    │
            │              │  │                              │    │
     make oci-deploy ─SSH──┼──┼─→ public subnet              │    │
            │              │  │   ├─ cp-vm (okesu-cp + UI)   │    │
            │              │  │   └─ fleet-* (daemon VMs)    │    │
            │              │  │                              │    │
            │              │  │   private subnet             │    │
            │              │  │   ├─ ch-vm (clickhouse)      │    │
            │              │  │   ├─ OCI Postgres            │    │
            │              │  │   └─ OCI Cache (Redis)       │    │
            │              │  └──────────────────────────────┘    │
            │              │                                      │
            │              │  OCI Streaming (Kafka)  ─ regional   │
            │              │  OCI Object Storage     ─ regional   │
            │              └──────────────────────────────────────┘
```

## Services used

| Concern                     | OCI service                          | Adapter (CP-side)                      |
|-----------------------------|--------------------------------------|----------------------------------------|
| Relational state            | OCI Database with PostgreSQL         | `db.Open(postgres://...)`              |
| Events firehose             | ClickHouse on a dedicated VM         | `adapters/clickhouseevents/`           |
| Async event pipeline        | OCI Streaming (Kafka API)            | `adapters/kafka/`                      |
| Pub/sub fan-out             | OCI Cache (Redis)                    | `adapters/redispubsub/`                |
| Blob storage                | OCI Object Storage (S3-compatible)   | `adapters/s3blob/`                     |
| Compute (CP, ClickHouse)    | OCI Compute (Oracle Linux 9 VMs)     | systemd units; SSH-deployed binary     |
| Fleet (daemon nodes)        | OCI Compute (Oracle Linux 9 VMs)     | SSH-deployed via the CP's UI           |
| OIDC SSO                    | Identity Domains                     | already integrated                     |

## Configuration

Two operator-controlled inputs:

- **`.env.oci`** (gitignored) — operator secrets the Makefile sources:
  - `ANTHROPIC_API_KEY` (required)
  - `OPENAI_API_KEY` (optional)
  - `OIDC_CLIENT_SECRET` (optional)
  - For child mode: `PARENT_FEDERATION_TOKEN`, `PARENT_FEDERATION_ACCESS_KEY`, `PARENT_FEDERATION_SECRET_KEY` (the rest can come from tfvars)

- **`<MODE>.tfvars`** (under `deploy/oci/terraform/`) — infra config:
  - `mode`, `tenancy_ocid`, `user_ocid`, `compartment_ocid`, `region`, `ssh_public_key`, `name_prefix`
  - For child mode: `parent_federation_bucket`, `parent_federation_endpoint`, `parent_federation_region`, `parent_federation_access_key` (paste from parent's `make oci-print MODE=parent` output)

## Make targets

| Target | What it does |
|---|---|
| `oci-build` | Cross-compile cp + UI bundle |
| `oci-plan` | terraform plan |
| `oci-apply` | terraform apply (idempotent) |
| `oci-render` | render cp.yaml + okesu-cp.env from terraform output |
| `oci-install` | scp + systemctl install + healthcheck |
| `oci-deploy` | apply + render + install (the headline target) |
| `oci-redeploy` | render + install only — for fast iteration after a code change |
| `oci-destroy` | terraform destroy + wipe secrets dir (typed confirmation) |
| `oci-print` | print operator-relevant outputs (in parent mode: federation bundle) |
| `oci-test` | render-package goldens + terraform validate across all modules |

`MODE` defaults to `standalone`. Set `OCI_DIR=/path/to/terraform` if you're
working out of a non-default terraform directory (e.g., for child deploys).

## Iterating

```bash
# Change code, rebuild, redeploy without touching infra
make oci-build
make oci-redeploy MODE=standalone   # ~10 seconds
```

## Tearing down

```bash
make oci-destroy MODE=standalone
# Prompts: "type 'destroy' to confirm" — guards against accidental wipes
```

## What's in this directory

- `cp.example.yaml` — canonical YAML config (reference)
- `cp.yaml.tmpl` — Go template the Makefile renders into `dist/oci/<mode>/cp.yaml`
- `okesu-cp.env.tmpl` — template for `/etc/default/okesu-cp` (carries `OKESU_CP_FLEET_ANTHROPIC_API_KEY` etc.)
- `.env.oci.example` — operator secrets template
- `modes/` — per-mode tfvars examples
- `render/` — Go package that does the rendering
- `terraform/` — infra modules

## What's not here

- Multi-AZ / HA — single-VM CP, single-VM ClickHouse.
- Backups orchestration — see `scripts/cp-backup.sh` separately.
- TLS cert provisioning for a real DNS name — self-signed by default; bring-your-own via `.env.oci` paths.
- OCI Vault adapter for `ports.Secrets` (`oci-vault://` source) — Phase 8e.next.
- OCI Certificates adapter for `ports.CertManager` — Phase 8e.next.
