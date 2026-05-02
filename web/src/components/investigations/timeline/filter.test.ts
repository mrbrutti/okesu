import { describe, it, expect } from 'vitest';
import { applyTimelineFilter, type TimelineFilterConfig } from './filter';
import type { TimelineEvent } from './types';

function finding(over: Partial<Extract<TimelineEvent, { kind: 'finding' }>> = {}): TimelineEvent {
  return {
    kind: 'finding',
    ts: 1_000,
    id: 1,
    severity: 'HIGH',
    title: 't',
    agent: 'edr-agent',
    host: 'edr-fedora-3',
    ...over,
  };
}

function run(over: Partial<Extract<TimelineEvent, { kind: 'run' }>> = {}): TimelineEvent {
  return {
    kind: 'run',
    startTs: 1_000,
    endTs: 2_000,
    running: false,
    id: 1,
    status: 'completed',
    orchestrationName: 'orch',
    ...over,
  };
}

function note(over: Partial<Extract<TimelineEvent, { kind: 'note' }>> = {}): TimelineEvent {
  return { kind: 'note', ts: 1_500, id: 1, author: 'me', body: 'b', ...over };
}

function lifecycle(): TimelineEvent {
  return { kind: 'lifecycle', ts: 0, marker: 'created', title: 'created' };
}

function daimon(over: Partial<Extract<TimelineEvent, { kind: 'daimon' }>> = {}): TimelineEvent {
  return { kind: 'daimon', ts: 1_000, agent: 'edr-agent', findingID: 1, ...over };
}

describe('applyTimelineFilter', () => {
  it('returns all events for an empty filter', () => {
    const events: TimelineEvent[] = [lifecycle(), finding(), run(), note()];
    expect(applyTimelineFilter(events, {})).toEqual(events);
  });

  it('returns empty array on empty input', () => {
    expect(applyTimelineFilter([], { severities: ['HIGH'] })).toEqual([]);
  });

  it('severity filter keeps matching findings, passes through other kinds', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, severity: 'CRITICAL' }),
      finding({ id: 2, severity: 'LOW' }),
      note(),
      lifecycle(),
      run(),
    ];
    const filter: TimelineFilterConfig = { severities: ['CRITICAL'] };
    const out = applyTimelineFilter(events, filter);
    expect(out.map((e) => e.kind + ':' + (e.kind === 'finding' ? e.id : ''))).toEqual([
      'finding:1', 'note:', 'lifecycle:', 'run:',
    ]);
  });

  it('severity OR-s within the dimension', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, severity: 'CRITICAL' }),
      finding({ id: 2, severity: 'HIGH' }),
      finding({ id: 3, severity: 'MEDIUM' }),
    ];
    const out = applyTimelineFilter(events, { severities: ['CRITICAL', 'HIGH'] });
    expect(out.map((e) => (e.kind === 'finding' ? e.id : 0))).toEqual([1, 2]);
  });

  it('host filter applies to findings only (others pass through)', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, host: 'edr-fedora-3' }),
      finding({ id: 2, host: 'web-1' }),
      note(),
      run(),
    ];
    const out = applyTimelineFilter(events, { host: 'edr-fedora-3' });
    const findings = out.filter((e) => e.kind === 'finding');
    expect(findings.map((f) => (f.kind === 'finding' ? f.id : 0))).toEqual([1]);
    expect(out.some((e) => e.kind === 'note')).toBe(true);
    expect(out.some((e) => e.kind === 'run')).toBe(true);
  });

  it('agent filter applies to findings AND daimons', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, agent: 'edr-agent' }),
      finding({ id: 2, agent: 'web-agent' }),
      daimon({ findingID: 1, agent: 'edr-agent' }),
      daimon({ findingID: 2, agent: 'web-agent' }),
      note(),
    ];
    const out = applyTimelineFilter(events, { agent: 'edr-agent' });
    const findings = out.filter((e) => e.kind === 'finding');
    const daimons = out.filter((e) => e.kind === 'daimon');
    expect(findings.map((f) => (f.kind === 'finding' ? f.id : 0))).toEqual([1]);
    expect(daimons.map((d) => (d.kind === 'daimon' ? d.findingID : 0))).toEqual([1]);
    expect(out.some((e) => e.kind === 'note')).toBe(true);
  });

  it('run-status filter applies to runs only', () => {
    const events: TimelineEvent[] = [
      run({ id: 1, status: 'completed' }),
      run({ id: 2, status: 'failed' }),
      run({ id: 3, status: 'cancelled' }),
      note(),
      finding(),
    ];
    const out = applyTimelineFilter(events, { run_statuses: ['failed', 'completed'] });
    const runs = out.filter((e) => e.kind === 'run');
    expect(runs.map((r) => (r.kind === 'run' ? r.id : 0)).sort()).toEqual([1, 2]);
    expect(out.some((e) => e.kind === 'finding')).toBe(true);
    expect(out.some((e) => e.kind === 'note')).toBe(true);
  });

  it('AND-s across dimensions (severity + host)', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, severity: 'HIGH', host: 'edr-fedora-3' }),
      finding({ id: 2, severity: 'HIGH', host: 'web-1' }),
      finding({ id: 3, severity: 'LOW', host: 'edr-fedora-3' }),
    ];
    const out = applyTimelineFilter(events, { severities: ['HIGH'], host: 'edr-fedora-3' });
    const findings = out.filter((e) => e.kind === 'finding');
    expect(findings.map((f) => (f.kind === 'finding' ? f.id : 0))).toEqual([1]);
  });
});
