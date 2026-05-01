import { Workflow } from 'lucide-react';
import { ChipFrame } from './common';

interface OrchestrationSnapshot {
  id?: number;
  name?: string;
  version?: number;
}

export function OrchestrationChip({ snapshot, cpInstanceID }: { snapshot: OrchestrationSnapshot; cpInstanceID?: string }) {
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="orchestration"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/orchestrations?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Workflow size={11} className="text-cyan-600" />}
      title={snapshot.name ?? 'Orchestration'}
      meta={snapshot.version != null ? `v${snapshot.version}` : undefined}
    />
  );
}
