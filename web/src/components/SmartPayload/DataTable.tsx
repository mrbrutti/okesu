// DataTable: homogeneous-array renderer.
//
// SmartPayload bottoms out a 50-element array of {kind, finding_id,
// status, reason, …} into a wall of 50 separate StructuredView cards.
// That's brutal to scan. When the items share a shape, a real table
// is what the operator actually wants.
//
// Heuristic for "homogeneous":
//   - ≥2 items
//   - every item is a non-null, non-array object
//   - ≥2/3 of items share at least 2 common top-level keys
//
// Columns: union of keys across all items, ordered as
//   identity (kind, id, *_id) →
//   classification (status, severity, level) →
//   short labels (name, tag, host, agent, value) →
//   prose (reason, evidence, message, error) at the end.
//
// Cell rendering: entity refs (finding_id, run_id, node_id, etc.)
// render as a chip linking to the entity. Plain scalars render as
// monospace; long strings get truncated with a hover-to-expand
// affordance. Nested objects render as a compact sub-tree.

import { useState } from 'react';
import { ChipForKind } from './chipForKind';
import { detectEntity } from './detectors';
import { StructuredView } from '../StructuredView';

type Obj = Record<string, unknown>;

interface Props {
  rows: Obj[];
  cpInstanceID?: string;
}

// isHomogeneous gate that the caller (TreeRenderer / PromptRenderer)
// uses to decide whether to switch to table mode.
export function isHomogeneousObjectArray(value: unknown): value is Obj[] {
  if (!Array.isArray(value)) return false;
  if (value.length < 2) return false;
  let objCount = 0;
  const keyHits: Record<string, number> = {};
  for (const v of value) {
    if (v === null || typeof v !== 'object' || Array.isArray(v)) continue;
    objCount++;
    for (const k of Object.keys(v as Obj)) {
      keyHits[k] = (keyHits[k] ?? 0) + 1;
    }
  }
  if (objCount < 2 || objCount < value.length * 0.66) return false;
  // ≥2 keys with ≥2/3 coverage.
  const coverageThreshold = Math.ceil(objCount * 0.66);
  let common = 0;
  for (const k of Object.keys(keyHits)) {
    if (keyHits[k] >= coverageThreshold) common++;
    if (common >= 2) return true;
  }
  return false;
}

// orderColumns sorts column names by the priority groups described
// in the file header. Keys not in any group keep their insertion
// order at the tail.
function orderColumns(cols: string[]): string[] {
  const idGroup = new Set(['kind', 'type', 'id', 'finding_id', 'run_id', 'node_id', 'agent_id', 'investigation_id', 'orchestration_id', 'cluster_id', 'event_id']);
  const classGroup = new Set(['status', 'severity', 'level', 'effective_severity', 'min_severity']);
  const labelGroup = new Set(['name', 'title', 'tag', 'host', 'hostname', 'agent', 'category', 'value', 'kind_label']);
  const proseGroup = new Set(['reason', 'evidence', 'message', 'error', 'note', 'description', 'summary']);
  const score = (k: string): number => {
    if (idGroup.has(k))    return 0;
    if (classGroup.has(k)) return 1;
    if (labelGroup.has(k)) return 2;
    if (proseGroup.has(k)) return 4;
    return 3;
  };
  return [...cols].sort((a, b) => {
    const sa = score(a), sb = score(b);
    if (sa !== sb) return sa - sb;
    return cols.indexOf(a) - cols.indexOf(b);
  });
}

// chipForCell tries to render a cell value as an entity chip when
// the column is a known foreign key (`finding_id`, `run_id`, etc.).
// Falls back to null so the caller can render the raw value.
function chipForCell(col: string, val: unknown, cpInstanceID?: string): React.ReactNode | null {
  if (val == null || (typeof val !== 'number' && typeof val !== 'string')) return null;
  const numericID = typeof val === 'number' ? val : Number(val);
  if (!Number.isFinite(numericID) || numericID <= 0) return null;
  const map: Record<string, string> = {
    finding_id:       'finding',
    run_id:           'run',
    node_id:          'node',
    investigation_id: 'investigation',
    orchestration_id: 'orchestration',
    agent_id:         'daimon',
  };
  const kind = map[col];
  if (!kind) return null;
  return (
    <ChipForKind
      kind={kind}
      snapshot={{ id: numericID }}
      cpInstanceID={cpInstanceID}
    />
  );
}

function renderCell(col: string, val: unknown, cpInstanceID?: string): React.ReactNode {
  const chip = chipForCell(col, val, cpInstanceID);
  if (chip) return chip;
  if (val === null || val === undefined) return <span className="text-ink-mute">—</span>;
  if (typeof val === 'boolean') {
    return (
      <span className={val ? 'text-green-700 font-medium' : 'text-ink-dim'}>
        {String(val)}
      </span>
    );
  }
  if (typeof val === 'number') {
    return <span className="font-mono">{val}</span>;
  }
  if (typeof val === 'string') {
    if (col === 'kind' || col === 'type' || col === 'category' || col === 'status' || col === 'severity') {
      return (
        <span className="inline-flex items-center text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-slate-700 bg-slate-50 ring-slate-200">
          {val}
        </span>
      );
    }
    if (val.length > 80) {
      return <CellTooltip text={val} />;
    }
    return <span className="break-words">{val}</span>;
  }
  // nested object/array — sub-tree
  return (
    <div className="text-[11px] max-w-[400px]">
      <StructuredView value={val} maxDepth={2} />
    </div>
  );
}

function CellTooltip({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <button
      type="button"
      onClick={() => setExpanded((e) => !e)}
      className="text-left hover:bg-slate-50 rounded px-1 py-0.5 max-w-md"
      title={text}
    >
      {expanded ? text : (text.slice(0, 80) + '…')}
    </button>
  );
}

export function DataTable({ rows, cpInstanceID }: Props) {
  // Detect homogeneous row-shape; fall back to per-item rendering if
  // the gate fails (the caller usually pre-checks, but we re-check
  // defensively for direct callers).
  if (!isHomogeneousObjectArray(rows as unknown)) {
    const fallback = rows as unknown as unknown[];
    return (
      <div className="space-y-2">
        {fallback.map((r, i) => (
          <StructuredView key={i} value={r} />
        ))}
      </div>
    );
  }

  const allKeys = new Set<string>();
  for (const r of rows) {
    for (const k of Object.keys(r)) allKeys.add(k);
  }
  const cols = orderColumns([...allKeys]);

  return (
    <div className="border border-border rounded-md overflow-hidden bg-panel">
      <div className="overflow-auto">
        <table className="w-full text-xs">
          <thead className="bg-slate-50 sticky top-0">
            <tr>
              {cols.map((c) => (
                <th
                  key={c}
                  className="text-left text-[10px] uppercase tracking-wide font-semibold text-ink-mute px-2 py-1.5 border-b border-border"
                >
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => {
              // If the whole row matches an entity shape, render as a single
              // chipped cell spanning all columns.
              const det = detectEntity(row);
              if (det) {
                return (
                  <tr key={i} className="hover:bg-slate-50/40">
                    <td colSpan={cols.length} className="px-2 py-1.5 border-b border-border">
                      <ChipForKind kind={det.kind} snapshot={det.snapshot} cpInstanceID={cpInstanceID} />
                    </td>
                  </tr>
                );
              }
              return (
                <tr key={i} className="hover:bg-slate-50/40">
                  {cols.map((c) => (
                    <td key={c} className="px-2 py-1.5 border-b border-border align-top">
                      {c in row ? renderCell(c, row[c], cpInstanceID) : <span className="text-ink-mute">—</span>}
                    </td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="px-2 py-1 text-[10px] text-ink-mute bg-slate-50 border-t border-border">
        {rows.length} row{rows.length === 1 ? '' : 's'} · {cols.length} column{cols.length === 1 ? '' : 's'}
      </div>
    </div>
  );
}
