// Recharts-based chart components for the Dashboard page. Lazy-loaded
// from Dashboard.tsx so the ~110KB recharts chunk only loads on the
// /dashboard route.

import { useMemo } from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  LabelList,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { DashboardResponse, InsightsEventsResponse, InsightsFindingsResponse, TimeRange } from '../../api';

export type Scale = 'linear' | 'log' | 'cumulative';

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

export function EventsTimelineChart({ data, range, scale = 'linear' }: { data: InsightsEventsResponse; range: TimeRange; scale?: Scale }) {
  const rows = useMemo(() => {
    let running = 0;
    return data.buckets.map((b) => {
      running += b.count;
      const v = scale === 'cumulative' ? running : b.count;
      return { ts: tsLabel(b.ts, data.bucket_ms), count: scale === 'log' ? Math.max(v, 0.5) : v };
    });
  }, [data, scale]);
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
          scale={scale === 'log' ? 'log' : 'auto'}
          domain={scale === 'log' ? [0.5, 'auto'] : undefined}
          allowDataOverflow={scale === 'log'}
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

export function FindingsTimelineChart({ data, range, scale = 'linear' }: { data: InsightsFindingsResponse; range: TimeRange; scale?: Scale }) {
  const rows = useMemo(() => {
    const running: Record<string, number> = Object.fromEntries(data.series.map((k) => [k, 0]));
    return data.buckets.map((b) => {
      const row: Record<string, number | string> = { ts: tsLabel(b.ts, data.bucket_ms) };
      for (const k of data.series) {
        const raw = b.by[k] ?? 0;
        running[k] += raw;
        const v = scale === 'cumulative' ? running[k] : raw;
        // Log scale chokes on 0 — clamp to 0.5 so a flat zero series still renders.
        row[k] = scale === 'log' ? Math.max(v, 0.5) : v;
      }
      return row;
    });
  }, [data, scale]);

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
          scale={scale === 'log' ? 'log' : 'auto'}
          domain={scale === 'log' ? [0.5, 'auto'] : undefined}
          allowDataOverflow={scale === 'log'}
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
    return (
      <div className="text-xs text-ink-mute py-12 text-center">
        <div className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-green-50 text-green-600 mb-2">✓</div>
        <div>No findings — fleet is clean.</div>
      </div>
    );
  }
  const rows = data.map((d) => ({ host: d.host, open: d.open, critical: d.critical }));
  const height = Math.max(120, rows.length * 32);
  // Highlight the host with the most criticals — Cell-by-Cell coloring
  // lets us tint that one in sev.critical purple while the rest carry
  // the brand gradient. Operators triage here first.
  const maxCritical = Math.max(0, ...rows.map((r) => r.critical));
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={rows} layout="vertical" margin={{ top: 0, right: 36, left: 8, bottom: 0 }}>
        <defs>
          <linearGradient id="topHostsBrand" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0%"   stopColor="#a78bfa" />
            <stop offset="100%" stopColor="#7c3aed" />
          </linearGradient>
          <linearGradient id="topHostsCrit" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0%"   stopColor="#c084fc" />
            <stop offset="100%" stopColor="#9333ea" />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="#f1f5f9" horizontal={false} />
        <XAxis
          type="number"
          tick={{ fontSize: 10, fill: '#94a3b8' }}
          axisLine={false}
          tickLine={false}
          allowDecimals={false}
          hide
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
        <Bar dataKey="open" radius={[0, 6, 6, 0]} barSize={14}>
          {rows.map((r, i) => (
            <Cell
              key={i}
              fill={r.critical === maxCritical && maxCritical > 0 ? 'url(#topHostsCrit)' : 'url(#topHostsBrand)'}
            />
          ))}
          <LabelList
            dataKey="open"
            position="right"
            offset={8}
            fill="#475569"
            fontSize={11}
            fontWeight={600}
          />
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

// ── OS distribution (horizontal bar — fills the card width) ───────

// Per-OS color map. Bars get distinct hues so the chart reads at a
// glance without a separate legend.
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

export function OSDistributionChart({ data }: { data: DashboardResponse['os_distribution'] }) {
  if (!data || data.length === 0) {
    return (
      <div className="text-xs text-ink-mute py-12 text-center">
        <div className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-slate-100 text-slate-400 mb-2">·</div>
        <div>No nodes registered yet.</div>
      </div>
    );
  }
  const total = data.reduce((sum, b) => sum + b.count, 0);
  const rows = data.map((b) => ({ os: b.os, count: b.count, pct: (b.count / Math.max(total, 1)) * 100 }));
  const height = Math.max(160, rows.length * 38);
  // Per-OS gradients give the bars depth — same hue, two stops (light
  // → solid). The ID is derived from the OS slug so each family gets
  // a stable gradient definition reused across renders.
  const grads = useMemo(() => {
    const seen = new Set<string>();
    return rows.flatMap((r) => {
      if (seen.has(r.os)) return [];
      seen.add(r.os);
      const base = OS_COLOR[r.os] ?? '#94a3b8';
      return [{
        id: `os-${slug(r.os)}`,
        light: lighten(base, 0.25),
        solid: base,
      }];
    });
  }, [rows]);
  return (
    <div>
      <div className="flex items-baseline justify-between mb-2">
        <span className="text-xs text-ink-dim">
          <span className="text-ink font-medium tabular-nums">{rows.length}</span>{' '}
          OS famil{rows.length === 1 ? 'y' : 'ies'}
        </span>
        <span className="text-xs text-ink-dim tabular-nums">
          <span className="text-ink font-medium">{total}</span> nodes
        </span>
      </div>
      <ResponsiveContainer width="100%" height={height}>
        <BarChart data={rows} layout="vertical" margin={{ top: 4, right: 36, left: 8, bottom: 0 }}>
          <defs>
            {grads.map((g) => (
              <linearGradient key={g.id} id={g.id} x1="0" y1="0" x2="1" y2="0">
                <stop offset="0%"   stopColor={g.light} />
                <stop offset="100%" stopColor={g.solid} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="#f1f5f9" horizontal={false} />
          <XAxis
            type="number"
            tick={{ fontSize: 10, fill: '#94a3b8' }}
            axisLine={false}
            tickLine={false}
            allowDecimals={false}
            hide
          />
          <YAxis
            type="category"
            dataKey="os"
            tick={{ fontSize: 11, fill: '#475569', fontWeight: 500 }}
            axisLine={false}
            tickLine={false}
            width={90}
          />
          <Tooltip contentStyle={tooltipStyle} cursor={{ fill: '#f8fafc' }} />
          <Bar dataKey="count" radius={[0, 6, 6, 0]} barSize={14}>
            {rows.map((r) => (
              <Cell key={r.os} fill={`url(#os-${slug(r.os)})`} />
            ))}
            <LabelList
              dataKey="count"
              position="right"
              offset={8}
              fill="#475569"
              fontSize={11}
              fontWeight={600}
            />
          </Bar>
        </BarChart>
      </ResponsiveContainer>
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

// slug normalises an OS family label into a stable URL-safe id used
// as the linearGradient id. "Red Hat Enterprise Linux" → "red-hat…".
function slug(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
}

// lighten blends a hex color toward white by `amount` (0–1). Used to
// build the light end of each per-OS bar gradient.
function lighten(hex: string, amount: number): string {
  const m = hex.replace('#', '').match(/.{2}/g);
  if (!m || m.length !== 3) return hex;
  const [r, g, b] = m.map((h) => parseInt(h, 16));
  const mix = (c: number) => Math.round(c + (255 - c) * amount);
  return '#' + [mix(r), mix(g), mix(b)].map((n) => n.toString(16).padStart(2, '0')).join('');
}

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
