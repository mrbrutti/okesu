// InvestigationAuditPanel — chronological timeline of case events.
//
// Renders a single column of events: case created, notes added,
// findings linked (with the link-method chip from PR #61), runs
// linked, case closed (with resolution).
//
// Kinds are stable, but `details` is loose JSON — the renderer just
// pulls out fields it knows about and falls back to the headline
// otherwise. New kinds added server-side land as plain rows with no
// detail block until a follow-up commit teaches the UI to format
// them.

import { useEffect, useState } from 'react';
import {
  CheckCircle2,
  ClipboardList,
  Hash,
  Loader2,
  MessageSquare,
  Plus,
  Sparkles,
} from 'lucide-react';
import { Link } from 'react-router-dom';
import { api, type InvestigationAuditEvent, type LinkMethod } from '../api';

const LINK_METHOD_LABEL: Record<LinkMethod, string> = {
  manual: 'manual',
  bulk: 'bulk',
  'auto-promote': 'promoted',
  autolink: 'auto',
  import: 'import',
};

const LINK_METHOD_TONE: Record<LinkMethod, string> = {
  manual: 'bg-slate-50 text-slate-700 border-slate-200',
  bulk: 'bg-emerald-50 text-emerald-800 border-emerald-200',
  'auto-promote': 'bg-violet-50 text-violet-800 border-violet-200',
  autolink: 'bg-blue-50 text-blue-800 border-blue-200',
  import: 'bg-slate-50 text-slate-700 border-slate-200',
};

export function InvestigationAuditPanel({
  invID,
  cpInstanceID,
}: {
  invID: number;
  cpInstanceID?: string;
}) {
  const [events, setEvents] = useState<InvestigationAuditEvent[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setError(null);
    api.investigations.audit(invID, cpInstanceID)
      .then(setEvents)
      .catch((e) => { setError(String(e)); setEvents([]); });
  }, [invID, cpInstanceID]);

  if (events === null) {
    return (
      <div className="p-6 text-sm text-ink-mute flex items-center gap-2">
        <Loader2 size={14} className="animate-spin" /> Loading timeline…
      </div>
    );
  }

  if (error) {
    return (
      <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
        {error}
      </div>
    );
  }

  if (events.length === 0) {
    return (
      <div className="p-6 text-sm text-ink-mute italic">
        No timeline events yet. Activity (notes, links, status changes) will appear here as the case develops.
      </div>
    );
  }

  return (
    <div className="border border-border rounded-md bg-white">
      <ul className="divide-y divide-border">
        {events.map((e, i) => (
          <li key={`${e.ts}-${i}`} className="px-4 py-3 flex items-start gap-3">
            <div className="shrink-0 w-6 h-6 mt-0.5 rounded-md bg-slate-100 text-slate-700 flex items-center justify-center">
              <KindIcon kind={e.kind} />
            </div>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-sm font-medium">{e.title}</span>
                {e.kind === 'finding_linked' && e.details?.method && (
                  <MethodChip method={e.details.method} />
                )}
                {e.kind === 'finding_linked' && e.details?.finding_id != null && (
                  <Link
                    to={`/findings?id=${e.details.finding_id}${cpInstanceID ? `&cp=${cpInstanceID}` : ''}`}
                    className="text-[11px] text-brand-700 hover:underline font-mono"
                  >
                    open finding
                  </Link>
                )}
                {e.kind === 'run_linked' && e.details?.run_id != null && (
                  <Link
                    to={`/orchestration-runs/${e.details.run_id}`}
                    className="text-[11px] text-brand-700 hover:underline font-mono"
                  >
                    open run
                  </Link>
                )}
              </div>
              {e.kind === 'note' && e.details?.body && (
                <div className="mt-1 text-xs text-ink-dim whitespace-pre-wrap">
                  {e.details.body}
                </div>
              )}
              <div className="mt-1 text-[11px] text-ink-mute font-mono flex items-center gap-2">
                <span>{fmtTs(e.ts)}</span>
                {e.by && <><span>·</span><span>{e.by}</span></>}
              </div>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

function KindIcon({ kind }: { kind: InvestigationAuditEvent['kind'] }) {
  switch (kind) {
    case 'created':        return <Sparkles size={12} className="text-brand-600" />;
    case 'closed':         return <CheckCircle2 size={12} className="text-green-700" />;
    case 'note':           return <MessageSquare size={12} className="text-slate-600" />;
    case 'finding_linked': return <Hash size={12} className="text-amber-700" />;
    case 'run_linked':     return <Plus size={12} className="text-violet-700" />;
    default:               return <ClipboardList size={12} />;
  }
}

function MethodChip({ method }: { method: LinkMethod }) {
  const cls = LINK_METHOD_TONE[method] ?? LINK_METHOD_TONE.manual;
  const label = LINK_METHOD_LABEL[method] ?? method;
  return (
    <span
      className={`text-[10px] px-1.5 py-0.5 rounded border font-medium uppercase tracking-wide ${cls}`}
      title={`linked via ${method}`}
    >
      {label}
    </span>
  );
}

function fmtTs(ts: string): string {
  // Server returns SQL-style "YYYY-MM-DD HH:MM:SS" (UTC) — render as
  // local-time short form. Empty / parse failures fall back to the
  // raw value so we never silently swallow weird data.
  if (!ts) return '—';
  // SQL timestamps lack a `T` and `Z`. Normalise so Date parses them.
  const norm = ts.includes('T') ? ts : ts.replace(' ', 'T') + 'Z';
  const d = new Date(norm);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString();
}
