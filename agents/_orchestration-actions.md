# Orchestration action protocol

When a step in an orchestration runs you, you may be allowed to
request CP-side mutations on findings or on the run itself. You don't
do these mutations yourself — you describe them in your
`orchestration_result` finding and the orchestration engine applies
them on your behalf, validated against the step's allowlist.

## How to request actions

Emit ONE `orchestration_result` finding at the end of your work, as
a **single-line JSON object** on its own line, **not inside a
markdown code block**. The orchestrator scans your stdout for a line
that parses as `{"type":"finding","category":"orchestration_result",…}`
and pulls the `attributes` from it. Anything wrapped in
```` ```json … ``` ```` won't be picked up — write the JSON
directly.

The orchestrator has a fallback that scans the assistant text for a
JSON object containing `actions` or `verdict` even when no proper
finding event was emitted, but rely on the strict path: it keeps
the audit trail clean.

Required form (copy this layout):

```
{"type":"finding","category":"orchestration_result","title":"auto-triaged: noise","severity":"INFO","attributes":{"verdict":"noise","reasoning":"internal scanner traffic; matches pattern in /etc/okesu/scanners.yml","actions":[{"kind":"update_finding_status","finding_id":245,"status":"false_positive","reason":"auto-triaged-noise: internal scanner"},{"kind":"add_finding_tag","finding_id":245,"tag":"auto-triaged"},{"kind":"link_run_to_finding","finding_id":245}]}}
```

Yes, on one line. Print it as your last assistant output. The harness
ships it as a `text` event but the engine recognises the embedded
finding shape and treats it correctly.

Each step's allowlist is narrow on purpose. If you request an action
the step isn't allowed to take, the engine logs the rejection and
moves on; the action does **not** apply. So describe what *should*
happen, but don't be surprised if the engine drops one — the
orchestration author scoped permission deliberately.

## Action kinds

| kind | payload | what it does |
|---|---|---|
| `update_finding_status` | `{ finding_id, status, reason? }` | Set finding triage state. Allowed values: `open`, `acknowledged`, `investigating`, `resolved`, `false_positive`, `wontfix`, `suppressed`. |
| `add_finding_tag` | `{ finding_id, tag, reason? }` | Append a tag to the finding's tag list. Idempotent. |
| `remove_finding_tag` | `{ finding_id, tag, reason? }` | Drop a tag. No-op when absent. |
| `set_finding_severity_override` | `{ finding_id, severity, reason? }` | Set the operator-severity override (CRITICAL/HIGH/MEDIUM/LOW/INFO). Use this for noise → INFO downgrades. |
| `link_run_to_finding` | `{ finding_id, reason? }` | Record this orchestration run as having handled the finding. Surfaces in the finding-detail page as "auto-handled by run #N". |
| `escalate` | `{ reason, severity? }` | Soft signal that the on-call should review the run even though it ran cleanly. v1: logged; v2: surfaces on the dashboard's "needs review" tile. |

## Conventions

- Always include `link_run_to_finding` when you mutate a finding —
  it's the audit trail entry future operators rely on.
- Set `reason` to a short human-readable phrase. It's preserved in
  the finding's edit history and shown to the next operator who
  opens the finding-detail page.
- The `finding_id` you reference is almost always
  `{{trigger.finding_id}}` (templated by the orchestration spec).
- Don't wrap actions inside other attributes — `attributes.actions`
  must be a top-level array of objects under `attributes`.
- Don't request the same action twice in one step — the engine
  applies whichever lands first; later duplicates are no-ops or
  conflicts.

## When NOT to request actions

- The orchestration step has no `actions:` allowlist. (You can still
  emit other attributes; they just stay structured outputs the next
  step can read via `{{step.result.*}}`.)
- You're uncertain about the verdict. Better to leave the finding
  open with your reasoning attached than to mis-suppress.
- The finding is already in a terminal state (`resolved`,
  `false_positive`). The status update is a no-op but it still
  pollutes the audit trail.

## Worked example: t1-finding-autotriage

The classify step's spec:

```yaml
- id: classify
  agent: investigator
  actions:
    - update_finding_status
    - set_finding_severity_override
    - add_finding_tag
    - link_run_to_finding
  prompt: …
```

Your output for a noise verdict:

```json
{
  "type": "finding",
  "category": "orchestration_result",
  "title": "classified: noise",
  "severity": "INFO",
  "attributes": {
    "verdict": "noise",
    "reasoning": "fired on 12 hosts in 30min, scanner pattern",
    "actions": [
      { "kind": "update_finding_status",          "finding_id": 245, "status": "false_positive", "reason": "auto-triage: noise" },
      { "kind": "set_finding_severity_override",  "finding_id": 245, "severity": "INFO",          "reason": "noise downgrade" },
      { "kind": "add_finding_tag",                "finding_id": 245, "tag": "auto-triaged-noise" },
      { "kind": "link_run_to_finding",            "finding_id": 245 }
    ]
  }
}
```

Your output for a confirmed verdict:

```json
{
  "type": "finding",
  "category": "orchestration_result",
  "title": "classified: confirmed",
  "severity": "INFO",
  "attributes": {
    "verdict": "confirmed",
    "reasoning": "novel binary path, mtime within 30min, parent_pid=cron",
    "actions": [
      { "kind": "add_finding_tag",     "finding_id": 245, "tag": "auto-confirmed" },
      { "kind": "link_run_to_finding", "finding_id": 245 }
    ]
  }
}
```

Notice the noise verdict applies a status change + severity override;
the confirmed verdict only tags + links — it leaves the on-call
seeing the finding at its original severity.
