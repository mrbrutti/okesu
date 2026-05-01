// Shared chip frame: a small clickable card with an entity icon, a
// summary line, an optional status pill, and an `↗` link. Click on
// the body fires the `entity:open` custom event so EntityDrawerHost
// (Group I) can render the per-kind drawer in place; click on `↗`
// navigates to the full detail page.
import type { ReactNode } from 'react';
import { ArrowUpRight } from 'lucide-react';

export interface ChipProps {
  cpInstanceID?: string;
  kind: string;
  identityKey: string; // for the entity:open event payload
  href: string;        // navigate target for the ↗ icon
  icon: ReactNode;
  title: ReactNode;
  meta?: ReactNode;
  pill?: ReactNode;
}

export function ChipFrame({ kind, identityKey, href, icon, title, meta, pill, cpInstanceID }: ChipProps) {
  function handleOpen(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    window.dispatchEvent(new CustomEvent('entity:open', {
      detail: { kind, identityKey, cpInstanceID },
    }));
  }
  return (
    <span className="inline-flex items-center gap-1.5 align-baseline mx-0.5 my-0.5 px-2 py-1 rounded-md border border-border bg-panel hover:bg-slate-50 text-xs leading-tight max-w-full">
      <button
        type="button"
        onClick={handleOpen}
        className="inline-flex items-center gap-1.5 cursor-pointer text-left min-w-0"
      >
        <span className="shrink-0">{icon}</span>
        <span className="font-medium text-ink truncate">{title}</span>
        {pill}
        {meta && <span className="text-ink-mute truncate">{meta}</span>}
      </button>
      <a
        href={href}
        className="text-ink-mute hover:text-ink shrink-0"
        title={`Open ${kind} page`}
        onClick={(e) => e.stopPropagation()}
      >
        <ArrowUpRight size={12} />
      </a>
    </span>
  );
}
