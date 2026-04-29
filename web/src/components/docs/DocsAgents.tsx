// Agents authoring reference. Agents are the on-demand, single-shot
// counterparts to daimons — same Markdown-frontmatter shape but no
// schedule. Operators (or orchestration steps) invoke them once and
// the run terminates.

import { Link } from 'react-router-dom';
import {
  DocsContainer, DocsSection, DocsCallout, DocsCode,
  DocsTable, DocsTableRow, DocsTableCell,
} from './DocsLayout';

export function DocsAgents() {
  return (
    <DocsContainer>
      <p className="text-ink-dim">
        An agent is a single-shot Markdown definition: prompt, tools, model preference. The CP
        invokes it via <code>okesu auto --agent &lt;name&gt;</code> on a target host (or on the CP
        itself for cp-local orchestration steps). The run streams JSONL output back to the CP and
        terminates.
      </p>

      <DocsCallout tone="tip">
        The shipped agents (investigator, incident-responder, threat-hunter, …) live in{' '}
        <Link to="/agents" className="text-brand-700 underline">Agents → Library</Link>. They're
        the canonical examples for what works.
      </DocsCallout>

      <DocsSection title="File shape" anchor="shape">
        <DocsCode language="yaml">{`---
name: investigator
description: |
  Triage agent for orchestration steps. Reads structured CP data,
  decides what to do, and emits an orchestration_result with a
  list of actions for the engine to apply.

provider: claude
model: claude-sonnet-4-6
effort: medium
maxTurns: 8

tools:
  - read_file
  - bash
  - search

# Agents do NOT declare mode/interval/management/outputs — those
# fields are daimon-only.
---

You are a triage investigator. The orchestration step has already
fetched the data you need and bound it into this prompt. Your job
is to reason about it and emit an orchestration_result …`}</DocsCode>
      </DocsSection>

      <DocsSection title="Difference from daimons" anchor="vs-daimons">
        <p>Same syntax, different fields. Agents omit:</p>
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>field</DocsTableCell>
            <DocsTableCell head>why agents skip it</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>mode</code> / <code>interval</code> / <code>cron</code></DocsTableCell>
              <DocsTableCell>One-shot — the orchestration engine (or operator) drives invocation timing.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>collectors</code></DocsTableCell>
              <DocsTableCell>Use the orchestration's <code>data:</code> block instead — engine fetches data once and binds into the prompt.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>outputs</code></DocsTableCell>
              <DocsTableCell>Output streams to stdout for the orchestrator to capture; no daimon-style sinks.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>management</code></DocsTableCell>
              <DocsTableCell>No long-running registration; runs are short-lived.</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
        <p>Agents keep:</p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><code>name</code>, <code>description</code></li>
          <li><code>provider</code>, <code>model</code>, <code>effort</code>, <code>maxTurns</code></li>
          <li><code>tools</code></li>
          <li>The system prompt body (after the closing <code>---</code>)</li>
        </ul>
      </DocsSection>

      <DocsSection title="Tools" anchor="tools">
        <p>The standard tool surface available to every agent:</p>
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>tool</DocsTableCell>
            <DocsTableCell head>purpose</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>read_file</code></DocsTableCell>
              <DocsTableCell>Read a file from the host the agent runs on</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>write_file</code></DocsTableCell>
              <DocsTableCell>Write a file. Use sparingly in observe-only agents.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>list_files</code></DocsTableCell>
              <DocsTableCell>Directory listing</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>search</code></DocsTableCell>
              <DocsTableCell>grep across files (faster than bash + grep)</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>bash</code></DocsTableCell>
              <DocsTableCell>Arbitrary shell. Powerful — keep narrow when used in production daimons.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>lookup_findings</code></DocsTableCell>
              <DocsTableCell>(Daimon-only) Query the CP for prior findings via mTLS — auto-included when management plane is configured.</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
      </DocsSection>

      <DocsSection title="Orchestration integration — the action protocol" anchor="actions">
        <p>
          When invoked from an orchestration step, the agent communicates back via a single
          <strong> orchestration_result finding</strong> — a one-line JSON object as the last
          assistant output. The engine parses it and applies the requested actions.
        </p>
        <DocsCode language="json">{`{
  "type": "finding",
  "category": "orchestration_result",
  "title": "classified: noise",
  "severity": "INFO",
  "attributes": {
    "verdict": "noise",
    "reasoning": "fired on 12 hosts in 30min, scanner pattern",
    "actions": [
      { "kind": "update_finding_status",  "finding_id": 245, "status": "false_positive" },
      { "kind": "set_finding_severity_override", "finding_id": 245, "severity": "INFO" },
      { "kind": "add_finding_tag",  "finding_id": 245, "tag": "auto-triaged-noise" },
      { "kind": "link_run_to_finding", "finding_id": 245 }
    ]
  }
}`}</DocsCode>
        <DocsCallout tone="warn">
          Emit this as a single JSON object on its own line — <strong>not</strong> wrapped in a
          markdown code block. The engine looks for the strict
          <code>{`{"type":"finding",…}`}</code> shape on stdout. There's a fallback that scans
          assistant text for a JSON object containing <code>actions</code> or <code>verdict</code>,
          but the strict path keeps the audit trail clean.
        </DocsCallout>
      </DocsSection>

      <DocsSection title="System prompt — what to teach the agent" anchor="prompt">
        <p>The investigator-style system prompt has four sections that pull their weight:</p>
        <ol className="list-decimal ml-5 space-y-1.5">
          <li><strong>Identity + scope.</strong> "You are a triage investigator." Keep it one sentence — the model already knows.</li>
          <li><strong>Inputs description.</strong> Which fields the orchestration will template in. The agent doesn't have to ask.</li>
          <li><strong>Decision rules.</strong> Heuristics for the verdict, action mapping (verdict → which actions to request).</li>
          <li><strong>Output schema.</strong> The exact JSON the engine will parse. Be strict.</li>
        </ol>
        <DocsCallout tone="info">
          Avoid putting fetched data <em>in the prompt body</em>. Use the orchestration's{' '}
          <code>data:</code> block — the engine binds the result into <code>{`{{data.X}}`}</code>{' '}
          template variables and persists a snapshot for replay.
        </DocsCallout>
      </DocsSection>

      <DocsSection title="Provider + model selection" anchor="provider">
        <p>
          Agents run via the same <code>okesu auto</code> CLI as a developer's local agent. The{' '}
          <code>provider:</code> + <code>model:</code> fields decide which SDK is used:
        </p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><code>provider: claude</code> + an <code>ANTHROPIC_API_KEY</code> in env</li>
          <li><code>provider: codex</code> + an <code>OPENAI_API_KEY</code> in env</li>
          <li>If <code>provider:</code> is omitted, the model name's prefix decides (claude-* vs gpt-*/o1-*)</li>
        </ul>
        <p>
          For orchestration steps, keys come from the CP's <code>--fleet-anthropic-api-key</code>{' '}
          (cp-local execution) or from the node's <code>/etc/okesu/jobs.env</code> (tunnel/jobs
          execution). Operators don't have to thread keys per-step.
        </p>
      </DocsSection>
    </DocsContainer>
  );
}
