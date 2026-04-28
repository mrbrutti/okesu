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
const OSDistributionChart = lazy(() =>
  import('../components/dashboard/DashboardCharts').then((m) => ({ default: m.OSDistributionChart })),
);

type GroupBy = 'severity' | 'agent' | 'host';
export type Scale = 'linear' | 'log' | 'cumulative';

const RANGES: TimeRange[] = ['30m', '1h', '24h', '7d', '30d'];
const GROUPS: GroupBy[] = ['severity', 'agent', 'host'];
const SCALES: Scale[] = ['linear', 'log', 'cumulative'];

function parseRange(s: string | null): TimeRange {
  return RANGES.includes(s as TimeRange) ? (s as TimeRange) : '24h';
}
function parseGroupBy(s: string | null): GroupBy {
  return GROUPS.includes(s as GroupBy) ? (s as GroupBy) : 'severity';
}
function parseScale(s: string | null): Scale {
  return SCALES.includes(s as Scale) ? (s as Scale) : 'linear';
}

export default function DashboardPage() {
  const [data, setData] = useState<DashboardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [params, setParams] = useSearchParams();

  const range = parseRange(params.get('range'));
  const groupBy = parseGroupBy(params.get('groupBy'));
  // Two scales — one per timeline chart — so changing one doesn't yank
  // the other. Reads ?eventsScale=/?findingsScale=, falling back to the
  // legacy single ?scale= for old bookmarks, then to 'linear'.
  const legacyScale = params.get('scale');
  const eventsScale = parseScale(params.get('eventsScale') ?? legacyScale);
  const findingsScale = parseScale(params.get('findingsScale') ?? legacyScale);

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
  function setScale(key: 'eventsScale' | 'findingsScale', s: Scale) {
    const p = new URLSearchParams(params);
    p.set(key, s);
    // Drop the legacy combined ?scale= so it doesn't shadow the per-chart
    // values once the operator opts into the new picker.
    p.delete('scale');
    setParams(p, { replace: true });
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
            <Activity size={14} />
          </span>
          Dashboard
        </h1>
        <p className="text-xs text-ink-dim mt-0.5 ml-9">Fleet health at a glance — refreshes every 15 seconds.</p>
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
            accent={
              !data ? 'info'
                : data.daimons.unhealthy > 0 ? 'warn'
                : data.daimons.suspended > 0 ? 'info'
                : 'good'
            }
            sub={data ? formatDaimonsSub(data.daimons) : ''}
            // Inline segmented bar showing running / suspended / unhealthy
            // split. Gives the pause feature visual weight without growing
            // the tile's footprint.
            segments={data ? [
              { value: data.daimons.healthy,   color: 'bg-green-500',  title: `${data.daimons.healthy} running` },
              { value: data.daimons.suspended, color: 'bg-yellow-400', title: `${data.daimons.suspended} paused` },
              { value: data.daimons.unhealthy, color: 'bg-red-400',    title: `${data.daimons.unhealthy} unhealthy` },
            ] : undefined}
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
            subtitle={`${scaleLabel(eventsScale)} / ${range}`}
            right={
              <div className="flex items-center gap-1.5">
                <Picker value={eventsScale} options={SCALES} onChange={(v) => setScale('eventsScale', v as Scale)} />
                <RangePicker value={range} onChange={setRange} />
              </div>
            }
          >
            {!events ? <ChartSkeleton /> : (
              <Suspense fallback={<ChartSkeleton />}>
                <EventsTimelineChart data={events} range={range} scale={eventsScale} />
              </Suspense>
            )}
          </Card>
          <Card
            title="Findings"
            subtitle={`${scaleLabel(findingsScale)} / by ${groupBy} / ${range}`}
            right={
              <div className="flex items-center gap-1.5">
                <Picker value={groupBy} options={GROUPS} onChange={(v) => setGroupBy(v as GroupBy)} />
                <Picker value={findingsScale} options={SCALES} onChange={(v) => setScale('findingsScale', v as Scale)} />
                <RangePicker value={range} onChange={setRange} />
              </div>
            }
          >
            {!findings ? <ChartSkeleton /> : (
              <Suspense fallback={<ChartSkeleton />}>
                <FindingsTimelineChart data={findings} range={range} scale={findingsScale} />
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
                <OSDistributionChart data={data.os_distribution} />
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

type Segment = { value: number; color: string; title: string };

function formatDaimonsSub(d: { suspended: number; unhealthy: number }): string {
  const parts: string[] = [];
  if (d.suspended > 0) parts.push(`${d.suspended} paused`);
  if (d.unhealthy > 0) parts.push(`${d.unhealthy} unhealthy`);
  if (parts.length === 0) return 'all running';
  return parts.join(' · ');
}

function StatTile({
  label, icon: Icon, primary, sub, accent, to, loading, segments,
}: {
  label: string;
  icon: typeof Cpu;
  primary: string;
  sub?: string;
  accent: Accent;
  to: string;
  loading: boolean;
  // Optional segmented bar rendered under the primary number. Each
  // segment's width is proportional to its value across the row.
  segments?: Segment[];
}) {
  const accentStripe = {
    good: 'bg-green-500',
    warn: 'bg-yellow-500',
    bad:  'bg-red-500',
    info: 'bg-brand-500',
  }[accent];
  const dotCls = {
    good: 'bg-green-500',
    warn: 'bg-yellow-500',
    bad:  'bg-red-500',
    info: 'bg-slate-400',
  }[accent];
  return (
    <Link
      to={to}
      className="group relative block bg-panel border border-border rounded-xl shadow-card overflow-hidden hover:border-brand-200 hover:shadow-md transition-all"
    >
      {/* accent stripe — colored top edge picks up the same tone as the
          status dot, giving the row of tiles a quick chromatic read */}
      <div className={cn('h-0.5', accentStripe)} />
      <div className="p-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 text-[11px] uppercase tracking-wider text-ink-mute font-medium">
            <Icon size={12} className="text-brand-500/70 group-hover:text-brand-500 transition-colors" />
            {label}
          </div>
          <span className={cn('w-2 h-2 rounded-full', dotCls)} />
        </div>
        <div className="mt-2 text-2xl font-semibold text-ink tabular-nums leading-tight">
          {loading ? <Loader2 size={20} className="animate-spin text-ink-mute" /> : primary}
        </div>
        {segments && segments.some((s) => s.value > 0) && <Segments segments={segments} />}
        {sub && <div className="text-[11px] text-ink-mute mt-1 truncate">{sub}</div>}
      </div>
    </Link>
  );
}

// Segmented bar — proportional widths, with hover titles. Tiny by
// design (h-1.5) so it doesn't compete with the headline number.
function Segments({ segments }: { segments: Segment[] }) {
  const total = segments.reduce((sum, s) => sum + s.value, 0);
  if (total === 0) return null;
  return (
    <div className="mt-2 flex h-1.5 rounded-full overflow-hidden bg-slate-100">
      {segments.map((s, i) => {
        if (s.value === 0) return null;
        const pct = (s.value / total) * 100;
        return (
          <div
            key={i}
            title={s.title}
            className={cn(s.color, 'transition-all')}
            style={{ width: `${pct}%` }}
          />
        );
      })}
    </div>
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
    <section className="bg-panel border border-border rounded-xl shadow-card hover:border-brand-200/60 transition-colors">
      <header className="px-4 pt-3 pb-2 flex items-start justify-between gap-2 border-b border-border/60">
        <div className="min-w-0 flex items-center gap-2">
          <span className="w-1 h-4 rounded-full bg-gradient-to-b from-brand-400 to-brand-600" />
          <div className="min-w-0">
            <h3 className="text-sm font-semibold text-ink">{title}</h3>
            {subtitle && <p className="text-[11px] text-ink-dim mt-0.5 truncate">{subtitle}</p>}
          </div>
        </div>
        {right}
      </header>
      <div className="p-4 pt-3">{children}</div>
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

function scaleLabel(s: Scale): string {
  switch (s) {
    case 'linear':     return 'Linear';
    case 'log':        return 'Log';
    case 'cumulative': return 'Cumulative';
  }
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
