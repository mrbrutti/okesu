---
name: t2-ioc-enrichment-escalator
description: Tier-2 escalator. Fires when a vendor enrichment returns a high-confidence malicious verdict; promotes the IOC's findings to HIGH severity, opens an investigation, and emits a brief operator-readable summary. The trigger fires only on FRESH cache writes, so re-running enrichment within the TTL window doesn't re-fire this orchestration.
trigger:
  on: ioc_enriched
  filter: "enrichment.verdict in ['malicious', 'suspicious']"
inputs:
  ioc_id:
    type: int
    required: false
    default: 0
defaults:
  timeout: 5m
steps:
  # 1. Pull the IOC's full context (kind, value, observation history,
  #    cross-CP pattern membership) so the next steps have what they
  #    need to make a sound escalation decision.
  - id: pull_context
    agent: investigator
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      An enrichment returned a high-confidence malicious / suspicious
      verdict for an IOC.

      IOC id:        {{trigger.ioc_id}}
      IOC kind:      {{trigger.ioc_kind}}
      Value:         {{trigger.normalized_value}}
      Adapter:       {{trigger.adapter}}
      Verdict:       {{trigger.verdict}}
      Score:         {{trigger.score}}

      Pull (curl ${OKESU_CP_URL}/api/iocs/{{trigger.ioc_id}}) and
      summarize:
        - first_seen / last_seen
        - observation_count + distinct hosts
        - active findings referencing this IOC (via
          /api/iocs/{{trigger.ioc_id}}/observations)
        - whether the IOC is in any cross-CP pattern
          (/api/iocs/cross-cp-patterns)

      Emit an orchestration_result finding with attributes:
        ioc_summary (string), affected_findings (array of ints),
        host_count (int), in_cross_cp_pattern (bool).

  # 2. Promote each affected finding to HIGH severity (unless it's
  #    already CRITICAL) and tag it `ioc-enriched-malicious` so the
  #    Kanban board surfaces it immediately. The agent decides
  #    per-finding by the IOC's role in that finding's evidence.
  - id: escalate_findings
    when: "{{pull_context.result.affected_findings | length > 0}}"
    agent: investigator
    timeout: 3m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      For each finding in {{pull_context.result.affected_findings | json}},
      decide whether the new enrichment verdict warrants escalation.

      Verdict:       {{trigger.verdict}}
      Source:        {{trigger.adapter}} (score {{trigger.score}})
      IOC summary:   {{pull_context.result.ioc_summary}}

      Apply per-finding:
        - If finding.severity is INFO/LOW/MEDIUM and the IOC is the
          finding's primary evidence: set_finding_severity_override
          to HIGH (CRITICAL only when verdict=malicious AND the IOC
          is in a cross-CP pattern).
        - add_finding_tag: ioc-enriched-{{trigger.verdict}}
        - link_run_to_finding (so the case workspace shows this run).

      Skip findings already at CRITICAL — no need to ratchet up.

      Emit an orchestration_result finding with attributes:
        promoted (int), tagged (int), skipped (int).

  # 3. Open / upsert an investigation keyed by IOC id so the
  #    operator has a workspace to triage from. Uses the same
  #    by-dedup endpoint the cross-CP pattern investigator uses.
  - id: open_case
    when: "{{pull_context.result.affected_findings | length > 0}}"
    agent: investigator
    timeout: 1m
    actions:
      - link_run_to_finding
    prompt: |
      Open or look up the investigation for this IOC.

      curl -sX POST "${OKESU_CP_URL}/api/investigations/by-dedup" \
        -H 'Content-Type: application/json' \
        -d '{
              "external_key": "ioc-enriched:{{trigger.ioc_id}}",
              "title": "Malicious IOC enriched: {{trigger.ioc_kind}} {{trigger.normalized_value}}",
              "summary": "Auto-opened by t2-ioc-enrichment-escalator. Verdict: {{trigger.verdict}} ({{trigger.adapter}}, score {{trigger.score}}). Touched {{pull_context.result.host_count}} host(s).",
              "created_by": "t2-ioc-enrichment-escalator",
              "link_findings_by_ioc_id": {{trigger.ioc_id}}
            }'

      Emit an orchestration_result finding with attributes:
        investigation_id (int), created (bool).
---

# Notes

This orchestration closes the "enrichment is a cache, not a signal"
gap in the IOC pipeline. Without it, a vendor returning "malicious"
for an IOC referenced by 5 findings would just fill the cache; an
operator wouldn't know unless they navigated to the IOC detail.

## Wiring

- `trigger.on: ioc_enriched` — fires once per fresh cache write.
  Cache hits within the TTL window do NOT re-fire (see
  `enrichment.EnrichedEvent` doc).
- `trigger.filter` narrows to `malicious` / `suspicious`. A
  `clean` verdict is informational and doesn't warrant escalation.

## Loop guard

A run triggered by enrichment that itself called the `enrich_ioc`
action on the SAME IOC would short-circuit at the cache (no fresh
write → no re-fire), so this orchestration is safe to author with
`enrich_ioc` actions if you want to fan out to additional vendors.
For now we don't — the verdict from the triggering vendor is
enough.

## Companion orchestrations

- `cross-cp-pattern-investigator` (PR B) opens cases for IOCs that
  span multiple CPs. `t2-ioc-enrichment-escalator` opens cases for
  IOCs flagged by external enrichment. Both use the same dedup
  endpoint, so an IOC that hits BOTH triggers ends up in a single
  case (with both daimons attaching their findings + runs).
