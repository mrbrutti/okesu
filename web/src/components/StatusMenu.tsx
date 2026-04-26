import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown } from 'lucide-react';
import { ALL_FINDING_STATUSES, type FindingStatus } from '../api';
import { STATUS_STYLES } from './StatusPill';
import { cn } from '../lib/cn';

// Triage status picker. Used in both the per-finding drawer (large variant
// with note input) and in the grouped-row inline menu (compact variant).
//
// The menu is rendered through a portal anchored to document.body so it can
// escape any clipping ancestor (drawer overflow:hidden, list-card border).
// Position is computed against the trigger's bounding rect, with simple
// viewport-aware flipping:
//
//   - Default opens BELOW + RIGHT-aligned to the trigger.
//   - If the menu would fall below the viewport, opens ABOVE.
//   - If the menu would clip the right edge, switches to LEFT-aligned.
//   - Re-measures on scroll/resize.
//
// Width is auto with a sensible min so all six status labels fit on one
// line each ("False positive" is the longest — needs ~150px including the
// icon and "current" badge).

const MENU_MIN_WIDTH = 240; // px — fits longest label + icon + "current" tag
const NOTE_MENU_WIDTH = 320;

export function StatusMenu({
  current,
  onPick,
  compact = false,
  withNote = false,
  busy = false,
}: {
  current: FindingStatus;
  onPick: (status: FindingStatus, note: string) => void | Promise<void>;
  /** Compact = smaller pill-trigger style for inline list rows. */
  compact?: boolean;
  /** Prompt for an optional note before applying any non-`open` status. */
  withNote?: boolean;
  busy?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState<FindingStatus | null>(null);
  const [note, setNote] = useState('');
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);

  // Close on outside click / Escape.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (
        triggerRef.current && !triggerRef.current.contains(e.target as Node) &&
        menuRef.current && !menuRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
        setPending(null);
        setNote('');
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false);
        setPending(null);
        setNote('');
      }
    };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Position the menu against the trigger, flipping when the viewport is tight.
  useLayoutEffect(() => {
    if (!open || !triggerRef.current) return;
    const update = () => {
      const t = triggerRef.current?.getBoundingClientRect();
      if (!t) return;
      const margin = 6;
      const desiredW = pending ? NOTE_MENU_WIDTH : MENU_MIN_WIDTH;
      const desiredH = pending ? 180 : 36 * ALL_FINDING_STATUSES.length + 12;

      // Horizontal: prefer right-aligned to the trigger; flip to left-aligned
      // if it would clip the right viewport edge.
      let left = t.right - desiredW;
      if (left < margin) {
        left = Math.min(t.left, window.innerWidth - desiredW - margin);
      }
      left = Math.max(margin, left);

      // Vertical: prefer below; flip above if no room.
      let top = t.bottom + 4;
      if (top + desiredH + margin > window.innerHeight) {
        const above = t.top - desiredH - 4;
        if (above >= margin) {
          top = above;
        } else {
          // Neither side fits cleanly — clamp into the viewport so the user
          // can still scroll inside the menu.
          top = Math.max(margin, window.innerHeight - desiredH - margin);
        }
      }
      setPos({ top, left });
    };
    update();
    window.addEventListener('resize', update);
    window.addEventListener('scroll', update, true);
    return () => {
      window.removeEventListener('resize', update);
      window.removeEventListener('scroll', update, true);
    };
  }, [open, pending]);

  const cur = STATUS_STYLES[current];
  const CurIcon = cur.icon;

  async function commit(status: FindingStatus) {
    if (withNote && status !== 'open') {
      setPending(status);
      return;
    }
    await onPick(status, '');
    setOpen(false);
  }

  async function commitWithNote() {
    if (!pending) return;
    await onPick(pending, note);
    setPending(null);
    setNote('');
    setOpen(false);
  }

  return (
    <>
      <button
        ref={triggerRef}
        onClick={(e) => {
          e.stopPropagation();
          e.preventDefault();
          setOpen((v) => !v);
          if (open) {
            setPending(null);
            setNote('');
          }
        }}
        disabled={busy}
        className={cn(
          'inline-flex items-center gap-1 rounded ring-1 font-medium transition-colors',
          'hover:brightness-95',
          cur.cls,
          compact
            ? 'text-[10px] uppercase tracking-wide px-1.5 py-0.5'
            : 'text-[11px] px-2 py-0.5',
          busy && 'opacity-50',
        )}
        title={`Current status: ${cur.label}`}
      >
        <CurIcon size={compact ? 9 : 11} />
        {cur.label}
        <ChevronDown size={compact ? 9 : 11} className="opacity-60" />
      </button>

      {open && pos && createPortal(
        <div
          ref={menuRef}
          onClick={(e) => { e.stopPropagation(); e.preventDefault(); }}
          style={{
            position: 'fixed',
            top: pos.top,
            left: pos.left,
            width: pending ? NOTE_MENU_WIDTH : MENU_MIN_WIDTH,
            zIndex: 9999,
          }}
          className="bg-panel border border-border rounded-md shadow-lg overflow-hidden"
        >
          {pending == null ? (
            <ul className="py-1">
              {ALL_FINDING_STATUSES.map((s) => {
                const st = STATUS_STYLES[s];
                const Icon = st.icon;
                return (
                  <li key={s}>
                    <button
                      onClick={(e) => { e.stopPropagation(); e.preventDefault(); commit(s); }}
                      className={cn(
                        'w-full text-left px-3 py-2 text-xs flex items-center gap-2 hover:bg-slate-50',
                        s === current && 'bg-slate-50 font-medium',
                      )}
                    >
                      <Icon size={13} className={cn(st.dot.replace('bg-', 'text-'), 'shrink-0')} />
                      <span className="flex-1 truncate">{st.label}</span>
                      {s === current && (
                        <span className="text-[10px] text-ink-mute shrink-0">current</span>
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <div className="p-3">
              <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">
                Mark as {STATUS_STYLES[pending].label.toLowerCase()}
              </div>
              <textarea
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="Optional note for the audit log + daemon context"
                rows={3}
                autoFocus
                className="w-full text-xs px-2 py-1 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
              />
              <div className="flex justify-end gap-2 mt-2">
                <button
                  onClick={(e) => { e.stopPropagation(); e.preventDefault(); setPending(null); setNote(''); }}
                  className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-50"
                >
                  Cancel
                </button>
                <button
                  onClick={(e) => { e.stopPropagation(); e.preventDefault(); commitWithNote(); }}
                  disabled={busy}
                  className="text-xs px-2 py-1 bg-brand-500 hover:bg-brand-600 text-white rounded-md disabled:opacity-50"
                >
                  Apply
                </button>
              </div>
            </div>
          )}
        </div>,
        document.body,
      )}
    </>
  );
}
