import { FormEvent, useEffect, useRef, useState } from 'react';
import { Loader2, Play, Square, Wifi, WifiOff } from 'lucide-react';
import { api, subscribeRunLog, type RunListItem } from '../api';
import { cn } from '../lib/cn';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';
import MarkdownEditor from '../components/LazyMarkdownEditor';
import { HarnessOutput } from '../components/HarnessOutput';

const RUNS_PAGE_SIZE = 200;

export default function RunsPage() {
  const [connected, setConnected] = useState<string[]>([]);
  const [history, setHistory] = useState<RunListItem[]>([]);
  const [hasMoreRuns, setHasMoreRuns] = useState(true);
  const [agentLib, setAgentLib] = useState<string[]>([]);

  const [node, setNode] = useState('');
  const [provider, setProvider] = useState<'auto' | 'claude' | 'codex'>('auto');
  const [agent, setAgent] = useState('');
  const [prompt, setPrompt] = useState('');

  const [activeRunID, setActiveRunID] = useState<string | null>(null);
  const [lines, setLines] = useState<string[]>([]);
  const [running, setRunning] = useState(false);
  const [doneStatus, setDoneStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const scrollRef = useRef<HTMLDivElement>(null);

  // Refresh connected nodes + first page of history every 6s. The
  // first-page refresh keeps the live state of the most recent runs
  // (status flips from running → succeeded/failed) without paging.
  useEffect(() => {
    const refresh = () => {
      api.connectedNodes().then((c) => {
        setConnected(c);
        setNode((prev) => prev || c[0] || '');
      }).catch(() => { /* ignore */ });
      api.runs(RUNS_PAGE_SIZE, 0).then((list) => {
        setHistory((prev) => {
          // Splice the fresh first-page in front of any older pages
          // we've already lazy-loaded so live status updates take effect
          // without losing scroll position further down.
          const newest = list;
          const existingIDs = new Set(newest.map((r) => r.id));
          const tail = prev.filter((r) => !existingIDs.has(r.id));
          return [...newest, ...tail];
        });
        setHasMoreRuns(list.length >= RUNS_PAGE_SIZE);
      }).catch(() => { /* ignore */ });
    };
    refresh();
    const t = setInterval(refresh, 6_000);
    return () => clearInterval(t);
  }, []);

  const runsScroll = useInfiniteScroll({
    hasMore: hasMoreRuns,
    loadMore: async () => {
      const next = await api.runs(RUNS_PAGE_SIZE, history.length);
      if (next.length === 0) {
        setHasMoreRuns(false);
        return;
      }
      setHistory((prev) => [...prev, ...next]);
      if (next.length < RUNS_PAGE_SIZE) setHasMoreRuns(false);
    },
  });

  // Source the picker from the short-form Agent Library — these are the
  // one-shot Claude/Codex agents (sourced from ~/.claude/agents,
  // ~/.codex/agents, --agent-files-dir). Daimons (long-form, scheduled)
  // are NOT shown here on purpose — to trigger a daimon ad-hoc, use
  // the daimon detail page instead.
  useEffect(() => {
    api.agentLibrary()
      .then((items) => setAgentLib(items.map((a) => a.name)))
      .catch(() => { /* ignore — page works without a picker */ });
  }, []);

  // Auto-scroll the console as new lines arrive.
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [lines]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLines([]);
    setDoneStatus(null);
    setRunning(true);
    try {
      const { run_id } = await api.createRun({
        node,
        provider,
        agent: agent || undefined,
        prompt,
      });
      setActiveRunID(run_id);
      const unsubscribe = subscribeRunLog(
        run_id,
        (line) => setLines((prev) => [...prev, line]),
        (status) => { setDoneStatus(status); setRunning(false); unsubscribe(); },
      );
    } catch (err) {
      setError(String(err));
      setRunning(false);
    }
  }

  async function handleCancel(id: string, cpInstanceID?: string) {
    try {
      await api.cancelRun(id, cpInstanceID);
      // The SSE 'done' callback will flip running=false once the node sends exit.
    } catch (err) {
      setError(String(err));
    }
  }

  const promptValid = prompt.trim().length > 0;
  const canSubmit = !running && node && promptValid;

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel">
        <h1 className="text-lg font-semibold">Run Agent</h1>
        <p className="text-xs text-ink-dim">
          Trigger an ad-hoc <code>claude</code> / <code>codex</code> run on a
          connected node. Output streams here live.
        </p>
      </header>

      <div className="flex-1 overflow-auto p-6 grid grid-cols-12 gap-6">
        {/* Form panel */}
        <form onSubmit={handleSubmit} className="col-span-5 bg-panel border border-border rounded-xl shadow-card p-5 space-y-4 self-start">
          <Field label="Node">
            <div className="space-y-1">
              <select
                value={node}
                onChange={(e) => setNode(e.target.value)}
                className={inputCls}
                disabled={connected.length === 0}
              >
                {connected.length === 0 && <option value="">(no nodes connected)</option>}
                {connected.map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
              <ConnectedHint count={connected.length} />
            </div>
          </Field>

          <Field label="Provider">
            <div className="flex gap-1">
              {(['auto', 'claude', 'codex'] as const).map((p) => (
                <button
                  type="button"
                  key={p}
                  onClick={() => setProvider(p)}
                  className={cn(
                    'flex-1 text-xs px-2 py-1 rounded-md border',
                    provider === p
                      ? 'bg-brand-50 border-brand-500 text-brand-700'
                      : 'border-border text-ink-dim hover:bg-slate-50'
                  )}
                >
                  {p}
                </button>
              ))}
            </div>
          </Field>

          <Field label="Agent file (optional)">
            <select value={agent} onChange={(e) => setAgent(e.target.value)} className={inputCls}>
              <option value="">(none — use --system or default)</option>
              {agentLib.map((a) => <option key={a} value={a}>{a}</option>)}
            </select>
          </Field>

          <Field label="Prompt">
            <div className="border border-border rounded-md focus-within:ring-2 focus-within:ring-brand-500/30 overflow-hidden">
              <MarkdownEditor
                value={prompt}
                onChange={setPrompt}
                height={160}
                showLineNumbers={false}
                ariaLabel="Run prompt"
              />
            </div>
            {!prompt && (
              <p className="mt-1 text-[11px] text-ink-mute italic">
                e.g. <span className="font-mono">audit ./src for security vulnerabilities</span>
              </p>
            )}
          </Field>

          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}

          <div className="flex gap-2">
            <button
              type="submit"
              disabled={!canSubmit}
              className="flex-1 inline-flex items-center justify-center gap-2 bg-brand-500 hover:bg-brand-600 disabled:bg-brand-500/50 text-white text-sm font-medium py-2 rounded-md"
            >
              {running ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />}
              {running ? 'Running…' : 'Run'}
            </button>
            {running && activeRunID && (
              <button
                type="button"
                onClick={() => handleCancel(activeRunID)}
                className="inline-flex items-center justify-center gap-1.5 border border-border hover:bg-red-50 hover:text-red-700 text-sm font-medium px-4 py-2 rounded-md"
              >
                <Square size={12} /> Cancel
              </button>
            )}
          </div>
        </form>

        {/* Console panel */}
        <section className="col-span-7 bg-panel border border-border rounded-xl shadow-card flex flex-col min-h-[480px]">
          <header className="px-4 py-2.5 border-b border-border flex items-center justify-between">
            <div className="flex items-center gap-2 text-xs">
              <span className="font-medium text-ink-dim">Output</span>
              {activeRunID && (
                <code className="text-[10px] bg-slate-100 px-1.5 py-0.5 rounded text-ink-dim">
                  {activeRunID}
                </code>
              )}
            </div>
            {doneStatus && (
              <span className={cn(
                'text-[10px] uppercase tracking-wide font-medium px-2 py-0.5 rounded ring-1',
                doneStatus === 'succeeded' && 'text-green-700 bg-green-50 ring-green-200',
                doneStatus === 'failed'    && 'text-red-700 bg-red-50 ring-red-200',
                doneStatus === 'cancelled' && 'text-ink-dim bg-slate-100 ring-slate-200',
              )}>
                {doneStatus}
              </span>
            )}
          </header>
          <div ref={scrollRef} className="flex-1 overflow-auto p-3">
            {lines.length === 0 ? (
              <div className="text-xs text-ink-mute italic">
                {running ? 'Waiting for output…' : 'Submit a prompt to start.'}
              </div>
            ) : (
              <HarnessOutput text={lines.join('\n')} maxHeight={null} />
            )}
          </div>
        </section>

        {/* Recent runs */}
        <section className="col-span-12 bg-panel border border-border rounded-xl shadow-card">
          <header className="px-4 py-2.5 border-b border-border">
            <h2 className="text-sm font-semibold">Recent runs</h2>
          </header>
          {history.length === 0 ? (
            <p className="px-4 py-4 text-xs text-ink-mute">No runs yet.</p>
          ) : (
            <table className="w-full text-sm">
              <thead className="bg-slate-50 border-b border-border">
                <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute">
                  <th className="px-4 py-2 font-medium w-36">Started</th>
                  <th className="px-3 py-2 font-medium w-28">CP</th>
                  <th className="px-3 py-2 font-medium w-36">Node</th>
                  <th className="px-3 py-2 font-medium w-20">Provider</th>
                  <th className="px-3 py-2 font-medium">Prompt</th>
                  <th className="px-3 py-2 font-medium w-24">Status</th>
                </tr>
              </thead>
              <tbody>
                {history.map((r) => (
                  <tr key={`${r.cp_source?.instance_id ?? 'local'}:${r.id}`} className="border-b border-border/60 last:border-0">
                    <td className="px-4 py-2 text-xs text-ink-dim font-mono">{new Date(r.started_at).toLocaleTimeString()}</td>
                    <td className="px-3 py-2 text-xs">
                      {r.cp_source ? (
                        <span
                          className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-violet-700 bg-violet-50 ring-violet-200"
                          title={`Run lives on ${r.cp_source.display_name} (${r.cp_source.region})`}
                        >
                          {r.cp_source.display_name}
                        </span>
                      ) : (
                        <span className="text-[10px] uppercase tracking-wide text-ink-mute">local</span>
                      )}
                    </td>
                    <td className="px-3 py-2 font-medium">{r.node}</td>
                    <td className="px-3 py-2 text-xs text-ink-dim">{r.provider || '—'}</td>
                    <td className="px-3 py-2 text-xs text-ink-dim truncate max-w-md">{r.prompt}</td>
                    <td className="px-3 py-2">
                      <div className="flex items-center gap-2">
                        <span className={cn(
                          'text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1',
                          r.status === 'running'   && 'text-brand-700 bg-brand-50 ring-brand-100',
                          r.status === 'succeeded' && 'text-green-700 bg-green-50 ring-green-200',
                          r.status === 'failed'    && 'text-red-700 bg-red-50 ring-red-200',
                          r.status === 'cancelled' && 'text-ink-dim bg-slate-100 ring-slate-200',
                        )}>
                          {r.status}
                        </span>
                        {r.status === 'running' && (
                          <button
                            onClick={() => handleCancel(r.id, r.cp_source?.instance_id)}
                            className="text-[10px] text-ink-mute hover:text-red-700 inline-flex items-center gap-0.5"
                            title={r.cp_source
                              ? `Cancel run on ${r.cp_source.display_name}`
                              : 'Cancel run'}
                          >
                            <Square size={9} /> cancel
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {history.length > 0 && (
            <div ref={runsScroll.sentinelRef} className="px-4 py-2 text-[11px] text-ink-mute text-center border-t border-border/60">
              {runsScroll.loading
                ? 'loading older runs…'
                : hasMoreRuns
                  ? 'scroll for more'
                  : `— end of ${history.length} runs —`}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function ConnectedHint({ count }: { count: number }) {
  if (count === 0) {
    return (
      <span className="inline-flex items-center gap-1 text-[11px] text-ink-mute">
        <WifiOff size={11} /> No connected nodes. Run `okesu node` on a host with a CA-signed cert.
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 text-[11px] text-green-700">
      <Wifi size={11} /> {count} node{count === 1 ? '' : 's'} connected
    </span>
  );
}

const inputCls = "w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white";

const Field = ({ label, children }: { label: string; children: React.ReactNode }) => (
  <div>
    <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
    {children}
  </div>
);
