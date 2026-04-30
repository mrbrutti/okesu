# Side panel grouping — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Group the eleven flat sidebar nav items into Triage / Automation / Fleet collapsible sections, with smart-default expansion of the active route's group and localStorage persistence of the collapse state.

**Architecture:** Move the nav data out of `Layout.tsx` into a typed `sidebarNav.ts` source-of-truth. Add a `SidebarGroup.tsx` component that owns one section's open/closed state (seeded by smart default, persisted to localStorage). Refactor `Layout.tsx` to walk `sidebarNav` and emit a `<NavLink>`, divider, or `<SidebarGroup>` per entry — generic, no hard-coded item knowledge.

**Tech Stack:** React 18 + TypeScript, react-router-dom (`NavLink`, `useLocation`), Tailwind, lucide-react.

**Spec:** `docs/superpowers/specs/2026-04-29-side-panel-grouping-design.md`

---

## File structure

**Created (2):**
- `web/src/lib/sidebarNav.ts` — types + the `sidebarNav` data array
- `web/src/components/SidebarGroup.tsx` — collapsible section component + its localStorage helpers

**Modified (1):**
- `web/src/components/Layout.tsx` — delete inline `nav` array, walk `sidebarNav`, render leafs/dividers/groups

---

## Task 1: `sidebarNav.ts` — types + data

**Files:**
- Create: `web/src/lib/sidebarNav.ts`

- [ ] **Step 1: Create the file**

Create `web/src/lib/sidebarNav.ts`:

```ts
import type { LucideIcon } from 'lucide-react';
import {
  Activity, AlertTriangle, BookOpen, ClipboardList,
  LayoutDashboard, Network, Server, Layers, Settings,
  Sparkles, Workflow,
} from 'lucide-react';

// One nav-leaf renders as a single <NavLink> in the sidebar — same
// shape Layout.tsx used inline before this refactor. `enabled: false`
// items render greyed out and are not clickable; the existing flat
// list already supported the flag, so we keep it for parity.
export type NavLeaf = {
  to: string;
  label: string;
  icon: LucideIcon;
  enabled: boolean;
};

// String-literal union — keeps group ids type-safe and distinct from
// any NavLeaf.to (the latter are paths starting with `/`).
export type GroupID = 'triage' | 'automation' | 'fleet';

// One nav-group renders as a collapsible section header followed by
// its `items`. The group label is what shows in the uppercase
// tracked-out header.
export type NavGroup = {
  id: GroupID;
  label: string;
  items: NavLeaf[];
};

// Layout.tsx walks NavSection[] and emits the right element per kind:
// 'leaf' → <NavLink>, 'group' → <SidebarGroup>, 'divider' → <hr/>.
// The discriminated union keeps the render loop pattern-matchable.
export type NavSection =
  | { kind: 'leaf'; item: NavLeaf }
  | { kind: 'group'; group: NavGroup }
  | { kind: 'divider' };

// Top-to-bottom order in the sidebar. Add new top-level items, group
// items, or whole new groups here; Layout.tsx is generic.
//
// Federation stays flat at the top until it has siblings (decision #3
// in the spec — don't group prematurely).
export const sidebarNav: NavSection[] = [
  { kind: 'leaf', item: { to: '/dashboard',  label: 'Dashboard',  icon: LayoutDashboard, enabled: true } },
  { kind: 'leaf', item: { to: '/federation', label: 'Federation', icon: Network,         enabled: true } },
  { kind: 'divider' },
  { kind: 'group', group: {
    id: 'triage', label: 'Triage', items: [
      { to: '/findings',       label: 'Findings',       icon: AlertTriangle, enabled: true },
      { to: '/investigations', label: 'Investigations', icon: ClipboardList, enabled: true },
      { to: '/events',         label: 'Live Events',    icon: Activity,      enabled: true },
    ],
  }},
  { kind: 'group', group: {
    id: 'automation', label: 'Automation', items: [
      { to: '/agents',         label: 'Agents',         icon: Sparkles, enabled: true },
      { to: '/orchestrations', label: 'Orchestrations', icon: Workflow, enabled: true },
    ],
  }},
  { kind: 'group', group: {
    id: 'fleet', label: 'Fleet', items: [
      { to: '/daimons', label: 'Daimons', icon: Layers, enabled: true },
      { to: '/nodes',   label: 'Nodes',   icon: Server, enabled: true },
    ],
  }},
  { kind: 'divider' },
  { kind: 'leaf', item: { to: '/docs',     label: 'Documentation', icon: BookOpen, enabled: true } },
  { kind: 'leaf', item: { to: '/settings', label: 'Settings',      icon: Settings, enabled: true } },
];
```

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: clean (no errors).

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/sidebarNav.ts
git commit -m "$(cat <<'EOF'
web(lib): sidebarNav — typed source-of-truth for the side panel

Eleven items split into top-level (Dashboard, Federation), three
collapsible groups (Triage, Automation, Fleet), and footer items
(Documentation, Settings). Layout.tsx will consume this in a
follow-up commit; the inline nav array goes away.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `SidebarGroup.tsx` — collapsible section component

**Files:**
- Create: `web/src/components/SidebarGroup.tsx`

This component owns its open/closed state. Smart default seeds it on
mount when localStorage has no entry for this group; every toggle
writes the merged state back. Skip animation when reduced-motion is on.

- [ ] **Step 1: Create the file**

Create `web/src/components/SidebarGroup.tsx`:

```tsx
import { useEffect, useId, useMemo, useRef, useState } from 'react';
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
    setOpen((cur) => {
      const next = !cur;
      const merged: Partial<GroupOpenState> = {
        ...readGroupState(),
        [group.id]: next,
      };
      writeGroupState(merged);
      return next;
    });
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
```

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/SidebarGroup.tsx
git commit -m "$(cat <<'EOF'
web(components): SidebarGroup — collapsible nav section

Header uses uppercase tracked-out text matching today's existing
type scale; chevron rotates -90° when collapsed. Open/closed state
persists in localStorage under okesu.sidebar.groups; the smart-default
seed only applies on first mount when the key is absent. Sub-items
render with the same NavLink shape Layout.tsx used inline before, so
the active-leaf highlight is unchanged. Reduced-motion skips the
height transition.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `Layout.tsx` — consume `sidebarNav` and render groups

**Files:**
- Modify: `web/src/components/Layout.tsx`

The existing inline `nav` array gets deleted. The render loop walks
`sidebarNav` and emits the right element for each section kind. The
active-route lookup uses `useLocation()` once at the top; each group
computes `containsActive` from that.

- [ ] **Step 1: Replace the file**

Open `web/src/components/Layout.tsx`. Replace the entire file with:

```tsx
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { LogOut } from 'lucide-react';
import { api, type User } from '../api';
import { cn } from '../lib/cn';
import CommandPalette from './CommandPalette';
import { sidebarNav, type NavLeaf, type NavGroup } from '../lib/sidebarNav';
import SidebarGroup from './SidebarGroup';

interface Props {
  user: User;
  onLogout: () => void;
}

// Pure helpers — kept outside the component so React doesn't have to
// recreate them on every render.

// True if any leaf under this group matches the current pathname.
// Mirrors NavLink's match semantics: an exact match for /dashboard
// and /findings (the routes that have their own sub-routes), prefix
// match elsewhere.
function groupContainsActive(group: NavGroup, pathname: string): boolean {
  return group.items.some((item) => leafIsActive(item, pathname));
}

function leafIsActive(leaf: NavLeaf, pathname: string): boolean {
  if (leaf.to === '/dashboard' || leaf.to === '/findings') {
    return pathname === leaf.to;
  }
  return pathname === leaf.to || pathname.startsWith(leaf.to + '/');
}

export default function Layout({ user, onLogout }: Props) {
  const navigate = useNavigate();
  const { pathname } = useLocation();

  async function handleLogout() {
    try { await api.logout(); } catch { /* ignore */ }
    onLogout();
    navigate('/login', { replace: true });
  }

  return (
    <div className="flex h-screen overflow-hidden">
      <aside className="w-60 shrink-0 border-r border-border bg-panel flex flex-col">
        <Link to="/" className="px-5 py-5 flex items-center gap-2 border-b border-border">
          <div className="w-7 h-7 rounded-md bg-brand-500 flex items-center justify-center">
            <div className="w-3 h-3 rounded-full bg-white"/>
          </div>
          <div>
            <div className="font-semibold text-sm leading-tight">Okesu</div>
            <div className="text-[11px] text-ink-mute leading-tight">Control Plane</div>
          </div>
        </Link>

        <nav className="flex-1 py-3 px-2 space-y-0.5 overflow-y-auto">
          {sidebarNav.map((section, idx) => {
            if (section.kind === 'divider') {
              return <div key={`d-${idx}`} className="border-t border-border my-2" />;
            }
            if (section.kind === 'leaf') {
              const item = section.item;
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
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
              );
            }
            // section.kind === 'group'
            const group = section.group;
            const contains = groupContainsActive(group, pathname);
            return (
              <SidebarGroup
                key={group.id}
                group={group}
                // Smart default: seed open=true only when the active
                // route is in this group. Other groups start collapsed
                // on first visit. Persisted choice overrides this on
                // subsequent renders (handled inside SidebarGroup).
                initiallyOpen={contains}
                containsActive={contains}
              />
            );
          })}
        </nav>

        <div className="p-3 border-t border-border">
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <div className="text-xs font-medium text-ink truncate">{user.email}</div>
              <div className="text-[11px] text-ink-mute capitalize">{user.role}</div>
            </div>
            <button
              onClick={handleLogout}
              className="p-1.5 text-ink-dim hover:text-ink hover:bg-slate-100 rounded-md"
              title="Sign out"
            >
              <LogOut size={15} />
            </button>
          </div>
        </div>
      </aside>

      <main className="flex-1 overflow-auto">
        <Outlet />
      </main>

      {/* Cmd-K / Ctrl-K palette. Mounted once at the layout level so the
       *  global hotkey is live on every authenticated route. */}
      <CommandPalette />
    </div>
  );
}
```

- [ ] **Step 2: Verify TS compiles**

Run: `cd web && npx tsc --noEmit`
Expected: clean.

- [ ] **Step 3: Verify the bundle builds**

Run: `cd web && npm run build 2>&1 | tail -10`
Expected: `✓ built in <N>s`. Build artifacts land in `controlplane/ui/dist/`.

The pre-existing chunk-size warning (`>500 kB after minification`) is
unrelated to this change — ignore.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Layout.tsx
git commit -m "$(cat <<'EOF'
web(layout): consume sidebarNav and render collapsible groups

Inline nav array gone — Layout walks sidebarNav and emits the right
element per section kind: NavLink for leaf, SidebarGroup for group,
divider for divider. Active-route lookup uses useLocation() once at
the top; each group computes containsActive from that to drive both
the header brand color and the smart-default open seed.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Manual verification

**Files:**
- None (verification only)

The web/ workspace has no test runner. This task is the explicit
acceptance check before declaring done.

- [ ] **Step 1: Build everything**

Run: `cd web && npm run build`
Expected: clean.

- [ ] **Step 2: Start dev server**

Run: `cd web && npm run dev &`
Expected: dev server up on `http://localhost:5173/` (or whichever port
vite picks).

The CP backend should also be running for full UI testing. From the
project root:

Run: `go run ./cmd/okesu-cp &`

(If the CP isn't running, the page still loads — auth fails — but the
sidebar layout itself renders.)

- [ ] **Step 3: Smart default works**

In the browser DevTools Application → Local Storage panel, delete the
`okesu.sidebar.groups` key (or run `localStorage.clear()` in the
console).

Navigate to `/agents`. Reload the page.

**Acceptance:**
- The Automation group is open (Agents + Orchestrations visible).
- Triage and Fleet groups are collapsed.
- The Automation header is brand-colored (purple).

- [ ] **Step 4: Persistence works**

Click the Triage header to open it. Click again to collapse it.
Reload the page.

**Acceptance:**
- Triage stays collapsed across the reload.
- The state in DevTools shows `okesu.sidebar.groups` with `triage:
  false` written.

Now click Triage to open it, click Automation to close it. Reload.

**Acceptance:**
- Triage open, Automation closed. Both choices stuck.

- [ ] **Step 5: Active-route highlight without auto-expand**

With Triage collapsed (close it if needed), navigate to `/findings`
(via Cmd-K, address bar, or any leaf-link that isn't in the sidebar
right now — the orchestrator's "Open finding" links work).

**Acceptance:**
- Triage header is brand-colored.
- Triage stays collapsed (does NOT auto-expand on the route change).

- [ ] **Step 6: Icon column alignment**

Open all three groups. Visually inspect the icons' left edges.

**Acceptance:**
- Icons in Dashboard, Federation, all group items, Documentation, and
  Settings all align on the same vertical line.
- No drift / indent on the grouped sub-items.

- [ ] **Step 7: Reduced motion**

In the OS settings, enable "Reduce motion" (macOS: System Settings →
Accessibility → Display → Reduce motion). Reload the browser.

Click a group header to toggle it.

**Acceptance:**
- The expand/collapse happens instantly (no height animation).
- The chevron rotation also happens instantly (no transition).
- After the toggle, the group's open/closed state is correct.

Disable reduced motion before continuing.

- [ ] **Step 8: Keyboard accessibility**

Click somewhere outside the sidebar to clear focus. Press Tab
repeatedly.

**Acceptance:**
- Focus moves through the sidebar in this order: Okesu logo → top-level
  items (Dashboard, Federation) → first group header → its visible
  children → next group header → its visible children → ... →
  Documentation → Settings → Sign-out button.
- Press Space or Enter while focused on a group header — the group
  toggles open/closed.
- A collapsed group's children are skipped in the tab order.

- [ ] **Step 9: Stop the dev servers**

```bash
kill %1 %2  # or whichever shell-job ids the dev servers are running under
```

- [ ] **Step 10: Final commit (only if anything changed during verification)**

If the smoke-test surfaced tweaks, fix them and commit. If everything
passed cleanly, no commit needed for this step.

---

## Self-review

**1. Spec coverage**

- Decision 1 (mental-model grouping with collapse) — Tasks 1, 2, 3. ✓
- Decision 2 (top-level + 3 groups + footer) — encoded in `sidebarNav`
  in Task 1. ✓
- Decision 3 (Federation flat) — encoded in `sidebarNav`. ✓
- Decision 4 (smart default by active route) — Task 3 passes
  `initiallyOpen={contains}` to each group. ✓
- Decision 5 (localStorage `okesu.sidebar.groups` with malformed
  fallback) — Task 2's `readGroupState` / `writeGroupState`. ✓
- Decision 6 (multi-open) — each `SidebarGroup` owns its state
  independently. ✓
- Decision 7 (active route doesn't auto-expand at runtime) — `useState`
  initial seed runs once at mount; subsequent route changes don't
  re-seed. ✓
- Decision 8 (uppercase header style; sub-items unchanged) — Task 2's
  visual classes; Task 3's `<NavLink>` shape mirrors today's. ✓
- Decision 9 (`sidebarNav.ts` source of truth) — Task 1. ✓

Visual treatment items (px-4 pt-3 pb-1 header padding, text-[10px]
uppercase tracking-wide font-semibold, color steps, ChevronDown size
10 with -rotate-90, sub-item parity with today) — all explicitly in
Task 2's component code. ✓

Edge cases:
- Disabled item — `cursor-not-allowed` preserved in both Task 2 (group
  sub-items) and Task 3 (top-level/footer leafs). ✓
- localStorage unavailable — `try/catch` in Task 2's helpers. ✓
- Group label collides with route — guarded by `GroupID` literal
  union from Task 1. ✓
- All items disabled — not explicitly handled (the spec called this
  out as "render the header with `aria-disabled`"). Currently the
  component doesn't gate the toggle on item availability. **Adding a
  follow-up note rather than a task: this scenario doesn't exist in
  the data today; revisit when a group is introduced where every item
  ships behind a flag.**

Manual verification checks (1–6 in spec) — Tasks 4 step 3–8 cover
each of: smart default, persistence, active-route highlight, icon
alignment, reduced motion, keyboard. ✓

**2. Placeholder scan**

No "TBD", "TODO", or "fill in details". Every step has the actual
code or command. The "all items disabled" deferral above is explicit
rather than a placeholder.

**3. Type / name consistency**

- `GroupID` literal union, `NavLeaf`, `NavGroup`, `NavSection` —
  defined in Task 1, imported by name in Tasks 2 and 3. ✓
- `readGroupState` / `writeGroupState` / `GroupOpenState` — defined
  in Task 2, used only inside Task 2. ✓
- `SidebarGroup` props (`group`, `initiallyOpen`, `containsActive`) —
  Task 2's interface, matched exactly by Task 3's call site. ✓
- `groupContainsActive` and `leafIsActive` (Task 3 helpers) — both
  defined and called within Task 3. ✓

**4. Scope**

Three files, four tasks, one PR. Sized for a single subagent-driven
session.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-29-side-panel-grouping.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
