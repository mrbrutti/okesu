---
name: t1-failed-login-noise-dedup
description: Tier-1 dedup of authentication-failure noise. Distinguishes brute-force attempts from internet background radiation (vuln scanners, mass DDoS sweeps), bundles repeats into a single SEV-3 incident, and only escalates when the same source actually authenticated successfully or hit a sensitive account.

trigger:
  on: finding
  filter: "finding.title contains 'auth' && (finding.title contains 'fail' || finding.title contains 'invalid')"

inputs:
  host:
    type: string
    required: false
    default: ""
  finding_id:
    type: int
    required: false
    default: 0

defaults:
  timeout: 4m

steps:
  # 1. Source attribution — collect the source IPs across recent
  #    failures, classify each, and decide if any pattern looks like
  #    real brute force vs background scanner traffic.
  - id: triage
    agent: investigator
    node: "{{trigger.host}}"
    prompt: |
      Triage failed-login finding #{{trigger.finding_id}} on {{trigger.host}}.

      Gather:
        - `journalctl -u sshd --since "60 minutes ago" | grep -iE "failed|invalid"` (or /var/log/auth.log)
        - Aggregate by source IP: count, first seen, last seen, account targeted
        - Look up each source IP: is it on a known threat-intel feed?
          (Use only what's locally available — no external lookups.)
        - Check for any SUCCESSFUL login from those source IPs in the same window:
          `journalctl -u sshd --since "60 minutes ago" | grep -i "accepted"`

      Classify each source:
        - `scanner` — high-volume, low-effort, hits common usernames (root, admin, oracle), no success
        - `targeted` — focused on a specific real account, slower cadence, sometimes succeeds
        - `unknown` — needs human eyes

      Verdict:
        - severity: one of `noise` | `elevated` | `incident`
          - noise:     all sources are scanners, no successful logins
          - elevated:  at least one targeted source, no success
          - incident:  any successful login from a flagged source
        - sources_count, attempts_total, accounts_hit (array)

      Emit an orchestration_result finding with attributes:
        severity (string)
        sources_count (int)
        attempts_total (int)
        accounts_hit (array of strings)
        has_successful_login (bool)
        worst_source (string — IP)
        rollup_window (string — e.g. "60m")

  # 2. Auto-bundle scanner noise. Writes a single dedup'd finding
  #    in place of N raw ones, suppresses the rest of the bundle
  #    for 24h via local fail2ban-style hosts.deny entry.
  - id: deduplicate
    when: "{{triage.result.severity == 'noise'}}"
    agent: investigator
    node: "{{trigger.host}}"
    timeout: 3m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Bundle the noise on {{trigger.host}}.

      The triage classified the {{triage.result.sources_count}} sources as
      scanners. Apply automatic mitigation:
        - If iptables/nftables present and a "okesu-scanners" chain exists,
          add the source IPs to it with a 24h timeout
        - Otherwise, append to /etc/hosts.deny for the next 24h with comment
          `# okesu T1 scanner-noise YYYY-MM-DD`
        - Roll the {{triage.result.attempts_total}} raw findings into one
          summary finding tagged `noise-bundled`

      Emit an orchestration_result finding with attributes:
        ips_blocked (int)
        block_method (string: iptables|nftables|hosts.deny|none)
        bundle_id (string)
        actions (array)

      Actions to request on the source finding #{{trigger.finding_id}}:
        update_finding_status → false_positive
          (reason: "scanner noise; bundled into <bundle_id>")
        set_finding_severity_override → INFO
        add_finding_tag → auto-triaged-noise
        add_finding_tag → noise-bundled
        link_run_to_finding

  # 3. Escalate when something targeted the host. The on-call sees
  #    the brief inline rather than digging through 200 raw findings.
  - id: escalate_brief
    when: "{{triage.result.severity != 'noise'}}"
    agent: incident-responder
    node: "{{trigger.host}}"
    timeout: 4m
    prompt: |
      Write an on-call brief for the auth-failure incident on {{trigger.host}}.

      Triage said:
        severity={{triage.result.severity}}
        sources={{triage.result.sources_count}}
        attempts={{triage.result.attempts_total}}
        accounts={{triage.result.accounts_hit}}
        had_success={{triage.result.has_successful_login}}
        worst_source={{triage.result.worst_source}}

      Cover:
        - One-paragraph timeline of what happened
        - Whether containment is needed NOW or can wait until business hours
        - Recommended actions, ordered by impact
          (key rotation? fail2ban? service-account audit? IR playbook?)

      Keep it ≤200 words. Emit an orchestration_result finding with attributes:
        urgency (string: low|medium|high)
        recommended_actions (array of strings)
        suggested_severity (string: SEV-2|SEV-3|SEV-4)
---

# Notes

A reasonably-exposed SSH host gets thousands of failed logins per
day from internet noise. Without this orchestration, the operator
sees N raw findings; with it, they see either zero (auto-bundled)
or one (escalated brief).

Containment actions intentionally stop short of operating-system
isolation — that's a Tier-2 decision. T1 here just absorbs the
firehose and surfaces the actually-interesting subset.
