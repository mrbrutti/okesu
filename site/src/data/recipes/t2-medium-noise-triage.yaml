---
name: t2-medium-noise-triage
description: Tier-2 batched triage for MEDIUM findings that already look like noise. Conservative rules — only auto-classifies findings whose recurrence_count is high (≥10) AND whose host carries a non-prod label (env=staging|dev|lab) OR whose category matches a known-noise pattern (config-drift on agent unconfigured, sre-health placeholder probes, log-self-detection). Real prod MEDIUM still reaches operators.

trigger:
  on: cron
  cron: "*/15 * * * *"   # every 15 minutes — slower than t1 because MEDIUM matters more

defaults:
  timeout: 5m

steps:
  # Pull the candidate batch — open MEDIUM findings, capped at 30 per
  # tick. The agent classifies and the engine applies the resulting
  # actions. Conservative cap so the agent has plenty of context-window
  # for each finding.
  - id: classify_batch
    agent: investigator
    timeout: 4m
    data:
      findings:
        query: findings.list
        params:
          state: queue
          severity: [MEDIUM]
          limit: 30
      summary:
        query: findings.summary
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Tier-2 noise triage on {{data.findings | length}} open MEDIUM
      findings. You are the LAST conservative pass before these reach
      a human operator.

      Findings:
      {{data.findings | json}}

      Cluster context:
      {{data.summary | json}}

      For each finding, pick ONE verdict — and DEFAULT to `keep` when
      uncertain. We accept noise on the operator queue; we do NOT
      accept silently dropped real signals.

      Verdicts:
        - `noise`     — confidently noise. SAFE to flip to false_positive.
        - `keep`      — anything else. Stays MEDIUM, no action needed.
        - `degrade`   — real, but not MEDIUM. Drops to LOW with reasoning.

      Heuristics for `noise` (ALL must be true):
        - recurrence_count >= 10 OR (Resource includes "host:" AND host
          appears in a known non-prod cluster — check {{data.summary}}
          for hostname patterns like dev/staging/lab/test).
        - At least one of:
          * Title/Resource matches "agent unconfigured" / "placeholder"
            / "example.com" — sre-health unconfigured probes.
          * Category=config AND status=open with no operator action in
            the last 24h (config drift the operator already saw and
            ignored).
          * Title/Resource matches "log file" AND Path contains
            "/var/log/okesu" — agent self-log detection (the agent
            tripped over its own writes).
          * Title contains "test fixture" / "scanner" / "monitoring" /
            "healthcheck" / "robotic" / "background".
        - NOT a CVE finding (CVE field non-empty → never noise here).
        - NOT IOC-attributed (IOCConfidence non-empty → never noise).
        - NOT on a host carrying label `criticality=prod` or
          `env=prod` — production hosts get every MEDIUM through.

      Heuristics for `degrade` (drop MEDIUM → LOW):
        - The finding is real but the host carries a "lab" / "staging"
          / "test" label that operators are watching less closely.
        - The agent acknowledges in evidence that the issue is
          "expected" / "scheduled" / "during a maintenance window."
        - The finding's process_name / path indicates a known dev
          tooling artifact (e.g. /tmp/build-*, debug-only binaries).

      Build ONE actions array covering every finding in the batch.
      Each action carries the right `finding_id`. Per finding:

        verdict=noise:
          update_finding_status        → false_positive (reason: ≤80 chars)
          set_finding_severity_override → INFO
          add_finding_tag              → auto-triaged-noise
          link_run_to_finding

        verdict=degrade:
          set_finding_severity_override → LOW
          add_finding_tag              → auto-degraded
          link_run_to_finding

        verdict=keep:
          (no action)

      Emit ONE orchestration_result finding (one-line JSON, no code
      block) with attributes:
        - batch_size: <int>
        - noise_count: <int>
        - degrade_count: <int>
        - keep_count: <int>
        - actions: <the actions array above>

      DO NOT emit per-finding diagnostic findings — the actions array
      IS the audit trail. Be terse.
---
