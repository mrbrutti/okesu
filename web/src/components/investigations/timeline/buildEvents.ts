// Pure derivation of the timeline event list from the
// InvestigationDetail bundle + the audit-events array (audit lane
// is fetched lazily by the component — only when the operator
// toggles the audit lane on, so the bundle path doesn't pay for it
// up-front).
import type { InvestigationDetail, InvestigationAuditEvent } from '../../../api';
import type { TimelineEvent } from './types';

const ZERO_TIME = '0001-01-01T00:00:00Z';

export function buildEvents(
  bundle: InvestigationDetail,
  audit: InvestigationAuditEvent[],
): TimelineEvent[] {
  const out: TimelineEvent[] = [];

  // Lifecycle: created always; closed when ClosedAt is non-zero.
  const inv = bundle.investigation;
  out.push({
    kind: 'lifecycle',
    ts: Date.parse(inv.CreatedAt),
    marker: 'created',
    title: 'Investigation created',
  });
  if (inv.ClosedAt && inv.ClosedAt !== ZERO_TIME) {
    out.push({
      kind: 'lifecycle',
      ts: Date.parse(inv.ClosedAt),
      marker: 'closed',
      title: 'Investigation closed',
    });
  }

  // Findings → finding events + daimon ticks.
  for (const f of bundle.findings) {
    out.push({
      kind: 'finding',
      ts: f.Ts,
      id: f.ID,
      severity: (f.Severity.Valid ? f.Severity.String : 'INFO') as 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO',
      title: f.Title.Valid ? f.Title.String : '(no title)',
      agent: f.Agent.Valid ? f.Agent.String : '',
      host: f.Host.Valid ? f.Host.String : '',
    });
    if (f.Agent.Valid && f.Agent.String) {
      out.push({
        kind: 'daimon',
        ts: f.Ts,
        agent: f.Agent.String,
        findingID: f.ID,
      });
    }
  }

  // Runs → bars (use now as endTs for in-flight ones).
  const now = Date.now();
  for (const r of bundle.runs) {
    const startTs = Date.parse(r.StartedAt);
    const endTs = r.EndedAt.Valid ? Date.parse(r.EndedAt.String) : now;
    out.push({
      kind: 'run',
      startTs,
      endTs,
      running: !r.EndedAt.Valid,
      id: r.ID,
      status: r.Status,
      orchestrationName: r.OrchestrationName.Valid ? r.OrchestrationName.String : `orchestration #${r.OrchestrationID}`,
    });
  }

  // Notes → icons.
  for (const n of bundle.notes) {
    out.push({
      kind: 'note',
      ts: Date.parse(n.CreatedAt),
      id: n.ID,
      author: n.Author,
      body: n.Body,
    });
  }

  // IOCs → bars (FirstSeen → LastSeen).
  for (const i of bundle.iocs) {
    out.push({
      kind: 'ioc',
      startTs: Date.parse(i.FirstSeen),
      endTs: Date.parse(i.LastSeen),
      id: i.ID,
      iocKind: i.Kind,
      value: i.Value,
    });
  }

  // Audit (excluding created/closed which are lifecycle).
  for (const a of audit) {
    if (a.kind === 'created' || a.kind === 'closed') continue;
    out.push({
      kind: 'audit',
      ts: Date.parse(a.ts),
      auditKind: a.kind,
      by: a.by,
      title: a.title,
    });
  }

  // Stable sort by primary timestamp ascending.
  out.sort((a, b) => primaryTs(a) - primaryTs(b));
  return out;
}

function primaryTs(e: TimelineEvent): number {
  switch (e.kind) {
    case 'run':
    case 'ioc':
      return e.startTs;
    default:
      return e.ts;
  }
}
