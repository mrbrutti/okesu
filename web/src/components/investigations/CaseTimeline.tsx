// Horizontal Gantt-style timeline of case events. Hand-rolled SVG;
// no extra deps. Default-curated lanes (lifecycle/findings/runs/
// notes) + opt-in lanes (iocs/daimons/audit). Audit-lane fetch is
// lazy: triggered the first time the operator toggles audit on,
// using the existing api.investigations.audit endpoint.
//
// Refresh is driven by the parent's existing 30s/5s war-room poll —
// re-render happens on every bundle replacement.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { InvestigationDetail, InvestigationAuditEvent } from '../../api';
import { api } from '../../api';
import { buildEvents } from './timeline/buildEvents';
import { ALL_LANES, DEFAULT_LANES_ON, type TimelineLane } from './timeline/types';
import { autoFitRange, presetRange, type Preset, type Range } from './timeline/scale';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
}

const STORAGE_KEY = 'investigation:overview:lanes';
const LANE_HEIGHT = 36;
const LANE_LABEL_W = 80;
const HEIGHT = 320;
const SVG_WIDTH = 800;

export function CaseTimeline({ bundle, cpInstanceID }: Props) {
  const [activeLanes, setActiveLanes] = useState<TimelineLane[]>(loadLanes);
  const [audit, setAudit] = useState<InvestigationAuditEvent[]>([]);
  const [range, setRange] = useState<Range | null>(null);
  const auditFetchedRef = useRef(false);

  // Lazy-fetch audit lane the first time it's enabled.
  useEffect(() => {
    if (!activeLanes.includes('audit') || auditFetchedRef.current) return;
    auditFetchedRef.current = true;
    api.investigations.audit(bundle.investigation.ID, cpInstanceID)
      .then(setAudit)
      .catch(() => { auditFetchedRef.current = false; });
  }, [activeLanes, bundle.investigation.ID, cpInstanceID]);

  const events = useMemo(() => buildEvents(bundle, audit), [bundle, audit]);

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

      {onlyLifecycle ? (
        <div className="h-[280px] flex items-center justify-center text-sm text-ink-mute italic">
          No signals yet — link findings to populate the timeline.
        </div>
      ) : (
        <svg viewBox={`0 0 ${SVG_WIDTH} ${HEIGHT}`} className="w-full" style={{ height: HEIGHT }} role="img" aria-label="Case timeline">
          {visibleLanes.map((l, i) => (
            <g key={l}>
              <text x={6} y={i * LANE_HEIGHT + LANE_HEIGHT / 2 + 4} fontSize="11" fill="#64748b">
                {l}
              </text>
              <line x1={LANE_LABEL_W} x2={SVG_WIDTH} y1={(i + 1) * LANE_HEIGHT} y2={(i + 1) * LANE_HEIGHT} stroke="#e2e8f0" strokeDasharray="2 4" />
            </g>
          ))}
          {/* Events go here in Phase D2 */}
          {range && events.length > 0 && (
            <EventsLayer events={events} visibleLanes={visibleLanes} range={range} laneLabelWidth={LANE_LABEL_W} drawableWidth={drawableWidth} cpInstanceID={cpInstanceID} />
          )}
        </svg>
      )}
    </div>
  );
}

// Stub for phase D2 — replaced with a real implementation there.
function EventsLayer(_props: {
  events: ReturnType<typeof buildEvents>;
  visibleLanes: TimelineLane[];
  range: Range;
  laneLabelWidth: number;
  drawableWidth: number;
  cpInstanceID?: string;
}) {
  return null;
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
