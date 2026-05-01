import { Server } from 'lucide-react';
import { ChipFrame } from './common';

interface NodeSnapshot {
  id?: number;
  name?: string;
  hostname?: string;
  status?: string;
}

export function NodeChip({ snapshot, cpInstanceID }: { snapshot: NodeSnapshot; cpInstanceID?: string }) {
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="node"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? snapshot.hostname ?? '')}
      href={`/nodes?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Server size={11} className="text-emerald-600" />}
      title={snapshot.name ?? snapshot.hostname ?? 'Node'}
      meta={snapshot.status}
    />
  );
}
