// Recharts-based chart components for the Dashboard page. Lazy-loaded
// from Dashboard.tsx so the ~110KB recharts chunk only loads on the
// /dashboard route.

import { useMemo } from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { DashboardResponse, InsightsEventsResponse, InsightsFindingsResponse, TimeRange } from '../../api';

// Palette aligned with tailwind.config.ts brand-500 + sev.* tokens.
// Slot 0 is brand-500. Slot 1 is cyan-600 (deliberately NOT red,
// which would collide with sev.high in multi-line severity charts).
// Slot 8 = sev.info for "other" — has contrast against the slate
// grid lines.
const PALETTE = [
  '#7c3aed', // brand-500
  '#0891b2', // cyan-600
  '#f59e0b', // amber-500
  '#10b981', // emerald-500
  '#8b5cf6', // violet-500
  '#ec4899', // pink-500
  '#84cc16', // lime-500
  '#0284c7', // sky-600
  '#64748b', // slate-500 — used for "other"
];

// Severity colors track tailwind sev.* tokens exactly.
const SEVERITY_COLOR: Record<string, string> = {
  CRITICAL: '#9333ea',
  HIGH:     '#dc2626',
  MEDIUM:   '#ea580c',
  LOW:      '#ca8a04',
  INFO:     '#64748b',
};

function colorFor(seriesName: string, idx: number, isSeverity: boolean): string {
  if (isSeverity) {
    return SEVERITY_COLOR[seriesName] ?? PALETTE[idx % PALETTE.length];
  }
  if (seriesName === 'other') return PALETTE[8];
  return PALETTE[(idx + 1) % PALETTE.length];
}

// ── Events timeline (single line) ───────────────────────────────────

export function EventsTimelineChart({ data, range }: { data: InsightsEventsResponse; range: TimeRange }) {
  const rows = useMemo(
    () => data.buckets.map((b) => ({ ts: tsLabel(b.ts, data.bucket_ms), count: b.count })),
    [data],
  );
  return (
    <ResponsiveContainer width="100%" height={180}>
      <LineChart data={rows} margin={{ top: 8, right: 12, left: -12, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#f1f5f9" vertical={false} />
        <XAxis
          dataKey="ts"
          tick={{ fontSize: 10, fill: '#94a3b8' }}
          interval={tickInterval(range)}
          axisLine={false}
          tickLine={false}
        />
        <YAxis
          tick={{ fontSize: 10, fill: '#94a3b8' }}
          allowDecimals={false}
          axisLine={false}
          tickLine={false}
          width={40}
        />
        <Tooltip contentStyle={tooltipStyle} labelStyle={{ fontWeight: 600 }} cursor={{ stroke: '#cbd5e1', strokeWidth: 1 }} />
        <Line
          type="monotone"
          dataKey="count"
          stroke={PALETTE[0]}
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4, strokeWidth: 0 }}
        />
      </LineChart>
    </ResponsiveContainer>
  );
}

// ── Findings over time, multi-series line ───────────────────────────

export function FindingsTimelineChart({ data, range }: { data: InsightsFindingsResponse; range: TimeRange }) {
  const rows = useMemo(() => {
    return data.buckets.map((b) => {
      const row: Record<string, number | string> = { ts: tsLabel(b.ts, data.bucket_ms) };
      for (const k of data.series) row[k] = b.by[k] ?? 0;
      return row;
    });
  }, [data]);

  const isSeverity = data.group_by === 'severity';

  return (
    <ResponsiveContainer width="100%" height={220}>
      <LineChart data={rows} margin={{ top: 8, right: 12, left: -12, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#f1f5f9" vertical={false} />
        <XAxis
          dataKey="ts"
          tick={{ fontSize: 10, fill: '#94a3b8' }}
          interval={tickInterval(range)}
          axisLine={false}
          tickLine={false}
        />
        <YAxis
          tick={{ fontSize: 10, fill: '#94a3b8' }}
          allowDecimals={false}
          axisLine={false}
          tickLine={false}
          width={40}
        />
        <Tooltip contentStyle={tooltipStyle} labelStyle={{ fontWeight: 600 }} cursor={{ stroke: '#cbd5e1', strokeWidth: 1 }} />
        <Legend wrapperStyle={{ fontSize: 11, paddingTop: 6 }} iconType="line" />
        {data.series.map((k, i) => (
          <Line
            key={k}
            type="monotone"
            dataKey={k}
            stroke={colorFor(k, i, isSeverity)}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4, strokeWidth: 0 }}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}

// ── Top affected hosts (horizontal bar) ─────────────────────────────

export function TopHostsChart({ data }: { data: DashboardResponse['top_hosts'] }) {
  if (!data || data.length === 0) {
    return <div className="text-xs text-ink-mute py-12 text-center">No findings — fleet is clean.</div>;
  }
  const rows = data.map((d) => ({ host: d.host, open: d.open, critical: d.critical }));
  const height = Math.max(120, rows.length * 28);
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={rows} layout="vertical" margin={{ top: 0, right: 12, left: 8, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#f1f5f9" horizontal={false} />
        <XAxis
          type="number"
          tick={{ fontSize: 10, fill: '#94a3b8' }}
          axisLine={false}
          tickLine={false}
          allowDecimals={false}
        />
        <YAxis
          type="category"
          dataKey="host"
          tick={{ fontSize: 11, fill: '#475569' }}
          axisLine={false}
          tickLine={false}
          width={120}
        />
        <Tooltip contentStyle={tooltipStyle} cursor={{ fill: '#f8fafc' }} />
        <Bar dataKey="open" fill={PALETTE[0]} radius={[0, 4, 4, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
}

// ── OS distribution donut ──────────────────────────────────────────

// Per-OS color map. Distinct colors so the donut + legend are
// glanceable even with 5+ slices. "Unknown" stays slate.
const OS_COLOR: Record<string, string> = {
  Debian:    '#a81d33', // debian red
  Ubuntu:    '#e95420', // ubuntu orange
  Fedora:    '#3c6eb4', // fedora blue
  Rocky:     '#10b981', // emerald
  AlmaLinux: '#0d597f',
  RHEL:      '#ee0000',
  CentOS:    '#9ccd2a',
  Alpine:    '#0d597f',
  Arch:      '#1793d1',
  macOS:     '#94a3b8',
  Unknown:   '#cbd5e1',
};

export function OSDistributionDonut({ data }: { data: DashboardResponse['os_distribution'] }) {
  const total = data.reduce((sum, b) => sum + b.count, 0);
  if (total === 0) {
    return <div className="text-xs text-ink-mute py-12 text-center">No nodes registered yet.</div>;
  }
  const slices = data.map((b) => ({
    name: b.os,
    value: b.count,
    color: OS_COLOR[b.os] ?? '#94a3b8',
  }));
  return (
    <div className="flex items-center gap-4">
      <div className="relative w-[140px] h-[140px] shrink-0">
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie
              data={slices}
              dataKey="value"
              nameKey="name"
              cx="50%"
              cy="50%"
              innerRadius={42}
              outerRadius={62}
              paddingAngle={2}
              startAngle={90}
              endAngle={-270}
            >
              {slices.map((s) => <Cell key={s.name} fill={s.color} />)}
            </Pie>
            <Tooltip contentStyle={tooltipStyle} />
          </PieChart>
        </ResponsiveContainer>
        <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
          <div className="text-2xl font-semibold tabular-nums text-ink">{total}</div>
          <div className="text-[10px] uppercase tracking-wide text-ink-mute">nodes</div>
        </div>
      </div>
      <ul className="text-xs space-y-1.5 flex-1 min-w-0">
        {slices.map((s) => (
          <li key={s.name} className="flex items-center gap-2">
            <span className="w-2 h-2 rounded-full shrink-0" style={{ background: s.color }} />
            <span className="text-ink-dim flex-1 truncate">{s.name}</span>
            <span className="font-mono tabular-nums text-ink">{s.value}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ── helpers ─────────────────────────────────────────────────────────

const tooltipStyle: React.CSSProperties = {
  background: '#fff',
  border: '1px solid #e2e8f0',
  borderRadius: 6,
  fontSize: 11,
  padding: '6px 10px',
  boxShadow: '0 2px 6px rgba(0,0,0,0.06)',
};

// tickInterval picks a sensible XAxis density for the chart so labels
// don't overlap. Recharts will draw every Nth tick.
function tickInterval(range: TimeRange): number | 'preserveStartEnd' {
  switch (range) {
    case '30m': return 4;  // 30 buckets / 1m, show every 5th
    case '1h':  return 4;
    case '24h': return 3;  // 24 buckets / 1h
    case '7d':  return 3;  // 28 buckets / 6h
    case '30d': return 4;  // 30 buckets / 1d
  }
}

function tsLabel(unixMs: number, bucketMs: number): string {
  const d = new Date(unixMs);
  if (bucketMs >= 24 * 3600 * 1000) {
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  if (bucketMs >= 6 * 3600 * 1000) {
    return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit' });
  }
  // <= 1h buckets: time of day, with seconds when bucket < 1m
  if (bucketMs < 60 * 1000) {
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}
