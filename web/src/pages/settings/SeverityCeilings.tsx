// Settings → Severity rules — per-label severity ceilings.
//
// Operators write (selector → max_severity) rules to cap agent-
// assigned severities on non-prod hosts. Cleanest examples:
//   env=staging  → MEDIUM
//   env=dev      → LOW
//   env=lab      → INFO
//
// Evaluated at finding-ingest time alongside per-fingerprint
// severity rules (Phase 14). Ceilings only LOWER — agent INFO
// findings never get promoted because some ceiling allows MEDIUM.

import { useEffect, useState } from 'react';
import { AlertCircle, Plus, Trash2, X } from 'lucide-react';
import {
  api,
  ApiError,
  type SeverityCeiling,
} from '../../api';
import { SelectorInput } from '../../components/labels/SelectorInput';

const SEVS = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'] as const;

export default function SeverityCeilingsSection() {
  const [rows, setRows] = useState<SeverityCeiling[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  function refresh() {
    setError(null);
    api.severityCeilings()
      .then(setRows)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }
  useEffect(refresh, []);

  async function destroy(id: number) {
    if (!confirm('Delete this severity ceiling?')) return;
    try {
      await api.deleteSeverityCeiling(id);
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  async function setMax(id: number, max: string) {
    try {
      await api.updateSeverityCeiling(id, { max_severity: max });
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  return (
    <div className="p-6 max-w-4xl mx-auto space-y-4">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold flex items-center gap-2">
            Severity ceilings
          </h2>
          <p className="text-xs text-ink-dim mt-1">
            Cap agent-assigned severities for nodes whose labels match a selector.
            Useful for staging/dev/lab noise: <code className="font-mono bg-slate-100 px-1 rounded">env=staging → MEDIUM</code>.
            Ceilings only lower — they never promote.
          </p>
        </div>
        <button
          onClick={() => setCreateOpen(true)}
          className="text-xs px-3 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 inline-flex items-center gap-1"
        >
          <Plus size={12} />
          Add ceiling
        </button>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md flex items-center gap-2">
          <AlertCircle size={12} /> {error}
        </div>
      )}

      <div className="border border-border rounded-md overflow-hidden bg-panel">
        <table className="w-full text-xs">
          <thead className="bg-slate-50">
            <tr>
              <th className="text-left px-3 py-2 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Selector</th>
              <th className="text-left px-3 py-2 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Max severity</th>
              <th className="text-left px-3 py-2 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Reason</th>
              <th className="text-left px-3 py-2 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Updated</th>
              <th className="px-3 py-2 border-b border-border w-6"></th>
            </tr>
          </thead>
          <tbody>
            {!rows ? (
              <tr><td colSpan={5} className="px-3 py-4 text-ink-mute italic">Loading…</td></tr>
            ) : rows.length === 0 ? (
              <tr><td colSpan={5} className="px-3 py-4 text-ink-mute italic">
                No ceilings configured. Click <strong>Add ceiling</strong> to start.
              </td></tr>
            ) : (
              rows.map((r) => (
                <tr key={r.id} className="hover:bg-slate-50/40">
                  <td className="px-3 py-2 border-b border-border font-mono">{r.selector}</td>
                  <td className="px-3 py-2 border-b border-border">
                    <select
                      value={r.max_severity}
                      onChange={(e) => setMax(r.id, e.target.value)}
                      className={`text-[11px] px-1.5 py-0.5 rounded ring-1 font-medium uppercase tracking-wide bg-white border-0 severity-${r.max_severity.toLowerCase()}`}
                    >
                      {SEVS.map((s) => <option key={s} value={s}>{s}</option>)}
                    </select>
                  </td>
                  <td className="px-3 py-2 border-b border-border italic text-ink-dim">{r.reason || '—'}</td>
                  <td className="px-3 py-2 border-b border-border font-mono text-[11px] text-ink-mute">
                    {r.updated_at?.replace('T', ' ').slice(0, 19) ?? ''}
                  </td>
                  <td className="px-3 py-2 border-b border-border">
                    <button
                      onClick={() => destroy(r.id)}
                      className="p-1 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded"
                      title="Delete this ceiling"
                    >
                      <Trash2 size={11} />
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {createOpen && (
        <CreateCeilingDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => { setCreateOpen(false); refresh(); }}
        />
      )}
    </div>
  );
}

function CreateCeilingDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [selector, setSelector] = useState('');
  const [maxSev, setMaxSev] = useState<typeof SEVS[number]>('MEDIUM');
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    const sel = selector.trim();
    if (!sel) { setError('selector required'); return; }
    setBusy(true); setError(null);
    try {
      await api.createSeverityCeiling({ selector: sel, max_severity: maxSev, reason });
      onCreated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 z-50 flex items-center justify-center p-4" onClick={onClose}>
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-md" onClick={(e) => e.stopPropagation()}>
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold">Add severity ceiling</h3>
          <button onClick={onClose} className="p-1 text-ink-mute hover:text-ink rounded-md"><X size={14} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <Field label="Selector">
            <SelectorInput
              kind="node"
              value={selector}
              onChange={setSelector}
              placeholder="env=staging"
            />
            <p className="text-[11px] text-ink-mute mt-1">
              K8s-style label selector against host nodes. Use the same syntax the
              orchestrator's <code className="font-mono">nodes_selector</code> accepts.
            </p>
          </Field>
          <Field label="Max severity">
            <select
              value={maxSev}
              onChange={(e) => setMaxSev(e.target.value as typeof SEVS[number])}
              className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
            >
              {SEVS.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </Field>
          <Field label="Reason (optional)">
            <input
              type="text"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="why operators chose this cap"
              className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
            />
          </Field>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2 py-1 rounded">{error}</div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={busy}
            className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md font-medium hover:bg-brand-700 disabled:opacity-50 inline-flex items-center gap-1"
          >
            <Plus size={12} />
            {busy ? 'Saving…' : 'Create'}
          </button>
        </footer>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
    </div>
  );
}
