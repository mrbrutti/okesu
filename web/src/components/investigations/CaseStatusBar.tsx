// CaseStatusBar: 5-cell single row at the top of the Investigation
// Overview tab. Status pill + severity histogram + freshness +
// counts + truncated Summary with Edit toggle. Pure presentation
// over the InvestigationDetail bundle; the Summary edit state is
// owned by InvestigationDetailPage and threaded in via props.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, Pencil } from 'lucide-react';
import type { InvestigationDetail, Severity } from '../../api';
import { ALL_SEVERITIES } from '../../api';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
  editing: boolean;
  draftSummary: string;
  setDraftSummary: (s: string) => void;
  onEditToggle: () => void;
}

const SEV_BG: Record<Severity, string> = {
  CRITICAL: 'bg-red-500',
  HIGH:     'bg-orange-500',
  MEDIUM:   'bg-amber-400',
  LOW:      'bg-blue-400',
  INFO:     'bg-slate-300',
};

const SEV_TEXT: Record<Severity, string> = {
  CRITICAL: 'text-red-700',
  HIGH:     'text-orange-700',
  MEDIUM:   'text-amber-700',
  LOW:      'text-blue-700',
  INFO:     'text-slate-700',
};

export function CaseStatusBar({ bundle, cpInstanceID, editing, draftSummary, setDraftSummary, onEditToggle }: Props) {
  const inv = bundle.investigation;
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  const invID = inv.ID;

  const sevCounts = useMemo(() => {
    const counts: Record<Severity, number> = { CRITICAL: 0, HIGH: 0, MEDIUM: 0, LOW: 0, INFO: 0 };
    for (const f of bundle.findings) {
      const s = (f.Severity.Valid ? f.Severity.String : 'INFO') as Severity;
      if (s in counts) counts[s] += 1;
    }
    return counts;
  }, [bundle.findings]);

  const sevTotal = bundle.findings.length;

  const freshness = useMemo(() => {
    let latest = 0;
    for (const f of bundle.findings) latest = Math.max(latest, f.Ts);
    for (const n of bundle.notes) latest = Math.max(latest, Date.parse(n.CreatedAt));
    for (const r of bundle.runs) {
      latest = Math.max(latest, Date.parse(r.StartedAt));
      if (r.EndedAt.Valid) latest = Math.max(latest, Date.parse(r.EndedAt.String));
    }
    return latest;
  }, [bundle]);

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
      {/* Status */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Status</div>
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className={`inline-flex items-center text-[11px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${statusTone(inv.Status)}`}>
            {inv.Status}
          </span>
          {bundle.war_room && (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-semibold px-1.5 py-0.5 rounded bg-red-600 text-white">
              <AlertTriangle size={10} /> War-room
            </span>
          )}
        </div>
        {inv.CreatedBy && <div className="text-[11px] text-ink-mute truncate">by <code className="font-mono">{inv.CreatedBy}</code></div>}
      </div>

      {/* Severity histogram */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Severity</div>
        {sevTotal === 0 ? (
          <div className="text-ink-mute italic">no findings linked yet</div>
        ) : (
          <>
            <div className="flex h-4 rounded overflow-hidden" role="img" aria-label={`Severity histogram across ${sevTotal} findings`}>
              {ALL_SEVERITIES.map((s) => sevCounts[s] > 0 && (
                <Link
                  key={s}
                  to={`/investigations/${invID}?tab=findings&severity=${s}${cpQS}`}
                  className={SEV_BG[s]}
                  style={{ flexBasis: `${(sevCounts[s] / sevTotal) * 100}%` }}
                  aria-label={`${s}: ${sevCounts[s]}`}
                  title={`${s}: ${sevCounts[s]}`}
                />
              ))}
            </div>
            <div className="flex flex-wrap gap-x-2 gap-y-0.5 text-[10px]">
              {ALL_SEVERITIES.map((s) => sevCounts[s] > 0 && (
                <span key={s} className={SEV_TEXT[s]}>{s} {sevCounts[s]}</span>
              ))}
            </div>
          </>
        )}
      </div>

      {/* Freshness */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Freshness</div>
        <div className="text-ink">Last activity {relTime(freshness)}</div>
        <div className="text-ink-mute">Created {relTime(Date.parse(inv.CreatedAt))}</div>
      </div>

      {/* Counts */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Linked</div>
        <CountLink href={`/investigations/${invID}?tab=findings${cpQS}`} label="findings" n={bundle.findings.length} />
        <CountLink href={`/investigations/${invID}?tab=runs${cpQS}`}     label="runs"     n={bundle.runs.length} />
        <CountLink href={`/investigations/${invID}?tab=iocs${cpQS}`}     label="iocs"     n={bundle.iocs.length} />
        <CountLink href={`/investigations/${invID}?tab=daimons${cpQS}`}  label="daimons"  n={bundle.daimons.length} />
        <CountLink href={`/investigations/${invID}?tab=notes${cpQS}`}    label="notes"    n={bundle.notes.length} />
      </div>

      {/* Summary */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5 lg:col-span-1">
        <div className="flex items-center justify-between gap-2">
          <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Summary</div>
          {!editing && (
            <button
              type="button"
              onClick={onEditToggle}
              className="text-[11px] text-brand-700 hover:text-brand-800 inline-flex items-center gap-0.5"
              aria-label="Edit summary"
            >
              <Pencil size={10} /> Edit
            </button>
          )}
        </div>
        {editing ? (
          <textarea
            value={draftSummary}
            onChange={(e) => setDraftSummary(e.target.value)}
            rows={3}
            className="w-full px-2 py-1 rounded border border-border bg-white text-xs"
            placeholder="Hypothesis, scope, working theory…"
          />
        ) : inv.Summary ? (
          <div className="text-ink line-clamp-3 whitespace-pre-wrap">{inv.Summary}</div>
        ) : (
          <div className="text-ink-mute italic">No summary yet.</div>
        )}
      </div>
    </div>
  );
}

function CountLink({ href, label, n }: { href: string; label: string; n: number }) {
  return (
    <div className="flex items-center justify-between gap-1.5">
      <Link to={href} className="text-ink-mute hover:text-brand-700">{label}</Link>
      <span className="font-mono text-ink">{n}</span>
    </div>
  );
}

function statusTone(s: 'active' | 'closed' | 'archived'): string {
  if (s === 'active') return 'text-blue-700 bg-blue-50 ring-blue-200';
  if (s === 'closed') return 'text-emerald-700 bg-emerald-50 ring-emerald-200';
  return 'text-slate-700 bg-slate-100 ring-slate-200';
}

function relTime(ts: number): string {
  if (!ts || Number.isNaN(ts)) return 'never';
  const ageMs = Date.now() - ts;
  const m = Math.floor(ageMs / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
