# Load test harness

Synthetic webhook clients hammering the CP. Validates that the ingest
path holds up at fleet-scale throughput before production launch.

## Quick start

```bash
# 1. Bring up the dev stack (or just run the CP with SQLite)
( cd dev && docker compose up -d )

# 2. Boot the CP pointing at it (see dev/README.md for full flags)
./okesu-cp serve \
  --db postgres://okesu:okesu@localhost:5432/okesu \
  --pubsub-url redis://localhost:6379/0 \
  --listen :8443 --mgmt-listen :8444 \
  --admin-password okesu-demo --webhook-secret demo-shared-1

# 3. Drive traffic
go run ./test/loadtest \
  --cp-url=https://localhost:8443/api/webhooks/events \
  --secret=demo-shared-1 \
  --nodes=1000 \
  --rate=10 \
  --duration=60s
```

That run simulates 1,000 nodes each emitting 10 events/sec (10k evt/s
aggregate) for 60 seconds.

## Useful node-count / rate combos

| Test name           | --nodes | --rate | Aggregate evt/s | What it stresses                       |
|---------------------|---------|--------|-----------------|----------------------------------------|
| smoke               | 10      | 1      | 10              | Wiring sanity                          |
| dev                 | 100     | 5      | 500             | Single-CP baseline (sqlite holds up)   |
| 10k-fleet           | 1000    | 10     | 10,000          | Tier-1 production target               |
| 100k-fleet-burst    | 5000    | 20     | 100,000         | Tier-3 production target — needs       |
|                     |         |        |                 | ClickHouse + Kafka adapters wired      |

The 100k-fleet-burst run will saturate the SQLite write path long
before reaching steady-state — that's the expected signal saying "time
to flip the EventStore adapter to ClickHouse" (Phase 8c.next).

## What's NOT here

- **Persistent tunnel load test** — opens N gRPC tunnels and holds them
  with periodic mgmt-plane heartbeats. Different scaling profile from
  webhook ingest; warrants its own harness.
- **Metric collection** — today the harness reports throughput +
  error rate per second. Hooking in a Prometheus pull endpoint on the
  CP and scraping it during a run would surface CPU / RAM / GC /
  goroutine count, which is what you'd want to graph for sizing
  decisions.

Both are TODOs that block actual production sign-off but are bounded
work. The goal of this commit is to land the harness skeleton so the
above gaps are visible and easy to plug.
