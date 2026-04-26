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

## Config flags map

```
--db postgres://user:pass@db.adb.<region>.oraclecloud.com:5432/cpdb
--pubsub-url redis://cache.<region>.oraclecloud.com:6379/0
--blob-url <namespace>.compat.objectstorage.<region>.oraclecloud.com
--blob-access-key <generated in IAM → Customer Secret Keys>
--blob-secret-key <ditto>
--blob-bucket okesu-prod
--blob-region us-ashburn-1
```

## What's not in this directory yet

- Terraform module that provisions the above (DB, Cache, Streaming,
  Object Storage, OKE, LBs, IAM, Certificates) — Phase 8f
- Helm chart for the CP itself + tunnel-server StatefulSet — Phase 8f
- Runbooks for cert rotation, DR, scaling out tunnel servers — Phase 8f

This file exists today as the reference architecture document so the
shape is locked in even before the IaC lands.
