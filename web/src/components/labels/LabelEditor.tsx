// LabelEditor: full add/remove UI on top of the generic
// /api/labels/{kind}/{id_or_key} endpoint set. Drop-in for any
// entity detail page — node, daimon, finding, etc.
//
// Admin-only mutation; viewers see the chips but no add row + no
// remove buttons. The component reads readonly from the prop, not
// from RBAC introspection — pages know who's looking.

import { useEffect, useState } from 'react';
import { Plus, X, Tag } from 'lucide-react';
import { api, ApiError, type LabelKind } from '../../api';
import { notifyLabelsChanged } from './labelsBus';

interface Props {
  kind: LabelKind;
  // Numeric id for most kinds; string composite key for daimon =
  // "name@host" / federation peer = instance UUID.
  idOrKey: number | string;
  readonly?: boolean;
  // Callback invoked when labels change so parent pages can refresh
  // anything that depends on the label set (visibility, selector
  // matches, etc.).
  onChange?: () => void;
  className?: string;
}

export function LabelEditor({ kind, idOrKey, readonly, onChange, className = '' }: Props) {
  const [labels, setLabels] = useState<Record<string, string> | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [draftKey, setDraftKey] = useState('');
  const [draftValue, setDraftValue] = useState('');

  function refresh() {
    setError(null);
    api.labels(kind, idOrKey).then(setLabels).catch((e) => setError(String(e)));
  }
  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, idOrKey]);

  async function add() {
    const k = draftKey.trim();
    if (!k) return;
    setBusy(true); setError(null);
    try {
      await api.setLabel(kind, idOrKey, k, draftValue.trim());
      setDraftKey('');
      setDraftValue('');
      refresh();
      onChange?.();
      notifyLabelsChanged(kind, idOrKey);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(key: string) {
    setBusy(true); setError(null);
    try {
      await api.deleteLabel(kind, idOrKey, key);
      refresh();
      onChange?.();
      notifyLabelsChanged(kind, idOrKey);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // Listen for sibling LabelStrip / LabelEditor mutations so the
  // chip list re-renders without waiting for a parent reload tick.
  // Uses a window-level CustomEvent so any consumer of the same
  // (kind, idOrKey) stays in sync — fixes the "I added two labels
  // but only see one" bug where the header strip cached state and
  // the body editor didn't notify it.
  useEffect(() => {
    function handler(e: Event) {
      const ev = e as CustomEvent<{ kind: string; idOrKey: string | number }>;
      if (ev.detail?.kind === kind && String(ev.detail?.idOrKey) === String(idOrKey)) {
        refresh();
      }
    }
    window.addEventListener('okesu:labels:changed', handler);
    return () => window.removeEventListener('okesu:labels:changed', handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, idOrKey]);

  return (
    <div className={className}>
      {error && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2 py-1 rounded mb-2">
          {error}
        </div>
      )}
      {labels === null ? (
        <p className="text-xs text-ink-mute">Loading…</p>
      ) : Object.keys(labels).length === 0 ? (
        <p className="text-xs text-ink-mute italic mb-2">
          No labels yet. Labels drive scoped permissions, secret bindings, and selector-based fan-out.
        </p>
      ) : (
        <div className="flex flex-wrap gap-1.5 mb-3">
          {Object.entries(labels).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => (
            <span
              key={k}
              className="inline-flex items-center gap-1 text-xs bg-slate-100 text-slate-800 px-2 py-0.5 rounded font-mono"
            >
              <Tag size={10} />
              {k}{v ? '=' : ''}{v}
              {!readonly && (
                <button
                  onClick={() => remove(k)}
                  disabled={busy}
                  className="hover:text-red-600 disabled:opacity-50"
                  title="Remove label"
                  type="button"
                >
                  <X size={10} />
                </button>
              )}
            </span>
          ))}
        </div>
      )}
      {!readonly && (
        <div className="flex items-center gap-2">
          <input
            type="text"
            value={draftKey}
            onChange={(e) => setDraftKey(e.target.value)}
            placeholder="key (e.g. env)"
            className="text-xs px-2 py-1 rounded border border-border font-mono w-32"
          />
          <input
            type="text"
            value={draftValue}
            onChange={(e) => setDraftValue(e.target.value)}
            placeholder="value (e.g. prod)"
            onKeyDown={(e) => { if (e.key === 'Enter') add(); }}
            className="text-xs px-2 py-1 rounded border border-border font-mono flex-1 max-w-xs"
          />
          <button
            onClick={add}
            disabled={busy || !draftKey.trim()}
            className="inline-flex items-center gap-1 text-xs px-2.5 py-1 rounded-md bg-brand-50 text-brand-800 ring-1 ring-brand-200 hover:bg-brand-100 disabled:opacity-50"
            type="button"
          >
            <Plus size={11} />
            Add
          </button>
        </div>
      )}
    </div>
  );
}
