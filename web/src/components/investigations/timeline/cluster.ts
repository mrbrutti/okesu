// Pure clustering of timeline point events into +N stacks. Buckets
// by (lane, x-bucket) and emits either a single-event entry or a
// cluster entry per bucket. Bars (run, ioc) skip clustering and pass
// through as singletons — their duration is itself the visual signal.

import type { TimelineEvent, TimelineLane } from './types';

export type ClusterOrEvent =
  | { kind: 'single'; event: TimelineEvent }
  | { kind: 'cluster'; lane: TimelineLane; events: TimelineEvent[]; cx: number };

const POINT_LANE: Record<string, TimelineLane | null> = {
  lifecycle: 'lifecycle',
  finding:   'findings',
  note:      'notes',
  ioc:       null,    // bar
  run:       null,    // bar
  daimon:    'daimons',
  audit:     'audit',
};

function primaryTs(e: TimelineEvent): number {
  if (e.kind === 'run' || e.kind === 'ioc') return e.startTs;
  return e.ts;
}

export function clusterEvents(
  events: TimelineEvent[],
  visibleLanes: TimelineLane[],
  tToX: (t: number) => number,
  bucketPx = 12,
  threshold = 4,
): ClusterOrEvent[] {
  const visible = new Set(visibleLanes);
  const bars: ClusterOrEvent[] = [];
  const buckets = new Map<string, { lane: TimelineLane; events: TimelineEvent[] }>();

  for (const e of events) {
    const lane = POINT_LANE[e.kind];
    if (lane === null) {
      // Bar — pass through as a singleton if its lane is visible.
      const barLane = e.kind === 'run' ? 'runs' : 'iocs';
      if (!visible.has(barLane as TimelineLane)) continue;
      bars.push({ kind: 'single', event: e });
      continue;
    }
    if (!lane || !visible.has(lane)) continue;
    const bucketIdx = Math.floor(tToX(primaryTs(e)) / bucketPx);
    const key = `${lane}:${bucketIdx}`;
    let b = buckets.get(key);
    if (!b) {
      b = { lane, events: [] };
      buckets.set(key, b);
    }
    b.events.push(e);
  }

  const out: ClusterOrEvent[] = [...bars];
  for (const [key, b] of buckets) {
    const bucketIdx = Number(key.slice(key.lastIndexOf(':') + 1));
    if (b.events.length < threshold) {
      for (const e of b.events) out.push({ kind: 'single', event: e });
      continue;
    }
    const sorted = [...b.events].sort((a, c) => primaryTs(a) - primaryTs(c));
    out.push({
      kind: 'cluster',
      lane: b.lane,
      events: sorted,
      cx: bucketPx * (bucketIdx + 0.5),
    });
  }
  return out;
}
