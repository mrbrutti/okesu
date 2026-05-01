import { Hash } from 'lucide-react';
import { ChipFrame } from './common';

interface IOCSnapshot {
  kind?: string;
  value?: string;
  last4?: string;
  observation_count?: number;
  severity_max?: string;
}

export function IOCChip({ snapshot, cpInstanceID }: { snapshot: IOCSnapshot; cpInstanceID?: string }) {
  const k = snapshot.kind ?? 'ioc';
  const last4 = snapshot.last4 ?? snapshot.value?.slice(-4) ?? '';
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="ioc"
      cpInstanceID={cpInstanceID}
      identityKey={`${k}:${snapshot.value ?? ''}`}
      href={`/iocs?kind=${encodeURIComponent(k)}&value=${encodeURIComponent(snapshot.value ?? '')}${cpQS}`}
      icon={<Hash size={11} className="text-purple-600" />}
      title={
        <>
          <span className="font-mono">{k}</span>:<span className="font-mono">…{last4}</span>
        </>
      }
      meta={snapshot.observation_count != null ? `seen ${snapshot.observation_count}×` : undefined}
    />
  );
}
