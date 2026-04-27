# OCI tenancy validation runbook

The smoke harness for the Phase 8 OCI integration. Five items needed
real-cloud validation; this runbook walks through provisioning the
minimum infrastructure that exercises each one, runs the smoke checks,
and tears it all down again.

> **Status:** unvalidated. The Terraform under `deploy/oci/terraform/`
> follows the OCI provider docs but has not been run against a live
> tenancy yet. Treat the first end-to-end pass as the validation;
> capture findings inline as a post-run section at the bottom.

## What we're validating

| # | Item | How |
|---|---|---|
| 1 | Postgres migrations + end-to-end queries | Apply 18 migrations against managed OCI Database with PostgreSQL; exercise login → daemon registration → finding ingest → audit query |
| 2 | Postgres adapter under traffic | Same DB, drive load via Phase 5 below |
| 3 | ClickHouse insert path under load | ClickHouse on OKE, batch inserts at fleet rate, validate `ORDER BY (ts, agent, host)` partitioning |
| 4 | Kafka SASL/TLS to OCI Streaming | OCI Streaming stream pool with the OCI-specific SASL username format `<tenancy>/<user>/<stream-pool-ocid>` |
| 5 | (closed by Phase 8c.next2 — already shipped) | webhook → Queue.Publish, worker projects findings |

## Constraints

- Budget: 10–20 instances, time-boxed runs
- Region: `us-ashburn-1`
- Fleet size default: 8 (2 in the OCI Always Free tier, 6 paid at ~$0.005/hr)
- The CP itself runs **on your laptop**, dialed into OCI services over the public internet — keeps blast radius small and lets you tear down everything except the laptop

## Estimated burn

| Service | Per hour | Per day | Notes |
|---|---|---|---|
| Database with PostgreSQL (smallest) | ~$0.10 | $2.40 | 2 OCPU, 32 GB |
| Cache (Redis, smallest) | ~$0.05 | $1.20 | |
| Streaming | per-message | <$0.10 | smoke traffic is trivial |
| Object Storage | per-GB-month | <$0.05 | |
| OKE control plane | $0 | $0 | OCI absorbs the control plane |
| OKE worker (1 node) | ~$0.005 | $0.12 | E2.1.Micro |
| Fleet VMs (8 — 2 free + 6 paid) | ~$0.03 | $0.72 | Always Free covers 2 |
| **Total** | **~$0.20** | **~$5** | full stack running |

Leaving the full stack up for a week is ~$35. Designed to be brought up,
smoke-tested for 1–4 hours, then torn down.

---

## Phase 0 — Pre-flight

Local-machine prep. No OCI spend.

```bash
# 1. Verify the OCI CLI is configured.
oci iam region list --query 'data[?name==`us-ashburn-1`]' --output table
# Expect a row for us-ashburn-1.

# 2. Capture your tenancy + compartment OCIDs into a tfvars file.
TENANCY_OCID=$(oci iam compartment list --all --compartment-id-in-subtree true \
  --query 'data[0]."compartment-id"' --raw-output)
USER_OCID=$(grep '^user' ~/.oci/config | awk -F= '{print $2}' | xargs)

# Pick a compartment for okesu — either reuse the root tenancy compartment
# or create a dedicated one (recommended for cleanup).
oci iam compartment create \
  --compartment-id "$TENANCY_OCID" \
  --name okesu-smoke \
  --description "Phase 8 OCI integration smoke test"
# Wait ~30s for ACTIVE state, then capture:
COMPARTMENT_OCID=$(oci iam compartment list --all \
  --query 'data[?name==`okesu-smoke`].id | [0]' --raw-output)

# 3. Verify Terraform 1.6+ is on path.
terraform version

# 4. Drop the variables file the Terraform expects.
cat > deploy/oci/terraform/terraform.tfvars <<EOF
tenancy_ocid     = "$TENANCY_OCID"
user_ocid        = "$USER_OCID"
compartment_ocid = "$COMPARTMENT_OCID"
region           = "us-ashburn-1"
fleet_size       = 8

# SSH public key the CP will install into each fleet VM for the
# ssh-deploy flow. Use a key whose private half lives only on your
# laptop.
ssh_public_key   = "$(cat ~/.ssh/id_ed25519.pub)"
EOF

# 5. Smoke-test access by listing availability domains.
oci iam availability-domain list --compartment-id "$TENANCY_OCID" --output table
```

If any of these fail, fix the underlying issue (CLI not installed,
permissions missing, expired API key) before continuing — every later
phase depends on the basics working.

---

## Phase 1 — Network

VCN, three subnets (public for LB / fleet, private for db/cache, OKE
worker subnet), internet + NAT gateways, route tables, security lists.

**Cost:** $0 — VCNs and gateways are free in OCI.

```bash
cd deploy/oci/terraform
make init
make plan-network
# Review the plan — should show ~12 resources to create.
make apply-network
```

**Smoke verify:**

```bash
oci network vcn list --compartment-id "$COMPARTMENT_OCID" --output table
oci network subnet list --compartment-id "$COMPARTMENT_OCID" --output table
```

Expect: 1 VCN named `okesu-vcn`, 3 subnets (`public`, `private`, `oke`).

---

## Phase 2 — Managed services (validates #1, #2, #4 + Object Storage)

Provisions the four managed services in parallel: Postgres, Streaming,
Cache, Object Storage. ~5–10 minutes wait while OCI provisions the DB.

**Cost:** ~$0.20/hour while running.

```bash
make plan-managed   # adds db + streaming + cache + objectstorage
make apply-managed  # interactive confirmation
```

The Terraform output emits the values you need for `cp.yaml`:

```
db_dsn          = "postgres://okesu@db.adb.us-ashburn-1.oraclecloud.com:5432/cpdb?sslmode=require"
kafka_brokers   = "streampool-xxxxx.streaming.us-ashburn-1.oci.oraclecloud.com:9092"
kafka_username  = "ocid1.tenancy.oc1..xxx/ocid1.user.oc1..yyy/ocid1.streampool.oc1.iad.zzz"
redis_url       = "redis://cache.us-ashburn-1.oci.oraclecloud.com:6379/0"
blob_endpoint   = "<namespace>.compat.objectstorage.us-ashburn-1.oraclecloud.com"
blob_bucket     = "okesu-smoke"
blob_access_key = "<customer-key-access-id>"
```

(Secrets — `db_password`, `kafka_sasl_password`, `redis_auth_token`,
`blob_secret_key` — are written to `~/.okesu-secrets/` with mode 0600
by the Terraform's local-exec step. They're never echoed to stdout.)

**Smoke verify:**

```bash
# 1. Postgres reachability and migrations.
psql "$(terraform output -raw db_dsn_with_password)" -c '\l'

# 2. Build the CP and run it locally pointing at the OCI services.
( cd ../../.. && go build -o okesu-cp ./cmd/cp )
cat > /tmp/cp.yaml <<EOF
listen: ":8443"
mgmt_listen: ":8444"
db: "$(terraform output -raw db_dsn)"
events_store: "sqlite"            # ClickHouse comes in Phase 3
queue: "kafka"
kafka_brokers: ["$(terraform output -raw kafka_brokers)"]
kafka_sasl_username: "$(terraform output -raw kafka_username)"
kafka_sasl_password: "\${secret:kafka/sasl-password}"
kafka_use_tls: true
pubsub_url: "$(terraform output -raw redis_url)"
blob_url: "$(terraform output -raw blob_endpoint)"
blob_access_key: "$(terraform output -raw blob_access_key)"
blob_secret_key: "\${secret:blob/secret-key}"
blob_bucket: "$(terraform output -raw blob_bucket)"
blob_region: "us-ashburn-1"
admin_email: "admin@local"
admin_password: "\${secret:cp/admin-password}"
session_key: "\${secret:cp/session-key}"
webhook_secret: "\${secret:cp/webhook-secret}"
EOF
echo -n okesu-smoke   > ~/.okesu-secrets/cp/admin-password
echo -n smoke-shared  > ~/.okesu-secrets/cp/webhook-secret
echo -n $(openssl rand -base64 32) > ~/.okesu-secrets/cp/session-key

../../../okesu-cp serve \
  --config /tmp/cp.yaml \
  --secrets-source file://$HOME/.okesu-secrets
```

Look for these log lines on boot:

```
db: postgres opened (postgres://okesu@db.adb.us-ashburn-1.oraclecloud.com:5432/cpdb)
db: applied 18 migrations
queue: kafka (streampool-xxxxx.streaming.us-ashburn-1.oci.oraclecloud.com:9092)
```

If migrations fail, capture the SQL line + error and fix in
`controlplane/db/migrations/postgres/`. **This is the most likely place
to find bugs** — every migration was hand-ported from SQLite.

**Validates:** items #1 (Postgres migrations) and #4 (Kafka SASL handshake).
Item #2 is partial — full validation requires actual webhook traffic, which
needs the fleet (Phase 4).

---

## Phase 3 — OKE + ClickHouse (validates #3)

Provisions a 1-node OKE cluster, deploys ClickHouse via the bitnami
Helm chart, exposes it inside the cluster.

**Cost:** ~$0.005/hour while running.

```bash
make plan-oke
make apply-oke   # ~10 minutes for OKE provisioning + chart install

# Capture the kubeconfig the Terraform wrote.
export KUBECONFIG="$(terraform output -raw oke_kubeconfig_path)"
kubectl get nodes
kubectl -n okesu get pods | grep clickhouse
```

Update `cp.yaml` to point events at ClickHouse:

```yaml
events_store: "clickhouse"
clickhouse_addrs:
  - "clickhouse.okesu.svc.cluster.local:9000"
clickhouse_database: "okesu_events"
clickhouse_username: "default"
clickhouse_password: "${secret:clickhouse/password}"
clickhouse_secure: false
```

For the laptop-CP to reach the in-cluster ClickHouse, port-forward:

```bash
kubectl -n okesu port-forward svc/clickhouse 9000:9000 &
# Then update clickhouse_addrs to ["localhost:9000"] in cp.yaml.
```

Restart the CP. On boot it bootstraps the MergeTree schema:

```
clickhouse: connected (single replica), database=okesu_events
clickhouse: events table ready (ORDER BY ts, agent, host)
```

**Validates:** item #3 partial — schema bootstrap and reachability. Full
load validation comes in Phase 5.

---

## Phase 4 — Fleet (8 daemon VMs)

Provisions the daemon hosts. Each VM gets the SSH public key from
`terraform.tfvars`. The CP then SSH-deploys the okesu binary +
`edr.md` agent file to each.

**Cost:** ~$0.03/hour for 6 paid + 2 Always Free.

```bash
make plan-fleet
make apply-fleet   # ~3 minutes
```

Captures fleet IPs into `terraform output -json fleet_ips`.

```bash
# Build the daemon binary for linux/amd64 (the fleet VM arch).
( cd ../../.. && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o okesu-linux-amd64 ./cmd/okesu )
sudo install -m 0755 ../../../okesu-linux-amd64 /usr/local/share/okesu/binaries/

# In the CP UI: Settings → Deploy → upload, or via API:
for ip in $(terraform output -json fleet_ips | jq -r '.[]'); do
  # Use the CP's "Add Node + Deploy" flow with examples/agents/edr.md
  curl -k -b cookies.txt -X POST https://localhost:8443/api/nodes \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"smoke-$ip\",\"hostname\":\"$ip\",\"ssh_user\":\"opc\",\"ssh_port\":22}"
  # Then trigger deploy for ["edr"] with the SSH key.
done
```

After ~1 minute each daemon starts heartbeating. The Agents page
shows 8 healthy rows.

**Smoke verify:**

```bash
# CP UI → Agents — all 8 hosts heartbeating
# CP UI → Live Events — events trickling in (every 30s per agent)
psql "$(terraform output -raw db_dsn_with_password)" \
  -c "SELECT COUNT(*) FROM agents WHERE last_heartbeat > NOW() - INTERVAL '2 minutes';"
# Expect: 8
```

**Validates:** item #2 partial (queries run against Postgres under real traffic).

---

## Phase 5 — Load test

The synthetic webhook driver simulates a much larger fleet than the 8
real VMs, so we can flush out async-pipeline bugs without paying for
1000 hosts.

```bash
( cd ../../.. && go build -o loadtest ./test/loadtest )

../../../loadtest \
  --target https://localhost:8443/api/webhooks/events \
  --secret smoke-shared \
  --nodes 1000 \
  --findings-per-sec 100 \
  --duration 1h
```

While it runs, watch:

```bash
# Kafka consumer lag — should stay near zero with the worker draining.
oci streaming admin stream-pool get --stream-pool-id "$(terraform output -raw stream_pool_id)" \
  --query 'data."metrics"' --output table

# ClickHouse insert rate.
kubectl -n okesu exec deploy/clickhouse -- clickhouse-client \
  -q "SELECT count() FROM okesu_events.events WHERE ts > now() - INTERVAL 1 MINUTE"
# Should grow by ~100 events/sec * 60 = 6000 per minute window.

# Postgres findings table — every finding should have a non-zero event_id.
psql "$(terraform output -raw db_dsn_with_password)" \
  -c "SELECT COUNT(*), COUNT(event_id) FROM findings WHERE ts > NOW() - INTERVAL '1 minute';"
# COUNT(*) and COUNT(event_id) should match.
```

**Validates:**

- Item #2 — Postgres under sustained load
- Item #3 — ClickHouse insert path with realistic batches; check
  `system.parts_log` to confirm `ORDER BY (ts, agent, host)` is producing
  reasonable partitions

If the worker falls behind (Kafka lag grows unbounded), tune
`BatchSize` / `BatchTimeout` in `controlplane/eventpipeline/pipeline.go`
or scale the CP to 2 replicas (the consumer group will rebalance
automatically).

---

## Phase 6 — Teardown

**Important:** `destroy` removes everything. The DB, the bucket, all
data inside. Take exports first if you want to preserve anything.

```bash
# Optional — pull a Postgres dump and ClickHouse export before teardown.
pg_dump "$(terraform output -raw db_dsn_with_password)" | gzip > /tmp/cp.db.sql.gz
kubectl -n okesu exec deploy/clickhouse -- clickhouse-client \
  -q "SELECT * FROM okesu_events.events FORMAT Native" > /tmp/events.native

# Then:
make destroy   # plan + interactive confirmation
```

Verify everything is gone:

```bash
oci network vcn list --compartment-id "$COMPARTMENT_OCID" --query 'data[*].name'
oci db postgres-cluster list --compartment-id "$COMPARTMENT_OCID" --query 'data[*]."display-name"'
oci streaming admin stream-pool list --compartment-id "$COMPARTMENT_OCID" --query 'data[*].name'
# All should return empty arrays.
```

You can keep the empty `okesu-smoke` compartment around for the next
smoke run, or delete it via:

```bash
oci iam compartment delete --compartment-id "$COMPARTMENT_OCID" --force
```

---

## Findings to capture inline

After the first end-to-end run, replace this section with:

- Migrations that needed fixes (which file, what the error was)
- Kafka SASL handshake quirks (any deviation from the segmentio defaults?)
- ClickHouse partitioning observations (skew? compression ratio?)
- Cost actuals for a 4-hour run vs the table above
- Anything else surprising

That captures the validation result so future smoke runs start from a
known-good baseline.

---

## Re-running from a failure

The Terraform is idempotent — `make apply-*` after a failed step
re-evaluates and only changes what drifted. To restart cleanly from
phase N:

```bash
terraform destroy -target=module.<later-modules>
make apply-<phase-N>
```

`make destroy` always works as a final reset.
