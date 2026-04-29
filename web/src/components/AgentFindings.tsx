// Findings produced by a single agent — a compact view shown as a tab on
// the agent detail page. Reuses the global FindingDrawer for evidence and
// acknowledge actions so behaviour is identical to the /findings page.

import { useEffect, useMemo, useState } from 'react';
import { ShieldAlert, ShieldOff, AlertTriangle, Filter } from 'lucide-react';
import { api, ApiError, type Finding } from '../api';
import { FindingDrawer } from '../pages/Findings';
import { cn } from '../lib/cn';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';

const FINDINGS_PAGE_SIZE = 250;

interface Props {
  agentName: string;
  /** Pin the findings to the daimon's specific host so we don't
   *  conflate findings from other daimons that happen to share the
   *  same agent name (common when a fleet has 10x edr daimons). */
  host?: string;
  /** When the daimon lives on a federated child CP, this is that
   *  child's instance_id — used to route the finding-detail drawer
   *  through the parent's ?cp= proxy. */
  cpInstanceID?: string;
}

type Sev = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';
type State = 'open' | 'acked' | 'all';

const ALL_SEVS: Sev[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];

export default function AgentFindings({ agentName, host, cpInstanceID }: Props) {
  const [findings, setFindings] = useState<Finding[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sevs, setSevs] = useState<Sev[]>([]);
  const [state, setState] = useState<State>('open');
  const [drawerID, setDrawerID] = useState<number | null>(null);
  const [hasMore, setHasMore] = useState(true);

  // Refresh the first page; merge with already-loaded older pages so the
  // periodic refresh interval doesn't wipe history the user has scrolled
  // through. New entries at the head overwrite tail dups by id.
  const refresh = () => {
    api.findings({ agent: agentName, host, severity: sevs, state, limit: FINDINGS_PAGE_SIZE })
      .then((list) => {
        setFindings((prev) => {
          const newest = list;
          const seen = new Set(newest.map((f) => f.id));
          const tail = (prev ?? []).filter((f) => !seen.has(f.id));
          return [...newest, ...tail];
        });
        setHasMore(list.length >= FINDINGS_PAGE_SIZE);
        setError(null);
      })
      .catch((e) => {
        setError(e instanceof ApiError && e.status === 401
          ? 'Session expired. Reload the page.'
          : `Could not load findings: ${String(e)}`);
      });
  };

  useEffect(() => {
    // Reset list when filters change so the next refresh starts from page 0
    // instead of merging into stale results filtered differently.
    setFindings(null);
    setHasMore(true);
    refresh();
    const t = setInterval(refresh, 8_000);
    return () => clearInterval(t);
    // refresh implicitly depends on agentName / sevs / state via the closure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentName, sevs.join(','), state]);

  const scroll = useInfiniteScroll({
    hasMore,
    loadMore: async () => {
      if (!findings) return;
      const next = await api.findings({
        agent: agentName,
        host,
        severity: sevs,
        state,
        limit: FINDINGS_PAGE_SIZE,
        offset: findings.length,
      });
      if (next.length === 0) {
        setHasMore(false);
        return;
      }
      setFindings((prev) => [...(prev ?? []), ...next]);
      if (next.length < FINDINGS_PAGE_SIZE) setHasMore(false);
    },
  });

  // Group counts for the filter bar.
  const counts = useMemo(() => {
    const c: Record<string, number> = { CRITICAL: 0, HIGH: 0, MEDIUM: 0, LOW: 0, INFO: 0 };
    for (const f of findings ?? []) {
      const k = (f.severity || 'INFO').toUpperCase();
      if (k in c) c[k]++;
    }
    return c;
  }, [findings]);

  // Per-category and top-resource roll-ups for the agent-specific summary.
  // Computed client-side from the already-loaded list — no extra request.
  const breakdown = useMemo(() => {
    const cats: Record<string, number> = {};
    const resources: Record<string, number> = {};
    for (const f of findings ?? []) {
      const cat = f.category || '(uncategorized)';
      cats[cat] = (cats[cat] || 0) + 1;
      const r = f.resource || f.path || f.network_endpoint || '(no resource)';
      resources[r] = (resources[r] || 0) + 1;
    }
    const topCats = Object.entries(cats).sort((a, b) => b[1] - a[1]).slice(0, 6);
    const topResources = Object.entries(resources).sort((a, b) => b[1] - a[1]).slice(0, 5);
    return { topCats, topResources };
  }, [findings]);

  const total = findings?.length ?? 0;

  return (
    <div className="h-full overflow-auto p-6">
      <header className="flex items-center justify-between flex-wrap gap-3 mb-4">
        <div className="flex items-center gap-2 text-sm">
          <ShieldAlert size={16} className="text-purple-600" />
          <span className="font-medium">Findings</span>
          <span className="text-ink-mute">·</span>
          <span className="text-ink-dim">
            {findings === null ? 'loading…' : `${total} ${state === 'all' ? '' : state} ${total === 1 ? 'finding' : 'findings'} from ${agentName}`}
          </span>
        </div>

        <div className="flex items-center gap-2 text-xs">
          <span className="inline-flex items-center gap-1 text-ink-mute mr-1">
            <Filter size={11} /> severity
          </span>
          {ALL_SEVS.map((s) => {
            const active = sevs.includes(s);
            return (
              <button
                key={s}
                onClick={() => {
                  setSevs((prev) =>
                    prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s],
                  );
                }}
                className={cn(
                  'severity-badge',
                  `severity-${s.toLowerCase()}`,
                  !active && 'opacity-40 hover:opacity-100',
                )}
                title={`${counts[s] ?? 0} in this view`}
              >
                {s} {counts[s] ?? 0}
              </button>
            );
          })}
          <span className="text-ink-mute mx-1">·</span>
          <StateToggle state={state} setState={setState} />
        </div>
      </header>

      {error && (
        <div className="mb-3 text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md flex items-start gap-2">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" />
          {error}
        </div>
      )}

      {findings !== null && findings.length > 0 && (
        <div className="mb-4 grid grid-cols-1 md:grid-cols-2 gap-3">
          <Panel title="Categories">
            {breakdown.topCats.length === 0 ? (
              <p className="text-xs text-ink-mute">none</p>
            ) : (
              <ul className="space-y-1 text-xs">
                {breakdown.topCats.map(([cat, n]) => (
                  <li key={cat} className="flex items-center justify-between gap-2">
                    <span className="font-mono truncate">{cat}</span>
                    <span className="text-ink-dim tabular-nums">{n}</span>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
          <Panel title="Top resources">
            {breakdown.topResources.length === 0 ? (
              <p className="text-xs text-ink-mute">none</p>
            ) : (
              <ul className="space-y-1 text-xs">
                {breakdown.topResources.map(([r, n]) => (
                  <li key={r} className="flex items-center justify-between gap-2">
                    <span className="font-mono truncate" title={r}>{r}</span>
                    <span className="text-ink-dim tabular-nums">{n}</span>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </div>
      )}

      {findings !== null && findings.length === 0 && (
        <div className="text-center py-16 text-ink-mute">
          <ShieldOff size={28} className="mx-auto mb-2 opacity-40" />
          <p className="text-sm">No matching findings from {agentName}.</p>
          <p className="text-xs mt-1">
            {state === 'open' ? 'All findings are acknowledged — switch to "All" to see history.' : 'Findings will appear here as the agent emits them.'}
          </p>
        </div>
      )}

      {findings !== null && findings.length > 0 && (
        <ul className="space-y-1.5">
          {findings.map((f) => (
            <FindingRow key={f.id} f={f} onClick={() => setDrawerID(f.id)} />
          ))}
        </ul>
      )}

      {findings !== null && findings.length > 0 && (
        <div ref={scroll.sentinelRef} className="h-6 flex items-center justify-center text-[11px] text-ink-mute mt-3">
          {scroll.loading
            ? 'loading more findings…'
            : hasMore
              ? 'scroll for more'
              : `— end of ${findings.length} findings —`}
        </div>
      )}

      {drawerID !== null && (
        <FindingDrawer
          id={drawerID}
          cpInstanceID={cpInstanceID}
          onClose={() => setDrawerID(null)}
          onChanged={() => { setDrawerID(null); refresh(); }}
        />
      )}
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="bg-panel border border-border rounded-md px-3 py-2 shadow-card">
      <h4 className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">{title}</h4>
      {children}
    </section>
  );
}

function FindingRow({ f, onClick }: { f: Finding; onClick: () => void }) {
  const sev = (f.severity || 'INFO').toLowerCase();
  return (
    <li>
      <button
        onClick={onClick}
        className={cn(
          'w-full text-left bg-panel border border-border rounded-md px-3 py-2 flex items-start gap-3',
          'hover:border-brand-300 hover:bg-brand-50/30 transition-colors',
          f.acknowledged && 'opacity-60',
        )}
      >
        <span className={cn('severity-badge mt-0.5 shrink-0', `severity-${sev}`)}>
          {f.severity || 'INFO'}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className={cn('text-sm font-medium', f.acknowledged && 'line-through')}>
              {f.title || '(untitled)'}
            </span>
            {f.acknowledged && (
              <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
                acknowledged
              </span>
            )}
          </div>
          <div className="text-[11px] text-ink-mute font-mono truncate">
            {f.resource || f.dedup_key || ''}
            {f.host ? `  ·  host=${f.host}` : ''}
          </div>
        </div>
        <span className="text-[11px] text-ink-mute font-mono whitespace-nowrap ml-2 mt-0.5">
          {fmtAge(f.ts)}
        </span>
      </button>
    </li>
  );
}

function StateToggle({ state, setState }: { state: State; setState: (s: State) => void }) {
  return (
    <div className="inline-flex bg-slate-100 rounded-md p-0.5">
      {(['open', 'acked', 'all'] as const).map((s) => (
        <button
          key={s}
          onClick={() => setState(s)}
          className={cn(
            'px-2 py-0.5 text-[11px] rounded',
            state === s ? 'bg-white shadow-sm font-medium' : 'text-ink-mute hover:text-ink',
          )}
        >
          {s === 'all' ? 'All' : s === 'open' ? 'Open' : 'Acked'}
        </button>
      ))}
    </div>
  );
}

function fmtAge(ms?: number): string {
  if (!ms) return '';
  const sec = Math.max(0, (Date.now() - ms) / 1000);
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}

