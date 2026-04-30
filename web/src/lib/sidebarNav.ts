import type { LucideIcon } from 'lucide-react';
import {
  Activity, AlertTriangle, BookOpen, ClipboardList,
  LayoutDashboard, Library, Network, Server, Layers, Settings,
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
      { to: '/catalog',        label: 'Catalog',        icon: Library,       enabled: true },
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
