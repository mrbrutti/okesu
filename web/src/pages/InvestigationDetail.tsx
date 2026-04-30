// Investigation detail — bundle view for a single T2 case.
//
// Renders the case header (title, status, summary, lifecycle controls)
// plus three section panels: linked findings, linked orchestration runs,
// and analyst notes. Operators close a case here with one of the four
// resolution kinds; the close dialog is inline.
//
// The handler returns:
//   { investigation, findings: number[], runs: number[], notes: [] }
// — see controlplane/api/investigations.go GetInvestigationHandler.

import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ArrowLeft,
  Archive,
  Check,
  CheckCircle2,
  Clock,
  ClipboardList,
  Hash,
  Loader2,
  Lock,
  MessageSquarePlus,
  Pencil,
  Save,
  Sparkles,
  X,
  XCircle,
} from 'lucide-react';
import {
  api,
  ApiError,
  type Investigation,
  type InvestigationDetail,
  type InvestigationNote,
} from '../api';
import { cn } from '../lib/cn';

type Resolution = 'resolved' | 'false_positive' | 'duplicate' | 'wont_fix';

const RESOLUTION_OPTIONS: { value: Resolution; label: string; icon: typeof Check; tone: string }[] = [
  { value: 'resolved',       label: 'Resolved',        icon: CheckCircle2, tone: 'text-green-700' },
  { value: 'false_positive', label: 'False positive',  icon: XCircle,      tone: 'text-slate-600' },
  { value: 'duplicate',      label: 'Duplicate',       icon: Archive,      tone: 'text-slate-600' },
  { value: 'wont_fix',       label: "Won't fix",       icon: Lock,         tone: 'text-orange-700' },
];

export default function InvestigationDetailPage() {
  const { id = '' } = useParams<{ id: string }>();
  const invID = Number(id);
  const navigate = useNavigate();

  const [bundle, setBundle] = useState<InvestigationDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draftTitle, setDraftTitle] = useState('');
  const [draftSummary, setDraftSummary] = useState('');
  const [closeOpen, setCloseOpen] = useState(false);
  const [noteAuthor, setNoteAuthor] = useState('');
  const [noteBody, setNoteBody] = useState('');
  const [noteBusy, setNoteBusy] = useState(false);

  const reload = () => {
    setError(null);
    api.investigations
      .get(invID)
      .then((b) => {
        setBundle(b);
        setDraftTitle(b.investigation.Title);
        setDraftSummary(b.investigation.Summary);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  };

  useEffect(() => {
    if (!Number.isFinite(invID) || invID <= 0) {
      setError('Bad investigation id.');
      return;
    }
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [invID]);

  async function saveEdits() {
    if (!bundle) return;
    setBusy(true); setError(null);
    try {
      await api.investigations.update(invID, {
        title: draftTitle.trim() || bundle.investigation.Title,
        summary: draftSummary,
      });
      setEditing(false);
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function closeCase(resolution: Resolution) {
    setBusy(true); setError(null);
    try {
      await api.investigations.update(invID, { status: 'closed', resolution });
      setCloseOpen(false);
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function reopen() {
    setBusy(true); setError(null);
    try {
      // PATCH treats empty string as "don't touch" for resolution; the
      // server allows status=active even when a resolution was set
      // previously (closed_at gets cleared on the SQL side).
      await api.investigations.update(invID, { status: 'active' });
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function addNote() {
    if (!noteBody.trim()) return;
    setNoteBusy(true); setError(null);
    try {
      await api.investigations.addNote(invID, noteAuthor.trim() || 'operator', noteBody.trim());
      setNoteBody('');
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setNoteBusy(false);
    }
  }

  if (error && !bundle) {
    return (
      <div className="p-6">
        <button
          onClick={() => navigate('/investigations')}
          className="text-xs text-ink-dim hover:text-ink inline-flex items-center gap-1 mb-3"
        >
          <ArrowLeft size={12} /> Back to investigations
        </button>
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      </div>
    );
  }

  if (!bundle) {
    return (
      <div className="p-6 text-ink-dim text-xs flex items-center gap-2">
        <Loader2 size={14} className="animate-spin" /> Loading investigation…
      </div>
    );
  }

  const inv = bundle.investigation;
  const isActive = inv.Status === 'active';

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <div className="flex items-center gap-2 text-xs text-ink-dim mb-2">
          <Link to="/investigations" className="hover:text-ink inline-flex items-center gap-1">
            <ArrowLeft size={12} /> Investigations
          </Link>
          <span>·</span>
          <code className="font-mono">#{inv.ID}</code>
        </div>
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-1">
              <span className="inline-flex items-center justify-center w-6 h-6 rounded-md bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
                <ClipboardList size={11} />
              </span>
              <StatusChip status={inv.Status} />
              {inv.Resolution && <ResolutionChip res={inv.Resolution} />}
            </div>
            {editing ? (
              <input
                value={draftTitle}
                onChange={(e) => setDraftTitle(e.target.value)}
                className="w-full px-2 py-1 rounded-md border border-border text-lg font-semibold"
              />
            ) : (
              <h1 className="text-lg font-semibold leading-tight">{inv.Title || '(untitled)'}</h1>
            )}
          </div>
          <div className="flex items-center gap-2 shrink-0">
            {!editing && (
              <button
                onClick={() => setEditing(true)}
                className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
              >
                <Pencil size={11} /> Edit
              </button>
            )}
            {editing && (
              <>
                <button
                  onClick={() => { setEditing(false); setDraftTitle(inv.Title); setDraftSummary(inv.Summary); }}
                  className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
                >
                  <X size={11} /> Cancel
                </button>
                <button
                  onClick={saveEdits}
                  disabled={busy}
                  className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
                >
                  <Save size={11} /> Save
                </button>
              </>
            )}
            {isActive && !editing && (
              <button
                onClick={() => setCloseOpen(true)}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-green-600 text-white hover:bg-green-700"
              >
                <CheckCircle2 size={12} /> Close case
              </button>
            )}
            {!isActive && !editing && (
              <button
                onClick={reopen}
                disabled={busy}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
              >
                <Sparkles size={12} /> Reopen
              </button>
            )}
          </div>
        </div>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-5">
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {/* Summary + identity */}
        <section className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2 border border-border rounded-md bg-white p-4">
            <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2">
              Summary
            </h4>
            {editing ? (
              <textarea
                value={draftSummary}
                onChange={(e) => setDraftSummary(e.target.value)}
                rows={6}
                className="w-full px-2.5 py-1.5 rounded-md border border-border bg-white text-sm"
                placeholder="Hypothesis, scope, working theory…"
              />
            ) : inv.Summary ? (
              <div className="text-sm whitespace-pre-wrap text-ink">{inv.Summary}</div>
            ) : (
              <div className="text-sm text-ink-mute italic">No summary yet.</div>
            )}
          </div>

          <div className="border border-border rounded-md bg-white p-4 text-xs space-y-1.5">
            <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2">
              Identity
            </h4>
            <KV label="ID"><code className="font-mono">#{inv.ID}</code></KV>
            <KV label="Created">
              <span className="font-mono text-ink-dim">{fmtDate(inv.CreatedAt)}</span>
            </KV>
            <KV label="Updated">
              <span className="font-mono text-ink-dim">{fmtDate(inv.UpdatedAt)}</span>
            </KV>
            {inv.CreatedBy && (
              <KV label="Created by"><code className="font-mono">{inv.CreatedBy}</code></KV>
            )}
            {!isZeroTime(inv.ClosedAt) && (
              <KV label="Closed">
                <span className="font-mono text-ink-dim">{fmtDate(inv.ClosedAt)}</span>
              </KV>
            )}
          </div>
        </section>

        {/* Linked findings */}
        <section className="border border-border rounded-md bg-white p-4">
          <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2 flex items-center gap-1.5">
            <Hash size={11} /> Linked findings
            <span className="text-ink-mute font-normal">({bundle.findings.length})</span>
          </h4>
          {bundle.findings.length === 0 ? (
            <div className="text-xs text-ink-mute italic">No findings linked yet.</div>
          ) : (
            <ul className="flex flex-wrap gap-2">
              {bundle.findings.map((fid) => (
                <li key={fid}>
                  <Link
                    to={`/findings?id=${fid}`}
                    className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-md bg-slate-50 ring-1 ring-border hover:bg-slate-100"
                  >
                    <Hash size={10} className="text-ink-mute" />
                    <code className="font-mono">#{fid}</code>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Linked runs */}
        <section className="border border-border rounded-md bg-white p-4">
          <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2 flex items-center gap-1.5">
            <Sparkles size={11} /> Linked orchestration runs
            <span className="text-ink-mute font-normal">({bundle.runs.length})</span>
          </h4>
          {bundle.runs.length === 0 ? (
            <div className="text-xs text-ink-mute italic">No runs linked yet.</div>
          ) : (
            <ul className="flex flex-wrap gap-2">
              {bundle.runs.map((rid) => (
                <li key={rid}>
                  <Link
                    to={`/orchestrations?run=${rid}`}
                    className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-md bg-slate-50 ring-1 ring-border hover:bg-slate-100"
                  >
                    <Sparkles size={10} className="text-ink-mute" />
                    <code className="font-mono">#{rid}</code>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Notes */}
        <section className="border border-border rounded-md bg-white p-4">
          <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2 flex items-center gap-1.5">
            <MessageSquarePlus size={11} /> Notes
            <span className="text-ink-mute font-normal">({bundle.notes.length})</span>
          </h4>

          {/* Note composer — kept above the timeline for easy access. */}
          <div className="mb-4 space-y-2">
            <input
              type="text"
              value={noteAuthor}
              onChange={(e) => setNoteAuthor(e.target.value)}
              className="w-full px-2.5 py-1 rounded-md border border-border text-xs"
              placeholder="Author (defaults to 'operator')"
            />
            <textarea
              value={noteBody}
              onChange={(e) => setNoteBody(e.target.value)}
              rows={3}
              className="w-full px-2.5 py-1.5 rounded-md border border-border text-sm"
              placeholder="Add a note — observations, hypotheses, next steps…"
            />
            <div className="flex justify-end">
              <button
                onClick={addNote}
                disabled={noteBusy || !noteBody.trim()}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <MessageSquarePlus size={12} /> Add note
              </button>
            </div>
          </div>

          {bundle.notes.length === 0 ? (
            <div className="text-xs text-ink-mute italic">No notes yet — add the first one above.</div>
          ) : (
            <NotesList notes={bundle.notes} />
          )}
        </section>
      </div>

      {closeOpen && (
        <CloseDialog
          onClose={() => setCloseOpen(false)}
          onPick={(r) => closeCase(r)}
          busy={busy}
        />
      )}
    </div>
  );
}

function NotesList({ notes }: { notes: InvestigationNote[] }) {
  // Newest-first reads more like a case log; the API returns oldest-first.
  const ordered = useMemo(
    () => [...notes].sort((a, b) => (a.CreatedAt < b.CreatedAt ? 1 : -1)),
    [notes],
  );
  return (
    <ul className="space-y-3">
      {ordered.map((n) => (
        <li key={n.ID} className="border border-border rounded-md bg-slate-50/50 p-3">
          <div className="flex items-center gap-2 text-[11px] text-ink-mute mb-1.5">
            <code className="font-mono text-ink-dim">{n.Author || 'operator'}</code>
            <span>·</span>
            <Clock size={10} />
            <span className="font-mono">{fmtDate(n.CreatedAt)}</span>
          </div>
          <div className="text-sm whitespace-pre-wrap text-ink">{n.Body}</div>
        </li>
      ))}
    </ul>
  );
}

function CloseDialog({
  onClose,
  onPick,
  busy,
}: {
  onClose: () => void;
  onPick: (r: Resolution) => void;
  busy: boolean;
}) {
  return (
    <div className="fixed inset-0 z-50 bg-black/30 flex items-center justify-center" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-lg shadow-lg w-[420px] max-w-full"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-4 py-3 border-b border-border">
          <h3 className="text-sm font-semibold">Close investigation</h3>
          <p className="text-xs text-ink-dim mt-0.5">Pick a resolution. The case can be reopened later.</p>
        </header>
        <div className="p-3 space-y-1.5">
          {RESOLUTION_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => onPick(opt.value)}
              disabled={busy}
              className={cn(
                'w-full inline-flex items-center gap-2 text-sm px-3 py-2 rounded-md ring-1 ring-border hover:bg-slate-50 text-left disabled:opacity-50',
                opt.tone,
              )}
            >
              <opt.icon size={14} />
              <span>{opt.label}</span>
            </button>
          ))}
        </div>
        <footer className="px-4 py-3 border-t border-border flex items-center justify-end">
          <button
            onClick={onClose}
            className="text-xs px-2.5 py-1.5 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
          >
            Cancel
          </button>
        </footer>
      </div>
    </div>
  );
}

function StatusChip({ status }: { status: Investigation['Status'] }) {
  const styles: Record<Investigation['Status'], string> = {
    active:   'text-brand-700 bg-brand-50 ring-brand-200',
    closed:   'text-green-700 bg-green-50 ring-green-200',
    archived: 'text-ink-mute bg-slate-100 ring-slate-200',
  };
  return (
    <span
      className={cn(
        'text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1',
        styles[status] ?? 'text-ink-mute bg-slate-100 ring-slate-200',
      )}
    >
      {status}
    </span>
  );
}

function ResolutionChip({ res }: { res: Investigation['Resolution'] }) {
  if (!res) return null;
  return (
    <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1 ring-slate-200 bg-slate-50 text-ink-dim">
      {res}
    </span>
  );
}

function KV({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[100px_1fr] gap-2">
      <dt className="text-ink-mute">{label}</dt>
      <dd className="text-ink truncate">{children}</dd>
    </div>
  );
}

function fmtDate(iso: string): string {
  if (!iso || isZeroTime(iso)) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString(undefined, {
    month: 'short', day: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

// Go's zero-value time encodes to this RFC3339 literal — used by the
// server when a case is still active (no closed_at). Treat as "unset".
function isZeroTime(iso: string): boolean {
  return iso === '' || iso === '0001-01-01T00:00:00Z';
}
