// Triage status visuals shared across the Findings page, the AgentFindings
// tab, and the per-finding drawer. Single source of truth so a status looks
// identical wherever it's rendered.

import {
  AlertCircle,
  Check,
  CheckCheck,
  Eye,
  Search,
  XOctagon,
  type LucideIcon,
} from 'lucide-react';
import type { FindingStatus } from '../api';
import { cn } from '../lib/cn';

interface StatusStyle {
  label: string;
  cls: string;     // pill background/text/ring
  dot: string;     // small dot color
  icon: LucideIcon;
}

export const STATUS_STYLES: Record<FindingStatus, StatusStyle> = {
  open: {
    label: 'Open',
    cls: 'bg-brand-50 text-brand-700 ring-brand-200',
    dot: 'bg-brand-500',
    icon: AlertCircle,
  },
  acknowledged: {
    label: 'Acknowledged',
    cls: 'bg-slate-100 text-slate-700 ring-slate-200',
    dot: 'bg-slate-500',
    icon: Check,
  },
  investigating: {
    label: 'Investigating',
    cls: 'bg-blue-50 text-blue-700 ring-blue-200',
    dot: 'bg-blue-500',
    icon: Search,
  },
  resolved: {
    label: 'Resolved',
    cls: 'bg-green-50 text-green-700 ring-green-200',
    dot: 'bg-green-500',
    icon: CheckCheck,
  },
  false_positive: {
    label: 'False positive',
    cls: 'bg-amber-50 text-amber-800 ring-amber-200',
    dot: 'bg-amber-500',
    icon: XOctagon,
  },
  wontfix: {
    label: "Won't fix",
    cls: 'bg-slate-100 text-slate-600 ring-slate-200',
    dot: 'bg-slate-400',
    icon: Eye,
  },
  superseded: {
    label: 'Superseded',
    cls: 'bg-slate-100 text-slate-600 ring-slate-200',
    dot: 'bg-slate-400',
    icon: Eye,
  },
};

export function StatusPill({
  status,
  size = 'sm',
}: {
  status: FindingStatus;
  size?: 'xs' | 'sm';
}) {
  const s = STATUS_STYLES[status];
  const Icon = s.icon;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded ring-1 font-medium tabular-nums',
        s.cls,
        size === 'xs'
          ? 'text-[10px] uppercase tracking-wide px-1.5 py-0.5'
          : 'text-[11px] px-2 py-0.5',
      )}
    >
      <Icon size={size === 'xs' ? 9 : 11} />
      {s.label}
    </span>
  );
}

export function StatusDot({ status, className }: { status: FindingStatus; className?: string }) {
  return (
    <span className={cn('inline-block w-1.5 h-1.5 rounded-full', STATUS_STYLES[status].dot, className)} />
  );
}
