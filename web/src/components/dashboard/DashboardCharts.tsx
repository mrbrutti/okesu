// Recharts-based chart components for the Dashboard page. Lazy-loaded
// from Dashboard.tsx so the ~110KB recharts chunk only loads on the
// /dashboard route.

import { useMemo } from 'react';
import {
  Area,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  LabelList,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { DashboardResponse, InsightsEventsResponse, InsightsFindingsResponse, InsightsOrchestrationsTopResponse, InsightsTriageOutcomesResponse, OrchestrationRunView, TimeRange } from '../../api';

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
  // See FindingsTimelineChart for why these `?? []` coalesces matter.
  const buckets = data.buckets ?? [];
  const rows = useMemo(() => {
    let running = 0;
    return buckets.map((b) => {
      running += b.count;
      const v = scale === 'cumulative' ? running : b.count;
      return { ts: tsLabel(b.ts, data.bucket_ms), count: scale === 'log' ? Math.max(v, 0.5) : v };
    });
  }, [data, scale, buckets]);
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
  // Defensive coalesce — the Go API serializes empty slices as null
  // when the underlying slice is nil. Pre-fix servers (< 7755b08) and
  // federated children running an older binary still return null,
  // which would crash here without these `?? []` guards.
  const series = data.series ?? [];
  const buckets = data.buckets ?? [];
  const rows = useMemo(() => {
    const running: Record<string, number> = Object.fromEntries(series.map((k) => [k, 0]));
    return buckets.map((b) => {
      const row: Record<string, number | string> = { ts: tsLabel(b.ts, data.bucket_ms) };
      for (const k of series) {
        const raw = b.by?.[k] ?? 0;
        running[k] += raw;
        const v = scale === 'cumulative' ? running[k] : raw;
        // Log scale chokes on 0 — clamp to 0.5 so a flat zero series still renders.
        row[k] = scale === 'log' ? Math.max(v, 0.5) : v;
      }
      return row;
    });
  }, [data, scale, series, buckets]);

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
        {series.map((k, i) => (
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

// ── Orchestration runs over time (multi-line) ───────────────────────

// Status colors picked to align with the StatusPill / OrchestrationOutcomes
// rows so completed-green / failed-red / running-blue read the same on the
// dashboard, the runs tab, and the run-detail page.
const RUN_STATUS_COLOR: Record<string, string> = {
  completed: '#10b981', // emerald-500
  failed:    '#dc2626', // red-600
  running:   '#0891b2', // cyan-600
};

// Per-range bucket size — chosen so each range renders ~30–96 buckets,
// dense enough to see trends without crushing the X axis.
function runChartBucketMs(range: TimeRange): number {
  switch (range) {
    case '30m': return 60 * 1000;            //  1m  → 30 buckets
    case '1h':  return 2 * 60 * 1000;        //  2m  → 30 buckets
    case '24h': return 15 * 60 * 1000;       // 15m  → 96 buckets
    case '7d':  return 4 * 60 * 60 * 1000;   //  4h  → 42 buckets
    case '30d': return 24 * 60 * 60 * 1000;  //  1d  → 30 buckets
  }
}

function runChartSinceMs(range: TimeRange): number {
  const now = Date.now();
  switch (range) {
    case '30m': return now - 30 * 60 * 1000;
    case '1h':  return now - 60 * 60 * 1000;
    case '24h': return now - 24 * 60 * 60 * 1000;
    case '7d':  return now - 7 * 24 * 60 * 60 * 1000;
    case '30d': return now - 30 * 24 * 60 * 60 * 1000;
  }
}

export function OrchestrationRunsTimelineChart({
  runs, range, scale = 'linear',
}: {
  runs: OrchestrationRunView[];
  range: TimeRange;
  scale?: Scale;
}) {
  const { rows, totals } = useMemo(() => {
    const bucketMs = runChartBucketMs(range);
    const sinceMs = runChartSinceMs(range);
    const now = Date.now();

    // Pre-build empty buckets so the X axis is continuous and the chart
    // shows quiet stretches as flat zeros instead of jumping over them.
    const grid = new Map<number, { completed: number; failed: number; running: number }>();
    const start = Math.floor(sinceMs / bucketMs) * bucketMs;
    for (let t = start; t <= now; t += bucketMs) {
      grid.set(t, { completed: 0, failed: 0, running: 0 });
    }

    let totalCompleted = 0, totalFailed = 0, totalRunning = 0;
    for (const r of runs) {
      const ts = new Date(r.started_at).getTime();
      if (ts < sinceMs) continue;
      const bucket = Math.floor(ts / bucketMs) * bucketMs;
      const slot = grid.get(bucket);
      if (!slot) continue;
      if (r.status === 'completed')      { slot.completed++; totalCompleted++; }
      else if (r.status === 'failed')    { slot.failed++;    totalFailed++; }
      else if (r.status === 'running')   { slot.running++;   totalRunning++; }
    }

    let cumC = 0, cumF = 0, cumR = 0;
    const rows = [...grid.entries()].sort(([a], [b]) => a - b).map(([ts, by]) => {
      cumC += by.completed; cumF += by.failed; cumR += by.running;
      const c  = scale === 'cumulative' ? cumC : by.completed;
      const f  = scale === 'cumulative' ? cumF : by.failed;
      const rn = scale === 'cumulative' ? cumR : by.running;
      return {
        ts: tsLabel(ts, bucketMs),
        completed: scale === 'log' ? Math.max(c, 0.5)  : c,
        failed:    scale === 'log' ? Math.max(f, 0.5)  : f,
        running:   scale === 'log' ? Math.max(rn, 0.5) : rn,
      };
    });

    return { rows, totals: { completed: totalCompleted, failed: totalFailed, running: totalRunning } };
  }, [runs, range, scale]);

  if (totals.completed + totals.failed + totals.running === 0) {
    return (
      <div className="text-xs text-ink-mute py-12 text-center">
        <div className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-slate-100 text-slate-400 mb-2">·</div>
        <div>No orchestration runs in the last {range}.</div>
      </div>
    );
  }

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
        <Line type="monotone" dataKey="completed" stroke={RUN_STATUS_COLOR.completed} strokeWidth={2} dot={false} activeDot={{ r: 4, strokeWidth: 0 }} />
        <Line type="monotone" dataKey="failed"    stroke={RUN_STATUS_COLOR.failed}    strokeWidth={2} dot={false} activeDot={{ r: 4, strokeWidth: 0 }} />
        <Line type="monotone" dataKey="running"   stroke={RUN_STATUS_COLOR.running}   strokeWidth={2} strokeDasharray="4 3" dot={false} activeDot={{ r: 4, strokeWidth: 0 }} />
      </LineChart>
    </ResponsiveContainer>
  );
}

// ── Findings & Triage (stacked area) ────────────────────────────────

// Color tokens picked so the visual mirrors the operator's mental
// model: incoming wraps everything as a thin envelope line; the
// auto-* outcomes stack underneath in cooling colors (T0 darkest,
// T1 lighter). Anything left between auto-handled and incoming is
// "still in the operator queue" — visually obvious.
const TRIAGE_COLOR = {
  incoming:      '#1f2937', // slate-800 — outline / envelope
  t0_superseded: '#7c3aed', // brand-500 — Tier-0 dedup
  t1_resolved:   '#10b981', // emerald-500 — auto-closed
  t1_tagged:     '#0891b2', // cyan-600 — auto-confirmed (still open)
  still_open:    '#f97316', // orange-500 — surfaced to operator
};

export function FindingsTriageChart({
  data, range,
}: {
  data: InsightsTriageOutcomesResponse;
  range: TimeRange;
}) {
  const rows = useMemo(() => {
    const buckets = data.buckets ?? [];
    return buckets.map((b) => {
      const handled = b.t0_superseded + b.t1_resolved + b.t1_tagged;
      const stillOpen = Math.max(0, b.incoming - handled);
      return {
        ts: tsLabel(b.ts, data.bucket_ms),
        // Stack order matters — Recharts paints in declaration order.
        // T0 first (closest to baseline), then T1 closures, then T1
        // tag-only confirmations, then "still open" on top so the
        // operator's eye lands on the slice they actually own.
        t0_superseded: b.t0_superseded,
        t1_resolved:   b.t1_resolved,
        t1_tagged:     b.t1_tagged,
        still_open:    stillOpen,
        incoming:      b.incoming, // separate line, drawn over the stack
      };
    });
  }, [data]);

  const hasData = (data.buckets ?? []).some((b) =>
    b.incoming + b.t0_superseded + b.t1_resolved + b.t1_tagged > 0,
  );
  if (!hasData) {
    return (
      <div className="text-xs text-ink-mute py-12 text-center">
        <div className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-slate-100 text-slate-400 mb-2">·</div>
        <div>No findings or triage activity in the last {range}.</div>
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={220}>
      <ComposedChart data={rows} margin={{ top: 8, right: 12, left: -12, bottom: 0 }}>
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
        <Legend wrapperStyle={{ fontSize: 11, paddingTop: 6 }} iconType="square" />
        {/* Stack the four outcome categories. stackId ties them. */}
        <Area
          type="monotone"
          dataKey="t0_superseded"
          name="T0 dedup"
          stackId="triage"
          stroke={TRIAGE_COLOR.t0_superseded}
          fill={TRIAGE_COLOR.t0_superseded}
          fillOpacity={0.55}
        />
        <Area
          type="monotone"
          dataKey="t1_resolved"
          name="T1 resolved"
          stackId="triage"
          stroke={TRIAGE_COLOR.t1_resolved}
          fill={TRIAGE_COLOR.t1_resolved}
          fillOpacity={0.55}
        />
        <Area
          type="monotone"
          dataKey="t1_tagged"
          name="T1 tagged"
          stackId="triage"
          stroke={TRIAGE_COLOR.t1_tagged}
          fill={TRIAGE_COLOR.t1_tagged}
          fillOpacity={0.55}
        />
        <Area
          type="monotone"
          dataKey="still_open"
          name="Still in queue"
          stackId="triage"
          stroke={TRIAGE_COLOR.still_open}
          fill={TRIAGE_COLOR.still_open}
          fillOpacity={0.6}
        />
        {/* Incoming as an outline line so the operator can see the
            envelope at a glance — the gap between this line and the
            top of the stack is "auto-handled before close-of-bucket". */}
        <Line
          type="monotone"
          dataKey="incoming"
          name="Incoming"
          stroke={TRIAGE_COLOR.incoming}
          strokeWidth={1.5}
          dot={false}
          strokeDasharray="3 2"
        />
      </ComposedChart>
    </ResponsiveContainer>
  );
}

// ── Top orchestrations (horizontal stacked bar) ─────────────────────

// Surfaces which orchestration is the busiest, and within that, what
// fraction is succeeding vs failing. The avg-duration column gives a
// performance hint without needing a second chart.
export function TopOrchestrationsChart({
  data,
}: {
  data: InsightsOrchestrationsTopResponse;
}) {
  const rows = data.rows ?? [];
  if (rows.length === 0) {
    return (
      <div className="text-xs text-ink-mute py-12 text-center">
        <div className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-slate-100 text-slate-400 mb-2">·</div>
        <div>No orchestration runs in the last {data.since}.</div>
      </div>
    );
  }
  const maxTotal = Math.max(1, ...rows.map((r) => r.total));
  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between text-[11px] text-ink-mute">
        <span>{rows.length} orchestration{rows.length === 1 ? '' : 's'} active</span>
        <span>since {data.since}</span>
      </div>
      <ul className="space-y-1.5">
        {rows.map((r) => {
          const completedPct = r.total > 0 ? (r.completed / r.total) * 100 : 0;
          const failedPct    = r.total > 0 ? (r.failed    / r.total) * 100 : 0;
          const runningPct   = r.total > 0 ? (r.running   / r.total) * 100 : 0;
          const otherPct     = Math.max(0, 100 - completedPct - failedPct - runningPct);
          const widthPct = (r.total / maxTotal) * 100;
          return (
            <li key={r.orchestration_id} className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 items-center text-xs">
              <div className="min-w-0">
                <div className="flex items-center justify-between gap-2 mb-0.5">
                  <span className="font-mono truncate text-ink">{r.name}</span>
                  <span className="tabular-nums text-ink-dim shrink-0">{r.total}</span>
                </div>
                <div className="relative h-2 rounded ring-1 ring-border overflow-hidden bg-slate-50">
                  {/* outer bar — width proportional to total vs the
                      busiest orchestration in the window */}
                  <div className="h-full flex" style={{ width: `${widthPct}%` }}>
                    {r.completed > 0 && <div className="bg-emerald-500" style={{ width: `${completedPct}%` }} title={`${r.completed} completed`} />}
                    {r.failed    > 0 && <div className="bg-red-500"     style={{ width: `${failedPct}%` }}    title={`${r.failed} failed`} />}
                    {r.running   > 0 && <div className="bg-cyan-500"    style={{ width: `${runningPct}%` }}   title={`${r.running} running`} />}
                    {otherPct    > 0 && <div className="bg-slate-300"   style={{ width: `${otherPct}%` }}     title="cancelled / pending / approval" />}
                  </div>
                </div>
              </div>
              <div className="text-[10px] text-ink-mute tabular-nums whitespace-nowrap min-w-[64px] text-right">
                {r.avg_duration_ms ? `${formatDuration(r.avg_duration_ms)} avg` : '—'}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const r = s % 60;
  if (m < 60) return r === 0 ? `${m}m` : `${m}m${r}s`;
  const h = Math.floor(m / 60);
  return `${h}h${m % 60}m`;
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
