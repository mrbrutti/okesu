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
  let tMin = timestamps[0];
  let tMax = timestamps[0];
  for (const t of timestamps) {
    if (t < tMin) tMin = t;
    if (t > tMax) tMax = t;
  }
  return { tMin, tMax: Math.max(tMax, now) };
}

export type Preset = '1h' | '24h' | '7d';

export function presetRange(p: Preset, now: number): Range {
  if (p === '1h')  return { tMin: now - HOUR_MS, tMax: now };
  if (p === '24h') return { tMin: now - DAY_MS,  tMax: now };
  return { tMin: now - 7 * DAY_MS, tMax: now };
}
