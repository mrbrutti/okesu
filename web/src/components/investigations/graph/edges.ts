// Pure helpers that map "this entity is touched by N findings" weights
// to ReactFlow edge styling. Bands chosen so single-finding entities
// (the common case) stay visually quiet while heavily-shared entities
// (an IOC across 10 hosts) read clearly.

export interface EdgeStyle {
  stroke:      string;
  strokeWidth: number;
  opacity:     number;
}

/**
 * styleForEdgeWeight maps a weight (count of edges sharing the target)
 * to a stroke style. Bands: 1, 2-3, 4-9, 10+.
 */
export function styleForEdgeWeight(weight: number): EdgeStyle {
  if (weight >= 10) return { stroke: '#475569', strokeWidth: 2.5, opacity: 0.9 };
  if (weight >= 4)  return { stroke: '#64748b', strokeWidth: 2,   opacity: 0.85 };
  if (weight >= 2)  return { stroke: '#94a3b8', strokeWidth: 1.5, opacity: 0.8 };
  return                  { stroke: '#cbd5e1', strokeWidth: 1,   opacity: 0.75 };
}

/**
 * weightByTarget — given the graph's edges, returns a Map<targetID, count>.
 * Each target's incoming-edge count is used to choose a stroke band.
 */
export function weightByTarget(edges: ReadonlyArray<{ target: string }>): Map<string, number> {
  const m = new Map<string, number>();
  for (const e of edges) m.set(e.target, (m.get(e.target) ?? 0) + 1);
  return m;
}
