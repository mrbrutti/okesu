// Read-only DAG viewer for an in-flight or completed orchestration run.
// Same react-flow primitives as the authoring canvas, but:
//
//   - Layout is horizontal (left-to-right) — matches the operator's
//     mental model of "data flows from input → output → next agent."
//   - Currently-running step pulses a blue ring; waiting-for-approval
//     pulses amber with an inline Approve button.
//   - Each card shows the node it ran on (or is running on).
//   - No drag-to-create / edit affordances. Pan + zoom only.
//   - Click a step → the parent renders the expanded body below.

import { useEffect, useMemo } from 'react';
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import { CheckCircle2, Cpu, Loader2, Pause, Server, XCircle, AlertCircle, ChevronRight } from 'lucide-react';
import { cn } from '../lib/cn';
import type { OrchestrationStepStatus, OrchestrationStepView } from '../api';

import '@xyflow/react/dist/style.css';

interface RunStepNodeData {
  agent?: string;
  node?: string;
  status: OrchestrationStepStatus;
  cpInstanceID?: string;
  approving?: boolean;
  approvable?: boolean;
  onApprove?: (stepID: string) => void;
  stepID: string;
  [key: string]: unknown;
}

type RunStepNode = Node<RunStepNodeData>;

const NODE_W = 240;
const NODE_GAP = 60; // horizontal gap between cards

function RunStepNodeView({ data, selected }: NodeProps<RunStepNode>) {
  const tone = stepRunTone(data.status);
  const Icon = stepIcon(data.status);
  return (
    <div
      className={cn(
        'bg-panel border rounded-lg shadow-card overflow-hidden text-left',
        selected ? 'ring-2 ring-brand-300 border-brand-200' : 'border-border',
        // High-affordance highlights for live state — these are why
        // the operator opened the run page in the first place. The
        // ring + animation make "this is happening NOW" unmistakeable.
        data.status === 'running' && 'ring-2 ring-blue-400 shadow-lg',
        data.status === 'waiting_approval' && 'ring-2 ring-amber-400 animate-pulse',
      )}
      style={{ width: NODE_W }}
    >
      <Handle type="target" position={Position.Left} className="!bg-slate-400 !w-2 !h-2 !border-0" />
      <div className={cn('h-1', tone.stripe)} />
      <div className="p-3">
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
        {data.node && (
          <div className="text-[11px] text-ink-dim flex items-center gap-1 truncate">
            <Server size={10} className="shrink-0" />
            <span className="truncate font-mono">{data.node}</span>
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
      <Handle type="source" position={Position.Right} className="!bg-slate-400 !w-2 !h-2 !border-0" />
    </div>
  );
}

const nodeTypes = { runStep: RunStepNodeView };

function stepRunTone(status: OrchestrationStepStatus) {
  switch (status) {
    case 'running':
      return { stripe: 'bg-blue-500',  badge: 'bg-blue-50 text-blue-700',     iconColor: 'text-blue-600' };
    case 'completed':
      return { stripe: 'bg-green-500', badge: 'bg-green-50 text-green-700',   iconColor: 'text-green-600' };
    case 'failed':
      return { stripe: 'bg-red-500',   badge: 'bg-red-50 text-red-700',       iconColor: 'text-red-600' };
    case 'waiting_approval':
      return { stripe: 'bg-amber-400', badge: 'bg-amber-50 text-amber-700',   iconColor: 'text-amber-600' };
    case 'skipped':
      return { stripe: 'bg-slate-300', badge: 'bg-slate-100 text-slate-600',  iconColor: 'text-slate-400' };
    default:
      return { stripe: 'bg-slate-300', badge: 'bg-slate-100 text-slate-600',  iconColor: 'text-slate-400' };
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

// ── Run canvas ──────────────────────────────────────────────────────

interface Props {
  steps: OrchestrationStepView[];
  approvingStepID?: string | null;
  onApprove?: (stepID: string) => void;
  onSelectStep?: (stepID: string) => void;
  selectedStepID?: string | null;
  /** Optional spec parsed alongside the run — lets us pull `agent`
   *  and the original `node:` template into each card even when the
   *  persisted step record only stores the rendered node. */
  agentByStepID?: Record<string, string>;
  nodeByStepID?: Record<string, string>;
}

export default function OrchestrationRunCanvas(props: Props) {
  return (
    <ReactFlowProvider>
      <RunCanvasInner {...props} />
    </ReactFlowProvider>
  );
}

function RunCanvasInner({
  steps,
  approvingStepID,
  onApprove,
  onSelectStep,
  selectedStepID,
  agentByStepID,
  nodeByStepID,
}: Props) {
  const initialNodes = useMemo<RunStepNode[]>(() => {
    return steps.map((s, i) => ({
      id: s.step_id,
      type: 'runStep',
      position: { x: i * (NODE_W + NODE_GAP), y: 0 },
      data: {
        stepID: s.step_id,
        agent: agentByStepID?.[s.step_id],
        node: nodeByStepID?.[s.step_id],
        status: s.status,
        cpInstanceID: s.cp_instance_id,
        approvable: s.status === 'waiting_approval',
        approving: approvingStepID === s.step_id,
        onApprove,
      },
    }));
  }, [steps, approvingStepID, onApprove, agentByStepID, nodeByStepID]);

  const initialEdges = useMemo<Edge[]>(() => {
    return steps.slice(0, -1).map((s, i) => {
      const next = steps[i + 1];
      // Color the edge by the upstream step's status — gives a quick
      // read of where data has flowed vs where execution has stalled.
      const stroke =
        s.status === 'completed' ? '#16a34a' :
        s.status === 'running'   ? '#2563eb' :
        s.status === 'failed'    ? '#dc2626' :
        '#94a3b8';
      return {
        id: `${s.step_id}-${next.step_id}`,
        source: s.step_id,
        target: next.step_id,
        type: 'smoothstep',
        markerEnd: { type: MarkerType.ArrowClosed, color: stroke },
        style: { stroke, strokeWidth: 2 },
        animated: s.status === 'running',
      };
    });
  }, [steps]);

  const [nodes, setNodes, onNodesChange] = useNodesState<RunStepNode>(initialNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>(initialEdges);

  // Keep nodes/edges synced when the run polls update step statuses.
  // The reset-to-initial pattern is fine here — react-flow handles
  // few-dozen-node graphs cheaply, and we want the new statuses to
  // visually settle, not animate from the wrong position.
  useEffect(() => setNodes(initialNodes), [initialNodes, setNodes]);
  useEffect(() => setEdges(initialEdges), [initialEdges, setEdges]);

  return (
    <div className="w-full h-[360px] rounded-xl border border-brand-200 shadow-card overflow-hidden">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={(_, n) => onSelectStep?.(n.id)}
        nodeTypes={nodeTypes}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1.0 }}
        proOptions={{ hideAttribution: true }}
        // Dotted-grid backdrop matches the authoring canvas idiom
        // exactly so an operator's eye doesn't have to retune
        // between Library editor and Runs viewer.
        style={{
          background:
            'linear-gradient(to bottom, #faf8ff, #ffffff), radial-gradient(circle at 8px 8px, #e9d5ff 1px, transparent 1px) 0 0 / 16px 16px',
        }}
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="#e9d5ff" />
        <Controls className="!bg-panel !border-border !shadow-card" showInteractive={false} />
      </ReactFlow>
      {selectedStepID && (
        <SelectionRing nodes={nodes} selectedID={selectedStepID} />
      )}
    </div>
  );
}

// SelectionRing is a tiny invisible component that updates the
// selected-node visual when the parent toggles selectedStepID — keeps
// the parent's expanded-body state and the canvas's selection in
// sync without forcing a controlled-selection prop on react-flow.
function SelectionRing({ nodes, selectedID }: { nodes: RunStepNode[]; selectedID: string }) {
  useEffect(() => {
    // No-op: the selection state is driven by node click handlers on
    // the parent. Leaving this hook as the integration seam if we
    // need to programmatically focus a step in the future.
  }, [nodes, selectedID]);
  return null;
}

