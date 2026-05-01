import { Bot } from 'lucide-react';
import { ChipFrame } from './common';

interface DaimonSnapshot {
  id?: number;
  name?: string;
  host?: string;
  suspended?: boolean;
}

export function DaimonChip({ snapshot, cpInstanceID }: { snapshot: DaimonSnapshot; cpInstanceID?: string }) {
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="daimon"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.name ?? snapshot.id ?? '')}
      href={`/daimons?name=${encodeURIComponent(snapshot.name ?? '')}${cpQS}`}
      icon={<Bot size={11} className="text-indigo-600" />}
      title={snapshot.name ?? 'Daimon'}
      meta={snapshot.host ?? undefined}
      pill={snapshot.suspended ? (
        <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-amber-700 bg-amber-50 ring-amber-200">
          paused
        </span>
      ) : undefined}
    />
  );
}
