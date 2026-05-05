import { describe, it, expect } from 'vitest';
import { styleForEdgeWeight, weightByTarget } from './edges';

describe('styleForEdgeWeight', () => {
  it('returns the lightest band for weight 1', () => {
    const s = styleForEdgeWeight(1);
    expect(s.strokeWidth).toBe(1);
    expect(s.stroke).toBe('#cbd5e1');
  });

  it('returns 1.5px for weights 2-3', () => {
    expect(styleForEdgeWeight(2).strokeWidth).toBe(1.5);
    expect(styleForEdgeWeight(3).strokeWidth).toBe(1.5);
  });

  it('returns 2px for weights 4-9', () => {
    expect(styleForEdgeWeight(4).strokeWidth).toBe(2);
    expect(styleForEdgeWeight(9).strokeWidth).toBe(2);
  });

  it('returns 2.5px for weight 10+', () => {
    expect(styleForEdgeWeight(10).strokeWidth).toBe(2.5);
    expect(styleForEdgeWeight(99).strokeWidth).toBe(2.5);
  });
});

describe('weightByTarget', () => {
  it('counts edges per target id', () => {
    const m = weightByTarget([
      { target: 'a' },
      { target: 'a' },
      { target: 'b' },
    ]);
    expect(m.get('a')).toBe(2);
    expect(m.get('b')).toBe(1);
    expect(m.size).toBe(2);
  });

  it('returns empty map for empty input', () => {
    expect(weightByTarget([]).size).toBe(0);
  });
});
