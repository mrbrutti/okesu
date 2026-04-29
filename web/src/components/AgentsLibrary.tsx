// Agents page — the *short-form* Claude/Codex agent library. Agents are
// one-off invocation definitions (Run picker reads from here). They are
// distinct from Daimons (long-form, scheduled, deployed via Nodes).
//
// Sourced from ~/.claude/agents, ~/.codex/agents, and any
// --agent-files-dir overrides; the table shows which directory each
// file lives in so operators can tell apart shipped templates from
// CP-managed ones.

import { useEffect, useState } from 'react';
import {
  AlertCircle,
  Clock,
  FilePlus,
  Folder,
  Loader2,
  RefreshCw,
  Save,
  Sparkles,
  Trash2,
  X,
} from 'lucide-react';
import {
  api,
  ApiError,
  type AgentLibraryDetail,
  type AgentLibraryItem,
} from '../api';
import MarkdownEditor from './LazyMarkdownEditor';

const TEMPLATE = `---
name: NAME
description: One-line description.
model: claude-haiku-4-5-20251001
provider: claude
tools: [bash, read_file, write_file, list_files, search]
maxTurns: 50
effort: medium
---

You are an agent that ...
`;

export default function AgentsLibrary() {
  const [items, setItems] = useState<AgentLibraryItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ name: string; isNew: boolean } | null>(null);

  function refresh() {
    setError(null);
    api.agentLibrary()
      .then(setItems)
      .catch((e) => setError(formatError(e)));
  }

  useEffect(() => {
    refresh();
  }, []);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <Sparkles size={18} className="text-brand-500" />
            Agents
          </h1>
          <p className="text-xs text-ink-dim">
            Short-form agent definitions used for one-off Runs (Claude
            Code / Codex format). Sourced from{' '}
            <code className="bg-slate-100 px-1 rounded">~/.claude/agents</code>,{' '}
            <code className="bg-slate-100 px-1 rounded">~/.codex/agents</code>, and any{' '}
            <code className="bg-slate-100 px-1 rounded">--agent-files-dir</code> override.
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
            <FilePlus size={14} /> New agent
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
            <Sparkles size={32} className="mx-auto mb-2 opacity-40" />
            <p className="mb-1">No agents found in any search dir.</p>
            <p className="text-xs">
              Click <strong>New agent</strong> to create one — it'll be
              written to the first writable search dir.
            </p>
          </div>
        )}

        {items && items.length > 0 && (
          <ul className="bg-panel border border-border rounded-xl shadow-card divide-y divide-border">
            {items.map((it) => (
              <li
                key={it.name}
                onClick={() => setEditing({ name: it.name, isNew: false })}
                className="px-4 py-3 hover:bg-slate-50/60 cursor-pointer flex items-start gap-3"
              >
                <div className="w-10 h-10 shrink-0 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white text-xs font-semibold">
                  {it.name.slice(0, 2).toUpperCase()}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-medium text-ink truncate">{it.name}</span>
                    {it.provider && (
                      <span className="text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-100 px-1.5 py-0.5 rounded">
                        {it.provider}
                      </span>
                    )}
                  </div>
                  {it.description && (
                    <p className="text-xs text-ink-dim mt-0.5 line-clamp-2">{it.description}</p>
                  )}
                  <div className="text-[11px] text-ink-mute font-mono mt-1 flex items-center gap-3 flex-wrap">
                    {it.model && <span>{it.model}</span>}
                    {it.max_turns !== undefined && it.max_turns > 0 && (
                      <span>max {it.max_turns} turns</span>
                    )}
                    {it.effort && <span>effort {it.effort}</span>}
                    <span className="inline-flex items-center gap-1">
                      <Folder size={10} /> {it.dir}
                    </span>
                    <span className="ml-auto inline-flex items-center gap-1">
                      <Clock size={10} /> {fmtRel(it.modified_at)}
                    </span>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>

      {editing && (
        <AgentEditor
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

function AgentEditor({
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
  const [meta, setMeta] = useState<AgentLibraryDetail | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (isNew) return;
    setError(null);
    api.agentLibraryGet(name)
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
      await api.agentLibrarySave(targetName, content);
      onSaved();
    } catch (e) {
      setError(formatError(e));
    } finally {
      setBusy(false);
    }
  }

  async function del() {
    if (isNew) return;
    if (!confirm(`Delete agent "${name}"? Removes the .md file from its source dir.`)) return;
    setBusy(true); setError(null);
    try {
      await api.agentLibraryDelete(name);
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
              <span className="text-sm font-semibold">New agent</span>
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
                  {meta.dir} · {meta.size_bytes}B · last modified {fmtRel(meta.modified_at)}
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
          <div className="flex-1 min-h-[480px] border border-border rounded-md focus-within:ring-2 focus-within:ring-brand-500/30 overflow-hidden">
            <MarkdownEditor
              value={content}
              onChange={setContent}
              ariaLabel="Agent system prompt"
            />
          </div>
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

