// Pure filter predicate for the investigation timeline. Empty /
// missing fields = no filter on that dimension. Pass-through
// semantic: a dimension only narrows the kinds it applies to;
// other kinds pass through unchanged so the operator's lane
// toggles remain the only knob that hides whole kinds.

import type { TimelineEvent } from './types';
import type { Severity } from '../../../api';

export type { Severity };
export type RunStatus = 'completed' | 'failed' | 'cancelled' | 'running';

export interface TimelineFilterConfig {
  severities?: Severity[];
  host?: string;
  agent?: string;
  run_statuses?: RunStatus[];
}

export function applyTimelineFilter(
  events: TimelineEvent[],
  filter: TimelineFilterConfig,
): TimelineEvent[] {
  const sevSet = filter.severities && filter.severities.length > 0 ? new Set(filter.severities) : null;
  const runSet = filter.run_statuses && filter.run_statuses.length > 0 ? new Set(filter.run_statuses) : null;
  const host = filter.host !== undefined && filter.host.trim() !== '' ? filter.host.trim() : null;
  const agent = filter.agent !== undefined && filter.agent.trim() !== '' ? filter.agent.trim() : null;

  if (sevSet === null && runSet === null && host === null && agent === null) {
    return events;
  }

  return events.filter((e) => {
    if (sevSet && e.kind === 'finding' && !sevSet.has(e.severity)) return false;
    if (host && e.kind === 'finding' && e.host !== host) return false;
    if (agent && e.kind === 'finding' && e.agent !== agent) return false;
    if (agent && e.kind === 'daimon' && e.agent !== agent) return false;
    // `e.status` is typed `RunStatus | string` because the Run table can
    // carry server-side statuses outside the four chip values; the cast
    // is safe because Set.has returns false for any non-member string.
    if (runSet && e.kind === 'run' && !runSet.has(e.status as RunStatus)) return false;
    return true;
  });
}
