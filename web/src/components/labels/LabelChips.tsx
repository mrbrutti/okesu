// LabelChips: read-only display of a label map as small key=value
// pills. Used inline on list rows and detail page headers; pairs
// with LabelEditor for the full add/remove UI.

import { Tag } from 'lucide-react';
import { Link } from 'react-router-dom';
import type { LabelKind } from '../../api';

interface Props {
  labels: Record<string, string> | null | undefined;
  // When provided, each chip becomes a Link to the kind's list page
  // pre-filtered by ?selector=key=value. Operators click a chip on a
  // node to see "every other node with env=prod".
  linkKind?: LabelKind;
  size?: 'sm' | 'md';
  // When true, the rendered chips are wrapped in an inline-flex; use
  // 'block' (default) on detail pages and 'inline' on table rows.
  display?: 'block' | 'inline';
  className?: string;
}

const sizeClasses = {
  sm: 'text-[10px] px-1.5 py-0.5',
  md: 'text-xs px-2 py-0.5',
} as const;

export function LabelChips({
  labels,
  linkKind,
  size = 'md',
  display = 'block',
  className = '',
}: Props) {
  if (!labels || Object.keys(labels).length === 0) return null;
  const sorted = Object.entries(labels).sort(([a], [b]) => a.localeCompare(b));
  const wrapperCls =
    display === 'inline'
      ? `inline-flex flex-wrap gap-1 align-baseline ${className}`
      : `flex flex-wrap gap-1 ${className}`;
  return (
    <div className={wrapperCls}>
      {sorted.map(([k, v]) => {
        const text = `${k}${v ? '=' : ''}${v}`;
        const cls = `inline-flex items-center gap-1 bg-slate-100 text-slate-800 rounded font-mono ${sizeClasses[size]}`;
        if (linkKind) {
          const href = `/labels/${linkKind}?selector=${encodeURIComponent(`${k}=${v}`)}`;
          return (
            <Link key={k} to={href} className={`${cls} hover:bg-slate-200`} title={`Find all ${linkKind}s with ${text}`}>
              <Tag size={size === 'sm' ? 8 : 10} />
              {text}
            </Link>
          );
        }
        return (
          <span key={k} className={cls}>
            <Tag size={size === 'sm' ? 8 : 10} />
            {text}
          </span>
        );
      })}
    </div>
  );
}
