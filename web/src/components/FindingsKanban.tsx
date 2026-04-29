// Kanban-style view for the Findings page. Columns map 1:1 to the
// FindingStatus enum; cards can be dragged between columns to change
// status. Federated cards proxy through ?cp= so the drop handler works
// the same whether the finding lives on this CP or a child.
//
// Visual language matches the Dashboard / Findings list:
//   - Each column is a panel surface (bg-panel, rounded-xl, shadow-card)
//     with a 2-px top accent stripe in the status color and a vertical
//     brand-stripe inside the header (mirrors Dashboard Card).
//   - Status color tokens come from STATUS_STYLES (StatusPill) so the
//     pill, dot, and column tone all stay aligned.
//   - Each card carries a 1-px severity-color left stripe — same idiom
//     used by recent-list rows in /findings — plus the .severity-*
//     badge class so the tone matches every other badge on the page.
//
// Drag-and-drop uses native HTML5 — no extra dependency. Each card sets
// dataTransfer with the finding id + cp instance id; the column's drop
// handler reads them and calls api.setFindingStatus optimistically.

import { DragEvent, useEffect, useMemo, useState } from 'react';
import { Cpu, Inbox, Server } from 'lucide-react';
import {
  ALL_FINDING_STATUSES,
  api,
  type Finding,
  type FindingStatus,
} from '../api';
import { cn } from '../lib/cn';
import { CPSourceChip } from './CPSourceChip';
import { STATUS_STYLES } from './StatusPill';

interface Props {
  /** Filters echo the rest of the Findings page so toggling between
   *  views keeps the same scope. */
  agent?: string;
  host?: string;
  category?: string;
  severity?: string[];
  /** Called after a successful drag to nudge the rest of the page
   *  (counts, drawer-open list) to refresh. */
  onChanged?: () => void;
  /** Click → open the shared FindingDrawer in /findings. The page
   *  owns the drawer state since it deep-links into the URL. */
  onOpenCard?: (id: number, cpInstanceID?: string) => void;
}

const KANBAN_PAGE_SIZE = 500;

// Column accent stripe — uses the same dot-color token as StatusPill
// so the pill in the header and the stripe up top read as a single
// visual identity. The pulse-on-hover ring uses brand-* to keep drop
// affordance consistent with the rest of the platform.
const COL_ACCENT: Record<FindingStatus, string> = {
  open:           'bg-brand-500',
  acknowledged:   'bg-slate-400',
  investigating:  'bg-blue-500',
  resolved:       'bg-green-500',
  false_positive: 'bg-amber-500',
  wontfix:        'bg-slate-300',
};

export default function FindingsKanban({
  agent,
  host,
  category,
  severity,
  onChanged,
  onOpenCard,
}: Props) {
  const [findings, setFindings] = useState<Finding[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hoverCol, setHoverCol] = useState<FindingStatus | null>(null);
  const [busyID, setBusyID] = useState<number | null>(null);

  const refresh = () => {
    api.findings({
      state: 'all',
      agent: agent || undefined,
      host: host || undefined,
      category: category || undefined,
      severity: severity && severity.length ? (severity.filter(Boolean) as string[]) : undefined,
      limit: KANBAN_PAGE_SIZE,
    })
      .then((rows) => { setFindings(rows); setError(null); })
      .catch((e) => setError(String(e)));
  };

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 10_000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent, host, category, (severity ?? []).join(',')]);

  const columns = useMemo(() => {
    const out: Record<FindingStatus, Finding[]> = {
      open: [], acknowledged: [], investigating: [],
      resolved: [], false_positive: [], wontfix: [],
    };
    for (const f of findings ?? []) {
      const s = (f.status ?? 'open') as FindingStatus;
      if (!out[s]) continue;
      out[s].push(f);
    }
    return out;
  }, [findings]);

  async function moveTo(id: number, target: FindingStatus, cpInstanceID?: string) {
    if (!findings) return;
    const before = findings;
    const optimistic = findings.map((f) => f.id === id ? { ...f, status: target } : f);
    setFindings(optimistic);
    setBusyID(id);
    try {
      await api.setFindingStatus(id, target, { cpInstanceID });
      onChanged?.();
    } catch (e) {
      setFindings(before);
      setError(String(e));
    } finally {
      setBusyID(null);
    }
  }

  function onDragStart(e: DragEvent<HTMLElement>, f: Finding) {
    e.dataTransfer.setData('text/finding-id', String(f.id));
    e.dataTransfer.setData('text/cp-instance-id', f.cp_source?.instance_id ?? '');
    e.dataTransfer.effectAllowed = 'move';
  }
  function onDropCol(e: DragEvent<HTMLElement>, target: FindingStatus) {
    e.preventDefault();
    setHoverCol(null);
    const id = Number(e.dataTransfer.getData('text/finding-id'));
    const cp = e.dataTransfer.getData('text/cp-instance-id') || undefined;
    if (!id) return;
    const f = findings?.find((x) => x.id === id);
    if (!f || (f.status ?? 'open') === target) return;
    void moveTo(id, target, cp);
  }
  function onDragOverCol(e: DragEvent<HTMLElement>, col: FindingStatus) {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (hoverCol !== col) setHoverCol(col);
  }

  const totalCount = findings?.length ?? 0;

  return (
    <div className="h-full flex flex-col overflow-hidden bg-bg">
      {/* Sub-header: matches the chrome on Dashboard Cards — brand
          gradient hairline, small icon + descriptive copy. Keeps the
          Kanban anchored to the Findings page header above it. */}
      <div className="px-6 py-2.5 border-b border-border bg-gradient-to-r from-brand-50/40 via-panel to-panel flex items-center gap-3">
        <div className="flex items-center gap-2 text-xs text-ink-dim">
          <span className="w-1 h-4 rounded-full bg-gradient-to-b from-brand-400 to-brand-600" />
          <span>
            <span className="text-ink font-medium">{totalCount}</span> finding{totalCount === 1 ? '' : 's'}
            {' · '}drag a card between columns to update its status
          </span>
        </div>
        {findings === null && (
          <span className="text-[11px] text-ink-mute ml-auto">loading…</span>
        )}
      </div>

      {error && (
        <div className="mx-6 mt-3 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="text-red-700 hover:text-red-900 underline">dismiss</button>
        </div>
      )}

      <div className="flex-1 overflow-x-auto overflow-y-hidden p-4">
        <div className="flex gap-3 h-full min-w-min">
          {ALL_FINDING_STATUSES.map((status) => {
            const list = columns[status];
            const sty = STATUS_STYLES[status];
            const Icon = sty.icon;
            const isHover = hoverCol === status;
            return (
              <section
                key={status}
                onDragOver={(e) => onDragOverCol(e, status)}
                onDragLeave={() => setHoverCol((c) => c === status ? null : c)}
                onDrop={(e) => onDropCol(e, status)}
                className={cn(
                  'flex-1 min-w-[280px] max-w-[340px] flex flex-col rounded-xl bg-panel shadow-card overflow-hidden transition-all',
                  isHover
                    ? 'ring-2 ring-brand-300 border-brand-200'
                    : 'border border-border hover:border-brand-200/60',
                )}
              >
                {/* 2-px accent stripe in the status color — same trick
                    Dashboard StatTile uses to give the column a quick
                    chromatic identity at a glance. */}
                <div className={cn('h-0.5', COL_ACCENT[status])} />
                <header className="px-3.5 pt-3 pb-2 flex items-center justify-between gap-2 border-b border-border/60">
                  <div className="flex items-center gap-2 min-w-0">
                    <span className={cn('w-1 h-4 rounded-full', COL_ACCENT[status])} />
                    <span
                      className={cn(
                        'inline-flex items-center gap-1 text-[11px] font-medium tabular-nums px-1.5 py-0.5 rounded ring-1',
                        sty.cls,
                      )}
                    >
                      <Icon size={10} />
                      {sty.label}
                    </span>
                  </div>
                  <span
                    className={cn(
                      'text-[11px] tabular-nums font-semibold px-1.5 py-0.5 rounded',
                      list.length > 0 ? 'text-ink bg-slate-100' : 'text-ink-mute',
                    )}
                  >
                    {list.length}
                  </span>
                </header>

                <div className="flex-1 overflow-y-auto p-2 space-y-2">
                  {list.length === 0 ? (
                    <DropPlaceholder hovering={isHover} />
                  ) : (
                    list.map((f) => (
                      <KanbanCard
                        key={f.id}
                        f={f}
                        busy={busyID === f.id}
                        onClick={() => onOpenCard?.(f.id, f.cp_source?.instance_id)}
                        onDragStart={(e) => onDragStart(e, f)}
                      />
                    ))
                  )}
                </div>
              </section>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function DropPlaceholder({ hovering }: { hovering: boolean }) {
  return (
    <div
      className={cn(
        'h-full min-h-[120px] rounded-lg border-2 border-dashed flex flex-col items-center justify-center gap-1 text-[11px] transition-colors',
        hovering
          ? 'border-brand-300 bg-brand-50/40 text-brand-700'
          : 'border-border text-ink-mute',
      )}
    >
      <Inbox size={18} className={hovering ? 'text-brand-500' : 'text-ink-mute opacity-60'} />
      <span>{hovering ? 'Release to move' : 'Drop here'}</span>
    </div>
  );
}

interface CardProps {
  f: Finding;
  busy: boolean;
  onClick: () => void;
  onDragStart: (e: DragEvent<HTMLElement>) => void;
}

function KanbanCard({ f, busy, onClick, onDragStart }: CardProps) {
  const sev = (f.severity || 'INFO').toUpperCase();
  return (
    <div
      draggable
      onDragStart={onDragStart}
      onClick={onClick}
      className={cn(
        // Same panel idiom as StatTile / Dashboard Card — bg-panel +
        // shadow-card + ring on hover. Severity stripe lives inline,
        // matching how the recent-findings list renders rows.
        'group cursor-grab active:cursor-grabbing flex bg-panel border border-border rounded-md shadow-card hover:shadow-md hover:border-brand-200 transition-all overflow-hidden',
        busy && 'opacity-50 pointer-events-none',
      )}
      title="Click to open · drag to change status"
    >
      <span className={cn('w-1 shrink-0', sevBar(sev))} />
      <div className="flex-1 min-w-0 p-2.5">
        <div className="flex items-center gap-1.5 mb-1.5">
          <span className={cn('severity-badge text-[9px] px-1.5 py-0.5', `severity-${sev.toLowerCase()}`)}>
            {sev}
          </span>
          <CPSourceChip source={f.cp_source} />
        </div>
        <div className="text-xs font-medium text-ink leading-snug line-clamp-2 group-hover:text-brand-700 transition-colors">
          {f.title || `Finding #${f.id}`}
        </div>
        {(f.agent || f.host) && (
          <div className="mt-1.5 flex items-center gap-2 text-[10px] text-ink-mute">
            {f.agent && (
              <span className="inline-flex items-center gap-0.5 truncate min-w-0">
                <Cpu size={9} className="shrink-0" />
                <span className="truncate">{f.agent}</span>
              </span>
            )}
            {f.host && (
              <span className="inline-flex items-center gap-0.5 truncate min-w-0">
                <Server size={9} className="shrink-0" />
                <span className="truncate">{f.host}</span>
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// Mirrors sevBar() in pages/Findings.tsx so kanban cards pick up the
// exact severity hue the recent-list rows use. Duplicated rather than
// re-exported because the page-level helper is private and the Kanban
// is the only outside caller — duplicating one switch is cheaper than
// reshaping the module surface.
function sevBar(sev: string): string {
  switch (sev.toLowerCase()) {
    case 'critical': return 'bg-sev-critical';
    case 'high':     return 'bg-sev-high';
    case 'medium':   return 'bg-sev-medium';
    case 'low':      return 'bg-sev-low';
    default:         return 'bg-sev-info';
  }
}
