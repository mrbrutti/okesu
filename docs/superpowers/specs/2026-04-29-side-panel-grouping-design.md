# Side panel grouping

**Status:** approved
**Date:** 2026-04-29

## Goal

Group the eleven flat nav items in the app sidebar into expandable
sections so the panel teaches the operator the model (related items
visibly belong together) and has room to grow without redesign as new
features land. Cosmetics match the existing app — same Tailwind tokens,
same active-route treatment, same icon column alignment.

## Non-goals

- Mobile / narrow-viewport icon-only mode behind a hamburger. Same gap
  exists today and isn't worsened by this change.
- Drag-to-reorder, user-customisable groups, per-user nav layouts.
- Search-in-nav. Cmd-K already handles "go to X" globally.

## Decisions

| # | Decision |
|---|---|
| 1 | Mental-model grouping with collapsible sections — solves both the "teach the model" and "save space when not needed" goals from question 1. |
| 2 | Five buckets, mixed flat + grouped: top-level (Dashboard, Federation), three groups (Triage, Automation, Fleet), footer (Documentation, Settings). |
| 3 | Federation stays flat at top-level until it has siblings. Don't group prematurely (one-item groups feel overweight). |
| 4 | Smart default on first visit: only the group containing the active route starts expanded. |
| 5 | Persistence via localStorage key `okesu.sidebar.groups`. Missing/malformed → fall back to smart default. |
| 6 | Multi-open accordion (each group toggles independently). Friendlier for cross-group glances during work. |
| 7 | Active-route does NOT auto-expand a collapsed group at runtime. Parent header takes brand color as a breadcrumb instead. |
| 8 | Group headers are uppercase / tracked-out section labels with a chevron — strong visual hierarchy without indenting children. Sub-items render exactly like today's flat items, preserving the icon column alignment. |
| 9 | Source of truth for the nav structure moves from inline in `Layout.tsx` to a new `web/src/lib/sidebarNav.ts`. |

## Final structure

```
[ Dashboard ]                   ← top-level, ungrouped
[ Federation ]                  ← top-level (until it has siblings)
─────────────────────────       ← thin border-border divider
▼ TRIAGE                        ← uppercase tracked-out group header
   Findings
   Investigations
   Live Events
▼ AUTOMATION
   Agents
   Orchestrations
▼ FLEET
   Daimons
   Nodes
─────────────────────────       ← thin divider
[ Documentation ]               ← admin-style footer items
[ Settings ]
─────────────────────────       ← existing user-block border
[ user@email · sign out ]       ← unchanged
```

## Architecture

### Layers touched

```
web/src/lib/sidebarNav.ts                  (new — nav data + types)
web/src/components/Layout.tsx              (consume sidebarNav; render groups)
web/src/components/SidebarGroup.tsx        (new — collapsible section)
```

`Layout.tsx`'s inline `nav` array goes away. Any future nav addition
edits `sidebarNav.ts` only — `Layout.tsx` stays generic.

### `sidebarNav.ts`

```ts
import type { LucideIcon } from 'lucide-react';
import {
  Activity, AlertTriangle, BookOpen, ClipboardList,
  LayoutDashboard, Network, Server, Layers, Settings,
  Sparkles, Workflow,
} from 'lucide-react';

export type NavLeaf = {
  to: string;
  label: string;
  icon: LucideIcon;
  enabled: boolean;
};

export type GroupID = 'triage' | 'automation' | 'fleet';

export type NavGroup = {
  id: GroupID;
  label: string;
  items: NavLeaf[];
};

export type NavSection =
  | { kind: 'leaf'; item: NavLeaf }
  | { kind: 'group'; group: NavGroup }
  | { kind: 'divider' };

export const sidebarNav: NavSection[] = [
  { kind: 'leaf', item: { to: '/dashboard',  label: 'Dashboard',  icon: LayoutDashboard, enabled: true } },
  { kind: 'leaf', item: { to: '/federation', label: 'Federation', icon: Network,         enabled: true } },
  { kind: 'divider' },
  { kind: 'group', group: {
    id: 'triage', label: 'Triage', items: [
      { to: '/findings',      label: 'Findings',       icon: AlertTriangle,  enabled: true },
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

### `SidebarGroup.tsx`

A self-contained collapsible section. Owns its open/closed state via
the persistence helper described below. Renders its children only
when open (after the entrance animation).

Props:
```ts
{
  group: NavGroup,
  initiallyOpen: boolean,        // smart-default seed (used only when localStorage has no entry)
  containsActive: boolean,       // drives header brand color
}
```

The persistence key is global (`okesu.sidebar.groups`) — the component
writes its slice into the shared object using `group.id`.

The component:
- Renders a `<button type="button" aria-expanded={open} aria-controls={panelId}>` for the header.
- Renders a wrapper `<div id={panelId} role="region" aria-labelledby={headerId}>` for the children.
- Uses CSS `max-height` + chevron `rotate` for the expand/collapse
  transition. Skip animation when
  `window.matchMedia('(prefers-reduced-motion: reduce)').matches`.

### Persistence helper (lives in `SidebarGroup.tsx`)

`SidebarGroup` is the only consumer of these helpers, so they sit at
the top of that file rather than getting their own module.


```ts
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

export function writeGroupState(s: GroupOpenState): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    // private mode / quota — fail silently; in-memory state still works
  }
}
```

The reader returns `Partial` so the caller can fold the smart default
in for any group without a stored value: `stored.triage ?? smartDefault`.

### `Layout.tsx` (after the refactor)

The existing inline `nav` array is deleted. The render loop walks
`sidebarNav` and emits a `<NavLink>` for `leaf`, a thin divider for
`divider`, or a `<SidebarGroup>` for `group`. The user-block at the
bottom is unchanged. The active-route lookup is hoisted once via
`useLocation()` and passed down so each group can compute
`containsActive` for its children.

## Visual treatment

### Group header

- Padding: `px-4 pt-3 pb-1` (more top space than children to read as a
  section start).
- Typography: `text-[10px] uppercase tracking-wide font-semibold`.
- Default color: `text-ink-mute` (`#94a3b8`).
- Hover color: `text-ink-dim` (`#64748b`).
- Brand color when group contains active route: `text-brand-700`
  (`#5b21b6`) — same color as today's active leaf.
- Chevron: lucide `ChevronDown` size 10. Rotated `-rotate-90` when
  collapsed. Same color steps as the label. Transition
  `transition-transform duration-150`.
- Click target: full width (`w-full`).

### Sub-item

- **Identical** to today's flat items: `flex items-center gap-2.5
  px-3 py-2 rounded-md text-sm`. Icon size 16, `strokeWidth={2}`.
- Active state unchanged: `bg-brand-50 text-brand-700 font-medium`.
- Hover unchanged: `hover:bg-slate-100`.
- No indent — keeps the icon column visually aligned with the
  top-level (Dashboard, Federation) items.

### Top-level items, dividers, footer items

- Top-level items render exactly like today.
- Dividers are `<div className="border-t border-border my-2" />`.
- Footer items (Docs, Settings) render exactly like today.

## Testing

The web/ workspace has no test runner. Verification is manual; the
acceptance check at the end of the implementation plan covers:

1. **Smart default works** — visit `/agents` from a fresh state (clear
   `okesu.sidebar.groups`); only Automation is open on load.
2. **Persistence works** — toggle Triage closed, reload; Triage stays
   closed. Toggle it open; reload; stays open.
3. **Active highlight** — Triage closed, navigate to `/findings`. Header
   takes brand color; group does NOT auto-expand.
4. **Icon column** — open all groups, scroll the panel; icon left edges
   line up across grouped and ungrouped items.
5. **Reduced motion** — set OS reduced-motion; expand/collapse skips
   the height animation but still toggles.
6. **Keyboard** — Tab moves through top-level → group header → its
   visible children → next group header. Space/Enter on a header
   toggles open/closed.

## Edge cases

- **Disabled item under a group** — same `cursor-not-allowed` treatment
  as today; the parent header still shows it.
- **All items in a group disabled** — render the header with
  `aria-disabled` and grey it out. Click is a no-op.
- **localStorage unavailable** (private mode, quota) — read returns
  `{}`, write fails silently. In-memory state still works for the
  session.
- **Group label collides with a route** — guarded by the type system:
  `GroupID` is a string-literal union, distinct from any `NavLeaf.to`.

## Deferred / explicit non-goals

1. Mobile / narrow-viewport icon-only mode. Same as today; revisit when
   the app first targets a tablet-sized viewport.
2. Drag-to-reorder or user-customisable group memberships.
3. Group-level badges (e.g. unread count) — not designed in; can be
   added later by extending `NavGroup` with an optional `badge`.
