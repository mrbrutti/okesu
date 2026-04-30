# Fleet LLM API Keys in Settings — Design Spec

**Date:** 2026-04-30
**Status:** Approved (pending user review of this document)

## Goal

Operators manage the OpenAI and Anthropic API keys their fleet uses (for agents/daimons/jobs) directly from Settings, with persistence in the DB and automatic distribution to every node and every federated child CP across all transports — tunnel, poll, and S3 dead-drop. No CP restart required for rotation; new keys reach connected nodes via their existing heartbeat / poll / S3-read schedule.

## Background

Today's plumbing is end-to-end via environment variables:

- The CP reads `OKESU_CP_FLEET_ANTHROPIC_API_KEY` / `OKESU_CP_FLEET_OPENAI_API_KEY` at boot (`controlplane/config.go:160-161`, `:377-378`).
- `Config.FleetAnthropicAPIKey` / `FleetOpenAIAPIKey` are threaded into:
  - `FleetAutoDeployer` (`controlplane/api/auto_deploy.go:NewFleetAutoDeployer`) — writes `/etc/okesu/jobs.env` over SSH at deploy time (`controlplane/sshdeploy/deploy.go:354`)
  - `cpLocalEnv` (`controlplane/server.go:373-378`) — used by the CP's own jobs runtime
- The daemon's `okesu-jobs.service` reads `/etc/okesu/jobs.env` at start; updating requires re-write + restart.

Limitations:
- No DB persistence — restarting the CP without env vars set loses the keys.
- No automatic distribution to already-deployed nodes — they got their keys at install time only.
- No UI surface — operators edit env files by hand and restart processes.
- No federation propagation — every CP in a federated topology is configured independently.

This spec adds DB persistence, a Settings UI, and an automatic distribution layer that covers every existing transport and the federation hierarchy.

## Architecture

Three concerns, one per section.

### 1. CP-local storage + Settings UI

New table `fleet_env`. One row per CP (the table has a CHECK constraint enforcing `id = 1`, same singleton pattern other CP-config tables use). Columns:

```sql
CREATE TABLE fleet_env (
    id                       INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    anthropic_api_key_sealed BLOB,           -- AES-GCM ciphertext, NULL when unset
    openai_api_key_sealed    BLOB,
    version                  INTEGER NOT NULL DEFAULT 0,  -- monotonic; bumps on every save
    source                   TEXT NOT NULL DEFAULT 'local',  -- 'local' | 'federated_from_parent'
    parent_cp_id             TEXT,           -- when source = federated, which parent
    updated_at               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by_user_email    TEXT
);
INSERT OR IGNORE INTO fleet_env (id) VALUES (1);
```

Encryption: reuses the existing `MasterKeyFromMeta()` + AES-GCM seal/unseal helpers `cloud_credentials` already use. No new crypto.

**API endpoints (cookie-auth admin group):**

- `GET /api/fleet-env` — returns
  ```json
  {
    "anthropic_set": true, "anthropic_last4": "abc1",
    "openai_set":    false, "openai_last4":   "",
    "version":       7,
    "source":        "local",
    "parent_cp_id":  null,
    "updated_at":    "2026-04-30T...",
    "updated_by_user_email": "ops@example.com"
  }
  ```
  Never returns the plaintext key. Settings displays masked inputs; only the last 4 chars are shown for confirmation.

- `PUT /api/fleet-env` — body `{ anthropic_api_key?: string, openai_api_key?: string }`. Per-field semantics: omitted = leave unchanged; empty string = delete (sealed → NULL); non-empty = encrypt + store. On any change: bump `version`, set `source = "local"`, write `audit_log` entry, trigger immediate publish (Section 2), refresh `cpLocalEnv`.

**Settings UI:** new `web/src/pages/settings/LLMKeys.tsx` rendered at `/settings/llm-keys`. Two masked-input fields (Anthropic, OpenAI), a "Show last 4" indicator, Save / Clear buttons per key. The page also surfaces the `source` + `parent_cp_id` for federated children (Section 3 covers the inherited UX).

**Backwards compat:** at boot, if the `fleet_env` row has both keys NULL **and** `cfg.FleetAnthropicAPIKey` / `cfg.FleetOpenAIAPIKey` env vars are set, seed the row from env. Once an operator saves through the Settings UI, the DB wins forever — env-var fallback never re-fires. Operators who keep using env-only deployments are unaffected.

### 2. Distribution to nodes

Two channels, both backed by the `fleet_env` row, both keyed on `version` for idempotency.

**Channel A — HTTPS pull (tunnel + poll-mode + SSH-deployed nodes):**

- New `GET /api/v1/fleet/env` on the CP, mounted in the **mTLS-authed daemon-management group** (the same group used for heartbeat, jobs polling, etc.). Returns plaintext keys + version:
  ```json
  { "anthropic_api_key": "...", "openai_api_key": "...", "version": 7 }
  ```
  The plaintext is acceptable here because the channel is mTLS-authenticated and the daemon already receives equivalent secrets (e.g., management cert, SSH key on deploy). The endpoint requires the daemon's client cert.

- **Daemon side:** the existing `agent/daemon.go` heartbeat loop gains a new step: call `/api/v1/fleet/env`, compare `version` to the last-seen value (cached in-memory + persisted to a small state file alongside the existing daemon state), and on change:
  1. Rewrite `/etc/okesu/jobs.env` (atomic via temp-file + rename, mode 0600).
  2. Run `systemctl restart okesu-jobs.service` (same call the deploy already uses; `okesu-jobs` reads the env file at process start).
  3. Update the cached version.

  If the daemon doesn't run as root and can't `systemctl restart`, log a warning and continue — operator can manually restart. The heartbeat loop's existing error handling carries the rest.

**Channel B — S3 dead-drop publish (S3-deployed nodes + federated children using S3 transport):**

- The existing `controlplane/federation/s3publisher` already publishes `findings.json`, `daimons.json`, `nodes.json`, `orchestrations.json` to `cp/<self>/outbound/<peer>/`. We add a fifth artifact `fleet-env.json` with the same envelope shape:
  ```json
  { "anthropic_api_key": "...", "openai_api_key": "...", "version": 7 }
  ```
  Encryption uses the **per-peer fleet keypair** that's already part of the S3 dead-drop transport (Phase 9.7) — the parent encrypts to the peer's pubkey, the peer decrypts with its private half. No new crypto.

- **Consumer side (S3 daemon node):** the daemon's existing S3-reader loop adds a new path handler for `fleet-env.json`. On change-detected (version differs), same rewrite + restart sequence as Channel A.

- **Consumer side (S3 federation child CP):** the existing federation poller adds a new "topic" handler that maps to Section 3.

**Trigger on save:** `PUT /api/fleet-env` does (after the DB write):
1. Bump `version` and persist.
2. Update `cpLocalEnv` (so the CP's own jobs runtime immediately uses the new value — no restart of the CP itself).
3. Call the S3 publisher's existing publish-now hook to write `fleet-env.json` for every active S3 peer.
4. Tunnel/HTTPS consumers pick up the new value on their next heartbeat — typically within seconds, definitely within the next minute.

No daemon-side schema change. `/etc/okesu/jobs.env` already exists; the daemon just learns to rewrite it.

### 3. Federation propagation (CP → CP)

Mirrors how findings / daimons / orchestrations already federate. Two pieces, both small.

**Parent → child publish:**

- HTTPS-federated children: new `GET /api/v1/federation/fleet-env` endpoint on the parent (auth via `requireFederationToken`, same as existing `/api/v1/federation/*` exports). Returns `{ anthropic_api_key, openai_api_key, version }` from the parent's `fleet_env` row.

- S3-federated children: parent's federation S3 publisher writes `cp/<parent>/outbound/<child>/fleet-env.json` (same artifact Section 2 already adds — federation children are just one more peer category for the publisher).

**Child receive + republish:**

- The child CP's federation poller / S3 reader gains a new fleet-env topic handler. On received change:
  1. Stores keys in the **child's own** `fleet_env` row, sets `source = "federated_from_parent"`, sets `parent_cp_id = <parent's instance_id>`, bumps the child's `version`.
  2. Triggers Section 2's distribution automatically — the child's own publish path fans out to its nodes (HTTPS pull endpoint + S3 outbound). Transitive propagation falls out of the existing publish/pull plumbing.

**Inheritance + override semantics:**

- The Settings UI on a federated child shows an "Inherited from parent CP `<name>`" badge above the key fields, with the input fields disabled (read-only).
- An "Override locally" button switches `source` to `"local"`, enables the input fields, and stops the federation poller from overwriting them. Operator-set values win.
- A "Revert to parent" button restores `source = "federated_from_parent"`; the next federation poll picks the parent's current value back up.
- The federation poller respects the source flag: it only updates the row when `source = "federated_from_parent"` (or row is empty).

**Audit trail:** every save / federated-update writes an `audit_log` entry. Action keys: `fleet_env.set` (operator-driven), `fleet_env.federated_update` (parent-driven), `fleet_env.override_local`, `fleet_env.revert_to_parent`. Actor field: operator email or `federation:<parent_cp_instance_id>`. Existing `audit_log` table; no schema change.

## Data flow summary

```
Operator types key into Settings → Save
           │
           ▼
  PUT /api/fleet-env   (CP-local DB write, version bump, cpLocalEnv update)
           │
           ├─► trigger S3 publisher → cp/<self>/outbound/<peer>/fleet-env.json (per peer)
           │       │
           │       ├─► S3 daemon node reader picks up → rewrite /etc/okesu/jobs.env → systemctl restart okesu-jobs
           │       └─► S3 federated child CP reader picks up → updates child's fleet_env (source=federated) → child's own publish chain repeats
           │
           └─► daemon's existing heartbeat polls GET /api/v1/fleet/env → version differs → rewrite /etc/okesu/jobs.env → systemctl restart okesu-jobs
                   │
                   └─► HTTPS-federated child CP also polls GET /api/v1/federation/fleet-env on its existing federation tick → updates child's fleet_env → child's publish chain repeats
```

## Wire shape additions

`web/src/api.ts`:

```ts
export interface FleetEnvSummary {
  anthropic_set: boolean;
  anthropic_last4: string;
  openai_set: boolean;
  openai_last4: string;
  version: number;
  source: 'local' | 'federated_from_parent';
  parent_cp_id: string | null;
  parent_cp_display_name?: string;   // resolved server-side from federation_peers when source != local
  updated_at: string;
  updated_by_user_email: string | null;
}

export interface FleetEnvPatch {
  anthropic_api_key?: string;   // omit = no-op; "" = delete; non-empty = update
  openai_api_key?: string;
}
```

API helpers: `api.fleetEnv()`, `api.fleetEnvUpdate(patch)`, `api.fleetEnvOverrideLocal()`, `api.fleetEnvRevertToParent()`.

## Out of scope (deferred)

- **Per-node key overrides.** Some operators eventually want a different key per node (e.g., a separate Anthropic project per environment). Out of scope for v1 — fleet-wide single pair only.
- **Per-agent key overrides.** Same reasoning.
- **Other LLM providers.** Just Anthropic + OpenAI for v1. Adding Google / Cohere / etc. is a follow-up that drops two more columns.
- **Key validation.** v1 stores whatever the operator types. Doing a `models.list` API ping to verify the key works on save is a follow-up — useful but not load-bearing.
- **Usage tracking / quotas.** v1 is just storage and distribution. Tracking which agent burned how much is a separate concern.
- **Automatic rotation / scheduling.** v1 is operator-driven save. Time-based auto-rotation is a follow-up.

## Out of scope (cut)

- **Plaintext-key returns from `GET /api/fleet-env`.** The Settings UI never sees plaintext keys; only `*_set` + `*_last4`. Operators who need to retrieve the key copy it from their LLM provider's dashboard or use the daemon's `/etc/okesu/jobs.env`. Reduces blast radius if a session token is exfiltrated.

## Testing

### Backend

- **DB layer**: insert/get/update on `fleet_env`; encryption seal/unseal round-trip; version monotonicity (every update bumps).
- **Backwards-compat seed**: empty row + env vars → seed populates DB; non-empty row + env vars → DB wins.
- **API**:
  - `GET /api/fleet-env` returns masked summary, never plaintext.
  - `PUT /api/fleet-env` accepts partial body; empty string deletes; non-empty updates; missing keys leave fields unchanged; version bumps on any change; audit_log entry written.
  - `GET /api/v1/fleet/env` returns plaintext only with valid daemon mTLS cert (existing test infra for mTLS routes).
  - `GET /api/v1/federation/fleet-env` returns plaintext only with valid federation token.
- **S3 publisher**: writing the new `fleet-env.json` artifact + per-peer encryption round-trip.
- **Federation poller**: receiving a federated update, transitions `source = "federated_from_parent"`; respects local override (no overwrite when `source = "local"`).

### Daemon-side

- Heartbeat loop's new step: cached version vs returned version; rewrite + restart only on change; quiet when unchanged.
- Atomic file write (temp + rename), mode 0600.
- `systemctl restart` failure path logs warning, continues.

### Frontend

- `npm run build` clean.
- Lab smoke (post-merge):
  - Set both keys via Settings on a standalone CP. Verify `/etc/okesu/jobs.env` on a connected node updates within one heartbeat cycle.
  - Save on a parent CP federated to a child via HTTPS. Verify child's Settings page shows "Inherited from parent" within one federation poll, and child's nodes update within one heartbeat after that.
  - Same flow with an S3-federated child.
  - Override locally on a child; save a different key; parent updates; verify child's value doesn't change.
  - Revert child to parent; verify it picks up parent's current value.

## Risks

- **Plaintext key on the wire.** `GET /api/v1/fleet/env` returns plaintext over mTLS — the channel is encrypted but the daemon process holds the key in memory + writes it to `/etc/okesu/jobs.env` mode 0600. This matches today's operational posture (the same key is in env vars + jobs.env now). Mitigation: the file is mode 0600 and only readable by the okesu user; the API endpoint requires daemon mTLS.
- **`systemctl restart` interrupting in-flight jobs.** Restarting `okesu-jobs.service` during an active agent run kills the run. The daemon should gate the restart on no-active-jobs (or restart anyway and let the runner retry — it already has retry logic for transient failures). v1 takes the simpler path of restarting unconditionally; if this proves disruptive, defer to "drain then restart" in a follow-up.
- **Federation cycle on misconfiguration.** If two CPs federate to each other and both try to push fleet_env, you'd get an oscillation. The `source` flag prevents this: a CP whose `source = "local"` ignores incoming federation updates. Operators who genuinely want two-way are set up wrong.
- **Key leak via audit_log.** The `audit_log` writes the action and actor — never the key value. The existing audit_log code already excludes secret payloads; the new action keys follow the same convention.

## Open follow-ups (post-v1)

- Provider-specific validation on save (`anthropic.models.list` / `openai.models.list` ping).
- Per-node / per-agent key overrides.
- Other LLM providers.
- Usage tracking + quota.
- Time-based auto-rotation.
- Settings → "Test current keys" button that runs the validation ping on demand.
