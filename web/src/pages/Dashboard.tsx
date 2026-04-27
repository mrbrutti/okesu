// Operator landing page. Replaces /findings as the default route.
//
// Layout:
//   Row 1 — 4 stat tiles (Daimons / Nodes / Findings / Drift)
//   Row 2 — 2 wide charts (Events/hour 24h, Findings-over-time multi-line)
//   Row 3 — 2 lists       (Recent CRITICALs, Stale fleet drift)
//
// Auto-refreshes the dashboard payload every 15s. The multi-line
// chart re-fetches when range/groupBy change, with state in the URL
// query string so views are bookmarkable.

import { lazy, Suspense, useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
  Cpu,
  Loader2,
  Package,
  Server,
  ShieldAlert,
  TrendingUp,
} from 'lucide-react';
import { api, ApiError, type DashboardResponse, type InsightsFindingsResponse } from '../api';
import { cn } from '../lib/cn';

// lazy() requires a default export, but DashboardCharts ships two
// named components. Wrap each lookup in a tiny shim. Both wrappers
// resolve the same module — Vite caches the chunk so it only
// downloads once.
const EventsPerHourChart = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.EventsPerHourChart })),
);
const FindingsTimelineChart = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.FindingsTimelineChart })),
);

type Range = '24h' | '7d' | '30d';
type GroupBy = 'severity' | 'agent' | 'host';

function parseRange(s: string | null): Range {
  return s === '7d' || s === '30d' ? s : '24h';
}
function parseGroupBy(s: string | null): GroupBy {
  return s === 'agent' || s === 'host' ? s : 'severity';
}

export default function DashboardPage() {
  const [data, setData] = useState<DashboardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [params, setParams] = useSearchParams();

  const range = parseRange(params.get('range'));
  const groupBy = parseGroupBy(params.get('groupBy'));

  const [insights, setInsights] = useState<InsightsFindingsResponse | null>(null);
  const [insightsErr, setInsightsErr] = useState<string | null>(null);

  // Aggregate dashboard payload — every 15s.
  useEffect(() => {
    let cancelled = false;
    const refresh = () => {
      api.dashboard()
        .then((d) => { if (!cancelled) { setData(d); setError(null); } })
        .catch((e) => { if (!cancelled) setError(e instanceof ApiError ? e.message : String(e)); });
    };
    refresh();
    const t = setInterval(refresh, 15_000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  // Multi-series timeline — refetches on range/groupBy change.
  useEffect(() => {
    let cancelled = false;
    api.insightsFindings({ since: range, group_by: groupBy })
      .then((d) => { if (!cancelled) { setInsights(d); setInsightsErr(null); } })
      .catch((e) => { if (!cancelled) setInsightsErr(e instanceof ApiError ? e.message : String(e)); });
    return () => { cancelled = true; };
  }, [range, groupBy]);

  function setRange(r: Range) {
    const p = new URLSearchParams(params);
    p.set('range', r);
    setParams(p, { replace: true });
  }
  function setGroupBy(g: GroupBy) {
    const p = new URLSearchParams(params);
    p.set('groupBy', g);
    setParams(p, { replace: true });
  }

  return (
    <div className="p-6 max-w-7xl space-y-6">
      <header>
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <Activity size={18} className="text-brand-500" />
          Dashboard
        </h1>
        <p className="text-xs text-ink-dim mt-0.5">Fleet health at a glance — auto-refreshes every 15 seconds.</p>
      </header>

      {error && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      )}

      {/* Row 1: stat tiles */}
      <section className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatTile
          label="Daimons"
          icon={Cpu}
          to="/daimons"
          loading={!data}
          primary={data ? `${data.daimons.healthy}/${data.daimons.total}` : '—'}
          accent={data && data.daimons.unhealthy > 0 ? 'warn' : 'good'}
          sub={data ? `${data.daimons.unhealthy} unhealthy` : ''}
        />
        <StatTile
          label="Nodes"
          icon={Server}
          to="/nodes"
          loading={!data}
          primary={data ? `${data.nodes.connected}/${data.nodes.total}` : '—'}
          accent={data && data.nodes.connected < data.nodes.total ? 'warn' : 'good'}
          sub={data ? `${data.nodes.connected} tunnel-connected` : ''}
        />
        <StatTile
          label="Open findings"
          icon={ShieldAlert}
          to="/findings"
          loading={!data}
          primary={data ? `${data.findings.open}` : '—'}
          accent={data && data.findings.critical > 0 ? 'bad' : data && data.findings.high > 0 ? 'warn' : 'good'}
          sub={data ? `${data.findings.critical} critical · ${data.findings.high} high` : ''}
        />
        <StatTile
          label="Drift"
          icon={TrendingUp}
          to="/daimons"
          loading={!data}
          primary={data ? `${data.drift.total}` : '—'}
          accent={data && data.drift.total > 0 ? 'warn' : 'good'}
          sub={data && data.drift.total > 0 ? `${data.drift.total} on stale def/binary` : 'all on canonical'}
        />
      </section>

      {/* Row 2: charts */}
      <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card title="Events / hour (24h)" subtitle="Stacked by event type — spot a thrashing daimon by the spike.">
          {!data ? <ChartSkeleton /> : (
            <Suspense fallback={<ChartSkeleton />}>
              <EventsPerHourChart data={data.events_per_hour} />
            </Suspense>
          )}
        </Card>
        <Card
          title="Findings over time"
          subtitle={`${range} · grouped by ${groupBy}`}
          right={
            <div className="flex items-center gap-1">
              <Picker value={range} options={['24h', '7d', '30d']} onChange={(v) => setRange(v as Range)} />
              <Picker value={groupBy} options={['severity', 'agent', 'host']} onChange={(v) => setGroupBy(v as GroupBy)} />
            </div>
          }
        >
          {insightsErr && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {insightsErr}
            </div>
          )}
          {!insights ? <ChartSkeleton /> : (
            <Suspense fallback={<ChartSkeleton />}>
              <FindingsTimelineChart data={insights} />
            </Suspense>
          )}
        </Card>
      </section>

      {/* Row 3: actionable lists */}
      <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card title="Recent CRITICAL findings" subtitle="10 most-recent open. Click any row to triage.">
          {!data ? <ListSkeleton /> : data.recent_critical.length === 0 ? (
            <EmptyRow icon={CheckCircle2} text="No open critical findings." />
          ) : (
            <ul className="divide-y divide-border">
              {data.recent_critical.map((f) => (
                <li key={f.id}>
                  <Link
                    to={`/findings?id=${f.id}`}
                    className="flex items-center gap-3 px-3 py-2 hover:bg-slate-50/60"
                  >
                    <span className="severity-critical text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded font-mono">
                      CRITICAL
                    </span>
                    <span className="flex-1 text-sm text-ink truncate">{f.title || '(no title)'}</span>
                    <span className="text-[11px] text-ink-mute font-mono shrink-0">
                      {f.agent || '?'}@{f.host || '?'}
                    </span>
                    <span className="text-[11px] text-ink-mute shrink-0">{relTime(f.ts)}</span>
                    <ChevronRight size={12} className="text-ink-mute shrink-0" />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Card>
        <Card title="Stale fleet" subtitle="Hosts running an older daimon definition or binary than the canonical version.">
          {!data ? <ListSkeleton /> : data.drift.items.length === 0 ? (
            <EmptyRow icon={CheckCircle2} text="Every host is on the canonical version." />
          ) : (
            <ul className="divide-y divide-border">
              {data.drift.items.map((d) => (
                <li key={`${d.name}@${d.host}`}>
                  <Link
                    to={`/daimons/${encodeURIComponent(d.name)}`}
                    className="flex items-center gap-3 px-3 py-2 hover:bg-slate-50/60"
                  >
                    <Cpu size={12} className="text-ink-mute" />
                    <span className="text-sm text-ink font-medium">{d.name}</span>
                    <span className="text-[11px] text-ink-mute font-mono truncate flex-1">{d.host}</span>
                    {d.definition_version && (
                      <span className="text-[10px] font-mono text-yellow-800 bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">
                        def v{d.definition_version}
                      </span>
                    )}
                    {d.binary_version && (
                      <span className="text-[10px] font-mono text-slate-700 bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
                        bin {d.binary_version.slice(0, 12)}
                      </span>
                    )}
                    <Package size={12} className="text-ink-mute shrink-0" />
                    <ChevronRight size={12} className="text-ink-mute shrink-0" />
                  </Link>
                </li>
              ))}
              {data.drift.total > data.drift.items.length && (
                <li className="px-3 py-2 text-[11px] text-ink-mute">
                  + {data.drift.total - data.drift.items.length} more not shown
                </li>
              )}
            </ul>
          )}
        </Card>
      </section>
    </div>
  );
}

// ── primitives ──────────────────────────────────────────────────────

function StatTile({
  label, icon: Icon, primary, sub, accent, to, loading,
}: {
  label: string;
  icon: typeof Cpu;
  primary: string;
  sub?: string;
  accent: 'good' | 'warn' | 'bad';
  to: string;
  loading: boolean;
}) {
  const dotCls = {
    good: 'bg-green-500',
    warn: 'bg-yellow-500',
    bad: 'bg-red-500',
  }[accent];
  return (
    <Link
      to={to}
      className="block bg-panel border border-border rounded-xl shadow-card p-4 hover:bg-slate-50/40 transition-colors"
    >
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-xs uppercase tracking-wide text-ink-mute font-medium">
          <Icon size={12} />
          {label}
        </div>
        <span className={cn('w-2 h-2 rounded-full', dotCls)} />
      </div>
      <div className="mt-2 text-2xl font-semibold text-ink tabular-nums">
        {loading ? <Loader2 size={20} className="animate-spin text-ink-mute" /> : primary}
      </div>
      {sub && <div className="text-[11px] text-ink-mute mt-0.5">{sub}</div>}
    </Link>
  );
}

function Card({
  title, subtitle, right, children,
}: {
  title: string;
  subtitle?: string;
  right?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card">
      <header className="px-4 pt-3 pb-2 flex items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">{title}</h3>
          {subtitle && <p className="text-xs text-ink-dim mt-0.5">{subtitle}</p>}
        </div>
        {right}
      </header>
      <div className="p-4 pt-2">{children}</div>
    </section>
  );
}

function Picker<T extends string>({ value, options, onChange }: { value: T; options: readonly T[] | T[]; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex rounded-md ring-1 ring-border overflow-hidden">
      {options.map((opt) => (
        <button
          key={opt}
          onClick={() => onChange(opt)}
          className={cn(
            'px-2 py-0.5 text-[11px] uppercase tracking-wide font-medium',
            opt === value ? 'bg-brand-50 text-brand-700' : 'bg-panel text-ink-dim hover:bg-slate-50',
          )}
        >
          {opt}
        </button>
      ))}
    </div>
  );
}

function ChartSkeleton() {
  return (
    <div className="h-[220px] flex items-center justify-center text-ink-mute text-xs">
      <Loader2 size={14} className="animate-spin mr-1.5" />
      loading…
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="h-[200px] flex items-center justify-center text-ink-mute text-xs">
      <Loader2 size={14} className="animate-spin mr-1.5" />
      loading…
    </div>
  );
}

function EmptyRow({ icon: Icon, text }: { icon: typeof CheckCircle2; text: string }) {
  return (
    <div className="px-3 py-6 text-center text-ink-dim text-xs flex flex-col items-center gap-1.5">
      <Icon size={18} className="text-green-500" />
      {text}
    </div>
  );
}

function relTime(unixMs: number): string {
  const sec = (Date.now() - unixMs) / 1000;
  if (sec < 60) return `${Math.floor(sec)}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h`;
  return `${Math.floor(sec / 86400)}d`;
}

// AlertTriangle is imported above for icon use elsewhere — keep the
// import so future additions can reuse it without re-importing.
void AlertTriangle;
