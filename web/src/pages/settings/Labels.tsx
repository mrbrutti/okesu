// Settings → Labels — central admin view for the labels system.
//
// The per-entity LabelEditor is great for the "I'm looking at this
// node and want to tag it" flow, but it doesn't surface the
// fleet-wide picture. This page does:
//
//   - Lists every (target_kind, target, key, value) row in one
//     filterable table.
//   - Filters by kind, key, value (server-side query params).
//   - "Create label" wizard: pick kind → pick target (autocomplete
//     by name) → key/value → save. Uses the same /api/labels
//     endpoints the per-entity editor uses.
//   - Each row links to the entity it's attached to so operators
//     can drill in.
//   - Bulk delete from selection.

import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  AlertCircle,
  Plus,
  Tag,
  Trash2,
  X,
  RefreshCw,
} from 'lucide-react';
import {
  api,
  ApiError,
  ALL_LABEL_KINDS,
  type LabelKind,
  type LabelRow,
  type NodeItem,
  type DaimonItem,
  type Group,
  type Secret,
} from '../../api';

export default function LabelsSection() {
  const [rows, setRows] = useState<LabelRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);

  // Filters bound to local state (no URL sync — admin tool, not a
  // bookmarked surface).
  const [kindFilter, setKindFilter] = useState<LabelKind | ''>('');
  const [keyFilter, setKeyFilter] = useState('');
  const [valueFilter, setValueFilter] = useState('');

  // Selection for bulk delete.
  const [selected, setSelected] = useState<Set<number>>(new Set());

  function refresh() {
    setLoading(true);
    setError(null);
    api.allLabels({
      kind:  kindFilter || undefined,
      key:   keyFilter   || undefined,
      value: valueFilter || undefined,
    })
      .then(setRows)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)))
      .finally(() => setLoading(false));
  }
  useEffect(refresh, [kindFilter, keyFilter, valueFilter]);

  async function deleteOne(row: LabelRow) {
    const ident = row.target_id !== 0 ? row.target_id : row.target_key;
    try {
      await api.deleteLabel(row.target_kind, ident, row.key);
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  async function deleteSelected() {
    if (selected.size === 0) return;
    if (!confirm(`Delete ${selected.size} label${selected.size === 1 ? '' : 's'}? This cannot be undone.`)) return;
    const all = rows ?? [];
    const targets = all.filter((r) => selected.has(r.id));
    setLoading(true);
    setError(null);
    try {
      for (const r of targets) {
        const ident = r.target_id !== 0 ? r.target_id : r.target_key;
        await api.deleteLabel(r.target_kind, ident, r.key);
      }
      setSelected(new Set());
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  function toggle(id: number) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  }

  function toggleAll() {
    if (!rows) return;
    if (selected.size === rows.length) {
      setSelected(new Set());
    } else {
      setSelected(new Set(rows.map((r) => r.id)));
    }
  }

  // Group rows by (kind, key) for a small "what-keys-exist" summary
  // at the top — operators want to see "we use env=prod/staging/dev
  // on nodes" at a glance.
  const summary = useMemo(() => {
    const m = new Map<string, { kind: LabelKind; key: string; values: Set<string>; count: number }>();
    for (const r of rows ?? []) {
      const k = `${r.target_kind}::${r.key}`;
      let e = m.get(k);
      if (!e) {
        e = { kind: r.target_kind, key: r.key, values: new Set(), count: 0 };
        m.set(k, e);
      }
      e.values.add(r.value);
      e.count++;
    }
    return [...m.values()].sort((a, b) => b.count - a.count);
  }, [rows]);

  return (
    <div className="p-6 max-w-6xl mx-auto space-y-4">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold flex items-center gap-2">
            <Tag size={16} className="text-brand-500" />
            Labels
          </h2>
          <p className="text-xs text-ink-dim mt-1">
            Cross-entity label manager. Labels drive scoped roles, secret bindings,
            selector-based fan-out, and group dashboards. The per-entity editor on each
            detail page edits the same data.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={refresh}
            disabled={loading}
            className="text-xs px-2.5 py-1.5 rounded-md border border-border hover:bg-slate-50 inline-flex items-center gap-1 disabled:opacity-50"
            title="Refresh"
          >
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
            Refresh
          </button>
          <button
            onClick={() => setCreateOpen(true)}
            className="text-xs px-3 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 inline-flex items-center gap-1"
          >
            <Plus size={12} />
            Create label
          </button>
        </div>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md flex items-center gap-2">
          <AlertCircle size={12} /> {error}
        </div>
      )}

      {/* Summary cards: a quick "what keys exist" view */}
      {summary.length > 0 && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
          {summary.slice(0, 8).map((s) => (
            <button
              key={s.kind + ':' + s.key}
              onClick={() => { setKindFilter(s.kind); setKeyFilter(s.key); setValueFilter(''); }}
              className="text-left bg-panel border border-border rounded-md p-2.5 hover:bg-slate-50"
            >
              <div className="text-[10px] uppercase tracking-wide text-ink-mute">{s.kind}</div>
              <div className="font-mono text-xs text-ink mt-0.5">{s.key}</div>
              <div className="text-[11px] text-ink-dim mt-0.5">
                {[...s.values].slice(0, 3).join(', ')}{s.values.size > 3 ? `, +${s.values.size - 3}` : ''}
              </div>
              <div className="text-[10px] text-ink-mute mt-0.5">{s.count} row{s.count === 1 ? '' : 's'}</div>
            </button>
          ))}
        </div>
      )}

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-2 bg-panel border border-border rounded-md p-2.5">
        <select
          value={kindFilter}
          onChange={(e) => setKindFilter(e.target.value as LabelKind | '')}
          className="text-xs px-2 py-1 rounded border border-border bg-white"
        >
          <option value="">All kinds</option>
          {ALL_LABEL_KINDS.map((k) => (
            <option key={k} value={k}>{k}</option>
          ))}
        </select>
        <input
          type="search"
          value={keyFilter}
          onChange={(e) => setKeyFilter(e.target.value)}
          placeholder="key (e.g. env)"
          className="text-xs px-2 py-1 rounded border border-border font-mono w-32"
        />
        <input
          type="search"
          value={valueFilter}
          onChange={(e) => setValueFilter(e.target.value)}
          placeholder="value (e.g. prod)"
          className="text-xs px-2 py-1 rounded border border-border font-mono w-32"
        />
        {(kindFilter || keyFilter || valueFilter) && (
          <button
            onClick={() => { setKindFilter(''); setKeyFilter(''); setValueFilter(''); }}
            className="text-[11px] text-ink-mute hover:text-ink inline-flex items-center gap-1"
          >
            <X size={10} /> clear
          </button>
        )}
        <span className="ml-auto text-[11px] text-ink-mute">
          {rows ? `${rows.length} row${rows.length === 1 ? '' : 's'}` : ''}
        </span>
        {selected.size > 0 && (
          <button
            onClick={deleteSelected}
            disabled={loading}
            className="text-xs px-2.5 py-1 rounded-md ring-1 text-red-700 bg-red-50 ring-red-200 hover:bg-red-100 inline-flex items-center gap-1 disabled:opacity-50"
          >
            <Trash2 size={11} />
            Delete {selected.size}
          </button>
        )}
      </div>

      {/* Table */}
      <div className="border border-border rounded-md overflow-hidden bg-panel">
        <table className="w-full text-xs">
          <thead className="bg-slate-50">
            <tr>
              <th className="text-left px-2 py-1.5 border-b border-border w-6">
                <input
                  type="checkbox"
                  checked={!!rows && rows.length > 0 && selected.size === rows.length}
                  onChange={toggleAll}
                  aria-label="Select all"
                />
              </th>
              <th className="text-left px-2 py-1.5 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Kind</th>
              <th className="text-left px-2 py-1.5 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Target</th>
              <th className="text-left px-2 py-1.5 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Key</th>
              <th className="text-left px-2 py-1.5 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Value</th>
              <th className="text-left px-2 py-1.5 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Source</th>
              <th className="text-left px-2 py-1.5 border-b border-border text-[10px] uppercase tracking-wide text-ink-mute">Updated</th>
              <th className="px-2 py-1.5 border-b border-border w-6"></th>
            </tr>
          </thead>
          <tbody>
            {!rows ? (
              <tr><td colSpan={8} className="px-2 py-4 text-ink-mute italic">Loading…</td></tr>
            ) : rows.length === 0 ? (
              <tr><td colSpan={8} className="px-2 py-4 text-ink-mute italic">
                No labels match. Click <strong>Create label</strong> to add one,
                or open any entity's detail page and use the inline editor.
              </td></tr>
            ) : (
              rows.map((r) => (
                <tr key={r.id} className="hover:bg-slate-50/40">
                  <td className="px-2 py-1.5 border-b border-border">
                    <input
                      type="checkbox"
                      checked={selected.has(r.id)}
                      onChange={() => toggle(r.id)}
                    />
                  </td>
                  <td className="px-2 py-1.5 border-b border-border">
                    <span className="inline-flex items-center text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-slate-700 bg-slate-50 ring-slate-200">
                      {r.target_kind}
                    </span>
                  </td>
                  <td className="px-2 py-1.5 border-b border-border font-mono">
                    <TargetLink kind={r.target_kind} id={r.target_id} key_={r.target_key} />
                  </td>
                  <td className="px-2 py-1.5 border-b border-border font-mono">{r.key}</td>
                  <td className="px-2 py-1.5 border-b border-border font-mono">{r.value || <span className="text-ink-mute">—</span>}</td>
                  <td className="px-2 py-1.5 border-b border-border text-[10px] text-ink-mute uppercase tracking-wide">{r.source}</td>
                  <td className="px-2 py-1.5 border-b border-border text-ink-mute font-mono text-[11px]">
                    {r.updated_at?.replace('T', ' ').slice(0, 19) ?? ''}
                  </td>
                  <td className="px-2 py-1.5 border-b border-border">
                    <button
                      onClick={() => deleteOne(r)}
                      className="p-1 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded"
                      title="Delete this label"
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
        <CreateLabelDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => { setCreateOpen(false); refresh(); }}
        />
      )}
    </div>
  );
}

// TargetLink renders a target column cell with a clickthrough link
// to the right detail page per kind. Names + ids both surface so the
// operator sees what they're labelling at a glance.
function TargetLink({ kind, id, key_ }: { kind: LabelKind; id: number; key_: string }) {
  const ident = id !== 0 ? String(id) : key_;
  const display = ident.length > 32 ? ident.slice(0, 32) + '…' : ident;
  let href: string | null = null;
  switch (kind) {
    case 'node':          href = `/nodes/${id}`; break;
    case 'daimon':        href = `/daimons/${encodeURIComponent(key_.split('@')[0] ?? '')}`; break;
    case 'finding':       href = `/findings?id=${id}`; break;
    case 'investigation': href = `/investigations/${id}`; break;
    case 'orchestration': href = `/orchestrations?tab=library&id=${id}`; break;
    case 'run':           href = `/runs?id=${id}`; break;
    case 'cp':            href = `/federation`; break;
    case 'secret':        href = `/settings/credentials?id=${id}`; break;
    case 'group':         href = `/settings/groups?id=${id}`; break;
  }
  if (!href) return <span>{display}</span>;
  return (
    <Link to={href} className="text-brand-700 hover:underline" title={ident}>
      {display}
    </Link>
  );
}

// CreateLabelDialog walks the operator through (kind → target → key/value).
// Rather than reimplementing per-kind target pickers, we lean on the
// existing list APIs (nodes / daimons / groups / secrets) and offer a
// simple select dropdown of every visible target. Findings /
// investigations / runs / orchestrations / cps are entered by id since
// their list space is large; the per-entity editor on those pages is
// the better path for those kinds anyway.
function CreateLabelDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [kind, setKind] = useState<LabelKind>('node');
  const [targetID, setTargetID] = useState('');
  const [targetKey, setTargetKey] = useState('');
  const [labelKey, setLabelKey] = useState('');
  const [labelValue, setLabelValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Live target picker — fetched per-kind. Reset on kind change.
  const [nodes, setNodes] = useState<NodeItem[] | null>(null);
  const [daimons, setDaimons] = useState<DaimonItem[] | null>(null);
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [secrets, setSecrets] = useState<Secret[] | null>(null);
  useEffect(() => {
    setTargetID('');
    setTargetKey('');
    if (kind === 'node' && nodes === null) {
      api.nodes().then(setNodes).catch(() => setNodes([]));
    }
    if (kind === 'daimon' && daimons === null) {
      api.daimons(500).then(setDaimons).catch(() => setDaimons([]));
    }
    if (kind === 'group' && groups === null) {
      api.groups.list().then(setGroups).catch(() => setGroups([]));
    }
    if (kind === 'secret' && secrets === null) {
      api.secrets.list().then(setSecrets).catch(() => setSecrets([]));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind]);

  async function submit() {
    setBusy(true); setError(null);
    try {
      const ident = targetID || targetKey;
      if (!ident) { setError('pick a target'); return; }
      if (!labelKey.trim()) { setError('label key required'); return; }
      await api.setLabel(kind, ident, labelKey.trim(), labelValue.trim());
      onCreated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 z-50 flex items-center justify-center p-4" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold">Create label</h3>
          <button onClick={onClose} className="p-1 text-ink-mute hover:text-ink rounded-md"><X size={14} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <Field label="Kind">
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value as LabelKind)}
              className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
            >
              {ALL_LABEL_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
          </Field>

          <Field label="Target">
            {kind === 'node' && (
              <select
                value={targetID}
                onChange={(e) => setTargetID(e.target.value)}
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
              >
                <option value="">— pick a node —</option>
                {(nodes ?? []).map((n) => (
                  <option key={n.id} value={n.id}>{n.name} (#{n.id})</option>
                ))}
              </select>
            )}
            {kind === 'daimon' && (
              <select
                value={targetKey}
                onChange={(e) => setTargetKey(e.target.value)}
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
              >
                <option value="">— pick a daimon —</option>
                {(daimons ?? []).map((d) => (
                  <option key={`${d.name}@${d.host}`} value={`${d.name}@${d.host}`}>{d.name}@{d.host}</option>
                ))}
              </select>
            )}
            {kind === 'group' && (
              <select
                value={targetID}
                onChange={(e) => setTargetID(e.target.value)}
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
              >
                <option value="">— pick a group —</option>
                {(groups ?? []).map((g) => (
                  <option key={g.id} value={g.id}>{g.name} (#{g.id})</option>
                ))}
              </select>
            )}
            {kind === 'secret' && (
              <select
                value={targetID}
                onChange={(e) => setTargetID(e.target.value)}
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
              >
                <option value="">— pick a secret —</option>
                {(secrets ?? []).map((s) => (
                  <option key={s.id} value={s.id}>{s.name} (#{s.id})</option>
                ))}
              </select>
            )}
            {(kind === 'finding' || kind === 'investigation' || kind === 'run' || kind === 'orchestration') && (
              <input
                type="number"
                value={targetID}
                onChange={(e) => setTargetID(e.target.value)}
                placeholder={`${kind} id`}
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
              />
            )}
            {kind === 'cp' && (
              <input
                type="text"
                value={targetKey}
                onChange={(e) => setTargetKey(e.target.value)}
                placeholder="cp instance_id (UUID)"
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
              />
            )}
          </Field>

          <Field label="Key">
            <input
              type="text"
              value={labelKey}
              onChange={(e) => setLabelKey(e.target.value)}
              placeholder="e.g. env"
              className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
            />
          </Field>
          <Field label="Value">
            <input
              type="text"
              value={labelValue}
              onChange={(e) => setLabelValue(e.target.value)}
              placeholder="e.g. prod"
              className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
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
