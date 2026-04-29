---
name: t1-fleet-health-sweep
description: Tier-1 morning briefing. Cron-driven sweep of the fleet that produces a single executive finding summarising stale daimons, recently-failed deploys, finding clusters, and any host that looks worth a closer look. The on-call opens the dashboard already knowing what to investigate.

trigger:
  on: cron
  cron: "0 7 * * *"   # 07:00 UTC — before US business hours

defaults:
  timeout: 8m

steps:
  # 1. Aggregate the fleet state. Read-only sweep against the CP's
  #    own API; no per-host work yet, just summarisation of what the
  #    CP already knows.
  - id: aggregate
    agent: investigator
    timeout: 5m
    prompt: |
      Build the morning fleet briefing.

      You're running on the CP host. The CP exposes its own URL +
      admin creds via env so you can query the API:
        $OKESU_CP_URL              e.g. https://localhost:8443
        $OKESU_CP_ADMIN_EMAIL      e.g. admin@local
        $OKESU_CP_ADMIN_PASSWORD

      Login once, then fetch:
        curl -sk -c /tmp/cp.cookies -X POST "$OKESU_CP_URL/api/auth/login" \
          -H 'Content-Type: application/json' \
          -d "{\"email\":\"$OKESU_CP_ADMIN_EMAIL\",\"password\":\"$OKESU_CP_ADMIN_PASSWORD\"}" \
          > /dev/null
        curl -sk -b /tmp/cp.cookies "$OKESU_CP_URL/api/nodes"
        curl -sk -b /tmp/cp.cookies "$OKESU_CP_URL/api/findings?since=24h&limit=500"
        curl -sk -b /tmp/cp.cookies "$OKESU_CP_URL/api/runs?since=24h"
        curl -sk -b /tmp/cp.cookies "$OKESU_CP_URL/api/orchestration-runs?since=24h"

      Compute:
        - stale_daimons: hosts with jobs_runtime_seen_at older than 5×poll_interval
        - failed_deploys: nodes with status=failed
        - finding_clusters: groups of >=3 findings with identical title in last 24h —
          what host(s)? what severity?
        - top_offenders: hosts in the top decile for raw finding count
        - orchestration_failures: orchestration_runs with status=failed in last 24h
        - approval_backlog: orchestration_runs with status=approval_required older than 4h

      Emit an orchestration_result finding with attributes:
        stale_daimons (array of strings — hostnames)
        failed_deploys (array of strings)
        finding_clusters (array of {title: string, count: int, hosts: array of string})
        top_offenders (array of {host: string, finding_count: int})
        orchestration_failures (int)
        approval_backlog (int)
        anything_concerning (bool)

  # 2. Triage the concerning items. When the aggregate flagged
  #    anything_concerning, do a one-paragraph dive per category so
  #    the brief is actionable, not just a wall of numbers.
  - id: triage
    when: "{{aggregate.result.anything_concerning == true}}"
    agent: investigator
    timeout: 4m
    prompt: |
      Triage the concerning items from the aggregate.

      Aggregate: {{aggregate.result | json}}

      For each non-trivial category, produce a 2-3 sentence
      assessment:
        - stale_daimons: are these flapping or persistently down?
          Recommend whether t1-stale-agent-restart should auto-fire.
        - failed_deploys: are these all the same OS/region (likely a
          shared problem) or scattered (likely individual issues)?
        - finding_clusters: which clusters look like real signal vs
          noise the auto-triage hasn't caught yet?
        - approval_backlog: any items older than 24h (need re-routing
          or auto-cancel policy)?

      Emit an orchestration_result finding with attributes:
        stale_daimons_assessment (string)
        failed_deploys_assessment (string)
        cluster_assessments (array of {title: string, assessment: string, action: string})
        approval_backlog_assessment (string)
        priority_items (array of strings — top 3 things the on-call should look at)

  # 3. Compose the executive brief that lands in the on-call's
  #    inbox / chat. Markdown-formatted, ≤300 words. Even on quiet
  #    days the brief is sent so the operator knows the sweep ran.
  - id: brief
    agent: investigator
    timeout: 3m
    prompt: |
      Compose the morning fleet briefing.

      Aggregate: {{aggregate.result | json}}
      Triage (only set when concerning): {{triage.result | json}}

      Format as markdown, ≤300 words. Keep `aggregate.result.anything_concerning`
      in mind: when it's false, the body is short — TL;DR + Numbers + a
      two-line "no action items" + the overnight automation count.

      Sections:

      ## TL;DR
      One sentence. If everything is quiet, say so explicitly —
      "fleet healthy, no items requiring attention."

      ## Numbers
      - Hosts ready / deploying / failed / pending
      - Findings (last 24h, grouped by severity)
      - Orchestration runs (succeeded / failed / awaiting approval)

      ## Items requiring attention
      Bulleted list, ordered by urgency, each bullet ≤ 2 lines:
        - one-line title
        - hostname(s)
        - recommended action OR "auto-handled by <orchestration name>"
      If `aggregate.result.anything_concerning` is false, render this
      section as a single line: "None. Sweep clean."

      ## What ran overnight
      One-line mention of how many orchestration runs completed in the
      last 24h, broken down by outcome. Reassures the operator that
      automation is working without bloating the brief.

      Emit an orchestration_result finding with attributes:
        markdown (string — the brief itself)
        sentiment (string: quiet|elevated|concerning)
        action_count (int)
---

# Notes

Daily 07:00 UTC. The brief is the orchestration's own
`orchestration_result` finding — pin a notification rule on it and
the on-call gets the brief in their morning chat.

The orchestration handles its own load — the aggregate step uses
the CP API, not per-host work, so the fleet doesn't feel the
sweep. Triage only runs on concerning days, so the steady-state
cost is one Claude call per day.

A "no news is good news" delivery is intentional: the operator
should learn quickly that this fires reliably, otherwise the
orchestration gets ignored when it actually matters.
