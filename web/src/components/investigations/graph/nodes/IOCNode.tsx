// IOC node — 200×56 card with a kind icon (Globe / Fingerprint /
// FileText / Hash) and a middle-truncated value. Long hashes elide
// to "prefix…suffix" so a sha256 fits in the card width.

import { Hash, Globe, FileText, Fingerprint } from 'lucide-react';
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react';

export interface IOCNodeData {
  ioc_kind:    string;
  ioc_value:   string;
  obs_count?:  number;
  host_count?: number;
  [key: string]: unknown;
}

export const IOC_NODE_W = 200;
export const IOC_NODE_H = 56;

function iocIcon(kind: string) {
  switch (kind) {
    case 'ip':     case 'domain':                  return Globe;
    case 'sha256': case 'sha1': case 'md5':        return Fingerprint;
    case 'path':   case 'file':                    return FileText;
    default:                                       return Hash;
  }
}

export function shortValue(v: string): string {
  if (v.length <= 18) return v;
  return v.slice(0, 6) + '…' + v.slice(-6);
}

export function IOCNode({ data }: NodeProps<Node<IOCNodeData>>) {
  const Icon = iocIcon(data.ioc_kind);
  const obs = data.obs_count ?? 0;
  const hosts = data.host_count ?? 0;
  return (
    <div
      className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
      style={{ width: IOC_NODE_W, height: IOC_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="w-8 h-8 rounded bg-purple-50 text-purple-600 flex items-center justify-center shrink-0">
        <Icon size={15} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[11px] text-ink-mute uppercase tracking-wide">{data.ioc_kind}</div>
        <div className="text-[12px] font-mono font-medium truncate text-ink">{shortValue(data.ioc_value)}</div>
        <div className="text-[10px] text-ink-mute">{obs} obs · {hosts} host{hosts === 1 ? '' : 's'}</div>
      </div>
    </div>
  );
}
