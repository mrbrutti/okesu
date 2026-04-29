---
name: t1-finding-batch-triage
description: Tier-1 batched auto-triage. Every 5 min, pulls up to 50 untagged INFO/LOW opens from the operator queue and classifies them in a single investigator run. Replaces the per-finding fast-lane for low-severity noise — one Claude call covers a whole batch instead of 50 separate calls saturating the API. Per-finding `t1-finding-autotriage` still handles HIGH/CRITICAL where responsiveness beats efficiency.

trigger:
  on: cron
  cron: "*/5 * * * *"   # every 5 minutes

defaults:
  timeout: 5m

steps:
  # 1. Classify every untagged INFO/LOW finding currently in the
  #    operator queue. The engine resolves the `data:` block
  #    server-side and binds the result into the prompt as
  #    {{data.findings}} + {{data.summary}} — no curl-and-parse.
  - id: classify_batch
    agent: investigator
    timeout: 4m
    data:
      findings:
        query: findings.list
        params:
          state: queue
          severity: [INFO, LOW]
          limit: 50
      summary:
        query: findings.summary
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Batch-classify {{data.findings | length}} untagged INFO/LOW
      findings from the operator queue.

      Findings:
      {{data.findings | json}}

      Cluster context (per-CP rollup):
      {{data.summary | json}}

      For each finding, decide one verdict:
        - `noise`     — would not be worth a human's 2 minutes
        - `confirmed` — real but not urgent (stays at LOW for an
                       operator to pick up later)
        - `unknown`   — evidence is ambiguous; tag for human review

      Heuristics for `noise` (tighten as needed):
        - The same dedup_key has fired ≥3× in the last 60 min on the
          same host (cross-reference {{data.summary}}).
        - Source/title matches a known scanner / monitor /
          background-job pattern (e.g. "scanner", "robotic",
          "background", "test fixture", "monitoring", "healthcheck").
        - The change recorded matches a sanctioned automation
          (ansible run id, package manager update, systemd timer).
        - Self-reported finding from the agent that itself deployed
          (collector seeing its own writes).

      Build ONE actions array covering every finding in the batch.
      Each action must carry the right `finding_id` for that
      candidate. The engine validates each action against this
      step's allowlist and applies them in order.

      Per finding, request:
        verdict=noise:
          update_finding_status        → false_positive (reason: short)
          set_finding_severity_override → INFO
          add_finding_tag              → auto-triaged-noise
          link_run_to_finding
        verdict=confirmed:
          add_finding_tag              → auto-confirmed
          link_run_to_finding
        verdict=unknown:
          add_finding_tag              → needs-human
          link_run_to_finding

      Emit ONE orchestration_result finding (one-line JSON, no code
      block) with attributes:
        scanned    (int — total findings in {{data.findings}})
        noise      (int — count classified as noise)
        confirmed  (int)
        unknown    (int)
        actions    (array — every action across the whole batch)

      The full action protocol is at agents/_orchestration-actions.md.
---

# Notes

This orchestration replaces the per-finding fast-lane for low-severity
findings. Reasoning:

- The per-finding `t1-finding-autotriage` was firing 700+ runs in 12h
  during a noise burst and saturating the single Anthropic API key.
  Each run paid full LLM startup cost to classify one finding.
- Batched, one investigator call covers up to 50 findings — same
  reasoning quality, ~50× less API churn.
- HIGH/CRITICAL still goes through `t1-finding-autotriage` (its
  trigger filter is now `severity in ('HIGH','CRITICAL')`). For those,
  responsiveness beats efficiency.

The 5-minute cadence is the floor that still feels live to operators
opening the Findings page — anything slower and the queue visibly
piles up between sweeps. If load grows, drop to `*/2 * * * *` first
(within concurrency cap), then add severity-tier batches.

The `data:` block resolves server-side: the engine fetches the
matching findings + summary directly from the store, binds them as
`{{data.findings}}` / `{{data.summary}}` in the prompt, and persists
the snapshot on the step record so the run can be replayed against
the same input even after the underlying tables have moved on.
