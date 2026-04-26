// Investigate-this-finding dialog. Pre-fills a Run with the finding's
// context, lets the operator pick an agent + node, and on submit kicks
// off a one-shot run linked to the finding (runs.finding_id). The
// resulting transcript shows up in the FindingDrawer's "Investigations"
// section without leaving the page.

import { useEffect, useMemo, useState } from 'react';
import { Loader2, Play, Sparkles, X } from 'lucide-react';
import { api, type AgentLibraryItem, type Finding } from '../api';
import { cn } from '../lib/cn';

const PREF_LAST_AGENT = 'okesu.invest.last_agent';
const PREF_LAST_NODE = 'okesu.invest.last_node';

export function InvestigateDialog({
  finding,
  onClose,
  onLaunched,
}: {
  finding: Finding;
  onClose: () => void;
  onLaunched: (runID: string) => void;
}) {
  const [agents, setAgents] = useState<AgentLibraryItem[]>([]);
  const [nodes, setNodes] = useState<string[]>([]);
  const [agent, setAgent] = useState<string>(() => localStorage.getItem(PREF_LAST_AGENT) ?? '');
  const [node, setNode] = useState<string>(() => localStorage.getItem(PREF_LAST_NODE) ?? '');
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Default prompt — synthesized from the finding so the agent has the
  // context it needs without the operator typing anything. Editable.
  const defaultPrompt = useMemo(() => buildDefaultPrompt(finding), [finding]);

  useEffect(() => {
    setPrompt(defaultPrompt);
  }, [defaultPrompt]);

  useEffect(() => {
    api.agentLibrary().then(setAgents).catch(() => setAgents([]));
    api.connectedNodes().then(setNodes).catch(() => setNodes([]));
  }, []);

  // Default node: prefer the host that emitted the finding if it's connected,
  // else fall back to the operator's last choice or the first connected node.
  useEffect(() => {
    if (node) return;
    if (!nodes.length) return;
    const fromHost = finding.host && nodes.find((n) => n === finding.host);
    setNode(fromHost ?? nodes[0]);
  }, [nodes, finding.host, node]);

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
        finding_id: finding.id,
      });
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
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl max-h-[80vh] flex flex-col">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Sparkles size={14} className="text-brand-500" />
            Investigate finding
          </h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>

        <div className="flex-1 overflow-auto p-5 space-y-3 text-sm">
          <p className="text-xs text-ink-dim">
            Picks an agent from the Agent Library and runs it on the chosen node
            with the finding's context pre-filled. The transcript stays attached
            to this finding.
          </p>

          <div className="grid grid-cols-2 gap-3">
            <Field label="Agent">
              <select
                value={agent}
                onChange={(e) => setAgent(e.target.value)}
                className={inputCls}
              >
                <option value="">Select…</option>
                {agents.map((a) => (
                  <option key={a.name} value={a.name}>
                    {a.name}{a.description ? ` — ${a.description.slice(0, 60)}` : ''}
                  </option>
                ))}
              </select>
              {agents.length === 0 && (
                <p className="mt-1 text-[11px] text-ink-mute">
                  No agents in the library — add one on the Agents page.
                </p>
              )}
            </Field>
            <Field label="Node">
              <select
                value={node}
                onChange={(e) => setNode(e.target.value)}
                className={inputCls}
              >
                <option value="">Select…</option>
                {nodes.map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
              {nodes.length === 0 && (
                <p className="mt-1 text-[11px] text-ink-mute">
                  No connected nodes. Open a tunnel from <code>okesu node</code> first.
                </p>
              )}
            </Field>
          </div>

          <Field label="Prompt">
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={10}
              className={cn(inputCls, 'font-mono text-xs')}
              spellCheck={false}
            />
          </Field>

          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}
        </div>

        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">
            Cancel
          </button>
          <button
            onClick={launch}
            disabled={busy || !agent || !node || !prompt.trim()}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}
            {busy ? 'Launching…' : 'Launch investigation'}
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

function buildDefaultPrompt(f: Finding): string {
  const lines: string[] = [];
  lines.push(`Investigate the following finding:`);
  lines.push('');
  lines.push(`Title: ${f.title || '(untitled)'}`);
  if (f.severity) lines.push(`Severity: ${f.severity}`);
  if (f.agent) lines.push(`Reporting agent: ${f.agent}`);
  if (f.host) lines.push(`Host: ${f.host}`);
  if (f.resource) lines.push(`Resource: ${f.resource}`);
  if (f.category) lines.push(`Category: ${f.category}`);
  if (f.dedup_key) lines.push(`Fingerprint: ${f.dedup_key}`);
  if (f.evidence) {
    lines.push('');
    lines.push('Evidence:');
    lines.push(f.evidence);
  }
  lines.push('');
  lines.push('Look at the host directly to confirm or refute. Provide a short verdict — true positive, false positive, or needs more data — and the specific evidence supporting your call.');
  return lines.join('\n');
}
