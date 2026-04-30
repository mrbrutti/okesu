---
name: t1-stuck-finding-sweep
description: Tier-1 reconciliation sweep. Every 6h, finds open findings that look like the auto-triage's noise verdict missed (same dedup_key fired N times, no auto-* tag yet, classify-orchestration didn't reach them) and applies the appropriate close/tag. Backstop for the per-finding T1 — the per-finding chain handles the fast path; this catches anything that fell through.

trigger:
  on: cron
  cron: "0 */6 * * *"   # every 6h on the hour

defaults:
  timeout: 6m

steps:
  # 1. Build the candidate list. Read-only — engine resolves the
  #    `data:` block server-side so the agent gets structured input
  #    without curl-and-parse plumbing.
  - id: scan
    agent: investigator
    timeout: 4m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    data:
      opens:
        query: findings.list
        params:
          state: open
          limit: 500
      summary:
        query: findings.summary
    prompt: |
      Build the list of "stuck" open findings — opens that the
      Tier-0 dedup closure + per-finding T1 should have closed but
      haven't.

      Open findings ({{data.opens | length}}):
      {{data.opens | json}}

      Cluster context:
      {{data.summary | json}}

      Identify candidates:
        - Same dedup_key has occurred ≥3 times in the last 24h
          (the dedup-closure runs only on new finding ingest, so
          older opens with the same key get superseded automatically;
          this catches gaps where the dedup_key was empty or where
          the projection happened before the dedup feature was on)
        - No `auto-*` tag set
        - Severity is INFO or LOW (anything HIGH+ stays in the
          operator queue regardless of pattern noise)
        - Title matches a known-noisy substring set:
            "scanner", "robotic", "background", "test fixture"
            (operators can extend by editing this orchestration's
            spec — the list is authoritative here)

      Decide per-candidate:
        - close (yes/no): close when severity is INFO/LOW AND
          (count >=3 OR title matches noise list)
        - reason: short justification

      Emit an orchestration_result finding with attributes:
        candidates (array of {finding_id: int, count: int,
                              dedup_key: string, severity: string,
                              close: bool, reason: string}),
        scanned_total (int),
        actionable (int — count of candidates with close=true).

  # 2. Apply closures via the action protocol. Engine validates each
  #    against the step's allowlist; rejections are logged.
  - id: apply
    when: "{{scan.result.actionable > 0}}"
    agent: investigator
    timeout: 3m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Apply the closures the scan step identified.

      Candidates: {{scan.result.candidates | json}}

      For every candidate where `close == true`:
        - update_finding_status → false_positive
          (reason: candidate.reason — short, ≤120 chars)
        - set_finding_severity_override → INFO
        - add_finding_tag → auto-triaged-stuck
        - link_run_to_finding

      For every candidate where `close == false`:
        - add_finding_tag → reviewed-by-sweep
          (reason: "<count>× repeats but kept open: <reason>")
        - link_run_to_finding

      Emit an orchestration_result finding with attributes:
        closed_count (int), reviewed_count (int),
        actions (array — see above).
---

# Notes

This orchestration is the *backstop*, not the *primary*. The primary
ways findings auto-close are:

1. **Tier-0 (engine, deterministic)** — when a new finding lands
   with the same `dedup_key` as an existing open, the older one
   gets `superseded` immediately during projection. Free, fast.
2. **Tier-1 per-finding** — `t1-finding-autotriage` runs on every
   new non-INFO finding and applies its noise / confirmed verdict.

This sweep handles the corner cases:
  - Findings ingested before Tier-0 was on (legacy data).
  - Findings whose dedup_key was empty so Tier-0 had no key to
    cluster on.
  - Per-finding classify runs that failed silently (rare since the
    synthesiser fallback, but possible).

Cron 6h cadence — operators don't want morning briefs surprising
them; the sweep runs while they sleep, the operator queue is a
little smaller in the morning, audit trail attributes the closures
to this run.
