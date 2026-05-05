// Host node — compact 160×44 badge with green server icon. Hosts
// have no drawer; CaseGraph routes their click to /findings?host=<n>.

import { Server } from 'lucide-react';
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react';

export interface HostNodeData {
  label:          string;
  finding_count?: number;
  [key: string]:  unknown;
}

export const HOST_NODE_W = 160;
export const HOST_NODE_H = 44;

export function HostNode({ data }: NodeProps<Node<HostNodeData>>) {
  return (
    <div
      className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
      style={{ width: HOST_NODE_W, height: HOST_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="w-7 h-7 rounded bg-emerald-50 text-emerald-600 flex items-center justify-center shrink-0">
        <Server size={14} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[12px] font-medium truncate text-ink">{data.label}</div>
        <div className="text-[10px] text-ink-mute">host</div>
      </div>
    </div>
  );
}
