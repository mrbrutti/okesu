// Pure first-fit row assignment for overlapping phases. Sorts by
// start_ts ascending and places each phase on the lowest-numbered
// row whose latest end_ts is <= this phase's start_ts. New row if
// every existing row overlaps. Pixel positions are computed via
// tToX (output clamps to drawable area; callers decide whether to
// render off-screen pills).

import type { InvestigationPhase } from '../../../api';
import { tToX, type Range } from './scale';

export interface PhaseLayout {
  phase: InvestigationPhase;
  row:   number;
  x:     number;
  width: number;
}

export function layoutPhases(
  phases: InvestigationPhase[],
  range: Range,
  drawableWidth: number,
  laneLabelWidth: number,
): PhaseLayout[] {
  if (phases.length === 0) return [];

  const sorted = [...phases].sort((a, b) => {
    if (a.StartTs !== b.StartTs) return a.StartTs - b.StartTs;
    return a.ID - b.ID;
  });

  // rowEnds[i] = latest end_ts placed on row i. Empty array = no rows yet.
  const rowEnds: number[] = [];
  const out: PhaseLayout[] = [];

  for (const phase of sorted) {
    let placedRow = -1;
    for (let i = 0; i < rowEnds.length; i++) {
      if (rowEnds[i] <= phase.StartTs) {
        placedRow = i;
        break;
      }
    }
    if (placedRow === -1) {
      placedRow = rowEnds.length;
      rowEnds.push(phase.EndTs);
    } else {
      rowEnds[placedRow] = phase.EndTs;
    }
    const x0 = laneLabelWidth + tToX(phase.StartTs, range.tMin, range.tMax, drawableWidth);
    const x1 = laneLabelWidth + tToX(phase.EndTs, range.tMin, range.tMax, drawableWidth);
    out.push({
      phase,
      row:   placedRow,
      x:     x0,
      width: Math.max(0, x1 - x0),
    });
  }

  return out;
}
