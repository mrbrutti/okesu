# Orchestration library — T1/T2 operator support

These orchestrations exist so a human operator only sees the items
that genuinely need their attention. Tier 1 (T1) auto-resolves;
Tier 2 (T2) auto-investigates and pauses for an approval gate
before doing anything destructive.

Install one with the API:
```
curl -sk -b cookies.txt -X POST https://localhost:7443/api/orchestrations \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --arg yaml "$(cat t1-finding-autotriage.md)" '{spec_yaml: $yaml}')"
```

Or paste it into the orchestration editor in the UI.

## Tier 1 — autonomous (no approval gate)

| Orchestration | Trigger | What it does |
|---|---|---|
| **t1-finding-autotriage** | every new finding (≠INFO) | classifies noise vs confirmed; suppresses noise; writes a brief for confirmed HIGH/CRITICAL |
| **t1-disk-pressure-cleanup** | finding "disk … free … MEDIUM+" | scopes pressure, runs whitelisted cleanup (rotated logs, journal, package cache, /tmp >7d), verifies recovery |
| **t1-stale-agent-restart** | daimon-stale finding | probes the unit, restarts via the right service manager, verifies the next heartbeat lands |
| **t1-failed-login-noise-dedup** | sshd auth-failure findings | classifies sources as scanner vs targeted; bundles scanner noise into a single SEV-3; escalates real attempts |
| **t1-fleet-health-sweep** | cron 07:00 UTC daily | aggregates fleet state via CP API; produces the on-call's morning brief |

T1 orchestrations are **silent on success** — the operator only hears
when the auto-handler couldn't resolve the issue.

## Tier 2 — approval-gated, deeper

| Orchestration | Trigger | What it does |
|---|---|---|
| **edr-critical-response** | EDR finding HIGH/CRITICAL | triage → analyze binary → hunt fleet → gated containment plan |
| **t2-fleet-ioc-hunt** | any finding carrying an IOC | scopes the hunt, fans out to the relevant population, builds heatmap, gated quarantine |
| **t2-cert-expiry-rotation** | cron 04:00 UTC daily | audits cert expiry across the fleet; auto-rotates lab/dev; gated rotation for production |
| **t2-drift-remediation** | FIM finding (instance-integrity) | attributes the change to a source; checks lateral spread; gated revert with forensics capture |

T2 orchestrations always finish with a one-page brief that gives
the operator everything they need to approve or decline confidently.

## Conventions used across this library

- **`continue_on_error: true`** on multi-host fan-outs — a single
  unreachable host shouldn't kill the chain.
- **`emit an orchestration_result finding`** — every step's brief
  is a structured finding so downstream steps + the run-detail UI
  can render it cleanly via `StructuredView`.
- **Operator-readable briefs** — every step that escalates ends with
  a markdown / paragraph the operator can read straight from the
  run-detail panel without context switching.
- **Whitelist-only auto-actions** — cleanup, rotation, revert all
  enumerate exactly what they touch. No "rm -rf, hope for the best".
- **Tag-based escape hatches** — hosts can opt out of automation by
  dropping tags into `/etc/okesu/labels` (e.g. `noremediate=disk`,
  `noisolate=yes`, `production=true`).

## Adding a new orchestration

1. Copy the closest existing example as a starting template.
2. Pick the trigger type: `manual`, `finding` (with a filter), or `cron`.
3. Decide T1 vs T2 — T1 has no `approval: required` step; T2 has
   exactly one before any destructive action.
4. Each step that emits structured data should ask the agent to
   "emit an orchestration_result finding with attributes: …" so
   downstream steps + the UI can render it.
5. Test manually with the inputs block; once stable, enable the
   trigger in the orchestrator UI.
