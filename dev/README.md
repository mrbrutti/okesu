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
  --pubsub-url redis://localhost:6379/0 \
  --blob-url localhost:9080 \
  --blob-access-key minio --blob-secret-key minio12345 \
  --blob-bucket okesu-dev --blob-region us-east-1 \
  --listen :8443 --mgmt-listen :8444 \
  --admin-password okesu-demo --webhook-secret demo-shared-1 \
  --daimon-files-dir ./agents
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

Phase 8c.next ports the daimon SQL to Postgres so `--db postgres://...`
fully works (today: connects + pings, but applyMigrations is sqlite-
only). Until then, the production-shape stack runs the events / pubsub
/ blob paths against real services, but state still lives in SQLite.
This is a deliberate split so each adapter can be reviewed in
isolation.
