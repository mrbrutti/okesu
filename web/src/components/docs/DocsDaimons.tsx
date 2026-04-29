// Daimons authoring reference. Daimons are the long-running scheduled
// agents — they live on a host, tick on an interval, and post findings
// + heartbeats back to the CP via the management plane.

import { Link } from 'react-router-dom';
import {
  DocsContainer, DocsSection, DocsCallout, DocsCode,
  DocsTable, DocsTableRow, DocsTableCell,
} from './DocsLayout';

export function DocsDaimons() {
  return (
    <DocsContainer>
      <p className="text-ink-dim">
        A daimon is an agent definition with a schedule. The CP installs it on a node, the local
        runtime ticks it every <code>interval</code>, and findings stream back via the webhook
        sink. Same Markdown-frontmatter shape as Agents — the difference is the <code>mode</code>{' '}
        and <code>interval</code> fields, plus the management-plane configuration.
      </p>

      <DocsCallout tone="tip">
        Open the <Link to="/daimons?tab=library" className="text-brand-700 underline">Daimons → Library</Link> tab to see
        the shipped daimons (edr, instance-integrity, instance-threat, sre-health, …). Each is a
        complete worked example.
      </DocsCallout>

      <DocsSection title="File shape" anchor="shape">
        <p>
          A daimon is a <code>.md</code> file. The YAML frontmatter declares identity, schedule,
          tools, collectors, outputs, and management plane config. The body after the frontmatter
          is the system prompt the LLM sees on every tick.
        </p>
        <DocsCode language="yaml">{`---
name: edr
version: "1"
description: |
  Endpoint Detection & Response agent for Linux cloud hosts.
  Runs every 5 minutes, collects process/network/file telemetry,
  and uses an LLM to triage anomalies.

provider: claude
model: claude-mythos-preview
effort: low
maxTurns: 20

mode: daemon
interval: 5m
overlap: skip

stateDir: /var/lib/okesu/edr
dedupeTtl: 1h

tools:
  - read_file
  - write_file
  - list_files
  - search
  - bash

collectors:
  - name: processes
    command: "ps aux --no-headers --sort=-%cpu | head -60"
    timeout: 5s
  # ...

outputs:
  - type: stdout
  - type: file
    path: /var/log/okesu/edr.jsonl
  - type: webhook
    url: "\${OKESU_WEBHOOK_URL}"
    secret: "\${OKESU_WEBHOOK_SECRET}"

management:
  url: "\${OKESU_MGMT_URL}"
  certDir: /etc/okesu
  heartbeatSec: 60
  pollSec: 300
---

You are an Endpoint Detection and Response (EDR) agent running on
host {{.HostID}} in region {{.CloudRegion}}.

## Telemetry collected this tick
{{range .CollectorsList -}}
### {{.Name}}
\`\`\`
{{.Output}}
\`\`\`
{{end}}

## Your task
Analyze the telemetry above for security anomalies …`}</DocsCode>
      </DocsSection>

      <DocsSection title="Identity + provider" anchor="identity">
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>field</DocsTableCell>
            <DocsTableCell head>purpose</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>name</code></DocsTableCell>
              <DocsTableCell>Slug; matches the file name and is what operators reference everywhere.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>version</code></DocsTableCell>
              <DocsTableCell>String. Bumped when you change behaviour so the CP can detect drift on running daimons.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>provider</code></DocsTableCell>
              <DocsTableCell><code>claude</code> or <code>codex</code>. Picks the SDK + API key.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>model</code></DocsTableCell>
              <DocsTableCell>Model id. Faster + cheaper for tick-heavy daimons (claude-haiku, gpt-4o-mini).</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>effort</code></DocsTableCell>
              <DocsTableCell><code>low</code> | <code>medium</code> | <code>high</code> | <code>xhigh</code>. Reasoning depth budget.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>maxTurns</code></DocsTableCell>
              <DocsTableCell>Hard cap on agentic loop iterations per tick. <code>20</code> is generous; <code>8</code> is normal.</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
      </DocsSection>

      <DocsSection title="Schedule" anchor="schedule">
        <DocsTable>
          <thead><DocsTableRow>
            <DocsTableCell head>field</DocsTableCell>
            <DocsTableCell head>purpose</DocsTableCell>
          </DocsTableRow></thead>
          <tbody>
            <DocsTableRow>
              <DocsTableCell><code>mode</code></DocsTableCell>
              <DocsTableCell>Always <code>daemon</code>. The other modes are for one-shot agents.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>interval</code></DocsTableCell>
              <DocsTableCell>Tick spacing. Use a Go duration: <code>30s</code>, <code>2m</code>, <code>1h</code>.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>cron</code></DocsTableCell>
              <DocsTableCell>5-field cron expression — alternative to <code>interval</code> when you need wall-clock alignment.</DocsTableCell>
            </DocsTableRow>
            <DocsTableRow>
              <DocsTableCell><code>overlap</code></DocsTableCell>
              <DocsTableCell><code>skip</code> (default) or <code>queue</code>. Behaviour when a previous tick is still running.</DocsTableCell>
            </DocsTableRow>
          </tbody>
        </DocsTable>
      </DocsSection>

      <DocsSection title="Collectors" anchor="collectors">
        <p>
          Pre-tick shell commands whose output is injected into the system prompt as{' '}
          <code>{`{{.CollectorsList}}`}</code>. Run in parallel; failed/optional ones don't abort
          the tick.
        </p>
        <DocsCode language="yaml">{`collectors:
  - name: processes
    command: "ps aux --no-headers --sort=-%cpu | head -60"
    timeout: 5s
  - name: memfd_procs
    command: |
      ls -la /proc/*/exe 2>/dev/null \\
        | grep -E 'deleted|memfd|/dev/(shm|null)'
    timeout: 5s
    optional: true   # skip silently if it errors`}</DocsCode>
      </DocsSection>

      <DocsSection title="Outputs" anchor="outputs">
        <p>One or more sinks the daimon's events stream to:</p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><strong>stdout</strong> — picked up by journald via systemd</li>
          <li><strong>file</strong> — rotating JSONL log; <code>maxBytes</code> controls rotation</li>
          <li>
            <strong>webhook</strong> — POSTs HMAC-SHA256-signed batches to the CP. The CP's webhook
            endpoint verifies the signature, parses the events, and writes findings to the queue.
          </li>
        </ul>
        <DocsCallout tone="info">
          The webhook secret + URL are usually injected via env at install time
          (<code>{`\${OKESU_WEBHOOK_URL}`}</code>) so the same daimon file works across multiple CPs.
        </DocsCallout>
      </DocsSection>

      <DocsSection title="Management plane" anchor="mgmt">
        <p>
          Daimons register themselves with the CP at boot via mTLS, then heartbeat + poll for
          config updates. The CP can hot-reload a daimon's prompt by changing the markdown body —
          the daimon's hash check sees drift and pulls the new definition without restarting.
        </p>
        <DocsCode language="yaml">{`management:
  url: "\${OKESU_MGMT_URL}"
  certDir: /etc/okesu      # must contain client.crt, client.key, ca.crt
  heartbeatSec: 60
  pollSec: 300`}</DocsCode>
      </DocsSection>

      <DocsSection title="System prompt" anchor="prompt">
        <p>
          The body after the YAML frontmatter is the system prompt — everything below the closing{' '}
          <code>---</code>. Go template variables exposed:
        </p>
        <ul className="list-disc ml-5 space-y-1.5">
          <li><code>{`{{.HostID}}`}</code>, <code>{`{{.CloudRegion}}`}</code> — environment</li>
          <li><code>{`{{.AgentName}}`}</code>, <code>{`{{.Tick}}`}</code>, <code>{`{{.TickTime}}`}</code> — run context</li>
          <li><code>{`{{.LastRunISO}}`}</code>, <code>{`{{.LastRunFile}}`}</code> — for since-time queries inside collectors</li>
          <li><code>{`{{range .CollectorsList}}`}</code> — iterate collected telemetry</li>
          <li><code>{`{{.StateDir}}`}</code> — where to write findings</li>
        </ul>
        <p>
          The prompt should always include a <strong>self-criticism block</strong> (novelty,
          concreteness, actionability, severity calibration) before the schema. Operators read the
          findings; daimons should ask "would I act on this?" before emitting one.
        </p>
      </DocsSection>

      <DocsSection title="Finding schema" anchor="finding">
        <p>The agent emits findings as JSON written to <code>{`{{.StateDir}}/findings/{{.TickTime}}.json`}</code>:</p>
        <DocsCode language="json">{`{
  "severity": "CRITICAL|HIGH|MEDIUM|LOW|INFO",
  "title": "Stable, descriptive title — same across ticks for the same issue",
  "resource": "k:v[, k:v]* (e.g. pid:1337, binary:/usr/bin/foo)",
  "evidence": ["exact lines from telemetry or tool output"],
  "recommended_action": "What an operator should do next",
  "dedup_key": "stable key — must NOT change between ticks for the same issue",

  "category": "process|file|network|cert|cloud|identity|config|other",
  "process_pid": 1337,
  "process_name": "python3",
  "path": "/etc/cron.d/maintenance",
  "network_endpoint": "10.0.0.5:443",
  "cve": "CVE-2024-12345",
  "tags": ["memfd", "rce", "persistence"],
  "attributes": { "anything": "domain-specific extras" }
}`}</DocsCode>
        <DocsCallout tone="warn">
          <code>title</code> + <code>dedup_key</code> must be stable across ticks for the same
          underlying issue. If they drift, the CP can't dedupe and the operator queue floods.
        </DocsCallout>
      </DocsSection>
    </DocsContainer>
  );
}
