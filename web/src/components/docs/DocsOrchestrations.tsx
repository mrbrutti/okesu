// Orchestrations authoring reference. Mirrors the structure operators
// see in the Library editor — frontmatter, then steps — and lays out
// the language features (trigger, data:, actions:, when:, fan-out)
// in the order an author actually meets them.

import { Link } from 'react-router-dom';
import {
  DocsContainer, DocsSection, DocsCallout, DocsCode,
  DocsTable, DocsTableRow, DocsTableCell,
} from './DocsLayout';

export function DocsOrchestrations() {
  return (
    <DocsContainer>
      <p className="text-ink-dim">
        An orchestration is a YAML spec that chains agent runs together. Each step's output is
        bound into the next step's prompt template, and CP-side mutations (status changes, tags,
        severity overrides) are requested through a structured action protocol so the engine —
        not the agent — owns persistence.
      </p>

      <DocsCallout tone="tip">
        The fastest way to learn the format is to open an existing one in the{' '}
        <Link to="/orchestrations" className="text-brand-700 underline">Library</Link>{' '}
        — every shipped example (t1-finding-batch-triage, t2-fleet-ioc-hunt, …) is a complete,
        annotated reference.
      </DocsCallout>

      <DocsSection title="File shape" anchor="shape">
        <p>
          An orchestration is a markdown file whose YAML frontmatter is the spec. Anything after
          the closing <code>---</code> is a notes block — the engine ignores it; operators see it
          rendered on the spec's library detail panel.
        </p>
        <DocsCode language="yaml">{`---
name: my-orchestration
description: One-line description of what this chain does.

trigger:
  on: cron
  cron: "*/5 * * * *"

defaults:
  timeout: 5m

steps:
  - id: scan
    agent: investigator
    data:
      findings:
        query: findings.list
        params: { state: queue, severity: [INFO, LOW], limit: 50 }
    prompt: |
      Classify {{data.findings | length}} findings.
      {{data.findings | json}}
---

# Notes (operator-facing; ignored by the engine)
This orchestration runs every 5 minutes …`}</DocsCode>
      </DocsSection>

      <DocsSection title="Top-level fields" anchor="top-level">
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>field</DocsTableCell>
            <DocsTableCell head>required</DocsTableCell>
            <DocsTableCell head>purpose</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>name</code></DocsTableCell>
              <DocsTableCell>yes</DocsTableCell>
              <DocsTableCell>slug-shaped (<code>[a-z][a-z0-9-]*[a-z0-9]</code>); used as the URL handle</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>description</code></DocsTableCell>
              <DocsTableCell>yes</DocsTableCell>
              <DocsTableCell>One-line human summary; shown in the library list</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>trigger</code></DocsTableCell>
              <DocsTableCell>no</DocsTableCell>
              <DocsTableCell>How runs auto-fire. Defaults to <code>manual</code> (operator-clicked only)</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>inputs</code></DocsTableCell>
              <DocsTableCell>no</DocsTableCell>
              <DocsTableCell>Operator-supplied parameters; bound as <code>{`{{inputs.X}}`}</code></DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>defaults</code></DocsTableCell>
              <DocsTableCell>no</DocsTableCell>
              <DocsTableCell>Per-step defaults: <code>timeout</code>, <code>continue_on_error</code>, <code>cp</code>, <code>dispatch</code></DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>steps</code></DocsTableCell>
              <DocsTableCell>yes</DocsTableCell>
              <DocsTableCell>Ordered list. Linear in v1 (each step waits for the previous)</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
      </DocsSection>

      <DocsSection title="trigger" anchor="trigger">
        <p>Three trigger kinds are supported:</p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><strong>manual</strong> — only fires when an operator clicks "Run". The default.</li>
          <li><strong>finding</strong> — fires whenever a new finding lands. Requires a <code>filter</code> expression: <code>{`finding.severity in ['HIGH','CRITICAL']`}</code>. Filter sees <code>finding.{`{severity,title,agent,host,category,dedup_key,resource,attributes}`}</code>.</li>
          <li><strong>cron</strong> — fires on schedule. Requires a 5-field <code>cron</code> string: <code>"*/5 * * * *"</code>.</li>
        </ul>
      </DocsSection>

      <DocsSection title="steps[]" anchor="steps">
        <p>Each step describes one agent run. Fields on a step:</p>
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>field</DocsTableCell>
            <DocsTableCell head>purpose</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>id</code></DocsTableCell>
              <DocsTableCell>Unique within the orchestration. Used as <code>{`{{step_id.result.X}}`}</code> from later steps.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>agent</code></DocsTableCell>
              <DocsTableCell>Name of an agent in the Library (<code>investigator</code>, <code>incident-responder</code>, …)</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>node</code> / <code>nodes</code></DocsTableCell>
              <DocsTableCell>Where to run. Single host (<code>node:</code>) or fan-out (<code>nodes:</code>). Omit both to run on the CP itself (cp-local).</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>prompt</code></DocsTableCell>
              <DocsTableCell>Templated string fed to the agent. Reads <code>trigger.*</code>, <code>inputs.*</code>, <code>data.*</code>, and earlier <code>{`{step_id}`}.result.*</code> bindings.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>data</code></DocsTableCell>
              <DocsTableCell>See "data block" below — declarative reads bound into the prompt.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>actions</code></DocsTableCell>
              <DocsTableCell>Allowlist of CP-side mutations the agent may request. See "actions" below.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>when</code></DocsTableCell>
              <DocsTableCell>Boolean expression — step is skipped if false. Same template language as <code>prompt</code>.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>approval</code></DocsTableCell>
              <DocsTableCell><code>required</code> pauses the run until an operator clicks Approve.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>timeout</code></DocsTableCell>
              <DocsTableCell>Per-step override of <code>defaults.timeout</code>. Format: <code>5m</code>, <code>30s</code>, <code>1h</code>.</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
      </DocsSection>

      <DocsSection title="data: block — server-side reads" anchor="data">
        <p>
          Step <code>data:</code> declares structured reads the engine should perform <em>before</em>{' '}
          dispatching the step. Each entry becomes a <code>{`{{data.<name>}}`}</code> binding
          available to the prompt + when expression. The engine handles auth, rate limiting, and
          audit; the agent's prompt stays focused on the reasoning.
        </p>
        <DocsCode language="yaml">{`data:
  findings:
    query: findings.list
    params:
      state: queue
      severity: [INFO, LOW]
      limit: 50
  summary:
    query: findings.summary`}</DocsCode>
        <p>The shipped query handlers:</p>
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>query</DocsTableCell>
            <DocsTableCell head>params</DocsTableCell>
            <DocsTableCell head>returns</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>findings.list</code></DocsTableCell>
              <DocsTableCell><code>state</code> (queue|open|acked|all), <code>severity</code> (list), <code>agent</code>, <code>host</code>, <code>category</code>, <code>tag</code>, <code>since_ms</code>, <code>until_ms</code>, <code>limit</code>, <code>offset</code></DocsTableCell>
              <DocsTableCell>Array of finding rows</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>findings.summary</code></DocsTableCell>
              <DocsTableCell>(none)</DocsTableCell>
              <DocsTableCell>Per-CP rollup: by severity / agent / category, last-24h trend</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>findings.history</code></DocsTableCell>
              <DocsTableCell><code>finding_id</code> (required)</DocsTableCell>
              <DocsTableCell>Edit history for a single finding</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>orchestration-runs.list</code></DocsTableCell>
              <DocsTableCell><code>status</code> (list), <code>trigger_kind</code> (list), <code>since</code> (relative), <code>since_ms</code>, <code>limit</code>, <code>offset</code></DocsTableCell>
              <DocsTableCell>Array of run rows</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>nodes.list</code></DocsTableCell>
              <DocsTableCell><code>limit</code>, <code>offset</code></DocsTableCell>
              <DocsTableCell>Array of node rows</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>agents.list</code></DocsTableCell>
              <DocsTableCell><code>limit</code>, <code>offset</code></DocsTableCell>
              <DocsTableCell>Array of (daemon agent, host) rows</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
        <DocsCallout tone="tip">
          The engine persists each step's resolved data on the run record (<code>data_snapshot</code>),
          so a run can be replayed or audited against the exact input the agent saw — even after
          the underlying tables have moved on.
        </DocsCallout>
      </DocsSection>

      <DocsSection title="actions: block — agent-requested mutations" anchor="actions">
        <p>
          Steps that should be allowed to modify CP state declare an <code>actions:</code> allowlist.
          The agent emits an <code>orchestration_result</code> finding with an <code>actions[]</code>{' '}
          array; the engine cross-checks each entry's <code>kind</code> against the allowlist and
          applies in order. Empty allowlist = the step can read but cannot write.
        </p>
        <DocsCode language="yaml">{`actions:
  - update_finding_status
  - set_finding_severity_override
  - add_finding_tag
  - link_run_to_finding`}</DocsCode>
        <p>Action kinds:</p>
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>kind</DocsTableCell>
            <DocsTableCell head>payload</DocsTableCell>
            <DocsTableCell head>effect</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>update_finding_status</code></DocsTableCell>
              <DocsTableCell><code>{`{ finding_id, status, reason? }`}</code></DocsTableCell>
              <DocsTableCell>Sets triage state: open|acknowledged|investigating|resolved|false_positive|wontfix|suppressed</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>add_finding_tag</code></DocsTableCell>
              <DocsTableCell><code>{`{ finding_id, tag, reason? }`}</code></DocsTableCell>
              <DocsTableCell>Idempotent tag append</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>remove_finding_tag</code></DocsTableCell>
              <DocsTableCell><code>{`{ finding_id, tag, reason? }`}</code></DocsTableCell>
              <DocsTableCell>Tag drop; no-op when absent</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>set_finding_severity_override</code></DocsTableCell>
              <DocsTableCell><code>{`{ finding_id, severity, reason? }`}</code></DocsTableCell>
              <DocsTableCell>Operator-severity override (CRITICAL|HIGH|MEDIUM|LOW|INFO)</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>link_run_to_finding</code></DocsTableCell>
              <DocsTableCell><code>{`{ finding_id, reason? }`}</code></DocsTableCell>
              <DocsTableCell>Records this run as having handled the finding (audit trail)</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>escalate</code></DocsTableCell>
              <DocsTableCell><code>{`{ reason, severity? }`}</code></DocsTableCell>
              <DocsTableCell>Soft signal that on-call should review the run</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
      </DocsSection>

      <DocsSection title="Templating" anchor="templates">
        <p>
          Strings (<code>prompt</code>, <code>node</code>, <code>when</code>) accept{' '}
          <code>{`{{ … }}`}</code> bindings:
        </p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><code>{`{{trigger.host}}`}</code> — for finding triggers, the host where the finding fired</li>
          <li><code>{`{{trigger.finding_id}}`}</code> — the finding's id</li>
          <li><code>{`{{inputs.foo}}`}</code> — operator-supplied input declared in <code>inputs:</code></li>
          <li><code>{`{{data.findings}}`}</code> — resolved <code>data:</code> block; pipe through filters</li>
          <li><code>{`{{step_id.result.field}}`}</code> — earlier step's <code>orchestration_result</code> attribute</li>
        </ul>
        <p>Filters work like Jinja: <code>{`{{data.findings | json}}`}</code>, <code>{`{{data.findings | length}}`}</code>, <code>{`{{step.output | tail(50)}}`}</code>.</p>
      </DocsSection>

      <DocsSection title="Where each step runs" anchor="dispatch">
        <p>The engine routes each step based on its target:</p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><code>node:</code> set → dispatched to that host via reverse-tunnel or jobs runtime, depending on what's connected</li>
          <li><code>nodes:</code> set → fan-out, one dispatch per host, results aggregated as <code>step.result.byNode</code></li>
          <li>Neither set → cp-local; the engine spawns <code>okesu auto --agent &lt;name&gt;</code> on the CP host. Use this for cron orchestrations that just read/write CP state.</li>
        </ul>
        <DocsCallout tone="warn">
          Cp-local steps run with the CP's environment + API keys. They can call any registered{' '}
          <code>data:</code> query without auth, so keep their <code>actions:</code> allowlist tight —
          a runaway batch step with <code>update_finding_status</code> permission will happily
          cancel a thousand findings in one shot.
        </DocsCallout>
      </DocsSection>

      <DocsSection title="Worked example: batch triage" anchor="example">
        <p>
          The cron orchestration that classifies the operator queue every 5 minutes. Notice how
          short the prompt is — almost all the heavy lifting is in <code>data:</code> +{' '}
          <code>actions:</code>, not in agent plumbing.
        </p>
        <DocsCode language="yaml">{`---
name: t1-finding-batch-triage
description: Tier-1 batched auto-triage of the operator queue.
trigger:
  on: cron
  cron: "*/5 * * * *"
defaults:
  timeout: 5m
steps:
  - id: classify_batch
    agent: investigator
    data:
      findings:
        query: findings.list
        params: { state: queue, severity: [INFO, LOW], limit: 50 }
      summary:
        query: findings.summary
    actions:
      - update_finding_status
      - set_finding_severity_override
      - add_finding_tag
      - link_run_to_finding
    prompt: |
      Batch-classify {{data.findings | length}} findings.

      Findings: {{data.findings | json}}
      Cluster context: {{data.summary | json}}

      For each finding, emit one verdict (noise|confirmed|unknown)
      and request the matching actions[] entries — one orchestration_result
      finding covers the whole batch.
---`}</DocsCode>
      </DocsSection>
    </DocsContainer>
  );
}
