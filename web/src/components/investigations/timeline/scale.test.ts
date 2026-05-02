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

  it('autoFitRange extends to now when the gap from latest event is within span (live-clock affordance preserved)', () => {
    // Events span 1 hour, now is 30 min past the last event. Gap (30m)
    // <= span (1h), so the live-clock extension applies and tMax = now.
    // 5% padding then nudges tMin/tMax outward.
    const now = Date.now();
    const tFirst = now - 90 * 60 * 1000;              // 90 min ago
    const tLast  = now - 30 * 60 * 1000;              // 30 min ago
    const r = autoFitRange([tFirst, tLast], now);
    const displaySpan = now - tFirst;                  // 90 min
    const pad = displaySpan * 0.05;                   // 4.5 min
    expect(r.tMin).toBeCloseTo(tFirst - pad, -2);
    expect(r.tMax).toBeCloseTo(now + pad, -2);
  });

  it('autoFitRange does NOT extend to now when latest event is much older than the events span (avoids crushing dots against left edge)', () => {
    // 23 findings spanning 1 hour, last finding 17 hours ago → without
    // this fix, the canvas would span 18h with events crammed into 5%
    // of the width. With the fix, tMax should NOT be `now` — the canvas
    // should fit the events plus a little padding.
    const now = Date.now();
    const lastEvent = now - 17 * 3_600_000;             // 17h ago
    const firstEvent = lastEvent - 1 * 3_600_000;       // 18h ago
    const stamps = [];
    for (let i = 0; i < 23; i++) stamps.push(firstEvent + i * (3_600_000 / 22));
    const r = autoFitRange(stamps, now);
    // tMax should be near lastEvent + small padding, not near now.
    expect(r.tMax).toBeLessThan(now - 16 * 3_600_000);
    // Canvas should span ~1h + padding, not ~18h.
    expect(r.tMax - r.tMin).toBeLessThan(2 * 3_600_000);
  });

  it('autoFitRange opens a 1-hour window when all events share one timestamp', () => {
    const now = Date.now();
    const r = autoFitRange([1_000_000, 1_000_000, 1_000_000], now);
    expect(r.tMin).toBe(1_000_000 - 1_800_000);
    expect(r.tMax).toBe(1_000_000 + 1_800_000);
  });

  it('autoFitRange ignores a single recent outlier (e.g., note added today on a 2-day-old findings cluster)', () => {
    // 23 findings clustered ~2 days ago, plus 1 note added recently.
    // Without outlier rejection the canvas spans ~50h with the cluster
    // crushed into ~21% of width. With percentile filtering the canvas
    // fits the cluster.
    const now = Date.now();
    const clusterStart = now - 50 * 3_600_000;        // 50h ago
    const clusterEnd   = now - 33 * 3_600_000;        // 33h ago
    const stamps = [];
    for (let i = 0; i < 23; i++) stamps.push(clusterStart + i * ((clusterEnd - clusterStart) / 22));
    stamps.push(now - 60 * 1000);                      // 1 outlier note 60s ago
    const r = autoFitRange(stamps, now);
    // Range should fit the cluster (~17h), not span the full 50h.
    expect(r.tMax - r.tMin).toBeLessThan(20 * 3_600_000);
    // tMax should be near clusterEnd, not near the outlier note.
    expect(r.tMax).toBeLessThan(now - 25 * 3_600_000);
  });

  it('autoFitRange handles ≤5 events without percentile filtering (small N falls back to min/max)', () => {
    const now = Date.now();
    const stamps = [now - 4 * 3_600_000, now - 3 * 3_600_000, now - 2 * 3_600_000, now - 3_600_000];
    const r = autoFitRange(stamps, now);
    // Should fit the events with extension to now (gap=1h <= span=3h).
    expect(r.tMax).toBeGreaterThanOrEqual(now);
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
