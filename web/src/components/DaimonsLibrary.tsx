// Daimon Library — manage long-form daimon definitions stored on the CP
// host filesystem (--daimon-files-dir). List on the left, raw markdown
// editor on the right. Save writes back to disk; delete removes the file
// (server blocks if the daimon name is still registered).
//
// MVP design: the editor is a textarea over the full markdown file
// (frontmatter + body). A structured form for the frontmatter could be a
// follow-up — for now operators familiar with the existing files prefer
// editing them directly.

import { useEffect, useState } from 'react';
import {
  AlertCircle,
  Clock,
  FileEdit,
  FilePlus,
  Loader2,
  RefreshCw,
  Save,
  Trash2,
  X,
} from 'lucide-react';
import { api, ApiError, type DaimonItem, type DaimonLibraryDetail, type DaimonLibraryItem } from '../api';
import { cn } from '../lib/cn';

const TEMPLATE = `---
# ── Identity ────────────────────────────────────────────────────────────────
name: NAME
description: >
  Short description of what this daimon does and how often it runs.

# ── Provider ─────────────────────────────────────────────────────────────────
provider: claude
model: claude-haiku-4-5-20251001
effort: low
maxTurns: 20

# ── Schedule ─────────────────────────────────────────────────────────────────
mode: daemon
interval: 5m
overlap: skip

# ── State ────────────────────────────────────────────────────────────────────
stateDir: /var/lib/okesu/NAME
dedupeTtl: 1h

# ── Tools ────────────────────────────────────────────────────────────────────
tools:
  - read_file
  - list_files
  - search

# ── Outputs ──────────────────────────────────────────────────────────────────
outputs:
  - kind: stdout
---

You are an agent that ...
`;

export default function DaimonsLibrary() {
  const [items, setItems] = useState<DaimonLibraryItem[] | null>(null);
  const [registered, setRegistered] = useState<DaimonItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ name: string; isNew: boolean } | null>(null);

  function refresh() {
    setError(null);
    api.daimonLibrary()
      .then(setItems)
      .catch((e) => setError(formatError(e)));
    // Pull the registered-daimon list in parallel so we can show
    // rollout drift ("8 of 10 on current") next to each library entry.
    api.daimons(1000, 0).then(setRegistered).catch(() => setRegistered([]));
  }

  useEffect(() => {
    refresh();
    // Poll moderately so a hot-reload propagation shows up as the daemons
    // update their reported hash.
    const t = setInterval(refresh, 15_000);
    return () => clearInterval(t);
  }, []);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel flex items-center justify-between">
        <div>
          <h2 className="text-base font-semibold flex items-center gap-2">
            <FileEdit size={16} className="text-brand-500" />
            Daimon Library
          </h2>
          <p className="text-xs text-ink-dim">
            Long-form definitions deployed to nodes. Edits land on disk in
            the configured <code className="bg-slate-100 px-1 rounded">--daimon-files-dir</code>.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={refresh}
            className="inline-flex items-center gap-1 text-[11px] text-ink-dim hover:text-ink px-2 py-1 rounded-md hover:bg-slate-100"
          >
            <RefreshCw size={11} /> refresh
          </button>
          <button
            onClick={() => setEditing({ name: '', isNew: true })}
            className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-3 py-1.5 rounded-md"
          >
            <FilePlus size={14} /> New daimon
          </button>
        </div>
      </header>

      <div className="flex-1 overflow-auto p-6">
        {error && (
          <div className="mb-4 text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md flex items-start gap-2">
            <AlertCircle size={14} className="mt-0.5 shrink-0" />
            {error}
          </div>
        )}

        {items === null && <p className="text-ink-mute">Loading…</p>}

        {items && items.length === 0 && !error && (
          <div className="text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <FileEdit size={32} className="mx-auto mb-2 opacity-40" />
            <p className="mb-1">No daimons defined yet.</p>
            <p className="text-xs">
              Click <strong>New daimon</strong> to author one — it'll be
              available to deploy on the Nodes page.
            </p>
          </div>
        )}

        {items && items.length > 0 && (
          <ul className="bg-panel border border-border rounded-xl shadow-card divide-y divide-border">
            {items.map((it) => {
              const rollout = computeRollout(it, registered);
              return (
              <li
                key={it.name}
                onClick={() => setEditing({ name: it.name, isNew: false })}
                className="px-4 py-3 hover:bg-slate-50/60 cursor-pointer flex items-start gap-3"
              >
                <div className="w-10 h-10 shrink-0 rounded-lg bg-gradient-to-br from-slate-700 to-slate-900 flex items-center justify-center text-white text-xs font-semibold">
                  {it.name.slice(0, 2).toUpperCase()}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-medium text-ink truncate">{it.name}</span>
                    {it.mode && (
                      <span className="text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-100 px-1.5 py-0.5 rounded">
                        {it.mode}
                      </span>
                    )}
                    {rollout && (
                      <span
                        title={
                          rollout.drifted > 0
                            ? `${rollout.drifted} daemon${rollout.drifted === 1 ? '' : 's'} still on a stale definition — they will hot-reload within ~60s`
                            : 'all running daemons are on the current definition'
                        }
                        className={cn(
                          'text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1 inline-flex items-center gap-1',
                          rollout.drifted === 0
                            ? 'text-green-700 bg-green-50 ring-green-200'
                            : 'text-yellow-700 bg-yellow-50 ring-yellow-200',
                        )}
                      >
                        {rollout.drifted === 0 ? '✓' : '⟳'}
                        {' '}
                        {rollout.synced}/{rollout.total} on current
                      </span>
                    )}
                  </div>
                  {it.description && (
                    <p className="text-xs text-ink-dim mt-0.5 line-clamp-2">{it.description}</p>
                  )}
                  <div className="text-[11px] text-ink-mute font-mono mt-1 flex items-center gap-3 flex-wrap">
                    {it.provider && <span>{it.provider}{it.model ? ` · ${it.model}` : ''}</span>}
                    {it.interval && <span>every {it.interval}</span>}
                    <span className="ml-auto inline-flex items-center gap-1">
                      <Clock size={10} /> {fmtRel(it.modified_at)}
                    </span>
                  </div>
                </div>
              </li>
              );
            })}
          </ul>
        )}
      </div>

      {editing && (
        <DaimonEditor
          name={editing.name}
          isNew={editing.isNew}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); refresh(); }}
          onDeleted={() => { setEditing(null); refresh(); }}
        />
      )}
    </div>
  );
}

function DaimonEditor({
  name, isNew, onClose, onSaved, onDeleted,
}: {
  name: string;
  isNew: boolean;
  onClose: () => void;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const [draftName, setDraftName] = useState(isNew ? '' : name);
  const [content, setContent] = useState<string | null>(isNew ? TEMPLATE : null);
  const [meta, setMeta] = useState<DaimonLibraryDetail | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (isNew) return;
    setError(null);
    api.daimonLibraryGet(name)
      .then((d) => { setContent(d.content); setMeta(d); })
      .catch((e) => setError(formatError(e)));
  }, [name, isNew]);

  async function save() {
    if (!content) return;
    const targetName = isNew ? draftName.trim() : name;
    if (!targetName) {
      setError('name required');
      return;
    }
    setBusy(true); setError(null);
    try {
      await api.daimonLibrarySave(targetName, content);
      onSaved();
    } catch (e) {
      setError(formatError(e));
    } finally {
      setBusy(false);
    }
  }

  async function del() {
    if (isNew) return;
    if (!confirm(`Delete daimon "${name}"? The .md file is removed from --daimon-files-dir. Already-deployed daimons keep running until uninstalled.`)) return;
    setBusy(true); setError(null);
    try {
      await api.daimonLibraryDelete(name);
      onDeleted();
    } catch (e) {
      setError(formatError(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <aside className="fixed inset-y-0 right-0 w-[720px] bg-panel border-l border-border shadow-card flex flex-col z-40">
      <header className="px-5 py-3 border-b border-border flex items-center justify-between">
        <div className="min-w-0 flex-1">
          {isNew ? (
            <div className="flex items-center gap-2">
              <span className="text-sm font-semibold">New daimon</span>
              <input
                value={draftName}
                onChange={(e) => setDraftName(e.target.value)}
                placeholder="name (a–z, 0–9, -, _)"
                className="text-sm px-2 py-1 border border-border rounded-md w-64 focus:outline-none focus:ring-2 focus:ring-brand-500/30"
              />
            </div>
          ) : (
            <>
              <h2 className="text-sm font-semibold">{name}</h2>
              {meta && (
                <p className="text-[11px] text-ink-mute font-mono mt-0.5">
                  {meta.size_bytes}B · last modified {fmtRel(meta.modified_at)}
                </p>
              )}
            </>
          )}
        </div>
        <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
          <X size={16} />
        </button>
      </header>

      <div className="flex-1 overflow-auto p-4 flex flex-col">
        {error && (
          <div className="mb-3 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md flex items-start gap-2">
            <AlertCircle size={12} className="mt-0.5 shrink-0" /> {error}
          </div>
        )}
        {content === null ? (
          <p className="text-ink-mute text-sm">Loading…</p>
        ) : (
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            spellCheck={false}
            className="flex-1 min-h-[480px] text-xs font-mono px-3 py-2 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 resize-y"
          />
        )}
      </div>

      <footer className="px-5 py-3 border-t border-border flex items-center justify-between gap-2">
        <div>
          {!isNew && (
            <button
              onClick={del}
              disabled={busy}
              className="inline-flex items-center gap-1 text-xs text-red-700 hover:text-red-800 hover:bg-red-50 px-2 py-1 rounded-md disabled:opacity-50"
            >
              <Trash2 size={12} /> Delete
            </button>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">
            Cancel
          </button>
          <button
            onClick={save}
            disabled={busy || content === null}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
            {busy ? 'Saving…' : 'Save'}
          </button>
        </div>
      </footer>
    </aside>
  );
}

// computeRollout returns synced/total counts for a library entry.
// "synced" = registered daimons whose hash matches the library hash
// AND whose heartbeat is fresh. Stale daemons (no recent heartbeat)
// don't count toward total — they're not actively running, so the
// rollout indicator shouldn't penalise the operator for their drift.
function computeRollout(
  lib: DaimonLibraryItem,
  registered: DaimonItem[],
): { total: number; synced: number; drifted: number } | null {
  if (!lib.hash) return null;
  const live = registered.filter((d) => d.name === lib.name && d.healthy);
  if (live.length === 0) return null;
  let synced = 0;
  for (const d of live) {
    if (d.current_definition_hash === lib.hash) synced++;
  }
  return { total: live.length, synced, drifted: live.length - synced };
}

function fmtRel(iso: string): string {
  const d = new Date(iso);
  const sec = Math.max(0, (Date.now() - d.getTime()) / 1000);
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return d.toLocaleDateString();
}

function formatError(e: unknown): string {
  if (e instanceof ApiError) return `${e.status} ${e.message}`;
  return String(e);
}

