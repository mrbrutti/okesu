// Severity → Tailwind class set for the finding card. Mirrors the
// canonical severity-* tokens (SeverityMenu.tsx, CommandPalette.tsx)
// so a CRITICAL chip on a Finding row and the CRITICAL ring on a
// graph node are visually identical.

export type Severity = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';

export interface NodeTone {
  bg:      string;
  ring:    string;
  badge:   string;
  iconBg:  string;
  iconFg:  string;
}

export function severityTone(sev: string): NodeTone {
  switch (sev?.toUpperCase()) {
    case 'CRITICAL': return { bg: 'bg-purple-50',  ring: 'ring-purple-500', badge: 'severity-badge severity-critical', iconBg: 'bg-purple-100',  iconFg: 'text-purple-700' };
    case 'HIGH':     return { bg: 'bg-red-50',     ring: 'ring-red-500',    badge: 'severity-badge severity-high',     iconBg: 'bg-red-100',     iconFg: 'text-red-700' };
    case 'MEDIUM':   return { bg: 'bg-orange-50',  ring: 'ring-orange-500', badge: 'severity-badge severity-medium',   iconBg: 'bg-orange-100',  iconFg: 'text-orange-700' };
    case 'LOW':      return { bg: 'bg-yellow-50',  ring: 'ring-yellow-500', badge: 'severity-badge severity-low',      iconBg: 'bg-yellow-100',  iconFg: 'text-yellow-700' };
    default:         return { bg: 'bg-slate-50',   ring: 'ring-slate-400',  badge: 'severity-badge severity-info',     iconBg: 'bg-slate-100',   iconFg: 'text-slate-700' };
  }
}
