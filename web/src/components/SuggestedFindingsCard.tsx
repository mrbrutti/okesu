// SuggestedFindingsCard — workspace Overview tab card that surfaces
// findings the case might be missing.
//
// The server (controlplane/api/investigations.go SuggestFindingsHandler)
// does the scoring; we render the top N with their score + which
// signals fired (chips) + Add / Dismiss buttons.
//
// Add → linkFinding (server-side side effect lifts any prior
//        dismissal tombstone for the pair).
// Dismiss → tombstone keyed to (case, finding); per-case, not
//           per-operator. Once dismissed, the suggestion stays gone
//           for everyone working the case until someone re-Adds it.
//
// Closed/archived cases never render this — OverviewPanel
// short-circuits before instantiating us.

import { useEffect, useState } from 'react';
import { Lightbulb, Loader2, Plus, RefreshCw, ThumbsDown } from 'lucide-react';
import { Link } from 'react-router-dom';
import { api, type SuggestedFinding, type SuggestionSignal } from '../api';

const SIGNAL_LABEL: Record<SuggestionSignal, string> = {
  dedup_key: 'same dedup',
  ioc: 'same IOC',
  host_window: 'same host ±1h',
  daimon_sev: 'same daimon+sev',
};

const SIGNAL_TONE: Record<SuggestionSignal, string> = {
  dedup_key: 'bg-purple-50 text-purple-800 border-purple-200',
  ioc: 'bg-rose-50 text-rose-800 border-rose-200',
  host_window: 'bg-amber-50 text-amber-800 border-amber-200',
  daimon_sev: 'bg-slate-50 text-slate-700 border-slate-200',
};

export function SuggestedFindingsCard({
  invID,
  cpInstanceID,
  onChange,
}: {
  invID: number;
  cpInstanceID?: string;
  /** Called after a successful Add so the parent reloads the bundle
   *  (the linked finding now lives in `bundle.findings`). Dismiss
   *  doesn't trigger this — it's a card-local refresh. */
  onChange: () => void;
}) {
  const [items, setItems] = useState<SuggestedFinding[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Per-row spinner so Add/Dismiss feedback is local.
  const [pendingID, setPendingID] = useState<number | null>(null);

  async function load() {
    setError(null);
    try {
      const rows = await api.investigations.suggestFindings(invID, { cpInstanceID });
      setItems(rows);
    } catch (e) {
      setError(String(e));
      setItems([]);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [invID, cpInstanceID]);

  async function add(findingID: number) {
    setPendingID(findingID); setBusy(true);
    try {
      await api.investigations.linkFinding(invID, findingID, cpInstanceID);
      // Reload the parent bundle (the linked finding now appears in
      // bundle.findings, which feeds the next suggestion query).
      onChange();
      // The suggestion list refreshes via the parent's reload →
      // OverviewPanel re-renders with the same invID, the effect
      // above re-fires. We also remove the row optimistically so the
      // operator gets immediate feedback even before the parent
      // round-trip completes.
      setItems((curr) => (curr ?? []).filter((s) => s.ID !== findingID));
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false); setPendingID(null);
    }
  }

  async function dismiss(findingID: number) {
    setPendingID(findingID); setBusy(true);
    try {
      await api.investigations.dismissSuggestedFinding(invID, findingID, { cpInstanceID });
      setItems((curr) => (curr ?? []).filter((s) => s.ID !== findingID));
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false); setPendingID(null);
    }
  }

  return (
    <div className="border border-border rounded-md bg-white">
      <header className="px-4 py-2.5 border-b border-border flex items-center gap-2">
        <Lightbulb size={13} className="text-amber-600" />
        <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">
          Suggested findings
        </h4>
        {items && items.length > 0 && (
          <span className="text-[11px] text-ink-mute">· {items.length}</span>
        )}
        <button
          onClick={() => load()}
          disabled={busy}
          className="ml-auto p-1 text-ink-mute hover:text-ink rounded-md disabled:opacity-50"
          title="Refresh suggestions"
        >
          <RefreshCw size={12} />
        </button>
      </header>

      {error && (
        <div className="px-4 py-2 text-xs text-red-700 bg-red-50 border-b border-red-200">
          {error}
        </div>
      )}

      {items === null ? (
        <div className="p-4 text-xs text-ink-mute flex items-center gap-2">
          <Loader2 size={12} className="animate-spin" /> Scoring candidates…
        </div>
      ) : items.length === 0 ? (
        <div className="p-4 text-xs text-ink-mute italic">
          Nothing related to suggest right now. Link more findings to widen the search.
        </div>
      ) : (
        <ul className="divide-y divide-border">
          {items.map((s) => (
            <li key={s.ID} className="px-4 py-2.5 flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/findings?id=${s.ID}${cpInstanceID ? `&cp=${cpInstanceID}` : ''}`}
                    className="text-brand-700 hover:underline font-mono text-[11px]"
                  >
                    #{s.ID}
                  </Link>
                  <SeverityBadge sev={s.Severity?.Valid ? s.Severity.String : ''} />
                  <span className="text-[11px] text-ink-mute">score {s.Score}</span>
                </div>
                <div className="text-sm font-medium truncate mt-0.5">
                  {s.Title?.Valid ? s.Title.String : <span className="text-ink-mute italic">(no title)</span>}
                </div>
                <div className="flex items-center gap-1.5 flex-wrap mt-1.5">
                  {s.Agent?.Valid && (
                    <span className="text-[11px] text-ink-mute font-mono">{s.Agent.String}</span>
                  )}
                  {s.Host?.Valid && (
                    <>
                      <span className="text-[11px] text-ink-mute">·</span>
                      <span className="text-[11px] text-ink-mute font-mono">{s.Host.String}</span>
                    </>
                  )}
                  {(s.Signals ?? []).map((sig) => (
                    <span
                      key={sig}
                      className={`text-[10px] px-1.5 py-0.5 rounded border font-medium ${SIGNAL_TONE[sig] ?? SIGNAL_TONE.daimon_sev}`}
                    >
                      {SIGNAL_LABEL[sig] ?? sig}
                    </span>
                  ))}
                </div>
              </div>
              <div className="shrink-0 flex items-center gap-1.5">
                <button
                  onClick={() => add(s.ID)}
                  disabled={busy}
                  className="text-[11px] px-2 py-1 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 inline-flex items-center gap-1"
                  title="Link this finding to the case"
                >
                  {pendingID === s.ID && busy ? <Loader2 size={11} className="animate-spin" /> : <Plus size={11} />}
                  Add
                </button>
                <button
                  onClick={() => dismiss(s.ID)}
                  disabled={busy}
                  className="text-[11px] px-2 py-1 rounded-md text-ink-dim border border-border hover:bg-slate-50 disabled:opacity-50 inline-flex items-center gap-1"
                  title="Dismiss this suggestion (per-case tombstone — won't surface here again)"
                >
                  {pendingID === s.ID && busy ? <Loader2 size={11} className="animate-spin" /> : <ThumbsDown size={11} />}
                  Dismiss
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function SeverityBadge({ sev }: { sev: string }) {
  if (!sev) return null;
  const tone =
    sev === 'CRITICAL' ? 'bg-red-100 text-red-800 border-red-200' :
    sev === 'HIGH'     ? 'bg-orange-100 text-orange-800 border-orange-200' :
    sev === 'MEDIUM'   ? 'bg-amber-100 text-amber-800 border-amber-200' :
    sev === 'LOW'      ? 'bg-emerald-100 text-emerald-800 border-emerald-200' :
                         'bg-slate-100 text-slate-700 border-slate-200';
  return (
    <span className={`text-[10px] px-1.5 py-0.5 rounded border font-medium uppercase tracking-wide ${tone}`}>
      {sev}
    </span>
  );
}
