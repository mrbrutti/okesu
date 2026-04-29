# S3 transport ("dead-drop" mode)

A bucket-based transport for nodes that can't reach the CP directly but can
reach object storage (NAT'd, air-gapped, DMZ). Both sides — the agent on the
node and the CP — only ever read and write objects in a shared bucket; they
never connect to each other directly.

This doc is the **wire-format spec**. Source of truth for object naming,
payload schemas, and lifecycle rules. The transport is implemented in
`agent/s3transport/` (node side) and `controlplane/transport/s3scanner/`
(CP side).

The transport lives **alongside** the existing HTTPS pull-mode and webhook
flows, never replacing them. Each node row carries a `transport` column
(`https | s3`) selecting which path that node uses.

## Bucket layout

One bucket per CP. The CP "owns" everything under `cp/<cp-id>/`. In a
federated topology, each CP has its own bucket and its own keyspace; nodes
are bound to one CP at package-generation time.

```
s3://okesu-<cp-id>/
  cp/<cp-id>/
    enrollment/
      pubkey.pem                              ← fleet trust anchor (CP-managed)
    registration/
      <node-uuid>.json                        ← node enrollment requests
    nodes/<node-id>/
      cert.pem                                ← CP issues post-enrollment
      config/
        desired.json                          ← CP-driven config (poll_interval_ms etc.)
      jobs/
        inbox/
          <job-id>.json                       ← CP enqueues here
        <job-id>/
          claimed.json                        ← node claims atomically
          output/
            00001.txt                         ← line-numbered chunks
            00002.txt
            ...
          exit.json                           ← terminal status
      heartbeat.json                          ← node touches every poll
      events/
        YYYY-MM-DD/
          <ts>-<rand>.ndjson                  ← batched daimon events
      findings/
        <ts>-<rand>.json
```

Slash is the only path separator. Adapters MUST treat keys as opaque.

### Object lifecycle

Bucket-level lifecycle rules clean up stale data:

| Prefix                                  | TTL       | Reason                                      |
|-----------------------------------------|-----------|---------------------------------------------|
| `cp/*/registration/`                    | 7 days    | Stale registration requests                 |
| `cp/*/nodes/*/jobs/*/output/`           | 30 days   | Job output retention                        |
| `cp/*/nodes/*/jobs/*/exit.json`         | 30 days   | Job exit history                            |
| `cp/*/nodes/*/jobs/inbox/`              | 7 days    | Unclaimed jobs (node went away)             |
| `cp/*/nodes/*/events/`                  | 7 days    | Daimon events (already ingested into CP DB) |
| `cp/*/nodes/*/heartbeat.json`           | (none)    | Constantly overwritten                      |

CP scanner ingests events and findings into its own persistent DB; the
bucket is hot-tier transport, not the system of record.

## Identity model

The package contains **fleet** credentials, not per-node identity. One
package can register N machines.

### Fleet enrollment

The CP holds a fleet keypair. Public key is published to
`cp/<cp-id>/enrollment/pubkey.pem`. Private key never leaves the CP.

Each generated package contains a **package signing certificate** signed
by the fleet key. The package identifies "you came from a legitimate
package" but says nothing about which node will use it.

### Per-node enrollment flow

1. Node boots, runs install script.
2. Node generates UUID + RSA/ECDSA keypair locally.
3. Node writes `cp/<cp-id>/registration/<node-uuid>.json`:
   ```json
   {
     "node_uuid": "<uuid>",
     "hostname": "<os-reported>",
     "os": "linux",
     "arch": "amd64",
     "csr_pem": "<x509 CSR base64>",
     "package_id": "<id>",
     "agents_requested": ["edr", "instance-integrity"],
     "signed_at": "2026-04-29T00:00:00Z",
     "signature": "<base64 signature over the above with package signing key>"
   }
   ```
4. CP scanner polls `cp/<cp-id>/registration/`. For each new object:
   - Verifies signature against fleet public key.
   - Allocates a `node_id` in the local `nodes` table (status=`ready`).
   - Signs the CSR using the CP's mTLS CA, producing a per-node cert
     bound to the assigned `node_id`.
   - Writes the cert to `cp/<cp-id>/nodes/<node-id>/cert.pem`.
   - Optionally seeds `cp/<cp-id>/nodes/<node-id>/jobs/inbox/<job-id>.json`
     with `agent_run` jobs to install the requested daimons.
   - Deletes the registration request.
5. Node polls `cp/<cp-id>/nodes/<node-id>/cert.pem` (resolved via the
   UUID-keyed redirect at `cp/<cp-id>/registration-result/<node-uuid>.json`).
   Once present, switches to per-node identity for all future writes.

### Cred rotation

CP can rotate the fleet keypair at any time. Existing per-node certs are
unaffected (they're signed by the mgmt CA, not the fleet key). New
packages get a new fleet pubkey; old packages stop being able to register.
Operators regenerate packages after rotation.

## Schemas

### Registration request

`cp/<cp-id>/registration/<node-uuid>.json`

```json
{
  "node_uuid": "string (uuid v4)",
  "hostname": "string",
  "os": "linux | darwin | freebsd | openbsd | illumos",
  "arch": "amd64 | arm64 | ...",
  "csr_pem": "string (base64-encoded PEM)",
  "package_id": "string (CP-issued at package-gen time)",
  "agents_requested": ["string (agent name)", ...],
  "signed_at": "RFC3339 timestamp",
  "signature": "string (base64; over JSON-canonical bytes excluding 'signature')"
}
```

### Registration result (CP → node)

`cp/<cp-id>/registration-result/<node-uuid>.json`

```json
{
  "node_uuid": "string",
  "node_id": 42,
  "cert_path": "cp/<cp-id>/nodes/42/cert.pem",
  "issued_at": "RFC3339 timestamp"
}
```

### Heartbeat (node → CP)

`cp/<cp-id>/nodes/<node-id>/heartbeat.json`

```json
{
  "node_id": 42,
  "ts": "RFC3339",
  "tunnel_running": false,
  "version": "okesu/0.x.y"
}
```

### Job queue

CP enqueues a job by writing `cp/<cp-id>/nodes/<node-id>/jobs/inbox/<job-id>.json`.
The body matches `agent.JobPayload` (existing wire type from
`agent/jobs_proto.go`), serialized as JSON.

Node polls `inbox/`. To claim atomically, it writes
`cp/<cp-id>/nodes/<node-id>/jobs/<job-id>/claimed.json` with an
**If-None-Match: \***  precondition. On success the node deletes the inbox
copy and proceeds. On 412 Precondition Failed (rare — would only happen
if two daemons share an identity, which shouldn't happen) it skips.

```json
{
  "job_id": "string",
  "claimed_at": "RFC3339",
  "node_id": 42
}
```

Output streams as numbered chunk objects:

`cp/<cp-id>/nodes/<node-id>/jobs/<job-id>/output/<seq>.txt`

Body: raw stdout bytes, no framing. Sequence is zero-padded so
list-by-name order is chronological.

Exit:

`cp/<cp-id>/nodes/<node-id>/jobs/<job-id>/exit.json`

```json
{
  "job_id": "string",
  "node_id": 42,
  "exit_code": 0,
  "error": "",
  "tunnel_started": false,
  "ended_at": "RFC3339"
}
```

### Events (daimon → CP)

Same `agent.Event` schema as the HTTPS webhook. Batched into newline-
delimited JSON files, one event per line, written to:

`cp/<cp-id>/nodes/<node-id>/events/YYYY-MM-DD/<unix-ms>-<rand>.ndjson`

Flush triggers (whichever first):
- 50 events buffered
- 15s elapsed since last flush
- daemon shutdown (best-effort sync flush)

CP scanner ingests, then deletes (or relies on lifecycle rule).

### Findings

`cp/<cp-id>/nodes/<node-id>/findings/<unix-ms>-<rand>.json`

Same shape as `agent.EventFinding` (existing).

### Desired config (CP → node)

`cp/<cp-id>/nodes/<node-id>/config/desired.json`

```json
{
  "version": 7,
  "poll_interval_ms": 10000,
  "heartbeat_interval_ms": 30000,
  "events_flush_interval_ms": 15000,
  "events_flush_max": 50,
  "agents": ["edr", "instance-integrity"]
}
```

CP bumps `version` on each change. Node polls this object at the same
cadence as the jobs inbox; on version increment, applies hot-reload.

## Polling cadence

Defaults — every value tunable via `desired.json`:

| What                 | Default | Range          |
|----------------------|---------|----------------|
| Jobs inbox poll      | 10s     | 5s – 300s      |
| Heartbeat            | 30s     | 10s – 300s     |
| Config poll          | 60s     | 30s – 600s     |
| Events flush         | 15s     | 5s – 60s       |
| Output chunk flush   | 5s      | 1s – 30s       |
| CP scanner sweep     | 10s     | 5s – 60s       |

Same `poll_interval_ms` applies to the existing HTTPS pull-mode runtime —
the field is now respected by both transports.

## Multi-CP / federation

Each CP has its own bucket (`okesu-<cp-id>`). Nodes are bound to one CP
at package-generation time. Federation reads happen through the existing
federation HTTP API (CP-to-CP), not via shared S3 buckets.

If an operator needs a node to migrate from CP A to CP B, they regenerate
the package for B and reinstall. No live migration in v1.

## Implementation files

| Layer       | File                                                           |
|-------------|----------------------------------------------------------------|
| Wire types  | `agent/s3transport/wire.go` (this spec, in code)               |
| Node side   | `agent/s3transport/runner.go`, `eventsink.go`, `enroll.go`     |
| S3 client   | `agent/s3transport/client.go` (minio-go wrapper)               |
| CP side     | `controlplane/transport/s3scanner/scanner.go`                  |
| CP CRUD     | `controlplane/api/transport_configs.go`                        |
| Packaging   | `controlplane/packaging/{generator.go, tarball.go, deb.go,…}`  |
| DB          | `controlplane/db/migrations/sqlite/028_s3_transport.sql`       |
| CLI         | `cmd/okesu/main.go` — `enroll`, `jobs --transport=s3`           |

## Phases

- **9.1** Spec (this doc) ✓
- **9.2** DB schema: `transport_configs` table + columns on `nodes`
- **9.3** Agent S3 transport: enroll, jobs runner, event sink
- **9.4** CP S3 scanner: ingest registrations, jobs ack, events
- **9.5** Self-register package generator (flexible: tar.gz first, deb/rpm/pkg via plugin interface)
- **9.6** UI: transport picker, package download, S3 connection status
