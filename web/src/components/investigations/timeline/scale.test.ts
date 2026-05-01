import { describe, it, expect } from 'vitest';
import { tToX, autoFitRange, presetRange } from './scale';

describe('scale', () => {
  it('tToX maps tMin to 0 and tMax to width', () => {
    expect(tToX(100, 100, 200, 1000)).toBe(0);
    expect(tToX(200, 100, 200, 1000)).toBe(1000);
    expect(tToX(150, 100, 200, 1000)).toBe(500);
  });

  it('tToX clamps below tMin and above tMax', () => {
    expect(tToX(50, 100, 200, 1000)).toBe(0);
    expect(tToX(250, 100, 200, 1000)).toBe(1000);
  });

  it('autoFitRange returns [tMin, max(now, tMax)] for a non-empty event set', () => {
    const now = 1_000_000_000;
    const r = autoFitRange([100, 200, 500_000_000], now);
    expect(r.tMin).toBe(100);
    expect(r.tMax).toBe(now);
  });

  it('autoFitRange falls back to [now-1h, now] for empty events', () => {
    const now = 1_000_000_000;
    const r = autoFitRange([], now);
    expect(r.tMax).toBe(now);
    expect(r.tMin).toBe(now - 3_600_000);
  });

  it('autoFitRange war-room mode caps to last 1h regardless of older events', () => {
    const now = 1_000_000_000;
    const r = autoFitRange([100, 200, 500_000_000], now, { warRoom: true });
    expect(r.tMax).toBe(now);
    expect(r.tMin).toBe(now - 3_600_000);
  });

  it('presetRange "1h" returns [now - 1h, now]', () => {
    const now = 1_000_000_000;
    const r = presetRange('1h', now);
    expect(r.tMax).toBe(now);
    expect(r.tMin).toBe(now - 3_600_000);
  });

  it('presetRange "24h" / "7d" produce expected widths', () => {
    const now = 1_000_000_000;
    expect(presetRange('24h', now).tMin).toBe(now - 86_400_000);
    expect(presetRange('7d', now).tMin).toBe(now - 7 * 86_400_000);
  });
});
