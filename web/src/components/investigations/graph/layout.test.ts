import { describe, it, expect } from 'vitest';
import { computePositions } from './layout';
import type { GraphNode, GraphEdge } from '../../../api';

const makeFinding = (id: number, severity = 'LOW'): GraphNode => ({
  id: `f:${id}`,
  kind: 'finding',
  label: `Finding ${id}`,
  severity,
});
const makeHost = (h: string): GraphNode => ({ id: `h:${h}`, kind: 'host', label: h });
const makeDaimon = (a: string): GraphNode => ({ id: `d:${a}`, kind: 'daimon', label: a });
const makeIOC = (kind: string, value: string): GraphNode => ({
  id: `i:${kind}:${value}`, kind: 'ioc', label: `${kind}:${value.slice(-4)}`,
  ioc_kind: kind, ioc_value: value,
});
const edge = (s: string, t: string): GraphEdge => ({ id: `${s}|${t}`, source: s, target: t });

describe('computePositions', () => {
  it('returns empty positions for empty input', () => {
    const out = computePositions([], []);
    expect(out.size).toBe(0);
  });

  it('places findings in the left column at x=80', () => {
    const out = computePositions([makeFinding(1)], []);
    const f = out.get('f:1');
    expect(f).toBeDefined();
    expect(f!.x).toBe(80);
  });

  it('places hosts/daimons/IOCs in the right column at x=560', () => {
    const out = computePositions(
      [makeFinding(1), makeHost('h1'), makeDaimon('a'), makeIOC('sha256', 'deadbeef')],
      [
        edge('f:1', 'h:h1'),
        edge('f:1', 'd:a'),
        edge('f:1', 'i:sha256:deadbeef'),
      ],
    );
    expect(out.get('h:h1')!.x).toBe(560);
    expect(out.get('d:a')!.x).toBe(560);
    expect(out.get('i:sha256:deadbeef')!.x).toBe(560);
  });

  it('orders right-column nodes hosts → daimons → IOCs', () => {
    const out = computePositions(
      [makeFinding(1), makeHost('h1'), makeDaimon('a'), makeIOC('sha256', 'deadbeef')],
      [
        edge('f:1', 'h:h1'),
        edge('f:1', 'd:a'),
        edge('f:1', 'i:sha256:deadbeef'),
      ],
    );
    const hY = out.get('h:h1')!.y;
    const dY = out.get('d:a')!.y;
    const iY = out.get('i:sha256:deadbeef')!.y;
    expect(hY).toBeLessThan(dY);
    expect(dY).toBeLessThan(iY);
  });

  it('sorts findings by edge degree desc within the left column', () => {
    // f:1 has 2 edges, f:2 has 1 edge → f:1 above f:2
    const out = computePositions(
      [makeFinding(1), makeFinding(2), makeHost('h1'), makeHost('h2')],
      [
        edge('f:1', 'h:h1'),
        edge('f:1', 'h:h2'),
        edge('f:2', 'h:h1'),
      ],
    );
    expect(out.get('f:1')!.y).toBeLessThan(out.get('f:2')!.y);
  });
});
