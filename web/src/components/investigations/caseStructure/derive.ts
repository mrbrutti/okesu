// Pure aggregators that derive structure-card content from the
// InvestigationDetail bundle. These are the fallback path for
// CaseStructure when the /structure endpoint is unavailable
// (federated child running an older binary, transient network
// failure). They produce the same wire shape as the server endpoint
// so the component renders identically from either source.

import type {
  InvestigationDetail,
  InvestigationStructure,
  InvestigationHostItem,
  InvestigationIOCItem,
  InvestigationDaimonItem,
  InvestigationOrchestrationItem,
} from '../../../api';

export function aggregateHosts(b: InvestigationDetail): InvestigationHostItem[] {
  const counts = new Map<string, number>();
  for (const f of b.findings) {
    if (!f.Host.Valid || !f.Host.String) continue;
    counts.set(f.Host.String, (counts.get(f.Host.String) ?? 0) + 1);
  }
  return Array.from(counts, ([host, count]) => ({ Host: host, Count: count }))
    .sort((a, b) => {
      if (a.Count !== b.Count) return b.Count - a.Count;
      if (a.Host < b.Host) return -1;
      if (a.Host > b.Host) return 1;
      return 0;
    });
}

export function topIOCs(b: InvestigationDetail): InvestigationIOCItem[] {
  return [...b.iocs].sort((a, b) => b.ObservationCount - a.ObservationCount);
}

export function topDaimons(b: InvestigationDetail): InvestigationDaimonItem[] {
  return [...b.daimons].sort((a, b) => {
    if (a.FindingCount !== b.FindingCount) return b.FindingCount - a.FindingCount;
    return b.LastSeenTs - a.LastSeenTs;
  });
}

export function deriveOrchestrations(b: InvestigationDetail): InvestigationOrchestrationItem[] {
  // Group runs by orchestration ID; count statuses; emit the same
  // shape as the server-side response.
  const byID = new Map<number, InvestigationOrchestrationItem>();
  for (const r of b.runs) {
    const id = r.OrchestrationID;
    let row = byID.get(id);
    if (!row) {
      row = {
        OrchestrationID:   { Valid: true, Int64: id },
        OrchestrationName: r.OrchestrationName.Valid ? r.OrchestrationName.String : `orchestration #${id}`,
        RunCount:          0,
        Completed:         0,
        Failed:            0,
        Cancelled:         0,
        Running:           0,
        LastStartedAt:     '',
      };
      byID.set(id, row);
    }
    row.RunCount += 1;
    if      (r.Status === 'completed') row.Completed += 1;
    else if (r.Status === 'failed')    row.Failed += 1;
    else if (r.Status === 'cancelled') row.Cancelled += 1;
    else                                row.Running += 1;
    if (r.StartedAt > row.LastStartedAt) row.LastStartedAt = r.StartedAt;
  }
  return Array.from(byID.values()).sort((a, b) => b.RunCount - a.RunCount);
}

export function deriveFromBundle(b: InvestigationDetail): InvestigationStructure {
  return {
    hosts:          aggregateHosts(b),
    iocs:           topIOCs(b),
    daimons:        topDaimons(b),
    orchestrations: deriveOrchestrations(b),
  };
}
