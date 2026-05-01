import { describe, it, expect } from 'vitest';
import { clusterEvents } from './cluster';
import type { TimelineEvent, TimelineLane } from './types';

const VISIBLE: TimelineLane[] = ['lifecycle', 'findings', 'runs', 'notes'];

function finding(id: number, ts: number): TimelineEvent {
  return { kind: 'finding', ts, id, severity: 'LOW', title: `f${id}`, agent: 'a', host: 'h' };
}

function run(id: number, startTs: number, endTs: number): TimelineEvent {
  return { kind: 'run', startTs, endTs, running: false, id, status: 'completed', orchestrationName: 'o' };
}

// Identity tToX so x === t for easy bucket reasoning.
const idScale = (t: number) => t;

describe('clusterEvents', () => {
  it('returns empty output for empty input', () => {
    expect(clusterEvents([], VISIBLE, idScale)).toEqual([]);
  });

  it('passes events through as singletons when below threshold (default 4)', () => {
    const evs = [finding(1, 0), finding(2, 1), finding(3, 2)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(3);
    out.forEach((c) => expect(c.kind).toBe('single'));
  });

  it('clusters when 4+ point events fall in the same bucket', () => {
    // bucketPx default 12 → bucket 0 = [0..11]; place 5 dots within that range
    const evs = [finding(1, 0), finding(2, 2), finding(3, 4), finding(4, 6), finding(5, 8)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(1);
    expect(out[0].kind).toBe('cluster');
    if (out[0].kind === 'cluster') {
      expect(out[0].lane).toBe('findings');
      expect(out[0].events.length).toBe(5);
      // cx is the bucket midpoint = bucketPx * (bucketIndex + 0.5) = 6
      expect(out[0].cx).toBe(6);
    }
  });

  it('does not cluster across adjacent buckets', () => {
    // bucket 0 = [0..11] (3 events), bucket 1 = [12..23] (2 events)
    const evs = [finding(1, 0), finding(2, 4), finding(3, 8), finding(4, 13), finding(5, 16)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(5);
    out.forEach((c) => expect(c.kind).toBe('single'));
  });

  it('passes bars (runs, iocs) through as singletons even when their x bucket would cluster', () => {
    // 4 runs all starting in bucket 0; bars must NOT cluster.
    const evs = [run(1, 0, 5), run(2, 2, 6), run(3, 4, 8), run(4, 6, 10)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(4);
    out.forEach((c) => expect(c.kind).toBe('single'));
  });

  it('keeps separate clusters per lane (findings + daimons in same x bucket)', () => {
    const evs: TimelineEvent[] = [
      finding(1, 0), finding(2, 2), finding(3, 4), finding(4, 6),  // 4 findings → cluster
      { kind: 'daimon', ts: 0, agent: 'a', findingID: 1 },
      { kind: 'daimon', ts: 2, agent: 'a', findingID: 2 },
      { kind: 'daimon', ts: 4, agent: 'a', findingID: 3 },
      { kind: 'daimon', ts: 6, agent: 'a', findingID: 4 },         // 4 daimons → cluster
    ];
    const out = clusterEvents(evs, ['findings', 'daimons'], idScale);
    expect(out.length).toBe(2);
    const lanes = out.map((c) => c.kind === 'cluster' ? c.lane : null);
    expect(lanes).toContain('findings');
    expect(lanes).toContain('daimons');
  });

  it('cluster events array is sorted ascending by primary timestamp', () => {
    const evs = [finding(1, 8), finding(2, 0), finding(3, 4), finding(4, 2)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(1);
    if (out[0].kind === 'cluster') {
      expect(out[0].events.map((e) => e.kind === 'finding' ? e.id : 0)).toEqual([2, 4, 3, 1]);
    }
  });

  it('hides clustered events when their lane is not visible', () => {
    const evs = [finding(1, 0), finding(2, 2), finding(3, 4), finding(4, 6)];
    // Only 'lifecycle' visible — findings should not appear at all
    const out = clusterEvents(evs, ['lifecycle'], idScale);
    expect(out).toEqual([]);
  });

  it('respects a custom threshold and bucketPx', () => {
    const evs = [finding(1, 0), finding(2, 5)];
    const out = clusterEvents(evs, VISIBLE, idScale, 10, 2);
    // bucketPx=10 → both in bucket 0; threshold=2 → cluster
    expect(out.length).toBe(1);
    expect(out[0].kind).toBe('cluster');
  });
});
