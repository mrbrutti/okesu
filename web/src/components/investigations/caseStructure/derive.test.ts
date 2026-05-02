import { describe, it, expect } from 'vitest';
import {
  aggregateHosts,
  topIOCs,
  topDaimons,
  deriveOrchestrations,
} from './derive';
import type { InvestigationDetail } from '../../../api';

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-04-30T10:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

function f(host: string): InvestigationDetail['findings'][number] {
  return {
    ID: 1, Ts: 0, Severity: { Valid: true, String: 'HIGH' },
    Title: { Valid: true, String: 't' },
    Agent: { Valid: true, String: 'edr-agent' },
    Host:  { Valid: !!host, String: host },
  } as unknown as InvestigationDetail['findings'][number];
}

describe('aggregateHosts', () => {
  it('counts non-empty hosts and sorts desc by count, alpha tiebreak', () => {
    const out = aggregateHosts(bundle({
      findings: [f('a'), f('b'), f('a'), f('c'), f('a'), f('')],
    }));
    expect(out).toEqual([
      { Host: 'a', Count: 3 },
      { Host: 'b', Count: 1 },
      { Host: 'c', Count: 1 },
    ]);
  });

  it('returns empty array for empty bundle', () => {
    expect(aggregateHosts(bundle())).toEqual([]);
  });
});

describe('topIOCs', () => {
  it('sorts by ObservationCount desc', () => {
    const out = topIOCs(bundle({
      iocs: [
        { ID: 1, Kind: 'ip', Value: 'x', Severity: { Valid: false, String: '' }, ObservationCount: 2, HostCount: 1, FirstSeen: '', LastSeen: '' },
        { ID: 2, Kind: 'ip', Value: 'y', Severity: { Valid: false, String: '' }, ObservationCount: 5, HostCount: 1, FirstSeen: '', LastSeen: '' },
      ],
    }));
    expect(out.map((i) => i.ID)).toEqual([2, 1]);
  });
});

describe('topDaimons', () => {
  it('sorts by FindingCount desc, LastSeenTs tiebreak', () => {
    const out = topDaimons(bundle({
      daimons: [
        { Agent: 'a', FindingCount: 3, LastSeenTs: 100 },
        { Agent: 'b', FindingCount: 5, LastSeenTs: 200 },
        { Agent: 'c', FindingCount: 3, LastSeenTs: 300 },
      ],
    }));
    expect(out.map((d) => d.Agent)).toEqual(['b', 'c', 'a']);
  });
});

describe('deriveOrchestrations', () => {
  it('groups runs and counts each status with extras bucketed as Running', () => {
    const out = deriveOrchestrations(bundle({
      runs: [
        { ID: 1, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'completed', StartedAt: '2026-04-01' },
        { ID: 2, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'completed', StartedAt: '2026-04-02' },
        { ID: 3, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'failed',    StartedAt: '2026-04-03' },
        { ID: 4, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'cancelled', StartedAt: '2026-04-04' },
        { ID: 5, OrchestrationID: 10, OrchestrationName: { Valid: true, String: 'auto-triage' }, Status: 'pending',   StartedAt: '2026-04-05' },
      ] as unknown as InvestigationDetail['runs'],
    }));
    expect(out).toHaveLength(1);
    expect(out[0].RunCount).toBe(5);
    expect(out[0].Completed).toBe(2);
    expect(out[0].Failed).toBe(1);
    expect(out[0].Cancelled).toBe(1);
    expect(out[0].Running).toBe(1); // 'pending' bucketed as Running
    expect(out[0].LastStartedAt).toBe('2026-04-05');
  });
});
