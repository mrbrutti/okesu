## Summary

Operators now manage the fleet-wide Anthropic + OpenAI API keys from
Settings → LLM Keys, instead of relying on env vars + CP restarts.
Keys are persisted in the DB (encrypted), exposed via Settings UI,
and automatically distributed to every connected node + every
federated child CP across all transports (tunnel, poll, S3 dead-drop).

- New singleton `fleet_env` table (migration 045), AES-GCM sealed
  via the existing master-key pattern with a separate HKDF info
  string (`okesu-fleet-env-v1`) for domain separation.
- New endpoints:
  - Operator: `GET/PUT /api/fleet-env`,
    `POST /api/fleet-env/{override-local,revert-to-parent}`.
  - Daemon (mTLS): `GET /api/v1/fleet/env`.
  - Federation (token): `GET /api/v1/federation/fleet-env`.
- New S3 publisher artifact `fleet-env.json` alongside the existing
  `findings.json` / `daimons.json` / etc. The bucket itself is the
  trust boundary (operator-controlled access keys); no per-artifact
  encryption.
- Daemon's existing heartbeat loop gains a fleet-env poll: rewrites
  `/etc/okesu/jobs.env` atomically (temp + rename, mode 0600) on
  version change and runs `systemctl restart okesu-jobs.service`.
- Federation poller gains a fleet-env path: after each successful
  introspect against a peer (HTTPS poller) or successful introspect
  fetch from a peer's bucket prefix (S3 reader), pulls the peer's
  fleet-env and applies via `Store.SetFleetEnvFromFederation`. Idempotent
  via the parent-version comparison; child stores keys with
  `source = "federated_from_parent"` and republishes through its
  own channels (transitive propagation).
- Settings UI: new `LLMKeys.tsx` page with masked-input fields,
  per-key Clear, override-local / revert-to-parent buttons, last4
  display, version + audit indicator.
- Backwards compat:
  - Existing `OKESU_CP_FLEET_ANTHROPIC_API_KEY` /
    `OKESU_CP_FLEET_OPENAI_API_KEY` env vars seed the DB on first
    boot if the row is empty.
  - A default-state row (`source=local`, `version=0`) is treated as
    "empty" — federation may seed it without operator action,
    matching the spec's "row is empty" carve-out.

Build matrix unchanged: `CGO_ENABLED=0` everywhere.

## Test plan

Unit tests landed in this PR:
- `controlplane/db/fleet_env_test.go` — encryption round-trip,
  version monotonicity, federation seed semantics (operator-set
  values not overridden, fresh row gets seeded, older versions
  skipped).
- `controlplane/api/fleet_env_test.go` — handler surface, audit
  logging, override/revert.
- `controlplane/federation/poller_test.go` — fleet-env apply on
  successful poll, idempotent re-poll, source='local' lockout,
  empty-upstream skip, HTTP 404 tolerated.
- `agent/jobs_env_test.go` — atomic rewrite of `/etc/okesu/jobs.env`,
  partial keys, empty case, replacement of an existing file.

Lab smoke (post-merge):

- [ ] `go test ./...` passes
- [ ] `npm run build` clean in `web/`
- [ ] On a standalone CP, set both keys via Settings → LLM Keys.
      Verify a connected node's `/etc/okesu/jobs.env` updates within
      one heartbeat cycle and `okesu-jobs.service` restarts.
- [ ] Save on a parent CP federated to a child via HTTPS. Verify
      child's Settings page shows "Inherited from parent" within
      one federation poll cycle, and child's connected nodes update
      within one heartbeat after that.
- [ ] Same flow with an S3-federated child.
- [ ] On a federated child, click "Override locally" and save a
      different key. Verify parent's subsequent updates do NOT
      change the child's key.
- [ ] Click "Revert to parent" on the child. Verify the child picks
      up the parent's current value on the next federation poll.
- [ ] Clear a key (empty string via the UI). Verify the daemon
      receives an empty value on next poll and `/etc/okesu/jobs.env`
      no longer contains that line.

## Files

- New table: `fleet_env` (migration 045).
- DB layer: `controlplane/db/fleet_env.go` (+ test).
- Operator API: `controlplane/api/fleet_env.go` (+ test).
- Daemon / federation API: `controlplane/api/fleet_env_daemon.go`.
- Server wiring: `controlplane/server.go` (env-seed, route mounts,
  asset publisher, fleetEnvExtras closure).
- Auto-deploy refactor: `controlplane/api/auto_deploy.go` reads
  fresh keys from the DB on each deploy.
- Federation poller: `controlplane/federation/poller.go` (HTTPS) +
  `controlplane/federation/s3reader/reader.go` (S3 dead-drop) (+ tests).
- Daemon poll: `agent/mgmt.go` `StartFleetEnvPoll` +
  `agent/jobs_env.go` atomic write helper (+ tests).
- Frontend: `web/src/api.ts` types + helpers,
  `web/src/pages/settings/LLMKeys.tsx`,
  `web/src/pages/Settings.tsx` route + nav entry.
- Docs: `docs/architecture.md` "Fleet LLM API keys" section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-fleet-llm-keys-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-fleet-llm-keys.md`

## Open follow-ups

- Provider-specific validation on save
  (`anthropic.models.list` / `openai.models.list` ping).
- Per-node / per-agent key overrides.
- Other LLM providers (Google / Cohere / etc.).
- Usage tracking + quota.
- Time-based auto-rotation.
- Drain-then-restart `okesu-jobs.service` instead of unconditional
  restart (currently kills in-flight jobs).
- Per-artifact encryption for `fleet-env.json` in the S3 publisher
  (today the bucket ACL is the trust boundary).
