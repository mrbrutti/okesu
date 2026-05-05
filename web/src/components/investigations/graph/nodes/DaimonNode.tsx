// Daimon node — 180×44 badge with indigo bot icon and a finding-count
// subtext. "Daimon" is the platform's term for an emitting agent.

import { Bot } from 'lucide-react';
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react';

export interface DaimonNodeData {
  label:          string;
  finding_count?: number;
  [key: string]:  unknown;
}

export const DAIMON_NODE_W = 180;
export const DAIMON_NODE_H = 44;

export function DaimonNode({ data }: NodeProps<Node<DaimonNodeData>>) {
  const fc = data.finding_count ?? 0;
  return (
    <div
      className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
      style={{ width: DAIMON_NODE_W, height: DAIMON_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="w-7 h-7 rounded bg-indigo-50 text-indigo-600 flex items-center justify-center shrink-0">
        <Bot size={14} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[12px] font-medium truncate text-ink">{data.label}</div>
        <div className="text-[10px] text-ink-mute">daimon · {fc} finding{fc === 1 ? '' : 's'}</div>
      </div>
    </div>
  );
}
