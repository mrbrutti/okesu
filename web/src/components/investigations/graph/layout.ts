// Pure layout math for the bipartite Graph view. Computes deterministic
// (x, y) positions for each node:
//
//   - Findings stack vertically in the left column (x=80).
//   - Right column (x=560) groups by kind: hosts top, then daimons,
//     then IOCs, with vertical gaps between kinds.
//   - Within each column-section, nodes sort by edge degree desc
//     (most-connected at the top).
//
// Output is a Map<nodeID, {x, y}> consumed by the react-flow renderer.

import type { GraphNode, GraphEdge } from '../../../api';

export interface Position {
  x: number;
  y: number;
}

export const LEFT_X = 80;
export const RIGHT_X = 560;        // widened to fit 240-px finding cards in the left column
export const FINDING_PITCH = 100;  // 86-tall card + 14 gap
export const ENTITY_PITCH  = 60;   // 56-tall IOC card + 4 gap (host/daimon at 44 also fit)
export const KIND_GAP = 24;

export function computePositions(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Map<string, Position> {
  const out = new Map<string, Position>();

  // Compute degree per node id.
  const degree = new Map<string, number>();
  for (const e of edges) {
    degree.set(e.source, (degree.get(e.source) ?? 0) + 1);
    degree.set(e.target, (degree.get(e.target) ?? 0) + 1);
  }
  const degOf = (id: string) => degree.get(id) ?? 0;

  // Bucket by kind.
  const findings: GraphNode[] = [];
  const hosts: GraphNode[] = [];
  const daimons: GraphNode[] = [];
  const iocs: GraphNode[] = [];
  for (const n of nodes) {
    if (n.kind === 'finding') findings.push(n);
    else if (n.kind === 'host') hosts.push(n);
    else if (n.kind === 'daimon') daimons.push(n);
    else if (n.kind === 'ioc') iocs.push(n);
  }

  // Sort each bucket by degree desc, then label ascending for stability.
  const sortByDegree = (a: GraphNode, b: GraphNode) => {
    const dd = degOf(b.id) - degOf(a.id);
    if (dd !== 0) return dd;
    return a.label.localeCompare(b.label);
  };
  findings.sort(sortByDegree);
  hosts.sort(sortByDegree);
  daimons.sort(sortByDegree);
  iocs.sort(sortByDegree);

  // Place findings in the left column.
  let y = 40;
  for (const f of findings) {
    out.set(f.id, { x: LEFT_X, y });
    y += FINDING_PITCH;
  }

  // Place hosts, daimons, IOCs in the right column with kind gaps.
  y = 40;
  for (const h of hosts) {
    out.set(h.id, { x: RIGHT_X, y });
    y += ENTITY_PITCH;
  }
  if (hosts.length > 0 && (daimons.length > 0 || iocs.length > 0)) y += KIND_GAP;
  for (const d of daimons) {
    out.set(d.id, { x: RIGHT_X, y });
    y += ENTITY_PITCH;
  }
  if (daimons.length > 0 && iocs.length > 0) y += KIND_GAP;
  for (const i of iocs) {
    out.set(i.id, { x: RIGHT_X, y });
    y += ENTITY_PITCH;
  }
  return out;
}
