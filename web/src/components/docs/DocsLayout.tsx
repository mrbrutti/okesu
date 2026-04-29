// Shared layout primitives for the Documentation tabs. Keeps the
// three doc pages visually consistent without dragging in a full
// markdown renderer — the content is curated React/JSX so we get
// inline syntax highlighting and links to live screens.

import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';

export function DocsContainer({ children }: { children: ReactNode }) {
  return (
    <div className="px-6 py-6 max-w-4xl mx-auto space-y-6 text-sm leading-relaxed text-ink">
      {children}
    </div>
  );
}

export function DocsSection({
  title, anchor, children,
}: {
  title: string;
  anchor?: string;
  children: ReactNode;
}) {
  return (
    <section id={anchor} className="space-y-2">
      <h2 className="text-base font-semibold text-ink mt-2 mb-1 pb-1 border-b border-border/60">
        {title}
      </h2>
      {children}
    </section>
  );
}

export function DocsCallout({
  tone = 'info', children,
}: {
  tone?: 'info' | 'tip' | 'warn';
  children: ReactNode;
}) {
  const cls = {
    info: 'border-brand-200 bg-brand-50/60 text-ink',
    tip:  'border-emerald-200 bg-emerald-50/60 text-ink',
    warn: 'border-amber-300 bg-amber-50/70 text-ink',
  }[tone];
  const label = { info: 'Note', tip: 'Tip', warn: 'Heads-up' }[tone];
  return (
    <div className={cn('border rounded-md px-3 py-2.5 text-xs', cls)}>
      <span className="font-semibold mr-1.5">{label}:</span>
      {children}
    </div>
  );
}

export function DocsCode({ language, children }: { language?: string; children: string }) {
  return (
    <pre className="bg-slate-900 text-slate-100 rounded-md p-3 text-[12px] leading-snug overflow-x-auto">
      <code className={language ? `language-${language}` : undefined}>{children}</code>
    </pre>
  );
}

export function DocsTable({ children }: { children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-md border border-border">
      <table className="w-full text-xs">
        {children}
      </table>
    </div>
  );
}

export function DocsTableRow({ children }: { children: ReactNode }) {
  return <tr className="border-b border-border last:border-b-0 hover:bg-slate-50">{children}</tr>;
}

export function DocsTableCell({
  head = false, children,
}: {
  head?: boolean;
  children: ReactNode;
}) {
  if (head) {
    return <th className="text-left px-3 py-1.5 font-medium text-ink-dim bg-slate-50 border-b border-border">{children}</th>;
  }
  return <td className="px-3 py-1.5 align-top text-ink">{children}</td>;
}
