import { useEffect, useMemo, useState } from 'react';
import { X, Server, ChevronRight } from 'lucide-react';
import { cn } from '../lib/cn';
import type { OrchestrationStepView, StepNodeDispatchView } from '../api';

interface Props {
  step: OrchestrationStepView;
  onSelectHost: (host: string) => void;
  onClose: () => void;
}

type Filter = 'all' | 'failed' | 'running' | 'completed';

export default function FanoutHostListDrawer({ step, onSelectHost, onClose }: Props) {
  const [filter, setFilter] = useState<Filter>('all');
  const [query, setQuery] = useState('');

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const all = step.per_node ?? [];
  const filtered = useMemo<StepNodeDispatchView[]>(() => {
    let out = all;
    if (filter !== 'all') out = out.filter((h) => h.status === filter);
    const q = query.trim().toLowerCase();
    if (q) out = out.filter((h) => h.host.toLowerCase().includes(q));
    return out;
  }, [all, filter, query]);

  return (
    <>
      <div className="fixed inset-0 bg-slate-900/20 z-30" onClick={onClose} />
      <aside
        className="fixed top-0 right-0 h-full w-[440px] max-w-[80vw] bg-panel border-l border-border z-40 shadow-xl flex flex-col"
        role="dialog"
        aria-label={`Hosts in ${step.step_id}`}
      >
        <header className="px-4 py-3 border-b border-border">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2 min-w-0">
              <Server size={14} className="text-brand-700 shrink-0" />
              <span className="text-[11px] text-ink-dim font-mono truncate">
                <span className="text-ink font-semibold">{step.step_id}</span> · {all.length} hosts
              </span>
            </div>
            <button onClick={onClose} className="text-ink-dim hover:text-ink p-1 rounded" aria-label="Close">
              <X size={14} />
            </button>
          </div>
          <input
            type="text"
            placeholder="filter hostnames…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-full text-[11px] font-mono px-2 py-1 rounded ring-1 ring-slate-200 bg-white focus:ring-brand-300 focus:outline-none"
          />
          <div className="flex gap-1 mt-2">
            {(['all', 'failed', 'running', 'completed'] as Filter[]).map((f) => (
              <button
                key={f}
                onClick={() => setFilter(f)}
                className={cn(
                  'text-[10px] uppercase tracking-wide px-2 py-0.5 rounded ring-1',
                  filter === f
                    ? 'bg-brand-50 text-brand-700 ring-brand-200'
                    : 'bg-white text-ink-dim ring-slate-200 hover:bg-slate-50',
                )}
              >
                {f}
              </button>
            ))}
          </div>
        </header>

        <div className="flex-1 overflow-y-auto">
          {filtered.length === 0 && (
            <div className="px-4 py-6 text-[11px] text-ink-mute italic">No hosts match.</div>
          )}
          {filtered.map((h) => (
            <button
              key={h.host}
              type="button"
              onClick={() => onSelectHost(h.host)}
              className="w-full flex items-center gap-2 px-4 py-2 border-b border-slate-100 hover:bg-slate-50 text-left text-[11px] font-mono"
            >
              <span className={cn(
                'w-2 h-2 rounded-full shrink-0',
                h.status === 'completed' && 'bg-green-500',
                h.status === 'failed' && 'bg-red-500',
                h.status === 'running' && 'bg-blue-500 animate-pulse',
                h.status === 'pending' && 'bg-slate-300',
              )} />
              <span className={cn(
                'flex-1 truncate',
                h.status === 'failed' && 'text-red-700',
                h.status === 'running' && 'text-blue-700',
              )}>{h.host}</span>
              {h.findings_count > 0 && (
                <span className="bg-brand-50 text-brand-700 ring-1 ring-brand-200 px-1 rounded text-[9px] font-semibold">
                  {h.findings_count}
                </span>
              )}
              <ChevronRight size={11} className="text-slate-300" />
            </button>
          ))}
        </div>
      </aside>
    </>
  );
}
