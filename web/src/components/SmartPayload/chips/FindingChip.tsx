import { AlertTriangle } from 'lucide-react';
import { ChipFrame } from './common';

interface FindingSnapshot {
  id?: number;
  severity?: string;
  title?: string;
  category?: string;
  status?: string;
  host?: string;
}

const SEV_TONE: Record<string, string> = {
  CRITICAL: 'text-red-700 bg-red-50 ring-red-200',
  HIGH:     'text-orange-700 bg-orange-50 ring-orange-200',
  MEDIUM:   'text-amber-700 bg-amber-50 ring-amber-200',
  LOW:      'text-blue-700 bg-blue-50 ring-blue-200',
  INFO:     'text-slate-700 bg-slate-50 ring-slate-200',
};

export function FindingChip({ snapshot, cpInstanceID }: { snapshot: FindingSnapshot; cpInstanceID?: string }) {
  const sev = (snapshot.severity ?? 'INFO').toUpperCase();
  const tone = SEV_TONE[sev] ?? SEV_TONE.INFO;
  const idLabel = snapshot.id ? `#${snapshot.id}` : '';
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="finding"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/findings?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<AlertTriangle size={11} className="text-red-500" />}
      title={
        <>
          {snapshot.title ?? 'Finding'} <span className="text-ink-mute font-normal">{idLabel}</span>
        </>
      }
      pill={
        <span className={`text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${tone}`}>
          {sev}
        </span>
      }
      meta={snapshot.category ? snapshot.category : undefined}
    />
  );
}
