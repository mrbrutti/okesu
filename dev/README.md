# Dev profile — production-shape stack on a laptop

Brings up Postgres, ClickHouse, Redpanda (Kafka-compat), Redis, and
MinIO (S3-compat) so the Control Plane runs through the same code paths
it'll hit in OCI production. Adapter selection is config — the same
binary boots against either stack.

## Quick start

```bash
cd dev
docker compose up -d
docker compose ps                 # wait until everything is healthy

# Build the CP if you haven't yet
( cd .. && go build -o okesu-cp ./cmd/cp )

../okesu-cp serve \
  --db postgres://okesu:okesu@localhost:5432/okesu \
  --events-store clickhouse \
  --clickhouse-addrs localhost:9000 \
  --clickhouse-database okesu_events \
  --clickhouse-username default \
  --queue kafka \
  --kafka-brokers localhost:9092 \
  --pubsub-url redis://localhost:6379/0 \
  --blob-url localhost:9080 \
  --blob-access-key minio --blob-secret-key minio12345 \
  --blob-bucket okesu-dev --blob-region us-east-1 \
  --listen :8443 --mgmt-listen :8444 \
  --admin-password okesu-demo --webhook-secret demo-shared-1 \
  --daimon-files-dir ./agents
```

### Or use a YAML config (Phase 8g)

Same stack, single file:

```bash
# Drop the canonical secrets, mode 0600
mkdir -p ~/.okesu-secrets/{cp,clickhouse}
echo -n okesu-demo    > ~/.okesu-secrets/cp/admin-password
echo -n demo-shared-1 > ~/.okesu-secrets/cp/webhook-secret
echo -n dev-key       > ~/.okesu-secrets/cp/session-key
chmod 600 ~/.okesu-secrets/cp/*

# Use the canonical example as a starting point
cp ../deploy/oci/cp.example.yaml ./cp.yaml
# Edit DSN/host fields to point at the local docker-compose services.

../okesu-cp serve \
  --config ./cp.yaml \
  --secrets-source file://$HOME/.okesu-secrets
```

Open https://localhost:8443 and sign in as `admin@local` / `okesu-demo`.

## What runs

| Service       | Port  | Console           | Why                                  |
|---------------|-------|-------------------|--------------------------------------|
| Postgres 16   | 5432  | psql              | Relational state (users, daimons…)   |
| ClickHouse 24 | 8123  | http://localhost:8123 | Events firehose                  |
| Redpanda      | 9092  | rpk               | Kafka-API queue                      |
| Redis 7       | 6379  | redis-cli         | Pub/sub fan-out                      |
| MinIO         | 9001  | http://localhost:9001 | S3-compat blob store             |

Default credentials on every service are weak (`okesu/okesu`,
`minio/minio12345`). Don't reuse this compose file outside dev.

## Switching between SQLite and the full stack

The CP defaults to SQLite + in-process for everything. To "downshift"
back to the original lightweight dev profile:

```bash
../okesu-cp serve --db ./cp.db --listen :8443 --mgmt-listen :8444 ...
```

Adapters are picked by config — no code change. Useful when you want
to iterate on UI without booting all the dependencies.

## Cleaning up

```bash
docker compose down              # stop containers, keep data
docker compose down -v           # also wipe volumes (fresh state)
```

## What's NOT here yet

- **OCI Vault adapter** (`oci-vault://...` for `--secrets-source`) —
  Phase 8e.next, blocked on a real tenancy round-trip.
- **OCI Certificates adapter** for `ports.CertManager` — Phase 8e.next.
- **Helm chart + Terraform module** for OKE deployment — Phase 8f
  (the dev compose file approximates the runtime, but does not
  generate IaC).

Everything else from the Phase 8 plan ships in this profile: Postgres
migrations apply via the dialect-aware runner, ClickHouse / Kafka /
Redis / S3 adapters all wire through `controlplane/ports/`, and the
YAML config + `${secret:NAME}` resolver matches the production OCI
deployment shape.
