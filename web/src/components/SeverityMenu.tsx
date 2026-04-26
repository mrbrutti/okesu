import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown, RotateCcw } from 'lucide-react';
import { ALL_SEVERITIES, type Severity } from '../api';
import { cn } from '../lib/cn';

// Severity override picker. Models the StatusMenu — portal-rendered
// against document.body, viewport-aware flipping, optional note input
// when the operator wants to apply the override fleet-wide
// (per-fingerprint rule) so the LLM picks it up via lookup_findings.
//
// The trigger renders the EFFECTIVE severity. When an override is in
// effect, a small "edited" affordance appears so the operator can revert.

const MENU_WIDTH = 280;
const APPLY_MENU_WIDTH = 340;

export interface SeverityChange {
  severity: '' | Severity; // "" reverts to the agent's original
  applyToFingerprint: boolean;
  applyToGroup: boolean;
  note: string;
}

const SEVERITY_STYLES: Record<Severity, { label: string; cls: string; dot: string }> = {
  CRITICAL: { label: 'Critical', cls: 'severity-critical text-sev-critical', dot: 'bg-sev-critical' },
  HIGH:     { label: 'High',     cls: 'severity-high     text-sev-high',     dot: 'bg-sev-high' },
  MEDIUM:   { label: 'Medium',   cls: 'severity-medium   text-sev-medium',   dot: 'bg-sev-medium' },
  LOW:      { label: 'Low',      cls: 'severity-low      text-sev-low',      dot: 'bg-sev-low' },
  INFO:     { label: 'Info',     cls: 'severity-info     text-sev-info',     dot: 'bg-sev-info' },
};

export function SeverityMenu({
  current,
  original,
  onPick,
  busy = false,
  compact = false,
}: {
  /** Current effective severity displayed on the trigger. */
  current: Severity | string | undefined;
  /** Agent's original assignment — used to render "originally X" hint
   *  and enable the revert action when current ≠ original. */
  original?: string;
  onPick: (change: SeverityChange) => void | Promise<void>;
  busy?: boolean;
  compact?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [pendingSev, setPendingSev] = useState<'' | Severity | null>(null);
  // Single "save as rule" toggle. When ticked, the override applies to
  // every existing finding sharing the fingerprint AND persists as a rule
  // so future occurrences inherit it. Two server-side flags kept distinct
  // so audit logs can attribute each effect — but the UI surfaces one
  // intent because the dominant operator goal is "fix this everywhere".
  const [saveAsRule, setSaveAsRule] = useState(false);
  const [note, setNote] = useState('');
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);

  const overridden = !!original && !!current && original.toUpperCase() !== String(current).toUpperCase();
  const sevKey = (current ? String(current).toUpperCase() : 'INFO') as Severity;
  const cur = SEVERITY_STYLES[sevKey] ?? SEVERITY_STYLES.INFO;

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (
        triggerRef.current && !triggerRef.current.contains(e.target as Node) &&
        menuRef.current && !menuRef.current.contains(e.target as Node)
      ) {
        reset();
      }
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') reset(); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open || !triggerRef.current) return;
    const update = () => {
      const t = triggerRef.current?.getBoundingClientRect();
      if (!t) return;
      const margin = 6;
      const desiredW = pendingSev !== null ? APPLY_MENU_WIDTH : MENU_WIDTH;
      const desiredH = pendingSev !== null ? 240 : 36 * (ALL_SEVERITIES.length + (overridden ? 1 : 0)) + 12;

      let left = t.right - desiredW;
      if (left < margin) left = Math.min(t.left, window.innerWidth - desiredW - margin);
      left = Math.max(margin, left);

      let top = t.bottom + 4;
      if (top + desiredH + margin > window.innerHeight) {
        const above = t.top - desiredH - 4;
        top = above >= margin ? above : Math.max(margin, window.innerHeight - desiredH - margin);
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
  }, [open, pendingSev, overridden]);

  function reset() {
    setOpen(false);
    setPendingSev(null);
    setSaveAsRule(false);
    setNote('');
  }

  async function pick(sev: '' | Severity) {
    // Reverting (severity = '') doesn't need an apply step; commit directly.
    if (sev === '') {
      await onPick({ severity: '', applyToFingerprint: false, applyToGroup: false, note: '' });
      reset();
      return;
    }
    setPendingSev(sev);
  }

  async function commit() {
    if (pendingSev === null) return;
    await onPick({
      severity: pendingSev,
      // "Save as rule" implies both: persist the rule for future findings
      // AND retroactively rewrite existing matches in the same transaction.
      // Single toggle, dual server-side effect — see the comment above.
      applyToFingerprint: saveAsRule,
      applyToGroup: saveAsRule,
      note,
    });
    reset();
  }

  return (
    <>
      <button
        ref={triggerRef}
        onClick={(e) => {
          e.stopPropagation();
          e.preventDefault();
          if (open) reset(); else setOpen(true);
        }}
        disabled={busy}
        className={cn(
          'inline-flex items-center gap-1.5 rounded ring-1 font-medium transition-colors hover:brightness-95',
          'severity-badge',
          cur.cls,
          compact
            ? 'text-[10px] uppercase tracking-wide px-1.5 py-0.5'
            : 'text-[11px] px-2 py-0.5',
          busy && 'opacity-50',
        )}
        title={
          overridden
            ? `Effective severity: ${cur.label} (operator override; agent originally said ${original})`
            : `Severity: ${cur.label}`
        }
      >
        {cur.label.toUpperCase()}
        {overridden && <span className="text-[8px] opacity-70">·edited</span>}
        <ChevronDown size={compact ? 9 : 11} className="opacity-70" />
      </button>

      {open && pos && createPortal(
        <div
          ref={menuRef}
          onClick={(e) => { e.stopPropagation(); e.preventDefault(); }}
          style={{
            position: 'fixed',
            top: pos.top,
            left: pos.left,
            width: pendingSev !== null ? APPLY_MENU_WIDTH : MENU_WIDTH,
            zIndex: 9999,
          }}
          className="bg-panel border border-border rounded-md shadow-lg overflow-hidden"
        >
          {pendingSev === null ? (
            <ul className="py-1">
              {ALL_SEVERITIES.map((s) => {
                const st = SEVERITY_STYLES[s];
                return (
                  <li key={s}>
                    <button
                      onClick={(e) => { e.stopPropagation(); e.preventDefault(); pick(s); }}
                      className={cn(
                        'w-full text-left px-3 py-2 text-xs flex items-center gap-2 hover:bg-slate-50',
                        sevKey === s && 'bg-slate-50 font-medium',
                      )}
                    >
                      <span className={cn('inline-block w-2 h-2 rounded-full', st.dot)} />
                      <span className="flex-1 truncate">{st.label}</span>
                      {sevKey === s && <span className="text-[10px] text-ink-mute shrink-0">current</span>}
                    </button>
                  </li>
                );
              })}
              {overridden && (
                <li className="border-t border-border mt-1 pt-1">
                  <button
                    onClick={(e) => { e.stopPropagation(); e.preventDefault(); pick(''); }}
                    className="w-full text-left px-3 py-2 text-xs flex items-center gap-2 hover:bg-slate-50 text-ink-dim"
                  >
                    <RotateCcw size={11} className="shrink-0" />
                    <span className="flex-1 truncate">Revert to original ({original})</span>
                  </button>
                </li>
              )}
            </ul>
          ) : (
            <div className="p-3">
              <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-2">
                Override severity → {SEVERITY_STYLES[pendingSev as Severity].label}
              </div>
              <label className="flex items-start gap-2 text-xs cursor-pointer hover:bg-slate-50/40 -mx-1 px-1 py-1 rounded">
                <input
                  type="checkbox"
                  checked={saveAsRule}
                  onChange={(e) => setSaveAsRule(e.target.checked)}
                  className="mt-0.5 rounded border-border text-brand-500"
                />
                <span>
                  <span className="font-medium">Save as a rule for this fingerprint</span>
                  <span className="block text-[11px] text-ink-mute mt-0.5">
                    Applies the override to every existing finding with this
                    fingerprint AND every future occurrence. Agents see the
                    rule via <code className="bg-slate-100 px-1 rounded">lookup_findings</code> and
                    self-calibrate. Without this, only the current row changes.
                  </span>
                </span>
              </label>
              <textarea
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="Optional note (visible in audit log + the rule's why)"
                rows={2}
                className="w-full text-xs px-2 py-1.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 mt-2"
              />
              <div className="flex justify-end gap-2 mt-2">
                <button
                  onClick={(e) => { e.stopPropagation(); e.preventDefault(); reset(); }}
                  className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-50"
                >
                  Cancel
                </button>
                <button
                  onClick={(e) => { e.stopPropagation(); e.preventDefault(); commit(); }}
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
