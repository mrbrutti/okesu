import type { LucideIcon } from 'lucide-react';
import { X } from 'lucide-react';

// Sticky bar that appears when the operator has selected ≥ 1 row from a
// list page. Renders a count + caller-provided action buttons + a clear
// button. Hidden entirely when count === 0 so the page layout doesn't
// jump as the bar appears/disappears.
export function BulkActionBar({
  count, onClear, label = 'Selected', children,
}: {
  count: number;
  onClear: () => void;
  label?: string;
  children: React.ReactNode;
}) {
  if (count === 0) return null;
  return (
    <div className="sticky bottom-0 left-0 right-0 z-20 -mx-6 mt-4 px-6 py-2.5 bg-white border-t border-border shadow-[0_-2px_8px_rgba(0,0,0,0.04)]">
      <div className="flex items-center gap-3">
        <span className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">{label}</span>
        <span className="text-sm font-semibold tabular-nums text-ink">{count}</span>
        <div className="flex-1 flex items-center gap-1.5 min-w-0 overflow-x-auto">{children}</div>
        <button
          type="button"
          onClick={onClear}
          className="inline-flex items-center gap-1 px-2 py-1 text-xs text-ink-dim hover:text-ink hover:bg-slate-50 rounded"
          title="Clear selection"
        >
          <X size={12} />
          Clear
        </button>
      </div>
    </div>
  );
}

// Pre-styled action button for use inside <BulkActionBar>. Keeps visual
// consistency across daimons / nodes / findings bars.
export function BulkActionButton({
  onClick, disabled, busy, icon: Icon, label, tone = 'neutral', title,
}: {
  onClick: () => void;
  disabled?: boolean;
  busy?: boolean;
  icon: LucideIcon;
  label: string;
  tone?: 'neutral' | 'warn' | 'good' | 'bad';
  title?: string;
}) {
  const toneCls = {
    neutral: 'text-ink-dim hover:text-ink hover:bg-slate-50 ring-border',
    warn:    'text-yellow-700 hover:bg-yellow-50 ring-yellow-200',
    good:    'text-green-700 hover:bg-green-50 ring-green-200',
    bad:     'text-red-700 hover:bg-red-50 ring-red-200',
  }[tone];
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled || busy}
      title={title}
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 text-xs ring-1 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${toneCls}`}
    >
      <Icon size={12} className={busy ? 'animate-spin' : ''} />
      {label}
    </button>
  );
}
