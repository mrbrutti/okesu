// SmartPayload: entity-aware renderer. Two modes:
//
//   prompt mode  — `value: string`. Tokenizes prose ↔ JSON segments;
//                  for each JSON segment, looks up the matching ref
//                  in `entities` (by literal_hash) or falls back to
//                  client-side shape detection. Renders chips inline
//                  at the splice site; prose stays as plain text.
//
//   tree mode    — `value: object | array`. Walks the tree; subtrees
//                  matching an entity shape render as chips in place;
//                  the rest renders via StructuredView.
//
// `auto` mode picks based on `typeof value === 'string'`.

import { useEffect, useState } from 'react';
import type { PromptEntities, PromptEntityRef } from '../../api';
import { tokenize } from './tokenize';
import { detectEntity } from './detectors';
import { literalHash } from './literalHash';
import { normalizeLegacyShape } from './normalize';
import { StructuredView } from '../StructuredView';
import { FindingChip } from './chips/FindingChip';
import { IOCChip } from './chips/IOCChip';
import { NodeChip } from './chips/NodeChip';
import { DaimonChip } from './chips/DaimonChip';
import { RunChip } from './chips/RunChip';
import { InvestigationChip } from './chips/InvestigationChip';
import { OrchestrationChip } from './chips/OrchestrationChip';

interface Props {
  value: unknown;
  entities?: PromptEntities;
  cpInstanceID?: string;
  variant?: 'prompt' | 'tree' | 'auto';
}

export function SmartPayload({ value, entities, cpInstanceID, variant = 'auto' }: Props) {
  const mode =
    variant === 'auto' ? (typeof value === 'string' ? 'prompt' : 'tree') : variant;

  if (mode === 'prompt' && typeof value === 'string') {
    return <PromptRenderer text={value} entities={entities} cpInstanceID={cpInstanceID} />;
  }
  return <TreeRenderer value={value} cpInstanceID={cpInstanceID} />;
}

// ─── prompt mode ──────────────────────────────────────────────

function PromptRenderer({
  text,
  entities,
  cpInstanceID,
}: {
  text: string;
  entities?: PromptEntities;
  cpInstanceID?: string;
}) {
  const tokens = tokenize(text);
  // Pre-compute hashes for each json token so we can resolve them
  // against entities.refs when provided. Hashing is async (Web Crypto),
  // so we resolve once after mount and cache.
  const [hashes, setHashes] = useState<Record<number, string>>({});
  useEffect(() => {
    let cancelled = false;
    Promise.all(
      tokens.map(async (t, i) => {
        if (t.kind !== 'json') return null;
        const h = await literalHash(t.src);
        return [i, h] as const;
      }),
    ).then((entries) => {
      if (cancelled) return;
      const acc: Record<number, string> = {};
      for (const e of entries) {
        if (e) acc[e[0]] = e[1];
      }
      setHashes(acc);
    });
    return () => { cancelled = true; };
  }, [text]);

  return (
    <div className="text-sm leading-relaxed whitespace-pre-wrap break-words text-ink">
      {tokens.map((t, i) => {
        if (t.kind === 'text') return <span key={i}>{t.text}</span>;
        const ref = matchRef(entities, hashes[i]);
        if (ref) return <ChipForRef key={i} entityRef={ref} cpInstanceID={cpInstanceID ?? ref.cp_instance_id} />;
        // Fallback to client-side detection. Normalize first so legacy
        // pre-projection prompts (Title-case + sql.NullString wrappers)
        // pass the detector's lowercase contract.
        let parsed: unknown;
        try {
          parsed = JSON.parse(t.src);
        } catch {
          return <span key={i} className="font-mono text-xs">{t.src}</span>;
        }
        parsed = normalizeLegacyShape(parsed);
        // Arrays of entities (e.g. {{data.findings | json}}) are the
        // common case for batch-style prompts. Iterate so each item
        // gets its own chip; non-entity items fall back to inline JSON.
        if (Array.isArray(parsed)) {
          return (
            <div key={i} className="my-1 flex flex-wrap gap-1 items-start">
              {parsed.map((item, j) => {
                const d = detectEntity(item);
                if (d) {
                  return <ChipForKind key={j} kind={d.kind} snapshot={d.snapshot} cpInstanceID={cpInstanceID} />;
                }
                // Non-entity item: render as a clean expandable tree
                // instead of raw JSON.stringify. Each item gets its own
                // mini-card so {actions: [...]} or {by_agent: [...]} from
                // findings.summary are scannable.
                return (
                  <div key={j} className="bg-slate-50 border border-border rounded px-2 py-1 text-[11px]">
                    <StructuredView value={item} />
                  </div>
                );
              })}
            </div>
          );
        }
        const det = detectEntity(parsed);
        if (det) return <ChipForKind key={i} kind={det.kind} snapshot={det.snapshot} cpInstanceID={cpInstanceID} />;
        // Non-entity object: render as a structured tree, not a raw
        // code dump. Findings summaries, action arrays, and arbitrary
        // {{data.X | json}} payloads all land here.
        return (
          <div key={i} className="my-1 bg-slate-50 border border-border rounded px-3 py-2">
            <StructuredView value={parsed} />
          </div>
        );
      })}
    </div>
  );
}

function matchRef(entities: PromptEntities | undefined, hash: string | undefined): PromptEntityRef | null {
  if (!entities || !hash) return null;
  const found = entities.refs.find((r) => r.literal_hash === hash);
  return found ?? null;
}

function ChipForRef({ entityRef, cpInstanceID }: { entityRef: PromptEntityRef; cpInstanceID?: string }) {
  return <ChipForKind kind={entityRef.kind} snapshot={entityRef.snapshot} cpInstanceID={cpInstanceID} />;
}

function ChipForKind({ kind, snapshot, cpInstanceID }: { kind: string; snapshot: Record<string, unknown>; cpInstanceID?: string }) {
  switch (kind) {
    case 'finding':       return <FindingChip       snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'ioc':           return <IOCChip           snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'node':          return <NodeChip          snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'daimon':        return <DaimonChip        snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'run':           return <RunChip           snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'investigation': return <InvestigationChip snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    case 'orchestration': return <OrchestrationChip snapshot={snapshot} cpInstanceID={cpInstanceID} />;
    default:              return null;
  }
}

// ─── tree mode ────────────────────────────────────────────────

function TreeRenderer({ value, cpInstanceID }: { value: unknown; cpInstanceID?: string }) {
  // Normalize legacy Go-struct shapes (Title-case keys, sql.NullString
  // wrappers) so old run results pick up chips too.
  value = normalizeLegacyShape(value);
  const det = detectEntity(value);
  if (det) {
    return <ChipForKind kind={det.kind} snapshot={det.snapshot} cpInstanceID={cpInstanceID} />;
  }
  if (Array.isArray(value)) {
    return (
      <div className="flex flex-wrap gap-1">
        {value.map((item, i) => {
          const d = detectEntity(item);
          if (d) return <ChipForKind key={i} kind={d.kind} snapshot={d.snapshot} cpInstanceID={cpInstanceID} />;
          return <StructuredView key={i} value={item} />;
        })}
      </div>
    );
  }
  if (value !== null && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    return (
      <div className="space-y-2">
        {Object.entries(obj).map(([k, v]) => {
          const d = detectEntity(v);
          return (
            <div key={k} className="grid grid-cols-[max-content_1fr] gap-x-3">
              <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">{k}</div>
              <div>
                {d
                  ? <ChipForKind kind={d.kind} snapshot={d.snapshot} cpInstanceID={cpInstanceID} />
                  : <StructuredView value={v} />}
              </div>
            </div>
          );
        })}
      </div>
    );
  }
  return <StructuredView value={value} />;
}
