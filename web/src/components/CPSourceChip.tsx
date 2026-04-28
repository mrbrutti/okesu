import { Network } from 'lucide-react';
import type { CPSourceRef } from '../api';

// CPSourceChip renders a small "from <child CP>" badge next to a row
// that was federated in from a child. Hidden when source is undefined
// (local row). Two sizes: 'inline' for list rows, 'badge' for detail
// pages.
//
// The label prefers display_name → region → instance_id prefix, in
// that order. Region is shown alongside the name when both differ
// usefully (operators commonly distinguish "primary" + "us-east-1"
// the same row).
export function CPSourceChip({ source, size = 'inline' }: { source?: CPSourceRef; size?: 'inline' | 'badge' }) {
  if (!source) return null;
  const label = source.display_name || source.region || source.instance_id.slice(0, 8);
  const tooltip = [
    source.display_name,
    source.region && `region ${source.region}`,
    source.instance_id && `id ${source.instance_id}`,
  ].filter(Boolean).join(' · ');
  const cls = size === 'badge'
    ? 'inline-flex items-center gap-1 text-[11px] font-medium uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-2 py-0.5 rounded'
    : 'inline-flex items-center gap-0.5 text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded';
  return (
    <span className={cls} title={tooltip}>
      <Network size={size === 'badge' ? 11 : 9} />
      {label}
    </span>
  );
}
