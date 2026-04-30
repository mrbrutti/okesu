---
name: t2-hypothesis-test
description: Tier-2 hypothesis closer. Fires when a finding with subtype=hypothesis lands, runs the agent's stated `how_to_test`, records the verdict (verified/refuted/inconclusive) on the original hypothesis finding, and links the run for case provenance.
trigger:
  on: finding
  filter: "finding.subtype == 'hypothesis'"
inputs:
  finding_id:
    type: int
    required: false
    default: 0
defaults:
  timeout: 5m
steps:
  # 1. Execute the test the hypothesis-writer described. The agent
  #    receives the full hypothesis context (claim, evidence,
  #    confidence, how_to_test) plus the host the original finding
  #    fired on, and emits a verdict.
  - id: run_test
    agent: investigator
    node: "{{trigger.host}}"
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      A hypothesis finding has been emitted. Execute its `how_to_test`
      and decide whether the claim holds.

      Finding id:    {{trigger.finding_id}}
      Host:          {{trigger.host}}
      Claim:         {{trigger.attributes.claim}}
      Evidence:      {{trigger.attributes.evidence}}
      Confidence:    {{trigger.attributes.confidence}}

      How to test:
      {{trigger.attributes.how_to_test}}

      Run the test. Be exact about what you executed and what you
      observed. Don't speculate beyond the evidence.

      Decide:
        - verdict: one of `verified` | `refuted` | `inconclusive`
        - rationale: 1-2 sentences citing specific evidence
        - confidence_after: low | medium | high
        - new_evidence: free-form observations from the test

      Emit an orchestration_result finding with attributes:
        verdict (string), rationale (string),
        confidence_after (string), new_evidence (string).

  # 2. Record the verdict on the original hypothesis finding so the
  #    operator opening the finding sees the test outcome inline.
  #    Tag with t2-hypothesis-{verdict} and (when refuted) close as
  #    false_positive — the agent's reasoning becomes the lesson the
  #    auto-lesson hook (PR C) records on the originating daimon.
  - id: record_outcome
    agent: investigator
    timeout: 2m
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Apply the verdict to finding #{{trigger.finding_id}}.

      Verdict:           {{run_test.result.verdict}}
      Rationale:         {{run_test.result.rationale}}
      Confidence after:  {{run_test.result.confidence_after}}

      Emit actions:
        - link_run_to_finding (always)
        - add_finding_tag: t2-hypothesis-{{run_test.result.verdict}}
        - When verdict=verified AND confidence_after=high:
            update_finding_status: investigating
            (the operator should still lay eyes; "verified
            hypothesis" is a starting point, not a closing point)
        - When verdict=refuted:
            update_finding_status: false_positive
              reason: "{{run_test.result.rationale}}"
            (this trips the auto-lesson hook from PR C — the
            daimon that emitted the original hypothesis learns
            from the refutation on its next tick)
        - When verdict=inconclusive:
            add_finding_tag: needs-human
            (operator decides whether to gather more evidence
            or close)

      Emit an orchestration_result finding with attributes:
        applied_status (string), applied_tags (array of strings).

  # 3. Attach a case-log note to any open investigation already
  #    linked to this finding. The investigations workspace shows
  #    these in the Notes tab so the case timeline reflects the
  #    test outcome without operator intervention.
  - id: append_to_cases
    agent: investigator
    timeout: 1m
    actions:
      - link_run_to_finding
    prompt: |
      For each open investigation linked to finding
      #{{trigger.finding_id}}, append an analyst note summarising
      the test outcome. Lookup:

      curl -s "${OKESU_CP_URL}/api/findings/{{trigger.finding_id}}/investigations"

      For each investigation in the response, POST a note:

      curl -sX POST "${OKESU_CP_URL}/api/investigations/<id>/notes" \
        -H 'Content-Type: application/json' \
        -d '{
              "author": "t2-hypothesis-test",
              "body": "Hypothesis verdict: {{run_test.result.verdict}}\n\nRationale: {{run_test.result.rationale}}\n\nNew evidence: {{run_test.result.new_evidence}}"
            }'

      If no investigations are linked, do nothing — this isn't a
      missing-data error.

      Emit an orchestration_result finding with attributes:
        notes_added (int).
---

# Notes

This orchestration closes the loop on the hypothesis subtype. The
`hypothesis-writer` agent emits a hypothesis finding with a
falsifiable claim + a stated way to test; without this
orchestration the test was operator-manual.

## Wiring

- `trigger.on: finding` with filter `finding.subtype == 'hypothesis'`
  — fires on every new hypothesis finding regardless of severity.
  Filter lives at the trigger layer (free), not in a per-step
  `when:` (would still spawn a run).

## Why this matters end-to-end

- A T1 daimon emits a hypothesis (e.g. "the recent CPU spikes on
  threat-rocky-1 are caused by the package-manager retry loop, not
  exfil; test by checking `journalctl -u apt` for repeated failures
  in the last 30 min").
- This orchestration auto-fires, runs the test, and either:
  - VERIFIES → tags the finding, escalates to `investigating`,
    operator decides next move
  - REFUTES → closes the finding as false_positive with the test
    rationale → the auto-lesson hook (PR C) records the rationale
    on the originating daimon → next tick that daimon classifies
    similar patterns more accurately
  - INCONCLUSIVE → tags `needs-human`, no other state change

The test outcome is mirrored to any open investigation, so an
operator deep-linked from a case sees the test result without
hunting through the run history.

## Companion orchestrations

- `t1-finding-autotriage` — runs first on every finding (including
  hypotheses). Marks low-severity hypotheses as INFO, tags them.
  This orchestration runs in parallel and produces the actual
  test outcome.
- `t2-ioc-enrichment-escalator` (PR D) — fires on `ioc_enriched`,
  not on findings, so the two don't compete for the same trigger.
