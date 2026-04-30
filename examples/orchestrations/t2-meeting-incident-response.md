---
name: t2-meeting-incident-response
description: Tier-2 meeting orchestration. On a critical EDR finding, runs a sequential meeting between investigator → threat-hunter → malware-analyst, then has incident-responder synthesize meeting_minutes for operator review.

trigger:
  on: finding
  filter: "finding.severity == 'CRITICAL' && finding.category == 'process'"

defaults:
  timeout: 20m

steps:
  - id: discuss
    kind: meeting
    meeting:
      participants:
        - investigator
        - threat-hunter
        - malware-analyst
      synthesizer: incident-responder
    prompt: |
      A critical finding has surfaced on `{{trigger.host}}`:

        - title: {{trigger.title}}
        - severity: {{trigger.severity}}
        - evidence: {{trigger.evidence}}

      Discuss the incident from your perspective. Be concrete and cite specific evidence.
      Don't repeat what prior participants have already said — extend or correct it.
---

Sequential meeting on critical EDR findings. Each participant gets the
prior speakers' outputs and their structured findings; the synthesizer
emits a meeting_minutes finding with agenda + positions + action items.

When `war_bridge: true` is set on the meeting step, the resulting
finding is tagged for immediate operator attention in the dashboard.
