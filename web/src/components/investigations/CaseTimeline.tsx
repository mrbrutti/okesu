// Horizontal Gantt-style timeline of case events. Hand-rolled SVG;
// no extra deps. Default-curated lanes (lifecycle/findings/runs/
// notes) + opt-in lanes (iocs/daimons/audit). Audit-lane fetch is
// lazy: triggered the first time the operator toggles audit on,
// using the existing api.investigations.audit endpoint.
//
// Refresh is driven by the parent's existing 30s/5s war-room poll —
// re-render happens on every bundle replacement.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { InvestigationDetail, InvestigationAuditEvent, SavedSearch } from '../../api';
import { api } from '../../api';
import { TimelineFilterBar } from './TimelineFilterBar';
import { SavedSearchesBar } from '../SavedSearchesBar';
import { applyTimelineFilter, type TimelineFilterConfig } from './timeline/filter';
import { buildEvents } from './timeline/buildEvents';
import { ALL_LANES, DEFAULT_LANES_ON, type TimelineLane, type TimelineEvent } from './timeline/types';
import { autoFitRange, presetRange, tToX, type Preset, type Range } from './timeline/scale';
import { Tooltip } from '../Tooltip';
import { clusterEvents, type ClusterOrEvent } from './timeline/cluster';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
}

const STORAGE_KEY = 'investigation:overview:lanes';
const LANE_HEIGHT = 36;
const LANE_LABEL_W = 80;
const SVG_WIDTH = 1600;
const TOP_PADDING = 8;
const BOTTOM_PADDING = 8;
// Minimum height keeps the empty-state placeholder readable; otherwise
// the SVG sizes to fit its visible lanes so the operator never sees
// a wall of empty 320-px canvas under a 4-lane case.
const MIN_HEIGHT = 200;

export function CaseTimeline({ bundle, cpInstanceID }: Props) {
  const [activeLanes, setActiveLanes] = useState<TimelineLane[]>(loadLanes);
  const [audit, setAudit] = useState<InvestigationAuditEvent[]>([]);
  const [range, setRange] = useState<Range | null>(null);
  const auditFetchedRef = useRef(false);
  const [filter, setFilter] = useState<TimelineFilterConfig>({});
  const [savedSearches, setSavedSearches] = useState<SavedSearch[]>([]);
  const defaultAppliedRef = useRef(false);

  const refreshSavedSearches = () => {
    api.savedSearches.list('investigation_timeline')
      .then(setSavedSearches)
      .catch(() => { /* silent — filtering still works without saved searches */ });
  };
  useEffect(() => { refreshSavedSearches(); }, []);

  // Apply default saved search once after the list lands. Skip if a
  // filter is already set (operator already changed something) so we
  // don't clobber explicit intent.
  useEffect(() => {
    if (defaultAppliedRef.current) return;
    if (savedSearches.length === 0) return;
    const def = savedSearches.find((s) => s.is_default);
    defaultAppliedRef.current = true;
    if (!def) return;
    const hasFilter = Object.keys(filter).length > 0;
    if (hasFilter) return;
    try {
      const parsed = JSON.parse(def.config_json) as TimelineFilterConfig;
      setFilter(parsed);
    } catch {
      console.warn('investigation_timeline saved search has malformed config_json', def.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedSearches]);

  // Lazy-fetch audit lane the first time it's enabled.
  useEffect(() => {
    if (!activeLanes.includes('audit') || auditFetchedRef.current) return;
    auditFetchedRef.current = true;
    api.investigations.audit(bundle.investigation.ID, cpInstanceID)
      .then(setAudit)
      .catch(() => { auditFetchedRef.current = false; });
  }, [activeLanes, bundle.investigation.ID, cpInstanceID]);

  const events = useMemo(() => buildEvents(bundle, audit), [bundle, audit]);

  const filteredEvents = useMemo(() => applyTimelineFilter(events, filter), [events, filter]);

  const activeSavedID = useMemo(() => {
    const target = JSON.stringify(filter);
    for (const s of savedSearches) {
      try {
        if (JSON.stringify(JSON.parse(s.config_json)) === target) return s.id;
      } catch { /* skip malformed */ }
    }
    return null;
  }, [savedSearches, filter]);

  const visibleLanes = useMemo(() => ALL_LANES.filter((l) => activeLanes.includes(l)), [activeLanes]);

  // Auto-fit on first render and when bundle freshness advances.
  useEffect(() => {
    const stamps: number[] = [];
    for (const e of events) {
      if (e.kind === 'run' || e.kind === 'ioc') {
        stamps.push(e.startTs, e.endTs);
      } else {
        stamps.push(e.ts);
      }
    }
    setRange(autoFitRange(stamps, Date.now(), { warRoom: bundle.war_room }));
  }, [events, bundle.war_room]);

  function toggleLane(l: TimelineLane) {
    setActiveLanes((prev) => {
      const next = prev.includes(l) ? prev.filter((x) => x !== l) : [...prev, l];
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)); } catch { /* ignore */ }
      return next;
    });
  }

  function applyPreset(p: Preset) {
    setRange(presetRange(p, Date.now()));
  }

  function applyFit() {
    const stamps: number[] = [];
    for (const e of events) {
      if (e.kind === 'run' || e.kind === 'ioc') {
        stamps.push(e.startTs, e.endTs);
      } else {
        stamps.push(e.ts);
      }
    }
    setRange(autoFitRange(stamps, Date.now(), { warRoom: bundle.war_room }));
  }

  const onlyLifecycle = events.length === 1 && events[0].kind === 'lifecycle';
  const drawableWidth = SVG_WIDTH - LANE_LABEL_W;
  // Fit height to the number of visible lanes — empty space below
  // the lanes was the "not expanding" complaint. We also keep a
  // minimum so the empty-state placeholder is readable.
  const computedHeight = Math.max(
    MIN_HEIGHT,
    visibleLanes.length * LANE_HEIGHT + TOP_PADDING + BOTTOM_PADDING,
  );

  return (
    <div className="border border-border rounded-md bg-white p-3 space-y-2">
      {/* Toggle bar + zoom controls */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-1.5 flex-wrap">
          {ALL_LANES.map((l) => (
            <button
              key={l}
              type="button"
              onClick={() => toggleLane(l)}
              aria-pressed={activeLanes.includes(l)}
              className={`text-[11px] px-2 py-0.5 rounded-md border ${activeLanes.includes(l) ? 'border-brand-300 bg-brand-50 text-brand-800' : 'border-border bg-white text-ink-mute'}`}
            >
              {l}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1 text-[11px]">
          <button type="button" onClick={applyFit} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">Fit</button>
          <button type="button" onClick={() => applyPreset('1h')} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">1h</button>
          <button type="button" onClick={() => applyPreset('24h')} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">24h</button>
          <button type="button" onClick={() => applyPreset('7d')} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">7d</button>
        </div>
      </div>

      {(savedSearches.length > 0 || Object.keys(filter).length > 0) && (
        <div className="flex items-center gap-2 flex-wrap pt-1 border-t border-border">
          <SavedSearchesBar
            scope="investigation_timeline"
            searches={savedSearches}
            currentConfig={filter}
            activeID={activeSavedID}
            onApply={(s) => {
              try { setFilter(JSON.parse(s.config_json) as TimelineFilterConfig); }
              catch { /* malformed — ignore */ }
            }}
            onSearchesChange={refreshSavedSearches}
          />
        </div>
      )}

      <TimelineFilterBar bundle={bundle} filter={filter} onChange={setFilter} />

      {filteredEvents.filter((e) => e.kind !== 'lifecycle' && e.kind !== 'daimon').length === 0 &&
        events.filter((e) => e.kind !== 'lifecycle' && e.kind !== 'daimon').length > 0 && (
        <div className="px-3 py-1.5 rounded-md text-xs text-amber-800 bg-amber-50 border border-amber-200">
          0 of {events.filter((e) => e.kind !== 'lifecycle' && e.kind !== 'daimon').length} events match — adjust filters or click Clear.
        </div>
      )}

      {onlyLifecycle ? (
        <div style={{ height: MIN_HEIGHT }} className="flex items-center justify-center text-sm text-ink-mute italic">
          No signals yet — link findings to populate the timeline.
        </div>
      ) : (
        <svg
          viewBox={`0 0 ${SVG_WIDTH} ${computedHeight}`}
          className="w-full"
          style={{ height: computedHeight }}
          preserveAspectRatio="none"
          role="img"
          aria-label="Case timeline"
        >
          {visibleLanes.map((l, i) => (
            <g key={l}>
              <text
                x={6}
                y={TOP_PADDING + i * LANE_HEIGHT + LANE_HEIGHT / 2 + 4}
                fontSize="11"
                fill="#64748b"
              >
                {l}
              </text>
              <line
                x1={LANE_LABEL_W}
                x2={SVG_WIDTH}
                y1={TOP_PADDING + (i + 1) * LANE_HEIGHT}
                y2={TOP_PADDING + (i + 1) * LANE_HEIGHT}
                stroke="#e2e8f0"
                strokeDasharray="2 4"
              />
            </g>
          ))}
          {range && filteredEvents.length > 0 && (
            <g transform={`translate(0, ${TOP_PADDING})`}>
              <EventsLayer
                events={filteredEvents}
                visibleLanes={visibleLanes}
                range={range}
                laneLabelWidth={LANE_LABEL_W}
                drawableWidth={drawableWidth}
                cpInstanceID={cpInstanceID}
              />
            </g>
          )}
        </svg>
      )}
    </div>
  );
}

const SEV_FILL: Record<string, string> = {
  CRITICAL: '#dc2626',
  HIGH:     '#ea580c',
  MEDIUM:   '#f59e0b',
  LOW:      '#3b82f6',
  INFO:     '#94a3b8',
};

const RUN_FILL: Record<string, string> = {
  completed: '#10b981',
  failed:    '#ef4444',
  cancelled: '#94a3b8',
  running:   '#3b82f6',
};

const SEV_RANK: Record<string, number> = { CRITICAL: 5, HIGH: 4, MEDIUM: 3, LOW: 2, INFO: 1 };

function highestSeverityColor(events: TimelineEvent[]): string {
  let bestRank = 0;
  let best: string = SEV_FILL.INFO;
  for (const e of events) {
    if (e.kind !== 'finding') continue;
    const r = SEV_RANK[e.severity] ?? 0;
    if (r > bestRank) {
      bestRank = r;
      best = SEV_FILL[e.severity] ?? SEV_FILL.INFO;
    }
  }
  return best;
}

function tooltipFor(e: TimelineEvent): React.ReactNode {
  switch (e.kind) {
    case 'finding':
      return (
        <div className="space-y-0.5">
          <div className="flex items-center gap-1">
            <span className={`inline-block px-1 py-0.5 text-[10px] uppercase font-medium rounded ring-1 ${sevTone(e.severity)}`}>{e.severity}</span>
            <span className="font-mono">Finding #{e.id}</span>
          </div>
          <div className="font-medium">{e.title}</div>
          <div className="text-ink-mute">{e.agent} on {e.host}</div>
          <div className="text-ink-mute italic text-[10px]">Click to open drawer</div>
        </div>
      );
    case 'run': {
      const dur = formatDuration(e.startTs, e.endTs, e.running);
      return (
        <div className="space-y-0.5">
          <div className="flex items-center gap-1">
            <span className={`inline-block px-1 py-0.5 text-[10px] uppercase font-medium rounded ring-1 ${runTone(e.status)}`}>{e.status}</span>
            <span className="font-mono">Run #{e.id}</span>
          </div>
          <div className="font-medium">{e.orchestrationName}</div>
          <div className="text-ink-mute">{dur}</div>
        </div>
      );
    }
    case 'note':
      return (
        <div className="space-y-0.5">
          <div className="text-ink-mute text-[10px]">{e.author}</div>
          <div className="font-mono whitespace-pre-wrap line-clamp-4">{e.body}</div>
        </div>
      );
    case 'ioc': {
      const display = e.value.length > 32 ? `…${e.value.slice(-4)}` : e.value;
      return (
        <div className="space-y-0.5">
          <div className="font-mono">{e.iocKind}:{display}</div>
        </div>
      );
    }
    case 'daimon':
      return <div className="font-mono">{e.agent} → Finding #{e.findingID}</div>;
    case 'audit':
      return (
        <div className="space-y-0.5">
          <div className="text-ink-mute text-[10px]">{e.by}</div>
          <div>{e.auditKind}: {e.title}</div>
        </div>
      );
    case 'lifecycle':
      return <div>{e.title}</div>;
  }
}

function sevTone(sev: string): string {
  if (sev === 'CRITICAL') return 'text-red-700 bg-red-50 ring-red-200';
  if (sev === 'HIGH') return 'text-orange-700 bg-orange-50 ring-orange-200';
  if (sev === 'MEDIUM') return 'text-amber-700 bg-amber-50 ring-amber-200';
  if (sev === 'LOW') return 'text-blue-700 bg-blue-50 ring-blue-200';
  return 'text-slate-700 bg-slate-50 ring-slate-200';
}

function runTone(status: string): string {
  if (status === 'completed') return 'text-emerald-700 bg-emerald-50 ring-emerald-200';
  if (status === 'failed') return 'text-red-700 bg-red-50 ring-red-200';
  if (status === 'cancelled') return 'text-slate-700 bg-slate-50 ring-slate-200';
  return 'text-blue-700 bg-blue-50 ring-blue-200';
}

function formatDuration(startTs: number, endTs: number, running: boolean): string {
  if (running) return 'in progress';
  const ms = Math.max(0, endTs - startTs);
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function ClusterList({ events, onPick, cpInstanceID }: {
  events: TimelineEvent[];
  onPick: () => void;
  cpInstanceID?: string;
}) {
  function fire(kind: string, identityKey: string) {
    window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind, identityKey, cpInstanceID } }));
    onPick();
  }
  return (
    <ul className="divide-y divide-border min-w-[16rem]">
      {events.map((e, i) => {
        const { kind, key, label } = clusterRowDetails(e);
        const clickable = kind !== null;
        return (
          <li key={i} className="py-1.5">
            <button
              type="button"
              onClick={clickable ? () => fire(kind!, key!) : undefined}
              disabled={!clickable}
              className={`w-full text-left ${clickable ? 'hover:bg-slate-50 cursor-pointer' : 'cursor-default'} px-1`}
            >
              {label}
            </button>
          </li>
        );
      })}
    </ul>
  );
}

function clusterRowDetails(e: TimelineEvent): { kind: string | null; key: string | null; label: React.ReactNode } {
  switch (e.kind) {
    case 'finding': return { kind: 'finding', key: String(e.id), label: tooltipFor(e) };
    case 'note':    return { kind: null, key: null, label: tooltipFor(e) };
    case 'audit':   return { kind: null, key: null, label: tooltipFor(e) };
    case 'lifecycle': return { kind: null, key: null, label: tooltipFor(e) };
    case 'daimon':  return { kind: 'finding', key: String(e.findingID), label: tooltipFor(e) };
    default:        return { kind: null, key: null, label: tooltipFor(e) };
  }
}

function EventsLayer({ events, visibleLanes, range, laneLabelWidth, drawableWidth, cpInstanceID }: {
  events: ReturnType<typeof buildEvents>;
  visibleLanes: TimelineLane[];
  range: Range;
  laneLabelWidth: number;
  drawableWidth: number;
  cpInstanceID?: string;
}) {
  const laneIndex = (l: TimelineLane) => visibleLanes.indexOf(l);
  const x = (t: number) => laneLabelWidth + tToX(t, range.tMin, range.tMax, drawableWidth);
  const yMid = (l: TimelineLane) => laneIndex(l) * LANE_HEIGHT + LANE_HEIGHT / 2;

  const clustered = useMemo(
    () => clusterEvents(events, visibleLanes, (t) => tToX(t, range.tMin, range.tMax, drawableWidth)),
    [events, visibleLanes, range, drawableWidth],
  );

  const [openCluster, setOpenCluster] = useState<ClusterOrEvent | null>(null);

  // Close orphaned popover when re-clustering produces a different
  // set of cluster objects.
  useEffect(() => {
    if (openCluster && !clustered.includes(openCluster)) setOpenCluster(null);
  }, [clustered, openCluster]);

  function fire(kind: string, identityKey: string) {
    window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind, identityKey, cpInstanceID } }));
  }

  return (
    <g>
      {clustered.map((c, i) => c.kind === 'single'
        ? renderSingle(c.event, i, { x, yMid, laneIndex, fire })
        : renderCluster(c, i, { yMid, openCluster, setOpenCluster, cpInstanceID }))}
    </g>
  );
}

function renderSingle(
  e: TimelineEvent,
  i: number,
  ctx: {
    x: (t: number) => number;
    yMid: (l: TimelineLane) => number;
    laneIndex: (l: TimelineLane) => number;
    fire: (kind: string, identityKey: string) => void;
  },
): React.ReactNode {
  const { x, yMid, laneIndex, fire } = ctx;
  switch (e.kind) {
    case 'lifecycle': {
      if (laneIndex('lifecycle') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <text x={x(e.ts)} y={yMid('lifecycle') + 4} fontSize="14" textAnchor="middle" aria-label={e.title}>
            {e.marker === 'created' ? '⊕' : '⊗'}
          </text>
        </Tooltip>
      );
    }
    case 'finding': {
      if (laneIndex('findings') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <circle
            cx={x(e.ts)} cy={yMid('findings')} r={5}
            fill={SEV_FILL[e.severity] ?? SEV_FILL.INFO}
            role="button"
            aria-label={`Finding #${e.id}: ${e.severity} ${e.title} on ${e.host}`}
            tabIndex={0}
            style={{ cursor: 'pointer' }}
            onClick={() => fire('finding', String(e.id))}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('finding', String(e.id)); }}
          />
        </Tooltip>
      );
    }
    case 'run': {
      if (laneIndex('runs') < 0) return null;
      const x0 = x(e.startTs);
      const x1 = x(e.endTs);
      const w = Math.max(2, x1 - x0);
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <rect
            x={x0} y={yMid('runs') - 6} width={w} height={12} rx={2}
            fill={RUN_FILL[e.status] ?? RUN_FILL.cancelled}
            role="button"
            aria-label={`Run #${e.id}: ${e.status} (${e.orchestrationName})`}
            tabIndex={0}
            style={{ cursor: 'pointer', opacity: e.running ? 0.85 : 1 }}
            onClick={() => fire('run', String(e.id))}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('run', String(e.id)); }}
          />
        </Tooltip>
      );
    }
    case 'note': {
      if (laneIndex('notes') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <text x={x(e.ts)} y={yMid('notes') + 4} fontSize="12" textAnchor="middle" aria-label={`Note by ${e.author}`}>
            ✎
          </text>
        </Tooltip>
      );
    }
    case 'ioc': {
      if (laneIndex('iocs') < 0) return null;
      const x0 = x(e.startTs);
      const x1 = x(e.endTs);
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <rect
            x={x0} y={yMid('iocs') - 2} width={Math.max(2, x1 - x0)} height={4}
            fill="#a855f7"
            role="button"
            aria-label={`IOC ${e.iocKind}:${e.value.slice(-4)}`}
            tabIndex={0}
            style={{ cursor: 'pointer' }}
            onClick={() => fire('ioc', `${e.iocKind}:${e.value}`)}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('ioc', `${e.iocKind}:${e.value}`); }}
          />
        </Tooltip>
      );
    }
    case 'daimon': {
      if (laneIndex('daimons') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <rect
            x={x(e.ts) - 2} y={yMid('daimons') - 2} width={4} height={4}
            fill="#6366f1"
            role="button"
            aria-label={`Daimon ${e.agent}`}
            tabIndex={0}
            style={{ cursor: 'pointer' }}
            onClick={() => fire('finding', String(e.findingID))}
          />
        </Tooltip>
      );
    }
    case 'audit': {
      if (laneIndex('audit') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <text x={x(e.ts)} y={yMid('audit') + 4} fontSize="11" textAnchor="middle" fill="#64748b" aria-label={`Audit ${e.auditKind} by ${e.by}`}>
            ↗
          </text>
        </Tooltip>
      );
    }
  }
}

function renderCluster(
  c: { kind: 'cluster'; lane: TimelineLane; events: TimelineEvent[]; cx: number },
  i: number,
  ctx: {
    yMid: (l: TimelineLane) => number;
    openCluster: ClusterOrEvent | null;
    setOpenCluster: (c: ClusterOrEvent | null) => void;
    cpInstanceID?: string;
  },
): React.ReactNode {
  const { yMid, openCluster, setOpenCluster, cpInstanceID } = ctx;
  const isOpen = openCluster === c;
  const cy = yMid(c.lane);
  const fill = c.lane === 'findings' ? highestSeverityColor(c.events) : '#475569';
  const r = 9;
  return (
    <Tooltip
      key={i}
      open={isOpen}
      onOpenChange={(o) => setOpenCluster(o ? c : null)}
      content={<ClusterList events={c.events} onPick={() => setOpenCluster(null)} cpInstanceID={cpInstanceID} />}
    >
      <g
        role="button"
        aria-label={`Cluster of ${c.events.length} ${c.lane}`}
        tabIndex={0}
        style={{ cursor: 'pointer' }}
        onClick={() => setOpenCluster(c)}
        onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') setOpenCluster(c); }}
      >
        <circle cx={c.cx} cy={cy} r={r} fill={fill} />
        <text x={c.cx} y={cy + 3} fontSize="9" textAnchor="middle" fill="white" fontWeight="bold" pointerEvents="none">
          +{c.events.length}
        </text>
      </g>
    </Tooltip>
  );
}

function loadLanes(): TimelineLane[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [...DEFAULT_LANES_ON];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...DEFAULT_LANES_ON];
    return parsed.filter((x): x is TimelineLane => ALL_LANES.includes(x as TimelineLane));
  } catch {
    return [...DEFAULT_LANES_ON];
  }
}
