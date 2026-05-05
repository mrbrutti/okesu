// nodeTypes — the map ReactFlow uses to render each GraphNode.kind
// with the right component. Registered in CaseGraph.tsx.

import { FindingNode } from './FindingNode';
import { HostNode }    from './HostNode';
import { DaimonNode }  from './DaimonNode';
import { IOCNode }     from './IOCNode';

export const nodeTypes = {
  finding: FindingNode,
  host:    HostNode,
  daimon:  DaimonNode,
  ioc:     IOCNode,
};

export { FindingNode, HostNode, DaimonNode, IOCNode };
