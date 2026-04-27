// Operator landing page. Replaces /findings as the default route.
//
// Layout (all aggregated, no lists):
//   Row 1 — 5 stat tiles (Daimons / Nodes / Tunnels / Findings / Drift)
//   Row 2 — 2 wide timeline charts (Events, Findings) with shared
//           range picker (30m / 1h / 24h / 7d / 30d) + Findings
//           group-by picker (severity / agent / host)
//   Row 3 — 2 aggregate cards (Top affected hosts bar, Fleet rollout donut)
//
// Auto-refreshes the dashboard payload every 15s. The two timeline
// charts re-fetch when range changes; findings also re-fetches on
// groupBy change. URL state in ?range=&groupBy= for bookmarkability.

import { lazy, Suspense, useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Cpu,
  Loader2,
  Lock,
  Radio,
  Server,
  ShieldAlert,
  TrendingUp,
  WifiOff,
} from 'lucide-react';
import { api, ApiError, type DashboardResponse, type InsightsEventsResponse, type InsightsFindingsResponse, type TimeRange } from '../api';
import { cn } from '../lib/cn';

const EventsTimelineChart = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.EventsTimelineChart })),
);
const FindingsTimelineChart = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.FindingsTimelineChart })),
);
const TopHostsChart = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.TopHostsChart })),
);
const OSDistributionDonut = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.OSDistributionDonut })),
);

type GroupBy = 'severity' | 'agent' | 'host';

const RANGES: TimeRange[] = ['30m', '1h', '24h', '7d', '30d'];
const GROUPS: GroupBy[] = ['severity', 'agent', 'host'];

function parseRange(s: string | null): TimeRange {
  return RANGES.includes(s as TimeRange) ? (s as TimeRange) : '24h';
}
function parseGroupBy(s: string | null): GroupBy {
  return GROUPS.includes(s as GroupBy) ? (s as GroupBy) : 'severity';
}

export default function DashboardPage() {
  const [data, setData] = useState<DashboardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [params, setParams] = useSearchParams();

  const range = parseRange(params.get('range'));
  const groupBy = parseGroupBy(params.get('groupBy'));

  const [events, setEvents] = useState<InsightsEventsResponse | null>(null);
  const [findings, setFindings] = useState<InsightsFindingsResponse | null>(null);

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

  // Events timeline — refetches on range change.
  useEffect(() => {
    let cancelled = false;
    api.insightsEvents({ since: range })
      .then((d) => { if (!cancelled) setEvents(d); })
      .catch(() => { /* ignore */ });
    return () => { cancelled = true; };
  }, [range]);

  // Findings timeline — refetches on range/groupBy change.
  useEffect(() => {
    let cancelled = false;
    api.insightsFindings({ since: range, group_by: groupBy })
      .then((d) => { if (!cancelled) setFindings(d); })
      .catch(() => { /* ignore */ });
    return () => { cancelled = true; };
  }, [range, groupBy]);

  function setRange(r: TimeRange) {
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
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel">
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <Activity size={18} className="text-brand-500" />
          Dashboard
        </h1>
        <p className="text-xs text-ink-dim mt-0.5">Fleet health at a glance — refreshes every 15 seconds.</p>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-6">
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {/* Row 1: stat tiles */}
        <section className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-4">
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
            primary={data ? `${data.nodes.heartbeating}/${data.nodes.total}` : '—'}
            accent={data && data.nodes.heartbeating < data.nodes.total ? 'warn' : 'good'}
            sub={data ? 'heartbeating in last 5min' : ''}
          />
          <StatTile
            label="Tunnels"
            icon={Radio}
            to="/nodes"
            loading={!data}
            primary={data ? `${data.tunnels.live}` : '—'}
            accent="info"
            sub="live reverse tunnels (Run Agent)"
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
            sub={data && data.drift.total > 0 ? 'on stale def/binary' : 'all on canonical'}
          />
        </section>

        {/* Row 2: timelines with shared range picker */}
        <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <Card
            title="Events"
            subtitle={`Total / ${range}`}
            right={<RangePicker value={range} onChange={setRange} />}
          >
            {!events ? <ChartSkeleton /> : (
              <Suspense fallback={<ChartSkeleton />}>
                <EventsTimelineChart data={events} range={range} />
              </Suspense>
            )}
          </Card>
          <Card
            title="Findings"
            subtitle={`Grouped by ${groupBy} / ${range}`}
            right={
              <div className="flex items-center gap-1.5">
                <Picker value={groupBy} options={GROUPS} onChange={(v) => setGroupBy(v as GroupBy)} />
                <RangePicker value={range} onChange={setRange} />
              </div>
            }
          >
            {!findings ? <ChartSkeleton /> : (
              <Suspense fallback={<ChartSkeleton />}>
                <FindingsTimelineChart data={findings} range={range} />
              </Suspense>
            )}
          </Card>
        </section>

        {/* Row 3: aggregate cards */}
        <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <Card
            title="Top affected hosts"
            subtitle="Open findings by host (top 10)"
          >
            {!data ? <ChartSkeleton /> : (
              <Suspense fallback={<ChartSkeleton />}>
                <TopHostsChart data={data.top_hosts} />
              </Suspense>
            )}
          </Card>
          <Card title="Operating systems" subtitle="Nodes by OS family">
            {!data ? <ChartSkeleton /> : (
              <Suspense fallback={<ChartSkeleton />}>
                <OSDistributionDonut data={data.os_distribution} />
              </Suspense>
            )}
          </Card>
        </section>

        {/* Row 4: fleet status keypoints — full-width strip */}
        <section>
          <Card title="Fleet status" subtitle="Each node falls into exactly one bucket">
            {!data ? <ChartSkeleton /> : <FleetStatusKeypoints data={data.fleet_status} />}
          </Card>
        </section>
      </div>
    </div>
  );
}

// ── primitives ──────────────────────────────────────────────────────

type Accent = 'good' | 'warn' | 'bad' | 'info';

function StatTile({
  label, icon: Icon, primary, sub, accent, to, loading,
}: {
  label: string;
  icon: typeof Cpu;
  primary: string;
  sub?: string;
  accent: Accent;
  to: string;
  loading: boolean;
}) {
  const dotCls = {
    good: 'bg-green-500',
    warn: 'bg-yellow-500',
    bad:  'bg-red-500',
    info: 'bg-slate-400',
  }[accent];
  return (
    <Link
      to={to}
      className="block bg-panel border border-border rounded-xl shadow-card p-4 hover:bg-slate-50/60 transition-colors"
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
      {sub && <div className="text-[11px] text-ink-mute mt-0.5 truncate">{sub}</div>}
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
        <div className="min-w-0">
          <h3 className="text-sm font-semibold">{title}</h3>
          {subtitle && <p className="text-xs text-ink-dim mt-0.5 truncate">{subtitle}</p>}
        </div>
        {right}
      </header>
      <div className="p-4 pt-2">{children}</div>
    </section>
  );
}

function RangePicker({ value, onChange }: { value: TimeRange; onChange: (v: TimeRange) => void }) {
  return <Picker value={value} options={RANGES} onChange={(v) => onChange(v as TimeRange)} />;
}

function Picker<T extends string>({ value, options, onChange }: { value: T; options: readonly T[] | T[]; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex rounded-md ring-1 ring-border overflow-hidden">
      {options.map((opt) => (
        <button
          key={opt}
          type="button"
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
    <div className="h-[180px] flex items-center justify-center text-ink-mute text-xs">
      <Loader2 size={14} className="animate-spin mr-1.5" />
      loading…
    </div>
  );
}

// CheckCircle2 used by FleetStatusKeypoints below.
void CheckCircle2;

// ── Fleet status keypoints — 4 big-number cells in one card ────────

function FleetStatusKeypoints({ data }: { data: DashboardResponse['fleet_status'] }) {
  const items: Array<{ label: string; value: number; icon: typeof CheckCircle2; tone: 'good' | 'warn' | 'bad' | 'mute' }> = [
    { label: 'Healthy',         value: data.healthy,         icon: CheckCircle2,   tone: 'good' },
    { label: 'Needs patching',  value: data.needs_patching,  icon: AlertTriangle,  tone: 'warn' },
    { label: 'Offline',         value: data.offline,         icon: WifiOff,        tone: 'bad' },
    { label: 'Frozen',          value: data.frozen,          icon: Lock,           tone: 'mute' },
  ];
  const toneCls = {
    good: 'text-green-700 bg-green-50 ring-green-200',
    warn: 'text-yellow-800 bg-yellow-50 ring-yellow-200',
    bad:  'text-red-700 bg-red-50 ring-red-200',
    mute: 'text-slate-700 bg-slate-100 ring-slate-200',
  };
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
      {items.map((it) => (
        <div
          key={it.label}
          className={cn(
            'rounded-lg ring-1 px-4 py-3 flex items-center gap-3',
            toneCls[it.tone],
          )}
        >
          <it.icon size={20} className="shrink-0 opacity-80" />
          <div className="min-w-0">
            <div className="text-2xl font-semibold tabular-nums leading-tight">{it.value}</div>
            <div className="text-[11px] uppercase tracking-wide leading-tight opacity-90">{it.label}</div>
          </div>
        </div>
      ))}
    </div>
  );
}
