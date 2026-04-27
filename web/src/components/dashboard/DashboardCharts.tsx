// Recharts-based chart components for the Dashboard page. Imported
// lazily by Dashboard.tsx via React.lazy so the ~80KB recharts bundle
// only loads when an operator visits /dashboard — Findings / Daimons /
// Nodes pages don't pay the cost.

import { useMemo } from 'react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { DashboardResponse, InsightsFindingsResponse } from '../../api';

// Palette aligned with tailwind.config.ts brand + sev tokens. Slot 0
// is brand-500 (the actual app brand, #7c3aed). Slot 1 deliberately
// is NOT red (sev.high collides) — chose cyan-600 for separation.
// Slot 8 is sev.info (#64748b) which gives "other" enough contrast
// against the chart's slate-200 grid lines.
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

// Severity colors track tailwind.config.ts sev.* tokens exactly so
// chart series match the badges on Findings, Dashboard, and the
// CRIT pills in the Stale fleet list.
const SEVERITY_COLOR: Record<string, string> = {
  CRITICAL: '#9333ea', // sev.critical (purple-600)
  HIGH:     '#dc2626', // sev.high (red-600)
  MEDIUM:   '#ea580c', // sev.medium (orange-600)
  LOW:      '#ca8a04', // sev.low (yellow-600)
  INFO:     '#64748b', // sev.info (slate-500)
};

function colorFor(seriesName: string, idx: number, isSeverity: boolean): string {
  if (isSeverity) {
    return SEVERITY_COLOR[seriesName] ?? PALETTE[idx % PALETTE.length];
  }
  if (seriesName === 'other') return PALETTE[8];
  // Skip slot 0 for non-severity grouping so the most-active series
  // doesn't read as "primary/selected" by virtue of using brand color.
  return PALETTE[(idx + 1) % PALETTE.length];
}

// ── Events / hour, stacked area ─────────────────────────────────────

export function EventsPerHourChart({ data }: { data: DashboardResponse['events_per_hour'] }) {
  // Recharts wants flat row objects: one per bucket, with each event
  // type as its own key. Find the union of keys across all buckets so
  // we can build a stable Area for each.
  const { rows, keys } = useMemo(() => {
    const allKeys = new Set<string>();
    const r = data.map((b) => {
      const row: Record<string, number | string> = { hour: hourLabel(b.hour_ts) };
      for (const [k, v] of Object.entries(b.by_type)) {
        allKeys.add(k);
        row[k] = v;
      }
      return row;
    });
    // Order: most-frequent types first so they're at the bottom of
    // the stack (chart convention).
    const totals: Record<string, number> = {};
    for (const k of allKeys) totals[k] = 0;
    for (const b of data) {
      for (const [k, v] of Object.entries(b.by_type)) totals[k] += v;
    }
    const ordered = [...allKeys].sort((a, b) => totals[b] - totals[a]);
    return { rows: r, keys: ordered };
  }, [data]);

  return (
    <ResponsiveContainer width="100%" height={220}>
      <AreaChart data={rows} margin={{ top: 5, right: 12, left: -10, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" vertical={false} />
        <XAxis dataKey="hour" tick={{ fontSize: 10, fill: '#64748b' }} interval={3} />
        <YAxis tick={{ fontSize: 10, fill: '#64748b' }} allowDecimals={false} />
        <Tooltip contentStyle={tooltipStyle} labelStyle={{ fontWeight: 600 }} />
        <Legend wrapperStyle={{ fontSize: 11 }} />
        {keys.map((k, i) => (
          <Area
            key={k}
            type="monotone"
            dataKey={k}
            stackId="1"
            stroke={PALETTE[i % PALETTE.length]}
            fill={PALETTE[i % PALETTE.length]}
            fillOpacity={0.55}
          />
        ))}
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ── Findings over time, multi-series line ───────────────────────────

export function FindingsTimelineChart({ data }: { data: InsightsFindingsResponse }) {
  const { rows, keys } = useMemo(() => {
    const r = data.buckets.map((b) => {
      const row: Record<string, number | string> = { ts: tsLabel(b.ts, data.bucket_ms) };
      // Pre-fill all series with 0 so Recharts renders a continuous
      // line through gaps instead of a tooltip-NaN.
      for (const k of data.series) row[k] = b.by[k] ?? 0;
      return row;
    });
    return { rows: r, keys: data.series };
  }, [data]);

  const isSeverity = data.group_by === 'severity';

  return (
    <ResponsiveContainer width="100%" height={260}>
      <LineChart data={rows} margin={{ top: 5, right: 12, left: -10, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" vertical={false} />
        <XAxis dataKey="ts" tick={{ fontSize: 10, fill: '#64748b' }} interval="preserveStartEnd" />
        <YAxis tick={{ fontSize: 10, fill: '#64748b' }} allowDecimals={false} />
        <Tooltip contentStyle={tooltipStyle} labelStyle={{ fontWeight: 600 }} />
        <Legend wrapperStyle={{ fontSize: 11 }} />
        {keys.map((k, i) => (
          <Line
            key={k}
            type="monotone"
            dataKey={k}
            stroke={colorFor(k, i, isSeverity)}
            dot={false}
            strokeWidth={2}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
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

function hourLabel(unixMs: number): string {
  const d = new Date(unixMs);
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

// tsLabel formats a bucket timestamp at a granularity matching
// bucket_ms. 24h view (1h buckets) shows time-of-day; 7d (6h
// buckets) shows day+hour; 30d (1d buckets) shows the date.
function tsLabel(unixMs: number, bucketMs: number): string {
  const d = new Date(unixMs);
  if (bucketMs >= 24 * 3600 * 1000) {
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  if (bucketMs >= 6 * 3600 * 1000) {
    return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit' });
  }
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}
