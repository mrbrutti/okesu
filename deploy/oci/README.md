# OCI deployment shape

Reference architecture for running the Okesu Control Plane on Oracle
Cloud Infrastructure. The CP itself is cloud-agnostic — every external
dependency goes through a port (see `controlplane/ports/`) and selecting
an OCI-native adapter is a config decision. This directory is the
operator-facing side of that story: how to provision the dependencies.

## Services used

| Concern                     | OCI service                          | Adapter (CP-side)                      |
|-----------------------------|--------------------------------------|----------------------------------------|
| Relational state            | OCI Database with PostgreSQL         | `db.Open(postgres://...)` (Phase 8b)   |
| Events firehose             | ClickHouse on OKE                    | `adapters/clickhouse/` (Phase 8c.next) |
| Async event pipeline        | OCI Streaming (Kafka API)            | `adapters/kafka/` (Phase 8c.next)      |
| Pub/sub fan-out             | OCI Cache (Redis)                    | `adapters/redispubsub/`                |
| Blob storage                | OCI Object Storage (S3-compatible)   | `adapters/s3blob/`                     |
| Compute                     | OKE                                  | (Helm chart — Phase 8f)                |
| L7 LB (UI / API)            | OCI Load Balancer                    | (Ingress)                              |
| L4 LB (tunnels, 1M+ conns)  | OCI Network Load Balancer            | (Service annotation)                   |
| OIDC SSO                    | Identity Domains                     | already integrated                     |
| PKI (mTLS for daemons)      | OCI Certificates                     | `adapters/oci-certs/` (Phase 8e.next)  |
| Secrets / KMS               | OCI Vault                            | `adapters/oci-vault/` (Phase 8e.next)  |

## Configuration

Production deployments use a YAML config file plus an out-of-band
secrets source. **No secret value ever appears on the CLI or in env
vars in production.**

```bash
okesu-cp serve \
  --config /etc/okesu/cp.yaml \
  --secrets-source file:///run/credentials/okesu-cp.service
```

See `cp.example.yaml` in this directory for the full canonical shape.
Highlights:

- Non-secret fields (DSN hosts, regions, bucket names, OIDC issuer
  URL) live in the YAML.
- Secret fields use `"${secret:NAME}"` references that the CP resolves
  at boot from the configured secrets source.
- Canonical secret names follow a slash-separated hierarchy
  (`cp/admin-password`, `clickhouse/password`, …) — see
  `controlplane/secrets.go` for the full list.

### Secrets source options

| Source                                              | Use when                                         |
|-----------------------------------------------------|--------------------------------------------------|
| `env` (default)                                      | dev — secrets in `OKESU_SECRET_*` env vars       |
| `file:///etc/okesu/secrets`                          | systemd `LoadCredential=` — secrets injected at start |
| `file:///run/credentials/okesu-cp.service`           | Same, with the systemd-managed default path     |
| `oci-vault://<compartment-ocid>?region=us-ashburn-1` | OCI Vault (Phase 8e.next — pending tenancy validation) |

### Quick-start: dev with a YAML config

```bash
mkdir -p ~/.okesu-secrets/cp
echo 'demo-pass'  > ~/.okesu-secrets/cp/admin-password
echo 'demo-key'   > ~/.okesu-secrets/cp/session-key
echo 'shared-1'   > ~/.okesu-secrets/cp/webhook-secret
chmod 600 ~/.okesu-secrets/cp/*

cat > /tmp/cp.yaml <<EOF
listen: ":8443"
mgmt_listen: ":8444"
db: "./cp.db"
admin_email: "admin@local"
admin_password: "\${secret:cp/admin-password}"
session_key:    "\${secret:cp/session-key}"
webhook_secret: "\${secret:cp/webhook-secret}"
EOF

okesu-cp serve --config /tmp/cp.yaml --secrets-source=file://$HOME/.okesu-secrets
```

## What's in this directory

- `cp.example.yaml` — canonical YAML config (this file's running example)
- `terraform/` — modules that provision the OCI side (DB, Streaming,
  Cache, Object Storage, OKE + ClickHouse, fleet VMs). See
  [`terraform/README.md`](terraform/README.md) for the layout and
  [`docs/oci-validation.md`](../../docs/oci-validation.md) for the
  end-to-end smoke runbook.

## What's not here yet

- Helm chart for the CP itself + tunnel-server StatefulSet — Phase 8f
- Runbooks for cert rotation, DR, scaling out tunnel servers — Phase 8f
- OCI Vault adapter for `ports.Secrets` (`oci-vault://` source) — Phase 8e.next
- OCI Certificates adapter for `ports.CertManager` — Phase 8e.next
