// Investigate flow — the single entry-point on a finding's drawer.
//
// Replaces the previous trio of buttons:
//   Investigate (run an agent)   → moved to the case workspace as "Run agent"
//   Open in investigation        → "New case" tab here
//   Add to existing              → "Existing case" tab here
//
// The dialog has a smart default:
//
//   • Finding is in 0 cases → tabs default to "New case"
//   • Finding is in N cases → header shows the existing memberships
//                             with deep-link buttons; tabs are still
//                             below for "add to a different case"
//
// Federated findings flow through the parent's ?cp=<id> proxy paths
// so the case + link both land on the owning child CP.

import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowUpRight, ClipboardList, Loader2, Plus, Search, X } from 'lucide-react';
import { api, type Finding, type Investigation } from '../api';
import { cn } from '../lib/cn';

type Mode = 'new' | 'existing';

export function InvestigateDialog({
  finding,
  cpInstanceID,
  onClose,
  onLinked,
}: {
  finding: Finding;
  cpInstanceID?: string;
  onClose: () => void;
  /** Called after a successful link or create+link. Caller can refresh
   *  the drawer's investigations panel. Navigation to the case
   *  workspace is the dialog's own concern. */
  onLinked: () => void;
}) {
  const navigate = useNavigate();
  const [memberships, setMemberships] = useState<Investigation[] | null>(null);
  const [mode, setMode] = useState<Mode>('new');
  const [err, setErr] = useState<string | null>(null);

  // Load existing memberships up-front. The shape of the dialog
  // changes if the finding is already in cases — operators expect
  // to see those first, not be asked to create a case that already
  // exists.
  useEffect(() => {
    api.findingInvestigations(finding.id, cpInstanceID)
      .then((rows) => {
        setMemberships(rows);
        // If the finding is in 0 cases, "New case" is the natural
        // default. If it's already in cases, default to "existing"
        // so the operator can pick another case from the picker
        // rather than spawning a duplicate.
        if (rows.length > 0) setMode('existing');
      })
      .catch(() => setMemberships([]));
  }, [finding.id, cpInstanceID]);

  function gotoCase(invID: number) {
    const qs = cpInstanceID ? `?cp=${encodeURIComponent(cpInstanceID)}` : '';
    navigate(`/investigations/${invID}${qs}`);
    onClose();
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-lg flex flex-col"
        onClick={(e) => e.stopPropagation()}
        style={{ maxHeight: '80vh' }}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <ClipboardList size={14} className="text-brand-500" />
            Investigate
          </h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>

        <div className="flex-1 overflow-auto">
          {/* Existing memberships — surfaced first so operators don't
              create duplicates of a case that's already tracking the
              finding. */}
          {memberships !== null && memberships.length > 0 && (
            <section className="px-5 pt-4 pb-3 border-b border-border bg-violet-50/40">
              <div className="text-[11px] uppercase tracking-wide text-violet-700 font-medium mb-2">
                Already in {memberships.length} case{memberships.length === 1 ? '' : 's'}
              </div>
              <ul className="space-y-1.5">
                {memberships.map((inv) => (
                  <li key={inv.ID}>
                    <button
                      onClick={() => gotoCase(inv.ID)}
                      className="w-full flex items-center gap-2 px-2.5 py-1.5 rounded-md text-left text-sm bg-white ring-1 ring-violet-200 hover:bg-violet-100"
                    >
                      <span className="font-medium truncate flex-1">{inv.Title || '(untitled)'}</span>
                      <span className="text-[10px] uppercase tracking-wide text-violet-700 font-medium">
                        {inv.Status}
                      </span>
                      <ArrowUpRight size={12} className="text-violet-700" />
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}

          {/* Mode tabs */}
          <nav className="px-5 pt-3 flex items-center gap-1">
            <ModeTab active={mode === 'new'}      onClick={() => setMode('new')}      label="New case" icon={Plus} />
            <ModeTab active={mode === 'existing'} onClick={() => setMode('existing')} label="Existing case" icon={Search} />
          </nav>

          <div className="p-5">
            {mode === 'new' && (
              <NewCasePanel
                finding={finding}
                cpInstanceID={cpInstanceID}
                onCreated={(invID) => { onLinked(); gotoCase(invID); }}
                setError={setErr}
              />
            )}
            {mode === 'existing' && (
              <ExistingCasePanel
                finding={finding}
                cpInstanceID={cpInstanceID}
                excludeIDs={new Set((memberships ?? []).map((m) => m.ID))}
                onLinked={(invID) => { onLinked(); gotoCase(invID); }}
                setError={setErr}
              />
            )}
            {err && (
              <div className="mt-3 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
                {err}
              </div>
            )}
          </div>
        </div>

        <footer className="px-5 py-3 border-t border-border flex items-center justify-end">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        </footer>
      </div>
    </div>
  );
}

// ── Tab pill ────────────────────────────────────────────────────────

function ModeTab({
  active, onClick, label, icon: Icon,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  icon: typeof Plus;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border-b-2 -mb-px transition',
        active
          ? 'border-brand-500 text-brand-700'
          : 'border-transparent text-ink-dim hover:text-ink',
      )}
    >
      <Icon size={11} />
      {label}
    </button>
  );
}

// ── New case panel ──────────────────────────────────────────────────

function NewCasePanel({
  finding, cpInstanceID, onCreated, setError,
}: {
  finding: Finding;
  cpInstanceID?: string;
  onCreated: (invID: number) => void;
  setError: (s: string | null) => void;
}) {
  const [title, setTitle] = useState(finding.title || `Finding #${finding.id}`);
  const [summary, setSummary] = useState(finding.evidence || '');
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!title.trim()) { setError('Title is required.'); return; }
    setBusy(true); setError(null);
    try {
      const inv = await api.investigations.create(
        { title: title.trim(), summary: summary.trim(), from_finding_id: finding.id },
        cpInstanceID,
      );
      onCreated(inv.ID);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3 text-sm">
      <p className="text-xs text-ink-dim">
        Promotes finding <code className="font-mono">#{finding.id}</code> into a new case.
        {cpInstanceID && (
          <> The case will live on the federated child <code className="font-mono">{cpInstanceID}</code> alongside the finding.</>
        )}
      </p>
      <div>
        <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">Title</div>
        <input
          autoFocus
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
      </div>
      <div>
        <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">Summary</div>
        <textarea
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          rows={4}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          placeholder="Hypothesis, scope, working theory…"
        />
      </div>
      <div className="flex justify-end pt-1">
        <button
          onClick={submit}
          disabled={busy || !title.trim()}
          className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
        >
          {busy && <Loader2 size={12} className="animate-spin" />}
          Open case
        </button>
      </div>
    </div>
  );
}

// ── Existing case panel ─────────────────────────────────────────────

function ExistingCasePanel({
  finding, cpInstanceID, excludeIDs, onLinked, setError,
}: {
  finding: Finding;
  cpInstanceID?: string;
  excludeIDs: Set<number>;
  onLinked: (invID: number) => void;
  setError: (s: string | null) => void;
}) {
  const [items, setItems] = useState<Investigation[] | null>(null);
  const [picked, setPicked] = useState<number | null>(null);
  const [filter, setFilter] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.investigations.list('active')
      .then((rows) => {
        // Cross-CP linking would FK-fail; restrict the picker to
        // cases on the same CP as the finding.
        const matched = cpInstanceID
          ? rows.filter((r) => r.cp_source && r.cp_source.instance_id === cpInstanceID)
          : rows.filter((r) => !r.cp_source);
        setItems(matched);
      })
      .catch((e) => setError(String(e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cpInstanceID]);

  async function submit() {
    if (!picked) return;
    setBusy(true); setError(null);
    try {
      await api.investigations.linkFinding(picked, finding.id, cpInstanceID);
      onLinked(picked);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  const filtered = (items ?? [])
    .filter((i) => !excludeIDs.has(i.ID))
    .filter((i) => !filter || i.Title.toLowerCase().includes(filter.toLowerCase()));

  return (
    <div className="space-y-3 text-sm">
      <p className="text-xs text-ink-dim">
        Pick an active case
        {cpInstanceID
          ? <> on <code className="font-mono">{cpInstanceID}</code> </>
          : ' '}
        to add this finding to. Closed and archived cases are filtered out — reopen them on the case page first if needed.
      </p>
      <input
        type="text"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter by title…"
        autoFocus
        className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
      />
      <div className="border border-border rounded-md max-h-60 overflow-auto">
        {items === null ? (
          <div className="p-3 text-xs text-ink-mute">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-3 text-xs text-ink-mute">
            No matching active cases{cpInstanceID ? ` on ${cpInstanceID}` : ''}.
          </div>
        ) : (
          <ul className="divide-y divide-border">
            {filtered.map((i) => (
              <li key={i.ID}>
                <label className="flex items-start gap-2 px-3 py-2 cursor-pointer hover:bg-slate-50">
                  <input
                    type="radio"
                    checked={picked === i.ID}
                    onChange={() => setPicked(i.ID)}
                    className="mt-1"
                  />
                  <div className="min-w-0">
                    <div className="text-sm font-medium truncate">{i.Title || '(untitled)'}</div>
                    <div className="text-[11px] text-ink-mute font-mono">#{i.ID}</div>
                  </div>
                </label>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="flex justify-end">
        <button
          onClick={submit}
          disabled={busy || !picked}
          className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
        >
          {busy && <Loader2 size={12} className="animate-spin" />}
          Add to case
        </button>
      </div>
    </div>
  );
}
