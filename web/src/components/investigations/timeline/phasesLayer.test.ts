import { describe, it, expect } from 'vitest';
import { layoutPhases } from './phasesLayer';
import type { InvestigationPhase } from '../../../api';

function p(id: number, start: number, end: number): InvestigationPhase {
  return {
    ID: id,
    InvestigationID: 1,
    Name: `phase-${id}`,
    StartTs: start,
    EndTs: end,
    CreatedBy: { Valid: false, String: '' },
    CreatedAt: '',
  };
}

const range = { tMin: 0, tMax: 1000 };
const width = 1000;
const labelW = 80;

describe('layoutPhases', () => {
  it('returns empty array for empty input', () => {
    expect(layoutPhases([], range, width, labelW)).toEqual([]);
  });

  it('places a single phase on row 0', () => {
    const out = layoutPhases([p(1, 100, 300)], range, width, labelW);
    expect(out).toHaveLength(1);
    expect(out[0].row).toBe(0);
    expect(out[0].x).toBe(labelW + 100);
    expect(out[0].width).toBe(200);
  });

  it('places non-overlapping phases on row 0', () => {
    const out = layoutPhases([p(1, 0, 300), p(2, 400, 700)], range, width, labelW);
    expect(out.map((o) => o.row)).toEqual([0, 0]);
  });

  it('overlapping phases stack on rows 0 and 1', () => {
    const out = layoutPhases([p(1, 0, 500), p(2, 300, 700)], range, width, labelW);
    expect(out.map((o) => o.row)).toEqual([0, 1]);
  });

  it('three-way overlap stacks on rows 0, 1, 2', () => {
    const out = layoutPhases(
      [p(1, 0, 800), p(2, 100, 600), p(3, 200, 700)],
      range, width, labelW,
    );
    expect(out.map((o) => o.row).sort()).toEqual([0, 1, 2]);
  });

  it('touching boundaries (start of B == end of A) both go on row 0', () => {
    const out = layoutPhases([p(1, 0, 400), p(2, 400, 700)], range, width, labelW);
    expect(out.map((o) => o.row)).toEqual([0, 0]);
  });

  it('phase outside visible range still in output', () => {
    // tToX clamps to canvas edges; the layout helper itself doesn't drop
    // anything — caller decides whether to render off-screen pills.
    const out = layoutPhases([p(1, 5000, 6000)], range, width, labelW);
    expect(out).toHaveLength(1);
    // x clamped to right edge; width clamped to 0.
    expect(out[0].x).toBe(labelW + width);
  });

  it('places earlier-starting phase on lower row when both stay open', () => {
    const out = layoutPhases(
      [p(1, 0, 300), p(2, 100, 700), p(3, 400, 600)],
      range, width, labelW,
    );
    const byID = new Map(out.map((o) => [o.phase.ID, o]));
    expect(byID.get(1)!.row).toBe(0);
    expect(byID.get(2)!.row).toBe(1);
    expect(byID.get(3)!.row).toBe(0);
  });
});
