// SavedSearchesBar — operator-saved filter sets for the Findings
// page. Renders as a row of clickable pills (one per saved search)
// with a "Save current as…" button on the right.
//
// Click a pill → apply that search's config (caller's onApply handles
// state restoration). Long-press / kebab menu → rename, mark default,
// or delete. The default search loads automatically on page mount
// (handled by the parent's apply-on-mount effect).
//
// Concurrency note: save uses { create OR update } based on whether
// a search with the same name already exists. This avoids a UNIQUE
// constraint surprise when an operator types the same name twice;
// instead it's "save = upsert by name".

import { useState } from 'react';
import { ApiError, api, type SavedSearch } from '../api';
import {
  Bookmark,
  BookmarkCheck,
  Loader2,
  MoreVertical,
  Pencil,
  Star,
  Trash2,
  X,
} from 'lucide-react';

export function SavedSearchesBar({
  scope = 'findings',
  searches,
  currentConfig,
  activeID,
  onApply,
  onSearchesChange,
}: {
  scope?: string;
  searches: SavedSearch[];
  /** What the page would persist if "Save" is clicked right now. */
  currentConfig: object;
  /** ID of the search whose config matches currentConfig, if any.
   *  Drives the "Save / Update" button label and the active pill. */
  activeID: number | null;
  /** Apply a saved search's config to the page state. */
  onApply: (s: SavedSearch) => void;
  /** Refresh trigger — caller re-fetches the list. */
  onSearchesChange: () => void;
}) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<number | null>(null);
  const [renaming, setRenaming] = useState<SavedSearch | null>(null);

  async function saveCurrent() {
    const name = (window.prompt('Name this search:') ?? '').trim();
    if (!name) return;
    setSaving(true); setError(null);
    try {
      // Upsert-by-name: if a search with this name exists, patch it.
      const existing = searches.find((s) => s.name === name);
      if (existing) {
        await api.savedSearches.update(existing.id, { config: currentConfig });
      } else {
        await api.savedSearches.create({ name, scope, config: currentConfig });
      }
      onSearchesChange();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function updateCurrent(s: SavedSearch) {
    setSaving(true); setError(null);
    try {
      await api.savedSearches.update(s.id, { config: currentConfig });
      onSearchesChange();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function setDefault(s: SavedSearch, on: boolean) {
    setSaving(true); setError(null);
    try {
      await api.savedSearches.update(s.id, { is_default: on });
      onSearchesChange();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false); setMenuFor(null);
    }
  }

  async function rename(s: SavedSearch) {
    setRenaming(s);
    setMenuFor(null);
  }

  async function commitRename(s: SavedSearch, newName: string) {
    if (!newName.trim() || newName === s.name) {
      setRenaming(null);
      return;
    }
    setSaving(true); setError(null);
    try {
      await api.savedSearches.update(s.id, { name: newName.trim() });
      onSearchesChange();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false); setRenaming(null);
    }
  }

  async function destroy(s: SavedSearch) {
    if (!window.confirm(`Delete saved search "${s.name}"?`)) return;
    setSaving(true); setError(null);
    try {
      await api.savedSearches.delete(s.id);
      onSearchesChange();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false); setMenuFor(null);
    }
  }

  return (
    <div className="flex items-center gap-2 flex-wrap">
      {searches.map((s) => {
        const isActive = activeID === s.id;
        const isRenaming = renaming?.id === s.id;
        return (
          <div key={s.id} className="relative inline-flex items-center">
            {isRenaming ? (
              <input
                autoFocus
                defaultValue={s.name}
                onBlur={(e) => commitRename(s, e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commitRename(s, (e.target as HTMLInputElement).value);
                  if (e.key === 'Escape') setRenaming(null);
                }}
                className="text-xs px-2 py-1 rounded-md border border-brand-300 outline-none focus:ring-2 focus:ring-brand-500/30"
                style={{ minWidth: 120 }}
              />
            ) : (
              <button
                onClick={() => onApply(s)}
                className={[
                  'text-xs px-2.5 py-1 rounded-md inline-flex items-center gap-1.5 border',
                  isActive
                    ? 'bg-brand-600 text-white border-brand-600'
                    : 'bg-white text-ink hover:bg-slate-50 border-border',
                ].join(' ')}
                title={s.name}
              >
                {s.is_default
                  ? <BookmarkCheck size={11} className={isActive ? 'text-white' : 'text-amber-600'} />
                  : <Bookmark size={11} className={isActive ? 'text-white/80' : 'text-ink-mute'} />
                }
                <span className="max-w-[180px] truncate">{s.name}</span>
                {s.is_default && (
                  <span className={`text-[10px] uppercase tracking-wide font-semibold ${isActive ? 'text-white/80' : 'text-amber-700'}`}>
                    default
                  </span>
                )}
              </button>
            )}
            {!isRenaming && (
              <button
                onClick={(e) => { e.stopPropagation(); setMenuFor(menuFor === s.id ? null : s.id); }}
                className="ml-0.5 p-1 text-ink-mute hover:text-ink rounded-md"
                title="Search options"
              >
                <MoreVertical size={11} />
              </button>
            )}
            {menuFor === s.id && (
              <div
                className="absolute top-full left-0 mt-1 z-30 bg-white border border-border rounded-md shadow-md py-1 w-48"
                onClick={(e) => e.stopPropagation()}
              >
                <MenuItem icon={Pencil} onClick={() => rename(s)} label="Rename" />
                {isActive ? (
                  <MenuItem
                    icon={BookmarkCheck}
                    onClick={() => updateCurrent(s)}
                    label="Update with current filters"
                  />
                ) : null}
                {s.is_default ? (
                  <MenuItem icon={Star} onClick={() => setDefault(s, false)} label="Unset as default" />
                ) : (
                  <MenuItem icon={Star} onClick={() => setDefault(s, true)} label="Set as default" />
                )}
                <MenuItem icon={Trash2} onClick={() => destroy(s)} label="Delete" tone="danger" />
              </div>
            )}
          </div>
        );
      })}
      <button
        onClick={saveCurrent}
        disabled={saving}
        className="text-xs px-2.5 py-1 rounded-md border border-dashed border-border text-ink-dim hover:bg-slate-50 disabled:opacity-50 inline-flex items-center gap-1"
        title="Save current filters as a new search"
      >
        {saving ? <Loader2 size={11} className="animate-spin" /> : <Bookmark size={11} />}
        Save current
      </button>
      {error && (
        <span className="text-[11px] text-red-700 inline-flex items-center gap-1">
          {error}
          <button onClick={() => setError(null)} className="hover:bg-red-50 rounded p-0.5">
            <X size={10} />
          </button>
        </span>
      )}
    </div>
  );
}

function MenuItem({
  icon: Icon,
  label,
  onClick,
  tone,
}: {
  icon: typeof Pencil;
  label: string;
  onClick: () => void;
  tone?: 'danger';
}) {
  const cls = tone === 'danger'
    ? 'text-red-700 hover:bg-red-50'
    : 'text-ink hover:bg-slate-50';
  return (
    <button
      onClick={onClick}
      className={`w-full text-left px-3 py-1.5 text-xs inline-flex items-center gap-2 ${cls}`}
    >
      <Icon size={11} /> {label}
    </button>
  );
}
