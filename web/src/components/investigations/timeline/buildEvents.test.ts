import { describe, it, expect } from 'vitest';
import { buildEvents } from './buildEvents';
import type { InvestigationDetail, InvestigationAuditEvent } from '../../../api';

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-05-01T00:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

describe('buildEvents', () => {
  it('emits a lifecycle "created" event from inv.CreatedAt', () => {
    const evs = buildEvents(bundle(), []);
    const created = evs.find((e) => e.kind === 'lifecycle' && e.marker === 'created');
    expect(created).toBeDefined();
    if (created && created.kind === 'lifecycle') {
      expect(created.ts).toBe(Date.parse('2026-05-01T00:00:00Z'));
    }
  });

  it('emits a lifecycle "closed" event when ClosedAt is non-zero', () => {
    const b = bundle({
      investigation: { ...bundle().investigation, Status: 'closed', ClosedAt: '2026-05-02T00:00:00Z' },
    });
    const evs = buildEvents(b, []);
    const closed = evs.find((e) => e.kind === 'lifecycle' && e.marker === 'closed');
    expect(closed).toBeDefined();
  });

  it('omits "closed" lifecycle for active cases (zero-time ClosedAt)', () => {
    const evs = buildEvents(bundle(), []);
    const closed = evs.find((e) => e.kind === 'lifecycle' && e.marker === 'closed');
    expect(closed).toBeUndefined();
  });

  it('maps each finding to a TimelineFindingEvent', () => {
    const b = bundle({
      findings: [
        { ID: 42, Ts: 1234567890000, Agent: { String: 'edr', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 'x', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const evs = buildEvents(b, []);
    const f = evs.find((e) => e.kind === 'finding');
    expect(f).toBeDefined();
    if (f && f.kind === 'finding') {
      expect(f.id).toBe(42);
      expect(f.severity).toBe('HIGH');
      expect(f.ts).toBe(1234567890000);
    }
  });

  it('treats an in-progress run (EndedAt invalid) as running with endTs=now', () => {
    const now = Date.now();
    const b = bundle({
      runs: [
        { ID: 1, OrchestrationID: 100, OrchestrationName: { String: 'tri', Valid: true }, Status: 'running', TriggerKind: 'auto', StartedAt: '2026-05-01T00:00:00Z', EndedAt: { String: '', Valid: false }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
      ],
    });
    const evs = buildEvents(b, []);
    const r = evs.find((e) => e.kind === 'run');
    expect(r).toBeDefined();
    if (r && r.kind === 'run') {
      expect(r.running).toBe(true);
      expect(r.endTs).toBeGreaterThanOrEqual(now);
    }
  });

  it('produces one daimon event per finding emitted by that agent', () => {
    const b = bundle({
      findings: [
        { ID: 1, Ts: 100, Agent: { String: 'edr-agent', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 200, Agent: { String: 'edr-agent', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const evs = buildEvents(b, []);
    const ds = evs.filter((e) => e.kind === 'daimon');
    expect(ds.length).toBe(2);
  });

  it('routes audit events into the audit lane, dropping created/closed', () => {
    const audit: InvestigationAuditEvent[] = [
      { ts: '2026-05-01T00:01:00Z', kind: 'created', by: 'me', title: 'opened' },
      { ts: '2026-05-01T00:05:00Z', kind: 'finding_linked', by: 'me', title: 'linked F#1' },
      { ts: '2026-05-02T00:00:00Z', kind: 'closed', by: 'me', title: 'closed' },
    ];
    const evs = buildEvents(bundle(), audit);
    const auditEvs = evs.filter((e) => e.kind === 'audit');
    expect(auditEvs.length).toBe(1);
    if (auditEvs[0] && auditEvs[0].kind === 'audit') {
      expect(auditEvs[0].auditKind).toBe('finding_linked');
    }
  });

  it('sorts events by primary timestamp ascending', () => {
    const b = bundle({
      findings: [
        { ID: 1, Ts: 200, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 100, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const findings = buildEvents(b, []).filter((e) => e.kind === 'finding');
    if (findings[0]?.kind === 'finding' && findings[1]?.kind === 'finding') {
      expect(findings[0].id).toBe(2);
      expect(findings[1].id).toBe(1);
    }
  });
});
