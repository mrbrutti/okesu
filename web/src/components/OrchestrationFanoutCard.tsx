import { useState } from 'react';
import { Handle, Position, type NodeProps, type Node } from '@xyflow/react';
import { CheckCircle2, Cpu, Loader2, Pause, XCircle, AlertCircle, ChevronRight } from 'lucide-react';
import { cn } from '../lib/cn';
import { fanoutAutoExpand } from '../lib/fanoutAutoExpand';
import type { OrchestrationStepStatus, StepNodeDispatchView } from '../api';

interface FanoutCardData {
  stepID: string;
  agent?: string;
  status: OrchestrationStepStatus;
  cpInstanceID?: string;
  approvable?: boolean;
  approving?: boolean;
  onApprove?: (stepID: string) => void;
  perNode: StepNodeDispatchView[];
  onSelectHost?: (stepID: string, host: string) => void;
  onOpenHostList?: (stepID: string) => void;
  [key: string]: unknown;
}

type FanoutCardNode = Node<FanoutCardData>;

const NODE_W = 260;
const NODE_W_LARGE = 280; // slightly wider when expanded with host rows

// ── Tone helpers (mirrored from OrchestrationRunCanvas for visual parity) ──

function stepRunTone(status: OrchestrationStepStatus) {
  switch (status) {
    case 'running':          return { stripe: 'bg-blue-500',  badge: 'bg-blue-50 text-blue-700',     iconColor: 'text-blue-600' };
    case 'completed':        return { stripe: 'bg-green-500', badge: 'bg-green-50 text-green-700',   iconColor: 'text-green-600' };
    case 'failed':           return { stripe: 'bg-red-500',   badge: 'bg-red-50 text-red-700',       iconColor: 'text-red-600' };
    case 'waiting_approval': return { stripe: 'bg-amber-400', badge: 'bg-amber-50 text-amber-700',   iconColor: 'text-amber-600' };
    case 'skipped':          return { stripe: 'bg-slate-300', badge: 'bg-slate-100 text-slate-600',  iconColor: 'text-slate-400' };
    default:                 return { stripe: 'bg-slate-300', badge: 'bg-slate-100 text-slate-600',  iconColor: 'text-slate-400' };
  }
}

function stepIcon(status: OrchestrationStepStatus) {
  switch (status) {
    case 'running':          return Loader2;
    case 'completed':        return CheckCircle2;
    case 'failed':           return XCircle;
    case 'waiting_approval': return Pause;
    case 'skipped':          return ChevronRight;
    default:                 return AlertCircle;
  }
}

function hostDot(status: StepNodeDispatchView['status']): string {
  switch (status) {
    case 'completed': return 'bg-green-500';
    case 'failed':    return 'bg-red-500';
    case 'running':   return 'bg-blue-500 animate-pulse';
    default:          return 'bg-slate-300';
  }
}

interface Counts { ok: number; fail: number; running: number; pending: number; total: number; }

function tally(per: StepNodeDispatchView[]): Counts {
  const c: Counts = { ok: 0, fail: 0, running: 0, pending: 0, total: per.length };
  for (const h of per) {
    if (h.status === 'completed')      c.ok++;
    else if (h.status === 'failed')    c.fail++;
    else if (h.status === 'running')   c.running++;
    else                                c.pending++;
  }
  return c;
}

export function FanoutCardView({ data, selected }: NodeProps<FanoutCardNode>) {
  const tone = stepRunTone(data.status);
  const Icon = stepIcon(data.status);
  const counts = tally(data.perNode);

  // The auto-expand rule decides initial state. Local override sticks
  // for the lifetime of the component (i.e. as long as react-flow
  // keeps the node mounted) but doesn't persist across reloads.
  const auto = fanoutAutoExpand(counts.total, data.status, data.perNode);
  const [override, setOverride] = useState<boolean | null>(null);
  const expanded = override ?? auto;

  const failedHosts = data.perNode.filter((h) => h.status === 'failed');
  const isLarge = counts.total > 20;

  return (
    <div
      className={cn(
        'bg-panel border rounded-lg shadow-card overflow-hidden text-left',
        selected ? 'ring-2 ring-brand-300 border-brand-200' : 'border-border',
        data.status === 'running' && 'ring-2 ring-blue-400 shadow-lg',
        data.status === 'waiting_approval' && 'ring-2 ring-amber-400 animate-pulse',
        data.status === 'failed' && 'ring-2 ring-red-300',
      )}
      style={{ width: expanded ? NODE_W_LARGE : NODE_W }}
    >
      <Handle type="target" position={Position.Left} className="!bg-slate-400 !w-2 !h-2 !border-0" />
      <div className={cn('h-1', tone.stripe)} />

      {/* Header */}
      <div
        className="p-3 cursor-pointer"
        onClick={() => !isLarge && setOverride((cur) => !(cur ?? auto))}
      >
        <div className="flex items-center gap-2 mb-1.5">
          <Icon size={14} className={tone.iconColor} />
          <span className="text-xs font-semibold text-ink truncate flex-1">{data.stepID}</span>
          <span className={cn('text-[9px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded', tone.badge)}>
            {data.status.replace('_', ' ')}
          </span>
        </div>

        {data.agent && (
          <div className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded font-medium mb-1">
            <Cpu size={9} />
            {data.agent}
          </div>
        )}

        {/* Headline + histogram + count */}
        <div className="text-[10px] text-ink-dim mt-1">
          {counts.total} hosts ({counts.ok} ok • {counts.fail} fail • {counts.running} running)
        </div>
        <div className="flex items-center gap-2 mt-1.5">
          <div className="flex-1 flex h-1.5 rounded overflow-hidden bg-slate-200">
            {counts.ok      > 0 && <div className="bg-green-500" style={{ width: `${(counts.ok      / counts.total) * 100}%` }} />}
            {counts.fail    > 0 && <div className="bg-red-500"   style={{ width: `${(counts.fail    / counts.total) * 100}%` }} />}
            {counts.running > 0 && <div className="bg-blue-500"  style={{ width: `${(counts.running / counts.total) * 100}%` }} />}
          </div>
          <span className="text-[9px] text-ink-dim font-semibold whitespace-nowrap">
            {counts.ok + counts.fail} / {counts.total}
          </span>
        </div>

        {/* Dot strip — only ≤20 */}
        {!isLarge && (
          <div className="mt-1.5 flex gap-[2px] flex-wrap p-1 rounded bg-slate-50">
            {data.perNode.map((h) => (
              <span
                key={h.host}
                className={cn('w-2 h-2 rounded-full', hostDot(h.status))}
                title={`${h.host} — ${h.status}`}
              />
            ))}
          </div>
        )}

        {/* Failed-host inline lines — only ≤20, up to 3 hosts; otherwise summary */}
        {!isLarge && failedHosts.length > 0 && failedHosts.length <= 3 && (
          <div className="mt-1.5 space-y-0.5">
            {failedHosts.map((h) => (
              <div key={h.host} className="text-[10px] text-red-700 font-mono truncate">
                ✕ {h.host} — {h.error || 'failed'}
              </div>
            ))}
          </div>
        )}
        {!isLarge && failedHosts.length > 3 && (
          <div className="mt-1.5 text-[10px] text-red-700 font-mono">
            ✕ {failedHosts.length} hosts failed — open to inspect
          </div>
        )}

        {data.cpInstanceID && data.cpInstanceID !== 'local' && (
          <div className="text-[10px] text-brand-700 mt-1 truncate">
            cp: {data.cpInstanceID.slice(0, 8)}…
          </div>
        )}

        {data.approvable && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              data.onApprove?.(data.stepID);
            }}
            disabled={data.approving}
            className="mt-2 w-full inline-flex items-center justify-center gap-1 bg-amber-500 hover:bg-amber-600 disabled:opacity-50 text-white text-[11px] font-medium px-2 py-1 rounded-md"
          >
            {data.approving ? <Loader2 size={10} className="animate-spin" /> : <CheckCircle2 size={10} />}
            Approve
          </button>
        )}
      </div>

      {/* Expanded host rows + large-mode footer button — wired in Task 11 */}

      <Handle type="source" position={Position.Right} className="!bg-slate-400 !w-2 !h-2 !border-0" />
    </div>
  );
}
