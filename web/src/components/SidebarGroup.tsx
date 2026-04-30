import { useId, useMemo, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import type { GroupID, NavGroup } from '../lib/sidebarNav';
import { cn } from '../lib/cn';

// ── localStorage helpers ────────────────────────────────────────────
//
// One global key holds open/closed for every group. The reader returns
// `Partial` so callers fold in the smart default for any group without
// a stored value: `stored[groupID] ?? smartDefault`.
//
// SidebarGroup is the only consumer of these helpers, so they live
// here rather than in their own module.

const KEY = 'okesu.sidebar.groups';

export type GroupOpenState = Record<GroupID, boolean>;

export function readGroupState(): Partial<GroupOpenState> {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return typeof parsed === 'object' && parsed !== null ? parsed : {};
  } catch (e) {
    console.warn('sidebar: malformed group state, falling back to defaults', e);
    return {};
  }
}

export function writeGroupState(s: Partial<GroupOpenState>): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    // private mode / quota — fail silently; in-memory state still works
  }
}

// ── prefers-reduced-motion ──────────────────────────────────────────

function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false;
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// ── SidebarGroup ────────────────────────────────────────────────────

interface Props {
  group: NavGroup;
  // Smart-default seed — only used on first mount when localStorage
  // has no entry for this group. After that, the persisted choice
  // wins.
  initiallyOpen: boolean;
  // True when one of this group's items is the current route. Drives
  // the brand-color header treatment as a "you are here" breadcrumb.
  // Per spec: does NOT auto-expand a collapsed group at runtime.
  containsActive: boolean;
}

export default function SidebarGroup({ group, initiallyOpen, containsActive }: Props) {
  // Seed open state from localStorage (if present), else from the
  // smart default. The reader is called once at mount; subsequent
  // toggles update both state and storage.
  const [open, setOpen] = useState<boolean>(() => {
    const stored = readGroupState();
    return stored[group.id] ?? initiallyOpen;
  });

  const reduceMotion = useMemo(prefersReducedMotion, []);

  const headerId = useId();
  const panelId = useId();

  // Toggle handler — flips local state and writes through the merged
  // global state to localStorage so the other groups' choices aren't
  // lost on this write.
  function toggle() {
    const next = !open;
    const merged: Partial<GroupOpenState> = {
      ...readGroupState(),
      [group.id]: next,
    };
    writeGroupState(merged);
    setOpen(next);
  }

  // The expand transition uses scrollHeight → max-height. The ref is
  // the wrapper around the children; we measure its scrollHeight when
  // open and apply that as max-height. When closed, max-height is 0.
  // Reduced motion skips the transition entirely (no animation, just
  // toggle visibility).
  const panelRef = useRef<HTMLDivElement>(null);

  // Header tone: brand color when this group contains the active
  // route, mute otherwise. Both states use the same hover step
  // (text-ink-dim) so the affordance is consistent.
  const headerTone = containsActive
    ? 'text-brand-700 hover:text-brand-700'
    : 'text-ink-mute hover:text-ink-dim';

  return (
    <div className="select-none">
      <button
        id={headerId}
        type="button"
        onClick={toggle}
        aria-expanded={open}
        aria-controls={panelId}
        className={cn(
          'w-full flex items-center justify-between px-4 pt-3 pb-1',
          'text-[10px] uppercase tracking-wide font-semibold',
          'transition-colors',
          headerTone,
        )}
      >
        <span>{group.label}</span>
        <ChevronDown
          size={10}
          className={cn(
            'transition-transform duration-150',
            !open && '-rotate-90',
            reduceMotion && '!transition-none',
          )}
        />
      </button>

      <div
        id={panelId}
        role="region"
        aria-labelledby={headerId}
        ref={panelRef}
        // Use overflow-hidden + max-height to animate. When reduced
        // motion is on, we still toggle visibility but skip the
        // height transition (handled via the !transition-none above
        // plus the inline style branch below).
        style={
          reduceMotion
            ? { display: open ? 'block' : 'none' }
            : {
                maxHeight: open ? `${panelRef.current?.scrollHeight ?? 999}px` : 0,
                overflow: 'hidden',
                transition: 'max-height 150ms ease-out',
              }
        }
      >
        {group.items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            // Same `end` predicate as today's flat items — only the
            // dashboard / findings routes need exact matching to
            // avoid overlap with their own sub-routes.
            end={item.to === '/dashboard' || item.to === '/findings'}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-2.5 px-3 py-2 rounded-md text-sm transition-colors',
                item.enabled
                  ? isActive
                    ? 'bg-brand-50 text-brand-700 font-medium'
                    : 'text-ink hover:bg-slate-100'
                  : 'text-ink-mute cursor-not-allowed',
              )
            }
            onClick={(e) => { if (!item.enabled) e.preventDefault(); }}
          >
            <item.icon size={16} strokeWidth={2} />
            <span>{item.label}</span>
            {!item.enabled && (
              <span className="ml-auto text-[10px] uppercase tracking-wide text-ink-mute">soon</span>
            )}
          </NavLink>
        ))}
      </div>
    </div>
  );
}
