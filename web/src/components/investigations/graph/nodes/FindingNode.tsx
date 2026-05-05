// Finding node — 240×86 card matching OrchestrationCanvas's step
// card shape so the relationship view feels like the same product
// surface. Severity rail on the left (4px), then a tinted body with
// the severity badge, finding id, title (clamped to 2 lines), and
// agent/host chips on the bottom row.

import { Handle, Position, type Node, type NodeProps } from '@xyflow/react';
import { cn } from '../../../../lib/cn';
import { severityTone } from './tones';

export interface FindingNodeData {
  id:        string;
  numericID: number;
  title:     string;
  severity:  string;
  host:      string;
  agent:     string;
  [key: string]: unknown;
}

export const FINDING_NODE_W = 240;
export const FINDING_NODE_H = 86;

export function FindingNode({ data, selected }: NodeProps<Node<FindingNodeData>>) {
  const tone = severityTone(data.severity);
  const railBg = tone.ring.replace('ring-', 'bg-'); // ring-purple-500 → bg-purple-500
  return (
    <div
      className={cn(
        'rounded-lg border border-border bg-panel shadow-card overflow-hidden text-left ring-1 transition',
        tone.ring,
        selected ? 'ring-2 ring-offset-1' : '',
      )}
      style={{ width: FINDING_NODE_W, height: FINDING_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="flex h-full">
        <div className={cn('w-1 h-full', railBg)} />
        <div className={cn('flex-1 px-2.5 py-1.5', tone.bg)}>
          <div className="flex items-center gap-1 mb-1">
            <span className={cn(tone.badge, 'text-[9px]')}>{(data.severity || 'INFO').toUpperCase()}</span>
            <span className="font-mono text-[10px] text-ink-mute">#{data.numericID}</span>
          </div>
          <div className="text-[12px] font-medium leading-tight line-clamp-2 text-ink">
            {data.title || '(no title)'}
          </div>
          <div className="mt-1 flex items-center gap-1 text-[10px] text-ink-mute">
            {data.agent && <span className="px-1 rounded bg-slate-100 truncate max-w-[100px]">{data.agent}</span>}
            {data.host && (
              <>
                <span>·</span>
                <span className="truncate max-w-[110px]">{data.host}</span>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
