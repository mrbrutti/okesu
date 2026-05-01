import { Play } from 'lucide-react';
import { ChipFrame } from './common';

interface RunSnapshot {
  id?: number;
  status?: string;
  started_at?: string;
  ended_at?: string;
}

const STATUS_TONE: Record<string, string> = {
  completed: 'text-emerald-700 bg-emerald-50 ring-emerald-200',
  failed:    'text-red-700 bg-red-50 ring-red-200',
  cancelled: 'text-slate-700 bg-slate-50 ring-slate-200',
  running:   'text-blue-700 bg-blue-50 ring-blue-200',
};

export function RunChip({ snapshot, cpInstanceID }: { snapshot: RunSnapshot; cpInstanceID?: string }) {
  const status = (snapshot.status ?? 'unknown').toLowerCase();
  const tone = STATUS_TONE[status] ?? STATUS_TONE.cancelled;
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  return (
    <ChipFrame
      kind="run"
      cpInstanceID={cpInstanceID}
      identityKey={String(snapshot.id ?? '')}
      href={`/runs?id=${snapshot.id ?? ''}${cpQS}`}
      icon={<Play size={11} className="text-blue-600" />}
      title={`Run #${snapshot.id ?? '?'}`}
      pill={
        <span className={`text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${tone}`}>
          {status}
        </span>
      }
    />
  );
}
