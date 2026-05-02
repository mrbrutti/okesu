// Pure time → x mapping + auto-fit / preset range helpers. Kept
// separate from the React component so the math is unit-testable
// in isolation.

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;

export interface Range {
  tMin: number;
  tMax: number;
}

export function tToX(t: number, tMin: number, tMax: number, width: number): number {
  if (tMax <= tMin) return 0;
  const clamped = Math.min(Math.max(t, tMin), tMax);
  return ((clamped - tMin) / (tMax - tMin)) * width;
}

export function autoFitRange(timestamps: number[], now: number, opts?: { warRoom?: boolean }): Range {
  if (opts?.warRoom) {
    return { tMin: now - HOUR_MS, tMax: now };
  }
  if (timestamps.length === 0) {
    return { tMin: now - HOUR_MS, tMax: now };
  }
  // Outlier-resistant min/max: a single event well outside the main
  // cluster (e.g., one note added today on a case whose findings are
  // all from two days ago) used to inflate the canvas span by an
  // order of magnitude, crushing the cluster into 5% of the width.
  // We use the 5th/95th percentile when the timestamp count is
  // large enough to make percentile selection meaningful (≥5 events);
  // outliers past those percentiles get clamped to the canvas edge by
  // tToX rather than driving range selection.
  const sorted = [...timestamps].sort((a, b) => a - b);
  let tMin: number;
  let tMax: number;
  if (sorted.length >= 5) {
    const lo = Math.floor(sorted.length * 0.05);
    const hi = Math.floor(sorted.length * 0.95);
    tMin = sorted[lo];
    tMax = sorted[Math.min(hi, sorted.length - 1)];
    // Guard: if the percentile filter collapsed the range to a point,
    // fall back to absolute min/max so we still render something.
    if (tMin === tMax) {
      tMin = sorted[0];
      tMax = sorted[sorted.length - 1];
    }
  } else {
    tMin = sorted[0];
    tMax = sorted[sorted.length - 1];
  }
  // Single-instant events: open a 1-hour window centered on the
  // timestamp so the dot has room to render.
  if (tMin === tMax) {
    return { tMin: tMin - HOUR_MS / 2, tMax: tMax + HOUR_MS / 2 };
  }
  // Extend to `now` only when the gap from the latest in-cluster event
  // to now is at most the cluster's own span. Preserves the live-clock
  // affordance for recent / in-flight cases without re-introducing the
  // "single recent note inflates the canvas" failure mode (the note
  // doesn't survive percentile filtering above, so tMax is the cluster
  // edge, not the note).
  const span = tMax - tMin;
  const includeNow = now - tMax <= span;
  const adjustedTMax = includeNow ? Math.max(tMax, now) : tMax;
  // 5% padding on each side keeps dots off the canvas edges without
  // materially shifting cluster grouping.
  const displaySpan = adjustedTMax - tMin;
  const pad = displaySpan * 0.05;
  return { tMin: tMin - pad, tMax: adjustedTMax + pad };
}

export type Preset = '1h' | '24h' | '7d';

export function presetRange(p: Preset, now: number): Range {
  if (p === '1h')  return { tMin: now - HOUR_MS, tMax: now };
  if (p === '24h') return { tMin: now - DAY_MS,  tMax: now };
  return { tMin: now - 7 * DAY_MS, tMax: now };
}
