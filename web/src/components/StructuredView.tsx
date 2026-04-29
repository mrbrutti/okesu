// Structured key/value renderer — replaces raw JSON.stringify dumps
// in the orchestration + runs pages with something that respects the
// rest of the app's UI/UX (chips for booleans, monospace for hashes
// and ids, ellipsis for long strings, nested cards for sub-objects).
//
// The component is deliberately small and dependency-free: pass any
// JSON-shaped value, get a readable card. For long arrays / large
// objects, the renderer collapses past a threshold with a "show all"
// toggle so an unexpected 10MB payload doesn't hang the UI.
import { useState } from 'react';
import { ChevronRight } from 'lucide-react';

type Json = string | number | boolean | null | Json[] | { [k: string]: Json };

interface Props {
  value: unknown;
  /** Maximum nesting depth before we fall back to a JSON.stringify
   *  block. 4 covers every shape we have today. */
  maxDepth?: number;
  className?: string;
}

export function StructuredView({ value, maxDepth = 4, className }: Props) {
  if (value == null) return <span className="text-ink-mute text-xs">—</span>;
  return (
    <div className={className}>
      <ValueNode value={value as Json} depth={0} maxDepth={maxDepth} />
    </div>
  );
}

function ValueNode({ value, depth, maxDepth }: { value: Json; depth: number; maxDepth: number }) {
  if (value === null || value === undefined) return <Scalar text="null" tone="mute" />;

  if (typeof value === 'boolean') {
    return (
      <span
        className={
          value
            ? 'inline-flex items-center text-[11px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-green-700 bg-green-50 ring-green-200'
            : 'inline-flex items-center text-[11px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-ink-dim bg-slate-100 ring-slate-200'
        }
      >
        {String(value)}
      </span>
    );
  }

  if (typeof value === 'number') {
    return <span className="font-mono text-xs text-ink">{value}</span>;
  }

  if (typeof value === 'string') {
    if (value === '') return <Scalar text="(empty)" tone="mute" />;
    if (looksLikeHash(value)) {
      return <code className="font-mono text-xs text-ink-dim break-all">{value}</code>;
    }
    if (looksLikeMultiline(value)) {
      return (
        <pre className="text-xs font-mono bg-slate-50 border border-border rounded p-2 whitespace-pre-wrap break-words text-ink">
          {value}
        </pre>
      );
    }
    return <span className="text-sm text-ink break-words">{value}</span>;
  }

  if (Array.isArray(value)) return <ArrayNode items={value} depth={depth} maxDepth={maxDepth} />;
  if (typeof value === 'object') return <ObjectNode obj={value} depth={depth} maxDepth={maxDepth} />;

  return <Scalar text={String(value)} tone="default" />;
}

function ObjectNode({ obj, depth, maxDepth }: { obj: { [k: string]: Json }; depth: number; maxDepth: number }) {
  const entries = Object.entries(obj);
  if (entries.length === 0) return <Scalar text="{}" tone="mute" />;
  if (depth >= maxDepth) {
    return (
      <pre className="text-[11px] font-mono bg-slate-50 border border-border rounded p-2 whitespace-pre-wrap break-words text-ink-dim">
        {JSON.stringify(obj, null, 2)}
      </pre>
    );
  }
  return (
    <div
      className={
        depth === 0
          ? 'space-y-1.5'
          : 'pl-3 border-l border-slate-200 space-y-1 mt-1'
      }
    >
      {entries.map(([k, v]) => (
        <KVRow key={k} label={k} depth={depth} maxDepth={maxDepth} value={v} />
      ))}
    </div>
  );
}

function KVRow({ label, value, depth, maxDepth }: { label: string; value: Json; depth: number; maxDepth: number }) {
  const isContainer =
    (typeof value === 'object' && value !== null && !Array.isArray(value)) ||
    (Array.isArray(value) && value.length > 0 && typeof value[0] === 'object');

  if (isContainer) {
    return (
      <div className="text-xs">
        <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-0.5">{prettyKey(label)}</div>
        <ValueNode value={value} depth={depth + 1} maxDepth={maxDepth} />
      </div>
    );
  }
  return (
    <div className="grid grid-cols-[10rem_1fr] gap-2 items-baseline text-xs">
      <span className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">{prettyKey(label)}</span>
      <div className="min-w-0">
        <ValueNode value={value} depth={depth + 1} maxDepth={maxDepth} />
      </div>
    </div>
  );
}

function ArrayNode({ items, depth, maxDepth }: { items: Json[]; depth: number; maxDepth: number }) {
  const [showAll, setShowAll] = useState(false);
  if (items.length === 0) return <Scalar text="[]" tone="mute" />;

  // Scalar arrays render inline as comma-separated chips.
  const allScalar = items.every(
    (i) => i == null || typeof i === 'string' || typeof i === 'number' || typeof i === 'boolean',
  );
  if (allScalar) {
    return (
      <div className="flex flex-wrap gap-1">
        {items.map((it, i) => (
          <span
            key={i}
            className="inline-flex items-center text-[11px] px-1.5 py-0.5 rounded bg-slate-100 ring-1 ring-slate-200 text-ink-dim font-mono"
          >
            {it == null ? 'null' : String(it)}
          </span>
        ))}
      </div>
    );
  }

  const limit = 10;
  const display = showAll ? items : items.slice(0, limit);
  return (
    <div className="space-y-1.5">
      {display.map((it, i) => (
        <div key={i} className="bg-slate-50 border border-slate-200 rounded p-2">
          <div className="text-[10px] uppercase text-ink-mute mb-1">item {i}</div>
          <ValueNode value={it} depth={depth + 1} maxDepth={maxDepth} />
        </div>
      ))}
      {items.length > limit && !showAll && (
        <button
          onClick={() => setShowAll(true)}
          className="inline-flex items-center gap-1 text-[11px] text-brand-700 hover:underline"
        >
          <ChevronRight size={10} /> show all {items.length}
        </button>
      )}
    </div>
  );
}

function Scalar({ text, tone }: { text: string; tone: 'mute' | 'default' }) {
  return (
    <span className={tone === 'mute' ? 'text-xs text-ink-mute' : 'text-xs text-ink'}>{text}</span>
  );
}

function prettyKey(k: string): string {
  return k.replace(/_/g, ' ');
}

function looksLikeHash(s: string): boolean {
  return /^[0-9a-f]{40,64}$/i.test(s) || /^sha\d+:/i.test(s);
}

function looksLikeMultiline(s: string): boolean {
  return s.includes('\n') || s.length > 200;
}
