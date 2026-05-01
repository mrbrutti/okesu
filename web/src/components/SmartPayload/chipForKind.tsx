// Switchboard for entity-kind → chip component. Lifted out of
// SmartPayload/index.tsx so DataTable + future renderers can reach
// it without dragging the whole renderer in (or fighting circular
// imports).

import { FindingChip } from './chips/FindingChip';
import { IOCChip } from './chips/IOCChip';
import { NodeChip } from './chips/NodeChip';
import { DaimonChip } from './chips/DaimonChip';
import { RunChip } from './chips/RunChip';
import { InvestigationChip } from './chips/InvestigationChip';
import { OrchestrationChip } from './chips/OrchestrationChip';
import { ClusterChip } from './chips/ClusterChip';
import { AgentChip } from './chips/AgentChip';

export function ChipForKind({
  kind,
  snapshot,
  cpInstanceID,
}: {
  kind: string;
  snapshot: Record<string, unknown>;
  cpInstanceID?: string;
}) {
  switch (kind) {
    case 'finding':       return <FindingChip       snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'ioc':           return <IOCChip           snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'node':          return <NodeChip          snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'daimon':        return <DaimonChip        snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'run':           return <RunChip           snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'investigation': return <InvestigationChip snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'orchestration': return <OrchestrationChip snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'cluster':       return <ClusterChip       snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'agent':         return <AgentChip         snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    default:              return null;
  }
}
