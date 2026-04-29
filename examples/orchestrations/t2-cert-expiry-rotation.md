---
name: t2-cert-expiry-rotation
description: Tier-2 cert lifecycle automation. Runs daily, sweeps every node's mTLS + webhook + jobs runtime certs, auto-rotates anything expiring in <30 days on lab/dev hosts, and pauses for operator approval before rotating production certs.

trigger:
  on: cron
  cron: "0 4 * * *"   # 04:00 UTC daily

inputs:
  rotate_threshold_days:
    type: int
    required: false
    default: 30
  prod_label:
    type: string
    required: false
    default: "production"

defaults:
  timeout: 15m

steps:
  # 1. Fan-out audit: every node reports its cert expiry windows.
  #    Read-only; no rotation here. continue_on_error so a single
  #    unreachable host doesn't kill the sweep.
  - id: audit
    agent: investigator
    nodes:
      - threat-rocky-1
      - threat-rocky-2
      - threat-fedora-1
      - threat-fedora-2
      - threat-debian-1
      - threat-debian-2
      - edr-rocky-1
      - edr-rocky-2
      - edr-fedora-1
      - edr-fedora-2
      - edr-fedora-3
      - edr-debian-1
      - edr-debian-2
      - edr-debian-3
      - edr-debian-4
      - fim-debian-1
      - fim-rocky-1
      - fim-rocky-2
      - fim-fedora-1
      - fim-fedora-2
      - sre-debian-1
      - sre-debian-2
      - sre-rocky-1
      - sre-rocky-2
      - sre-fedora-1
      - sre-fedora-2
      - sre-fedora-3
      - mixed-east-1
      - mixed-west-1
      - mixed-west-2
    continue_on_error: true
    timeout: 5m
    prompt: |
      Audit cert expiry on this host.

      Inspect (use openssl when possible):
        - /etc/okesu/*-mgmt-certs/client.crt — daimon mgmt-plane cert
        - /etc/okesu/node-certs/client.crt   — jobs / tunnel runtime cert
        - /etc/ssl/certs/* and /etc/letsencrypt/live/*/cert.pem — webhook cert if present

      For each cert: extract `notAfter`, compute days_until_expiry.

      Read /etc/okesu/labels for any `production=true` or `env=prod` line so the
      heatmap can flag prod hosts.

      Emit an orchestration_result finding with attributes:
        is_production (bool)
        certs (array of {path: string, days: int, subject: string})
        soonest_days (int)

  # 2. Build the rotation plan from the audit. Hosts with
  #    soonest_days < threshold are candidates; production hosts go
  #    behind the gate, lab hosts auto-rotate.
  - id: plan
    agent: investigator
    timeout: 3m
    prompt: |
      Build a rotation plan from the audit.

      Per-host: {{audit.byNode | json}}
      Threshold: {{trigger.rotate_threshold_days}} days

      Partition into:
        auto_rotate: lab/dev hosts (is_production=false) with soonest_days < threshold
        gated_rotate: production hosts with soonest_days < threshold
        skip:        soonest_days >= threshold

      For each rotation entry, list the cert paths needing renewal.

      Emit an orchestration_result finding with attributes:
        auto_rotate (array of {host: string, paths: array of string, days: int})
        gated_rotate (array of {host: string, paths: array of string, days: int})
        skip_count (int)

  # 3. Auto-rotate the lab/dev set. The CP issues a fresh
  #    node-cert (same flow the manual install endpoint uses).
  - id: rotate_lab
    when: "{{plan.result.auto_rotate | length > 0}}"
    agent: investigator
    timeout: 8m
    prompt: |
      Rotate certs on the auto-tier hosts.

      Targets: {{plan.result.auto_rotate | json}}

      For each entry:
        1. Call the CP's POST /api/nodes/{id}/issue-cert endpoint
           (auth via the orchestration's CP-side runner — same path
           the deploy flow uses).
        2. Drop the new cert + key into the recorded paths
           (atomic mv; preserve permissions).
        3. Reload the daimon: `systemctl reload okesu-agent-<name>` (or flavour equiv).
        4. Verify mgmt-plane connectivity within 60s of reload.

      Emit an orchestration_result finding with attributes:
        rotated_hosts (array of string)
        failed_hosts (array of string)
        elapsed_seconds (int)

  # 4. Operator-gated production rotation. Same procedure, just
  #    waits for the human "go". The brief gives the operator
  #    everything they need to approve confidently.
  - id: gate_prod
    approval: required
    when: "{{plan.result.gated_rotate | length > 0}}"
    agent: incident-responder
    timeout: 5m
    prompt: |
      Production cert rotation needs approval.

      Targets: {{plan.result.gated_rotate | json}}

      Write a one-page brief covering:
        - Window: pick a low-traffic 30m window in the next 24h based on the
          nodes' previously-recorded peak hours (read /var/log/okesu/access patterns
          if available; otherwise default to 02:00-02:30 host-local).
        - Rollback: restore from the .previous backup the rotation script
          will leave behind. Estimate restore time per host.
        - Blast radius: which services on each host depend on the cert.

      Emit an orchestration_result finding with attributes:
        proposed_window_utc (string — RFC3339)
        per_host_plan (array of {host: string, services: array of string, downtime_estimate_s: int})
        rollback_steps (array of string)

  # 5. Production rotation, post-approval.
  - id: rotate_prod
    when: "{{plan.result.gated_rotate | length > 0}}"
    agent: investigator
    timeout: 30m
    prompt: |
      Execute the prod rotation plan.

      Plan: {{gate_prod.result.per_host_plan | json}}
      Window: {{gate_prod.result.proposed_window_utc}}

      For each host:
        1. Pre-snapshot existing cert dir
        2. Reissue + drop new cert (same procedure as rotate_lab)
        3. Reload service
        4. Verify mTLS connectivity restored
        5. If verify fails within 90s: roll back from snapshot, mark host failed

      Emit an orchestration_result finding with attributes:
        rotated_hosts (array of string)
        rolled_back_hosts (array of string)
        elapsed_seconds (int)
        operator_summary (string — one paragraph for the audit log)
---

# Notes

Cron daily at 04:00 UTC. Lab hosts auto-rotate within their normal
maintenance window; production hosts wait for an explicit operator
approve, with a brief that includes timing + rollback so the gate
isn't a guessing game.

Hosts with no `is_production` label default to lab/auto behaviour.
The convention is to drop a `/etc/okesu/labels` file at provisioning
time with `production=true` for prod, anything else for non-prod.
