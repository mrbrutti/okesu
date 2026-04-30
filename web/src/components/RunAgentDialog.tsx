// RunAgentDialog — case-context "run an agent" flow.
//
// Lifted from the previous finding-side InvestigateDialog. The
// difference is the prompt is synthesized from the *case* (title +
// summary + linked findings/hosts/IOCs) rather than a single
// finding, and the resulting run is auto-linked to the case.
//
// Templates:
//   default — generic "investigate this case" prompt
//   triage  — narrow at the most-recent / highest-severity finding
//   contain — write a containment plan
//
// v1 ships these three; the dropdown is extensible from a static
// template array. Free-form prompt remains the operator escape hatch.

import { useEffect, useMemo, useState } from 'react';
import { Loader2, Play, Sparkles, X } from 'lucide-react';
import {
  api,
  type AgentLibraryItem,
  type Investigation,
  type InvestigationDetail,
  type InvestigationFindingItem,
} from '../api';
import MarkdownEditor from './LazyMarkdownEditor';

const PREF_LAST_AGENT = 'okesu.case.run-agent.last_agent';
const PREF_LAST_NODE  = 'okesu.case.run-agent.last_node';

type TemplateKind = 'default' | 'triage' | 'contain' | 'custom';

const TEMPLATES: { value: TemplateKind; label: string; help: string }[] = [
  { value: 'default', label: 'Default — investigate the case',  help: 'Read context, hit the host(s), confirm or refute the case hypothesis.' },
  { value: 'triage',  label: 'Triage — narrow on top finding',  help: 'Focus on the most-recent / highest-severity linked finding.' },
  { value: 'contain', label: 'Contain — draft response plan',   help: 'Write an isolation + remediation plan, no execution.' },
  { value: 'custom',  label: 'Custom prompt',                   help: 'Free-form. The case context is still appended automatically.' },
];

export function RunAgentDialog({
  bundle,
  cpInstanceID,
  onClose,
  onLaunched,
}: {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
  onClose: () => void;
  /** Caller refreshes the workspace bundle when the run is linked. */
  onLaunched: (runID: string) => void;
}) {
  const [agents, setAgents] = useState<AgentLibraryItem[]>([]);
  const [nodes, setNodes] = useState<string[]>([]);
  const [agent, setAgent] = useState<string>(() => localStorage.getItem(PREF_LAST_AGENT) ?? '');
  const [node, setNode] = useState<string>(() => localStorage.getItem(PREF_LAST_NODE) ?? '');
  const [template, setTemplate] = useState<TemplateKind>('default');
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Hosts touched by linked findings — surfaced first in the node
  // picker so the operator usually doesn't have to think about
  // which node to target.
  const linkedHosts = useMemo(() => {
    const seen = new Set<string>();
    for (const f of bundle.findings) {
      if (f.Host && f.Host.Valid && f.Host.String) {
        seen.add(f.Host.String);
      }
    }
    return Array.from(seen);
  }, [bundle.findings]);

  const synthesizedPrompt = useMemo(
    () => buildPrompt(bundle.investigation, bundle.findings, template),
    [bundle.investigation, bundle.findings, template],
  );

  useEffect(() => {
    if (template !== 'custom') setPrompt(synthesizedPrompt);
  }, [synthesizedPrompt, template]);

  useEffect(() => {
    api.agentLibrary().then(setAgents).catch(() => setAgents([]));
    api.connectedNodes().then(setNodes).catch(() => setNodes([]));
  }, []);

  // Default node: the first linked host that's also tunnel-connected,
  // else the first connected node, else operator's last choice.
  useEffect(() => {
    if (node) return;
    if (!nodes.length) return;
    const linkedAndConnected = linkedHosts.find((h) => nodes.includes(h));
    setNode(linkedAndConnected ?? nodes[0]);
  }, [nodes, linkedHosts, node]);

  async function launch() {
    if (!agent || !node || !prompt.trim()) {
      setError('agent, node, and prompt are required');
      return;
    }
    setBusy(true); setError(null);
    try {
      const { run_id } = await api.createRun({
        node,
        agent,
        prompt,
        // The run carries no finding_id — the case is the parent
        // entity. We auto-link via api.investigations.linkRun
        // immediately after the run starts so the workspace's Runs
        // tab reflects it on the next refresh.
        target_cp_instance_id: cpInstanceID,
      });
      // run_id from createRun is a string — orchestration_runs.id is
      // numeric, but ad-hoc runs/{id} uses string. linkRun expects
      // the orchestration_runs numeric id; we don't have one for
      // ad-hoc runs. For now, the auto-linkage on the run-finding
      // edge in PR G covers the case where the agent emits findings
      // referencing the case; ad-hoc runs without finding emissions
      // won't auto-link. Operator can manually link from the Runs
      // tab if needed.
      localStorage.setItem(PREF_LAST_AGENT, agent);
      localStorage.setItem(PREF_LAST_NODE, node);
      onLaunched(run_id);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl flex flex-col"
        onClick={(e) => e.stopPropagation()}
        style={{ maxHeight: '85vh' }}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Sparkles size={14} className="text-brand-500" />
            Run agent on case
          </h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>

        <div className="flex-1 overflow-auto p-5 space-y-3 text-sm">
          <p className="text-xs text-ink-dim">
            Picks an agent from the Agent Library and runs it on the chosen node with the case's
            context pre-filled. The case is{' '}
            <strong>{bundle.investigation.Title || `#${bundle.investigation.ID}`}</strong> with{' '}
            {bundle.findings.length} linked finding{bundle.findings.length === 1 ? '' : 's'}
            {linkedHosts.length > 0 && <> across {linkedHosts.length} host{linkedHosts.length === 1 ? '' : 's'}</>}.
          </p>

          <Field label="Template">
            <select
              value={template}
              onChange={(e) => setTemplate(e.target.value as TemplateKind)}
              className={inputCls}
            >
              {TEMPLATES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
            <p className="mt-1 text-[11px] text-ink-mute">{TEMPLATES.find((t) => t.value === template)?.help}</p>
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label="Agent">
              <select value={agent} onChange={(e) => setAgent(e.target.value)} className={inputCls}>
                <option value="">Select…</option>
                {agents.map((a) => (
                  <option key={a.name} value={a.name}>
                    {a.name}{a.description ? ` — ${a.description.slice(0, 60)}` : ''}
                  </option>
                ))}
              </select>
              {agents.length === 0 && (
                <p className="mt-1 text-[11px] text-ink-mute">No agents in the library — add one on the Agents page.</p>
              )}
            </Field>
            <Field label="Node">
              <select value={node} onChange={(e) => setNode(e.target.value)} className={inputCls}>
                <option value="">Select…</option>
                {/* Linked-and-connected hosts go first as a usability nicety.
                    Connected-but-not-linked nodes follow as fallback options. */}
                {linkedHosts.filter((h) => nodes.includes(h)).map((h) => (
                  <option key={`linked-${h}`} value={h}>{h} — case host</option>
                ))}
                {nodes.filter((n) => !linkedHosts.includes(n)).map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
              {nodes.length === 0 && (
                <p className="mt-1 text-[11px] text-ink-mute">No connected nodes. Open a tunnel from <code>okesu node</code> first.</p>
              )}
            </Field>
          </div>

          <Field label="Prompt">
            <div className="border border-border rounded-md focus-within:ring-2 focus-within:ring-brand-500/30 overflow-hidden">
              <MarkdownEditor
                value={prompt}
                onChange={(v) => { setPrompt(v); if (template !== 'custom') setTemplate('custom'); }}
                height={220}
                showLineNumbers={false}
                ariaLabel="Run-agent prompt"
              />
            </div>
            {template !== 'custom' && (
              <p className="mt-1 text-[11px] text-ink-mute">
                Switch to <em>Custom prompt</em> to edit, or just start typing — switching is automatic.
              </p>
            )}
          </Field>

          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
        </div>

        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={launch}
            disabled={busy || !agent || !node || !prompt.trim()}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}
            {busy ? 'Launching…' : 'Run agent'}
          </button>
        </footer>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
    </div>
  );
}

const inputCls = 'w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white';

function buildPrompt(
  inv: Investigation,
  findings: InvestigationFindingItem[],
  template: TemplateKind,
): string {
  if (template === 'custom') return '';
  const lines: string[] = [];

  if (template === 'default') {
    lines.push(`Investigate the case below. Read context, hit the host(s) directly, and report what you find.`);
  } else if (template === 'triage') {
    lines.push(`Triage this case. Narrow on the most-recent / highest-severity finding and decide whether it confirms a real issue.`);
  } else if (template === 'contain') {
    lines.push(`Draft a containment + remediation plan for this case. Do NOT execute remediation steps — write the plan only.`);
  }
  lines.push('');

  lines.push(`Case: ${inv.Title || `#${inv.ID}`}`);
  if (inv.Summary) {
    lines.push('');
    lines.push('Summary:');
    lines.push(inv.Summary);
  }

  if (findings.length > 0) {
    lines.push('');
    lines.push(`Linked findings (${findings.length}):`);
    for (const f of findings.slice(0, 10)) {
      const sev = f.Severity?.Valid ? f.Severity.String : '';
      const host = f.Host?.Valid ? f.Host.String : '';
      const title = f.Title?.Valid ? f.Title.String : '(no title)';
      lines.push(`  - [${sev || '—'}] ${title}${host ? ` (host: ${host})` : ''}`);
    }
    if (findings.length > 10) lines.push(`  - …and ${findings.length - 10} more`);
  }

  lines.push('');
  lines.push('Provide a verdict — confirmed real / noise / inconclusive — and the specific evidence supporting your call.');
  return lines.join('\n');
}
