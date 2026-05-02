// LabelStrip — tiny header-row badge that shows an entity's labels
// inline + an "Add label" button that scrolls to the lower-page
// Labels card. Drop into any detail-page header to make labels
// discoverable without forcing the operator to scroll.
//
// Reads via the same /api/labels/{kind}/{id_or_key} endpoint as
// LabelEditor; updates after a focus event so the strip stays fresh
// when the operator edits via the lower-page card.

import { useEffect, useState } from 'react';
import { Tag, Plus } from 'lucide-react';
import { api, type LabelKind } from '../../api';
import { LABELS_CHANGED_EVENT, type LabelsChangedDetail } from './labelsBus';

interface Props {
  kind: LabelKind;
  idOrKey: number | string;
  // Anchor id of the lower-page LabelEditor card to scroll into
  // view when the operator clicks "+ Add label". Defaults to
  // 'labels-card' which detail pages should set on the editor.
  scrollAnchor?: string;
  className?: string;
}

export function LabelStrip({ kind, idOrKey, scrollAnchor = 'labels-card', className = '' }: Props) {
  const [labels, setLabels] = useState<Record<string, string> | null>(null);

  function refresh() {
    api.labels(kind, idOrKey).then(setLabels).catch(() => setLabels({}));
  }
  useEffect(() => {
    refresh();
    // Refresh when window regains focus — picks up edits from
    // another tab without a manual refetch.
    function onFocus() { refresh(); }
    window.addEventListener('focus', onFocus);
    // Refresh on cross-component label mutations (LabelEditor
    // dispatches when add/delete completes). Fixes the case where
    // the operator adds a label via the lower-page editor and the
    // header strip stayed stuck on the previous chip set.
    function onLabelsChanged(e: Event) {
      const ev = e as CustomEvent<LabelsChangedDetail>;
      if (ev.detail?.kind === kind && String(ev.detail?.idOrKey) === String(idOrKey)) {
        refresh();
      }
    }
    window.addEventListener(LABELS_CHANGED_EVENT, onLabelsChanged);
    return () => {
      window.removeEventListener('focus', onFocus);
      window.removeEventListener(LABELS_CHANGED_EVENT, onLabelsChanged);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, idOrKey]);

  function jump() {
    const el = document.getElementById(scrollAnchor);
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' });
      // Pulse the card briefly so the operator sees where they
      // landed when the page is short and there's no visible scroll.
      el.classList.add('ring-2', 'ring-brand-300');
      setTimeout(() => el.classList.remove('ring-2', 'ring-brand-300'), 1200);
      // Try to focus the first input inside the card.
      const input = el.querySelector('input') as HTMLInputElement | null;
      input?.focus();
    }
  }

  const entries = labels ? Object.entries(labels).sort(([a], [b]) => a.localeCompare(b)) : [];

  return (
    <div className={`flex flex-wrap items-center gap-1 ${className}`}>
      {entries.length === 0 ? (
        <button
          type="button"
          onClick={jump}
          className="inline-flex items-center gap-1 text-[11px] text-ink-mute hover:text-brand-700 px-1.5 py-0.5 rounded border border-dashed border-border hover:border-brand-300"
          title="Scroll to the Labels card and start tagging this entity"
        >
          <Tag size={10} />
          <Plus size={9} />
          Add label
        </button>
      ) : (
        <>
          {entries.map(([k, v]) => (
            <span
              key={k}
              className="inline-flex items-center gap-1 text-[11px] bg-slate-100 text-slate-800 px-1.5 py-0.5 rounded font-mono"
            >
              <Tag size={9} />
              {k}{v ? '=' : ''}{v}
            </span>
          ))}
          <button
            type="button"
            onClick={jump}
            className="inline-flex items-center text-[11px] text-ink-mute hover:text-brand-700 px-1 py-0.5 rounded hover:bg-brand-50"
            title="Edit labels for this entity"
          >
            <Plus size={11} />
          </button>
        </>
      )}
    </div>
  );
}
