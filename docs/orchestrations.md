# Orchestrations

An **orchestration** chains multiple agents together so the output of
one feeds into the input of the next. Use them when an investigation
needs more than a single agent — find a suspicious binary, then
analyse it, then hunt the fleet for it; or triage a finding, gather
forensics, then propose a containment plan.

Orchestrations are written as YAML. They live on the CP that hosts
them; runs execute there but can dispatch steps to nodes on any
federated child CP. The visual editor (Visual mode in the UI) is
backed by the same YAML format documented here, so anything you can
draw is something you can hand-author.

## Quick example

```yaml
---
name: post-finding-deep-dive
description: Triage a HIGH/CRITICAL EDR finding, analyse the binary, then hunt the fleet.

trigger:
  on: finding
  filter: "finding.severity in ['HIGH','CRITICAL'] && finding.agent == 'edr'"

steps:
  - id: triage
    agent: investigator
    node: "{{trigger.host}}"
    prompt: |
      Investigate finding {{trigger.finding_id}} on {{trigger.host}}.
      Focus on processes spawned in the last 30 minutes.

  - id: analyze
    when: "{{triage.findings | any(category='process')}}"
    agent: binary-analyzer
    node: "{{triage.findings.first.host}}"
    prompt: |
      Analyse {{triage.findings.first.path}} on this host.

  - id: hunt
    agent: threat-hunter
    cp: "*"
    prompt: |
      Hunt the fleet for sha256 {{analyze.result.sha256}}.

  - id: respond
    approval: required
    agent: incident-responder
    node: "{{trigger.host}}"
    prompt: |
      Containment plan for {{trigger.finding_id}}.
      Triage: {{triage.output | tail(50)}}
      Hunt clusters: {{hunt.findings | json}}
---

# Notes

Anything below the closing `---` is operator documentation — ignored
by the engine, displayed in the library detail panel.
```

## Top-level fields

All spec lives in the YAML frontmatter. The markdown body is
documentation only.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Unique slug — `[a-z][a-z0-9-]*`. |
| `description` | string | yes | One-line description shown in the library. |
| `version` | int | no | Spec version; defaults to `1`. Reserved for breaking changes. |
| `trigger` | object | no | When the orchestration runs automatically. Default: manual only. |
| `inputs` | map | no | Inputs the operator (or trigger) must provide. |
| `defaults` | object | no | Per-step defaults inherited by every step. |
| `steps` | list | yes | The actual work — one or more steps. |

### `trigger`

Three modes — operators always retain manual run access regardless of
configured trigger:

```yaml
trigger:
  on: manual              # default — operator clicks "Run" to start
trigger:
  on: cron
  cron: "0 */6 * * *"     # see Cron below
trigger:
  on: finding
  filter: "finding.severity in ['HIGH','CRITICAL']"
```

#### Cron syntax

Standard 5-field cron — minute, hour, day-of-month, month, day-of-week
(0 = Sunday … 6 = Saturday).

| Form | Example | Meaning |
|---|---|---|
| Wildcard | `*` | every value |
| Single | `30` | exactly 30 |
| List | `1,3,5` | 1 or 3 or 5 |
| Range | `1-5` | 1 through 5 inclusive |
| Step | `*/15` | every 15th value (0, 15, 30, 45) |
| Step with range | `9-17/2` | 9, 11, 13, 15, 17 |

Examples:

| Expression | Fires |
|---|---|
| `0 * * * *` | top of every hour |
| `*/15 * * * *` | every 15 minutes |
| `0 9 * * 1-5` | 09:00 weekdays |
| `30 14 * * *` | 14:30 daily |
| `0 0 1 * *` | midnight on the 1st of every month |

The scheduler ticks every 60 seconds. The smallest practical
schedule is one minute. `last_fired_at` on the orchestration row
prevents double-fires across CP restarts.

Special characters not supported: `?`, `L`, `W`, `#`, named months
(`JAN`/`FEB`/…). Stick to numeric values.

#### Finding filter

Expression is evaluated against every finding the eventpipeline
projects on this CP. If true, the orchestration fires with the
finding's fields populating `{{trigger.*}}`. Empty filter = never
fires (finding triggers are opt-in by design).

The expression sees one binding, `finding`, with these fields:

| Field | Type | Source |
|---|---|---|
| `finding.id` | int | finding row id |
| `finding.severity` | string | CRITICAL/HIGH/MEDIUM/LOW/INFO |
| `finding.title` | string | finding title |
| `finding.agent` | string | which daimon emitted it |
| `finding.host` | string | host the finding is about |
| `finding.category` | string | finding category |
| `finding.dedup_key` | string | dedup fingerprint |
| `finding.resource` | string | scoped resource id (e.g. `pid:1337`) |
| `finding.attributes` | map | extra attributes from the agent |

Operators supported: `==`, `!=`, `<`, `<=`, `>`, `>=`, `&&`, `||`,
`!`, `in [a, b, c]`. Examples:

```yaml
filter: "finding.severity in ['HIGH', 'CRITICAL']"
filter: "finding.agent == 'edr' && finding.category == 'process'"
filter: "finding.attributes.process_pid > 0 && finding.host != 'jumphost'"
```

When fired, the run's `{{trigger}}` carries every finding field plus
`kind: "finding"`.

### `inputs`

Required for manual triggers when the orchestration needs operator
input that the trigger doesn't supply.

```yaml
inputs:
  finding_id:
    type: int
    required: true
  scope:
    type: string
    default: "fleet"
    enum: [host, cp, fleet]
```

Types: `string`, `int`, `bool`. `enum` constrains values. Inputs
become `{{trigger.<name>}}` in templates regardless of trigger source.

### `defaults`

```yaml
defaults:
  timeout: 5m
  continue_on_error: false
  cp: local
```

Steps inherit these; per-step values override.

## Step fields

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Unique within the orchestration; `[a-z][a-z0-9_-]*`. |
| `agent` | string | yes | Name of an agent in the agent library. |
| `prompt` | string | yes | Templated prompt sent to the agent. |
| `node` | string | no | Single node selector. Mutually exclusive with `nodes`. |
| `nodes` | list | no | Multiple node selectors — the step **fans out in parallel** across all of them. |
| `cp` | string | no | CP selector (default `local`). |
| `inputs` | map | no | Extra inputs passed alongside the prompt. |
| `timeout` | duration | no | Time limit (`5m`, `30s`, `1h`). |
| `continue_on_error` | bool | no | If true, a failed step does not halt the run. |
| `approval` | string | no | Set to `required` to pause for operator approval. |
| `when` | string | no | Skip the step unless this expression is truthy. |

### Single node vs fan-out (`node` vs `nodes`)

Pick one — they're mutually exclusive. The validator rejects specs
with both set on the same step.

```yaml
# Single host. The step runs once on web-prod-01.
- id: triage
  agent: investigator
  node: web-prod-01

# Fan-out. The step runs in parallel on three hosts; outputs merge.
- id: hunt
  agent: threat-hunter
  nodes:
    - web-prod-01
    - web-prod-02
    - web-prod-03
```

Fan-out semantics:

- Every host runs the **same prompt** on its own dispatched Run.
- The engine waits for **all** to terminate before advancing.
- Step status is `completed` if every node completed, `failed` if any
  node failed (subject to `continue_on_error`).
- Findings are flattened into `{{stepN.findings}}` for chained
  templates that don't care which host produced what.
- Per-host detail is exposed via `{{stepN.byNode["web-prod-01"]}}`.
- The full transcript carries a `── <hostname> ──` divider before each
  host's section so `{{stepN.output | tail(50)}}` is still readable.

The visual editor's node picker writes single hosts as `node:` and
multi-host selections as `nodes:` automatically. You don't normally
choose between the two by hand.

### Node selectors

The `node` and `nodes` fields take any of:

| Form | Example | Behaviour |
|---|---|---|
| Plain hostname | `web-prod-01` | Exact match against the node hostname registered in the CP. |
| Templated | `{{trigger.host}}` | Resolved at run time. |
| Selector by id | `id:42` | Match by node id (planned). |
| Selector by role | `role:webserver` | First node tagged `role=webserver` (planned). |
| Selector by OS | `os:linux` | First Linux node (planned). |

The `id:` / `role:` / `os:` selector forms are reserved in the
parser; the engine currently resolves only plain hostnames and
templated expressions. Selector resolution lands in a follow-up.

If `node`/`nodes` are both omitted, the step runs in the CP's local
context — useful for steps that only manipulate CP state (write a
finding, post to a webhook).

### CP selectors

| Form | Behaviour |
|---|---|
| `local` (default) | The CP that owns the orchestration. |
| `<instance_id>` | Specific federated child CP. |
| `from-step-N.cp` | Same CP that step `N` ran on (planned). |
| `*` | Fan out to every healthy federated CP (planned). |

A federated step (`cp: <child_id>`) dispatches via the parent's
federation proxy: the parent posts to the child's
`/api/v1/federation/runs/sync`, the child runs the step on its own
tunnel, the parent stitches the result back into the timeline.

`from-step-N.cp` and `cp: "*"` are reserved in the parser; full
support comes in a follow-up alongside cross-CP selector resolution.

## Templating reference

Templated strings use `{{ expr }}`. The bindings:

### Trigger context

- `{{trigger.<input_name>}}` — operator-provided input
- `{{trigger.kind}}` — `manual` | `finding` | `cron`

For finding-triggered runs, `{{trigger}}` also carries:
- `{{trigger.finding_id}}`
- `{{trigger.severity}}`
- `{{trigger.title}}`
- `{{trigger.agent}}`
- `{{trigger.host}}`
- `{{trigger.category}}`
- `{{trigger.dedup_key}}`
- `{{trigger.resource}}`
- `{{trigger.attributes}}` (map of finding attributes)

For cron-triggered runs:
- `{{trigger.tick}}` — unix-seconds at fire time
- `{{trigger.at}}` — RFC-3339 timestamp at fire time

### Step outputs

- `{{<step_id>.status}}` — `completed` | `failed` | `skipped`
- `{{<step_id>.output}}` — full transcript, capped at 100KB
- `{{<step_id>.findings}}` — array of finding objects emitted by the step
- `{{<step_id>.result}}` — structured payload from the agent's
  `orchestration_result` finding (see *Result convention*)
- `{{<step_id>.cp}}` — instance_id of the CP the step ran on
- `{{<step_id>.host}}` — hostname the step ran on (single-node steps)

For fan-out steps with `nodes:`, additional bindings:

- `{{<step_id>.nodes}}` — ordered list of hostnames the step ran on
- `{{<step_id>.byNode["web-prod-01"]}}` — per-host detail, with the
  same shape as the top-level step binding (status, findings, output,
  etc.) scoped to that one host.

```yaml
# Use the flattened union of all hosts' findings.
- id: respond
  prompt: "Hunt found {{hunt.findings | length}} matches."

# Or drill into one specific host's slice.
- id: focus
  prompt: |
    web-prod-01 reported: {{hunt.byNode["web-prod-01"].findings | json}}
```

### Filters

Pipe a value into one or more filters:

| Filter | Example | Result |
|---|---|---|
| `first` | `{{step.findings | first}}` | First element of an array |
| `last` | `{{step.findings | last}}` | Last element |
| `length` | `{{step.findings | length}}` | Count |
| `tail(N)` | `{{step.output | tail(50)}}` | Last N lines of a string |
| `head(N)` | `{{step.output | head(20)}}` | First N lines |
| `json` | `{{step.findings | json}}` | JSON-encode (use sparingly — burns context) |
| `where(<key>=<value>)` | `{{step.findings | where(severity='HIGH')}}` | Filter array by field |
| `any(<key>=<value>)` | `{{step.findings | any(category='process')}}` | True if any element matches |

Filters chain left-to-right: `step.findings | where(severity='HIGH') | length`.

### Path-style sugar

For convenience, slice access also works as a dotted path:

| Path | Equivalent |
|---|---|
| `step.findings.first` | `step.findings | first` |
| `step.findings.last` | `step.findings | last` |
| `step.findings.length` | `step.findings | length` |
| `step.findings.0` | first element by numeric index |

This lets you chain into properties without parens:
`step.findings.first.path` instead of `(step.findings | first).path`.

### Operators (in `when` and `trigger.filter`)

- Comparison: `==`, `!=`, `>`, `<`, `>=`, `<=`
- Boolean: `&&`, `||`, `!`
- Membership: `in [a, b, c]` (right-hand side is a list)
- Substring / element: `contains` — polymorphic on the LHS:
  - `string contains 'sub'` → strings.Contains
  - `array contains x`      → element membership

Numeric comparisons coerce strings to numbers when both sides parse:
`finding.attributes.process_pid > 0` works whether `process_pid` is
encoded as a number or a digit string in the agent's emitted JSON.

Equality is "zeroish-tolerant" — comparing `nil` against `''`, `0`,
`false`, or empty list returns true. This means
`finding.attributes.sha256 != ''` is correctly `false` for findings
that never carry the attribute, instead of always-true.

## Result convention

For clean step-to-step data passing, agents that will be chained
should emit a final finding with `category: orchestration_result`.
Its `attributes` JSON becomes `{{stepN.result}}`:

```jsonl
{"type":"finding","severity":"INFO","category":"orchestration_result","title":"binary-analyzer result","resource":"sha256:abc123","attributes":{"sha256":"abc123","family":"cobaltstrike","cmdline":"./mal -c1.2.3.4"}}
```

If a step doesn't emit one, `{{stepN.result}}` is empty — `null` in
template terms (truthy checks return false). The full
`{{stepN.output}}` transcript is always available as a fallback —
but it costs context, so prefer `result` for chained data flow.

For fan-out steps, `{{stepN.result}}` aggregates per-host attributes
under `byNode`: `{{stepN.result.byNode["web-prod-01"].sha256}}`.

## Data queries (CP-side `data:` block)

A step can declare structured CP-side reads via a `data:` block. The
engine resolves each entry before the step is dispatched and binds the
result into the prompt template as `{{data.<name>}}`. Replaces the
older "have the agent curl /api/findings" pattern: the engine handles
auth, audit, and persistence so the agent's prompt stays focused on
reasoning.

```yaml
- id: classify_batch
  agent: investigator
  data:
    findings:
      query: findings.list
      params:
        state: queue
        severity: [INFO, LOW]
        limit: 50
    summary:
      query: findings.summary
  prompt: |
    Classify {{data.findings | length}} findings.
    {{data.findings | json}}
```

Each entry has:

| field    | required | meaning |
|----------|----------|---------|
| `query`  | yes      | Registered handler name in the form `namespace.method`. Engine validates at run time. |
| `params` | no       | Free-form map; each handler validates its own params. Templates inside string values are rendered against the run context (trigger + previous steps) before dispatch, so e.g. `value: "{{trigger.attributes.sha256}}"` works. |

### Built-in queries

| query | params | returns |
|---|---|---|
| `findings.list`           | `state` (`queue`\|`open`\|`acked`\|`all`), `severity` (list), `agent`, `host`, `category`, `tag`, `since_ms`, `until_ms`, `limit`, `offset` | array of finding rows |
| `findings.summary`        | none | per-CP rollup (totals + 24h trend) |
| `findings.history`        | `finding_id` (required) | edit history for one finding |
| `orchestration-runs.list` | `status` (list), `trigger_kind` (list), `since` (relative `30m`/`1h`/...), `since_ms`, `limit`, `offset` | array of orchestration_run rows |
| `nodes.list`              | `limit`, `offset` | array of node rows |
| `agents.list`             | `limit`, `offset` | array of (daemon agent, host) rows |
| `iocs.lookup`             | `kind` (required, e.g. `sha256`/`ipv4`/`domain`/`url`/`cve`), `value` (required, any form — refanged/mixed-case ok; CP normalizes) | `{valid, kind, normalized_value, attribution?, severity_floor?, classification?, source?}` — `valid:false` on miss for `when:` branching; real DB errors fail the step |

The canonical list lives in `controlplane/api/data_resolver.go` —
adding a new query is a single `r.registerHandler("...", ...)` call
plus a typed handler function.

### Persistence + replay

The engine persists each step's resolved data on the run record
(`orchestration_steps.data_snapshot`, JSON-encoded). The runs API
exposes it as `step.data` on the step JSON, so the run-detail UI can
show exactly what the agent saw — even after the underlying tables
have moved on. Useful for audit ("what input made the agent decide
to suppress this?"), replay, and debugging weird verdicts.

Cross-reference: `agents/_orchestration-data.md` for the agent-author
view of the same protocol.

## Engine-applied actions (CP-side mutations)

A step can request CP-side mutations (status changes, tagging,
severity overrides, run linkage) by emitting an `actions:` array
inside its `orchestration_result.attributes`. The engine validates
each entry against the step's `actions:` allowlist and applies via
DB methods — agents never get CP credentials.

### Spec

```yaml
- id: classify
  agent: investigator
  actions:                              # allowlist (default deny)
    - update_finding_status
    - set_finding_severity_override
    - add_finding_tag
    - link_run_to_finding
  prompt: |
    …decide noise vs confirmed…
    Emit an orchestration_result finding with attributes:
      verdict, reasoning, actions (array)
```

### Wire format (what the agent emits)

```jsonl
{"type":"finding","category":"orchestration_result","title":"auto-triaged: noise","severity":"INFO","attributes":{
  "verdict":"noise",
  "reasoning":"internal scanner pattern",
  "actions":[
    {"kind":"update_finding_status","finding_id":245,"status":"false_positive","reason":"auto-triage: noise"},
    {"kind":"set_finding_severity_override","finding_id":245,"severity":"INFO"},
    {"kind":"add_finding_tag","finding_id":245,"tag":"auto-triaged-noise"},
    {"kind":"link_run_to_finding","finding_id":245}
  ]
}}
```

### Action kinds

| kind | payload | effect |
|---|---|---|
| `update_finding_status` | `{ finding_id, status, reason? }` | Set triage state. Allowed: `open`, `acknowledged`, `investigating`, `resolved`, `false_positive`, `wontfix`, `suppressed`. |
| `add_finding_tag` | `{ finding_id, tag, reason? }` | Append a tag. Idempotent. |
| `remove_finding_tag` | `{ finding_id, tag, reason? }` | Drop a tag. No-op when absent. |
| `set_finding_severity_override` | `{ finding_id, severity, reason? }` | Set operator-severity (CRITICAL/HIGH/MEDIUM/LOW/INFO). |
| `link_run_to_finding` | `{ finding_id, reason? }` | Record a run ↔ finding association in `finding_run_links`. Surfaces in the finding-detail "auto-handled by run #N" line. |
| `escalate` | `{ reason, severity? }` | Soft signal — operator review requested even if the run completed cleanly. |
| `reflect_with_lessons` | `{ lessons: [string, ...] }` | Append one or more short lessons to `agent_lessons` for the step's agent. The daemon prepends the 10 newest (200 chars each) to its system prompt on the next tick. |
| `enrich_ioc` | `{ ioc_id }` | Run all configured vendor adapters (VirusTotal, AbuseIPDB, Shodan) against an IOC and cache results in `ioc_enrichments`. Cache TTL is 24h by default. Class: enrich. |

Every applied action writes a `finding_edits` row tied to the
orchestration run + step, so the finding-detail History panel shows
"auto-resolved by run #42 / step:verify, 12m ago". Rejected actions
(not in the step's allowlist) are logged but never applied — the
engine does not abort.

The action protocol full spec is at `agents/_orchestration-actions.md`
— that's also the doc agents read when called from an orchestration
step.

### Action classes

Each action kind is internally classified into one of:

| Class | Meaning | Examples |
|---|---|---|
| read   | Pure read; no CP state change | (none yet) |
| enrich | Outbound vendor call (no CP write) | (Phase 22.4: enrich_ioc) |
| fetch  | Inbound content fetch | (none yet) |
| create | New row inserted / link created | link_run_to_finding, reflect_with_lessons |
| modify | Existing row mutated | update_finding_status, set_finding_severity_override, add_finding_tag, remove_finding_tag |

CP operators can set per-class auto-approve toggles in CP settings:

```yaml
policy:
  auto_approve:
    read: true
    enrich: true
    create: false
    modify: false
```

When a step's allowlist consists entirely of auto-approved kinds, the
engine bypasses the operator approval gate. A mixed allowlist (e.g.
`[link_run_to_finding (create), update_finding_status (modify)]`)
keeps the gate as long as any kind isn't auto-approved.

Default: no class is auto-approved (every gate fires). Unknown action
kinds map to `modify` (most-restrictive). The `escalate_run` action is
intentionally excluded from the registry — it's a soft signal, not a
state mutation, and falls through to the `modify` default so it never
auto-applies.

Cross-reference: `agents/_orchestration-actions.md` for the agent-author
view of the same taxonomy.

### Action authorship pattern

Two layers of authoring:

1. **Spec author** (the orchestration YAML) decides which action
   kinds the step is permitted to take by listing them under
   `actions:`. This is the security boundary.
2. **Prompt author** (still the spec author, in the same file) tells
   the agent which actions to request and under which conditions —
   inside the prompt body. The agent obeys; the engine validates.

A step that doesn't list `actions:` can still emit other structured
attributes; they just remain visible to downstream steps and the UI.
The default-deny posture means an agent that decides on its own to
request `update_finding_status` on a step missing that allowlist
entry has its request silently dropped (and logged for the operator).

### Recording lessons (`reflect_with_lessons`)

Use this action when an orchestration run produces an insight worth
remembering across runs — a heuristic that saved investigation time, a
class of false positive to skip, a validation step the agent should
run before recommending containment.

```yaml
- id: reflect
  agent: investigator
  actions:
    - reflect_with_lessons
  prompt: |
    Reflect on this run. Emit an orchestration_result with attributes:
      actions: [
        {
          "kind": "reflect_with_lessons",
          "lessons": ["short imperative-tense lesson", ...]
        }
      ]
```

The next time the daemon ticks for `agent: investigator`, the lessons
appear at the top of its system prompt under a `## Lessons from prior
runs` header. Bounded to 10 newest (200 chars each); the daemon
silently fails open if the CP is unreachable at fetch time.

The action class is `create`, so operators with
`policy.auto_approve.create: true` can let agents reflect without
approval gates.

## Meeting steps (`kind: meeting`)

A meeting step runs N participants sequentially against the trigger
payload, then has a synthesizer agent emit a structured `meeting_minutes`
finding. Use it for cross-perspective T2 case work where one agent's
analysis isn't enough.

```yaml
- id: discuss
  kind: meeting
  meeting:
    participants:
      - investigator
      - threat-hunter
      - malware-analyst
    synthesizer: incident-responder
  prompt: |
    A critical finding has surfaced on `{{trigger.host}}`.
    Discuss the incident from your perspective.
```

Each participant receives the trigger plus all prior participants'
outputs as a "## Conversation so far" section. The synthesizer
receives the full conversation and structured outputs and emits a
finding with `subtype: meeting_minutes` (agenda, positions, action
items, decision).

The step's timeout is the budget for the whole conversation, not per
turn — if participant 1 takes 18m of a 20m budget, the synthesizer has
2m left.

### War bridges

Set `war_bridge: true` on a meeting step to instruct the synthesizer to
tag the resulting finding `war-bridge`. The dashboard renders a red
banner listing active war-bridge findings (queries
`GET /api/findings/war-bridge`); operators can drill in immediately.

```yaml
- id: emergency_huddle
  kind: meeting
  war_bridge: true
  meeting:
    participants: [investigator, threat-hunter, ciso]
    synthesizer: incident-responder
  prompt: ...
```

Pair with a `severity == 'CRITICAL'` trigger filter so war bridges
fire only on alarms that actually warrant the attention.

## Finding subtypes

Findings carry an optional `subtype` that identifies the structured
shape of their attributes. Subtypes drive UI rendering hooks.

| Subtype          | Emitted by               | Attributes                                                                |
|------------------|--------------------------|---------------------------------------------------------------------------|
| `hypothesis`     | `hypothesis-writer`      | claim, evidence_for[], evidence_against[], confidence, how_to_test        |
| `meeting_minutes`| meeting-step synthesizer | agenda, positions, action_items, decision                                 |

## Approval gates

Set `approval: required` on a step to pause the run before that step
dispatches. The CP marks the run as `approval_required`, the canvas
pulses the gated card amber with an inline Approve button, and the
engine does not advance until an operator clicks Approve in the
run-detail page.

The same approval mechanism is available on standalone agent Runs —
operators can mark a Run as needing approval before destructive
actions (containment, deploy rollback) by ticking "Require approval"
in the Run dialog. The engine's gate code is shared between both
surfaces.

Approvals are scoped per-step: each gated step requires its own click,
and an Approve doesn't carry forward to later gates in the same run.

## Failure semantics

A step that fails (non-zero exit, agent error, timeout) halts the
run with status `failed` by default. Subsequent steps are marked
`skipped`.

Set `continue_on_error: true` to advance past the failure. Later
steps can branch on `{{stepN.status}}`:

```yaml
- id: cleanup
  when: "{{analyze.status == 'failed' || analyze.findings | length == 0}}"
  agent: investigator
  prompt: "Analyze step failed or returned nothing — investigate."
```

For fan-out steps, the step is `failed` if any node failed (with the
error listing which hosts). `continue_on_error` applies to the whole
step — there's no per-host opt-out today.

## Tunnels are required for step dispatch

Orchestration steps run agents on remote nodes via the **reverse-mTLS
tunnel** opened by the `okesu node` binary — this is a separate
connection from the long-running daimon webhook flow that pushes
findings + events into the CP.

If a node was deployed (status=`ready` in the inventory) but no
tunnel client is currently connected, the engine fails the step with:

```
node "X" is registered but no reverse-tunnel client is currently connected.
Run `okesu node` on the target host to open an mTLS tunnel — orchestration
steps dispatch agent runs through that tunnel, separate from the
long-running daimon webhook flow.
```

The CP shows live tunnels under each node's detail page. To open one
on a host you've already deployed daimons to:

```bash
# On the target host, after `okesu` is installed:
okesu node \
  --cp-url   https://your-cp.example.com:7443 \
  --cert     /etc/okesu/tunnel.crt \
  --key      /etc/okesu/tunnel.key \
  --ca       /etc/okesu/ca.crt
```

The tunnel persists; the host appears in `GET /api/nodes/connected`
and orchestration steps targeting it dispatch immediately.

### Cross-CP node auto-discovery

When a step's `node:` doesn't pin a `cp:` selector, the engine tries
local first, then queries every healthy federated peer for a connected
node with that name. If exactly one peer has it, the step dispatches
there automatically — operators don't have to know which CP each node
is registered on. If multiple peers have a node by that name, the
engine refuses to guess and asks the operator to set `cp:` explicitly.

## Federation

Orchestrations live on the CP that authored them. From the Global CP,
the create dialog asks where the orchestration should live — Global
itself, or any healthy federated child CP. Once saved, the Global
library shows orchestrations from every CP in the federation, the
same way Daimons and Findings already federate.

Steps with `cp: <child_id>` dispatch their Run via the parent's
federation proxy: the parent posts to the child's
`/api/v1/federation/runs/sync`, the child executes the Run as if
locally launched, the parent stitches the result back into the
orchestration timeline. From the operator's perspective there's a
single run page with steps that happen to be running on different
CPs — each step card carries a CP-source chip showing where it ran.

Trigger evaluation is local to each CP: a finding-trigger
orchestration on east only fires for findings projected on east. To
auto-trigger across the whole fleet, install the orchestration on
every CP that should react.

## Example library — T1/T2 operator support

The `examples/orchestrations/` directory ships a curated library that
exists so a human operator only sees what genuinely needs them. T1
auto-resolves; T2 auto-investigates and pauses for an approval gate
before doing anything destructive. See
`examples/orchestrations/README.md` for the full index.

**T1 (autonomous, silent on success):**

| Orchestration | Trigger | What it does |
|---|---|---|
| `t1-finding-autotriage` | every new finding (≠INFO) | classifies noise vs confirmed; suppresses noise via action protocol; tags + escalates HIGH/CRITICAL |
| `t1-disk-pressure-cleanup` | disk-low finding | scopes pressure → whitelisted cleanup → verify → resolves the finding |
| `t1-stale-agent-restart` | daimon-stale finding | probes the unit, restarts via the right service manager, verifies heartbeat |
| `t1-failed-login-noise-dedup` | sshd auth-failure findings | scanner vs targeted; bundles scanner noise; suppresses + tags |
| `t1-fleet-health-sweep` | cron 07:00 UTC daily | aggregates fleet state; produces the on-call's morning brief |

**T2 (approval-gated, deeper):**

| Orchestration | Trigger | What it does |
|---|---|---|
| `edr-critical-response` | EDR HIGH/CRITICAL | triage → analyze → fleet hunt → gated containment plan |
| `t2-fleet-ioc-hunt` | finding carrying an IOC | scope → fan-out hunt → heatmap → gated quarantine |
| `t2-cert-expiry-rotation` | cron 04:00 UTC daily | audit → auto-rotate lab → gated rotate prod |
| `t2-drift-remediation` | FIM (instance-integrity) finding | attribute → lateral check → gated revert with forensics |

Conventions used across the library:
- Every state-mutating step declares `actions:` and prompts its
  agent to request the right ones. The action protocol closes the
  loop: T1 chains *actually* resolve findings instead of just
  emitting briefs.
- Multi-host fan-outs use `continue_on_error: true`.
- Whitelist-only auto-actions (no rm-rf-and-hope).
- Tag-based escape hatches: hosts opt out via `/etc/okesu/labels`
  (`noremediate=disk`, `noisolate=yes`, `production=true`).
- T1 chains terminate silently on the happy path; T2 chains always
  finish with a one-page operator brief.

Trigger probes are local to each CP — install on each CP whose
daimons project the relevant findings (filed for federation
forwarding as task #155).

### Installing the library

```bash
# From a logged-in session cookie ($COOKIES) on the CP at $CP_URL
for f in examples/orchestrations/t1-*.md examples/orchestrations/t2-*.md; do
  body=$(python3 -c "import json,sys; print(json.dumps({'spec_yaml': open('$f').read()}))")
  curl -sk -b "$COOKIES" -X POST "$CP_URL/api/orchestrations" \
    -H 'Content-Type: application/json' -d "$body"
done
```

Or copy-paste a YAML into the visual editor's YAML mode and click
Save. The editor will round-trip cleanly back to the canvas.

## CLI / API summary

| Endpoint | Description |
|---|---|
| `GET /api/orchestrations` | List (federated when on parent CP) |
| `GET /api/orchestrations/{id}` | Detail |
| `POST /api/orchestrations` | Create — body: `{spec_yaml, target_cp_instance_id?}` |
| `PUT /api/orchestrations/{id}` | Update spec (accepts `?cp=` to edit federated row) |
| `DELETE /api/orchestrations/{id}` | Delete |
| `POST /api/orchestrations/{id}/run` | Start a manual run with `{inputs: {...}}` body |
| `GET /api/orchestration-runs` | History (federated) |
| `GET /api/orchestration-runs/{id}` | Run detail with steps |
| `POST /api/orchestration-runs/{id}/cancel` | Cancel an in-flight run |
| `POST /api/orchestration-runs/{id}/steps/{stepID}/approve` | Approve a gated step |

All read endpoints accept `?cp=<instance_id>` to proxy to a specific
child CP. The list endpoints federate by default on the parent.

## Phase status

| Phase | Scope | Status |
|---|---|---|
| **A** | Sequential local execution, manual trigger, approval gates, basic API. | shipped |
| **B** | Federation — `cp: <child>` step dispatch + cross-CP CRUD. | shipped |
| **C** | DAG-shaped run-detail canvas + visual authoring canvas with palette + inspector. | shipped |
| **D** | Finding triggers, cron triggers, multi-node `nodes:` fan-out. | shipped |
| **E** (future) | True parallel DAG execution, fan-in nodes, conditional branches with multiple downstream paths, `cp: "*"` fan-out, role/os node selectors. | planned |

## Cross-fleet IOC enrichment (Phase 22.4)

When you want to enrich an IOC against threat intelligence vendors, emit an `enrich_ioc` action with the IOC id from the `iocs` table. The applier fans out to VirusTotal, AbuseIPDB, and Shodan according to the IOC's kind, caches results in `ioc_enrichments` (24h TTL by default), and silently skips any adapter whose API key isn't configured.

The **cross-CP IOC pattern supervisor** (`agents/cross-cp-ioc-pattern-supervisor.md`) is a daimon that ticks every 5 minutes on the parent CP. It queries `GET /api/iocs/cross-cp-patterns?min_observations=N&window_hours=H` and emits a finding for each IOC that exceeds the configured observation threshold across the fleet. Operators can tune `min_observations` and `window_hours` in the daimon's spec to control sensitivity.
