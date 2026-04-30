// Investigations — T2 case workspace landing. Lists every case the
// operator has opened, filterable by lifecycle status. Phase 22.3.
//
// Detail view (per-case findings/runs/notes) lives in
// InvestigationDetail.tsx; this page is purely the index.

import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ClipboardList, Loader2, Sparkles } from 'lucide-react';
import { api, ApiError, type Investigation } from '../api';
import { cn } from '../lib/cn';

type StatusFilter = '' | 'active' | 'closed' | 'archived';

const STATUS_OPTIONS: { value: StatusFilter; label: string }[] = [
  { value: '',         label: 'All' },
  { value: 'active',   label: 'Active' },
  { value: 'closed',   label: 'Closed' },
  { value: 'archived', label: 'Archived' },
];

export default function InvestigationsPage() {
  const [items, setItems] = useState<Investigation[] | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('');
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const reload = () => {
    setError(null);
    api.investigations
      .list(statusFilter || undefined)
      .then(setItems)
      .catch((e) => {
        setItems([]);
        if (e instanceof ApiError) setError(e.message);
        else setError(String(e));
      });
  };

  useEffect(() => {
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statusFilter]);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel flex items-start justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
              <ClipboardList size={14} />
            </span>
            Investigations
          </h1>
          <p className="text-xs text-ink-dim mt-0.5 ml-9">
            T2 case workspace — open a case from a finding, link more findings/runs as the
            investigation develops, and close with a resolution.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setCreateOpen(true)}
            className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700"
          >
            <Sparkles size={12} /> New investigation
          </button>
        </div>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-4">
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        <div className="flex items-center gap-2 text-[11px]">
          <span className="text-ink-mute">Status</span>
          <div className="inline-flex rounded-md ring-1 ring-border bg-white p-0.5">
            {STATUS_OPTIONS.map((opt) => (
              <button
                key={opt.value || 'all'}
                onClick={() => setStatusFilter(opt.value)}
                className={cn(
                  'px-2.5 py-1 rounded text-xs transition-colors',
                  statusFilter === opt.value
                    ? 'bg-brand-50 text-brand-700 font-medium'
                    : 'text-ink-dim hover:text-ink',
                )}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>

        {items === null ? (
          <div className="flex items-center gap-2 text-ink-dim text-xs">
            <Loader2 size={14} className="animate-spin" /> Loading investigations…
          </div>
        ) : items.length === 0 ? (
          <div className="text-sm text-ink-mute border border-border rounded-md p-6 text-center bg-slate-50/50">
            No investigations yet. Open one from any finding's drawer or use
            "New investigation" above.
          </div>
        ) : (
          <div className="border border-border rounded-md bg-white overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute bg-slate-50 border-b border-border">
                  <th className="px-3 py-2 font-medium">Title</th>
                  <th className="px-3 py-2 font-medium w-28">Status</th>
                  <th className="px-3 py-2 font-medium w-36">Resolution</th>
                  <th className="px-3 py-2 font-medium w-44">Updated</th>
                </tr>
              </thead>
              <tbody>
                {items.map((inv) => (
                  <tr key={inv.ID} className="border-b border-border last:border-b-0 hover:bg-slate-50/60">
                    <td className="px-3 py-2">
                      <Link
                        to={`/investigations/${inv.ID}`}
                        className="text-brand-700 hover:underline"
                      >
                        {inv.Title || '(untitled)'}
                      </Link>
                    </td>
                    <td className="px-3 py-2">
                      <StatusChip status={inv.Status} />
                    </td>
                    <td className="px-3 py-2 text-ink-dim text-xs">
                      {inv.Resolution || <span className="text-ink-mute">—</span>}
                    </td>
                    <td className="px-3 py-2 text-ink-dim text-xs font-mono">
                      {fmtDate(inv.UpdatedAt)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {createOpen && (
        <CreateInvestigationDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => {
            setCreateOpen(false);
            reload();
          }}
        />
      )}
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
        'text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1 inline-block',
        styles[status] ?? 'text-ink-mute bg-slate-100 ring-slate-200',
      )}
    >
      {status}
    </span>
  );
}

function fmtDate(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString(undefined, {
    month: 'short', day: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

interface CreateProps {
  onClose: () => void;
  onCreated: () => void;
}

function CreateInvestigationDialog({ onClose, onCreated }: CreateProps) {
  const [title, setTitle] = useState('');
  const [summary, setSummary] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    if (!title.trim()) { setError('Title is required.'); return; }
    setBusy(true); setError(null);
    try {
      await api.investigations.create({ title: title.trim(), summary: summary.trim() });
      onCreated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 bg-black/30 flex items-center justify-center" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-lg shadow-lg w-[480px] max-w-full"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-4 py-3 border-b border-border">
          <h3 className="text-sm font-semibold">New investigation</h3>
        </header>
        <div className="p-4 space-y-3 text-sm">
          <label className="block">
            <span className="text-xs text-ink-mute">Title</span>
            <input
              autoFocus
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              className="mt-1 w-full px-2.5 py-1.5 rounded-md border border-border bg-white text-sm"
              placeholder="Suspicious east-west traffic from app-7"
            />
          </label>
          <label className="block">
            <span className="text-xs text-ink-mute">Summary (optional)</span>
            <textarea
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
              rows={4}
              className="mt-1 w-full px-2.5 py-1.5 rounded-md border border-border bg-white text-sm"
              placeholder="Initial hypothesis or context for the case…"
            />
          </label>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1.5 rounded-md">
              {error}
            </div>
          )}
        </div>
        <footer className="px-4 py-3 border-t border-border flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="text-xs px-2.5 py-1.5 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
          >
            Cancel
          </button>
          <button
            onClick={submit}
            disabled={busy || !title.trim()}
            className="text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {busy ? 'Creating…' : 'Create'}
          </button>
        </footer>
      </div>
    </div>
  );
}
