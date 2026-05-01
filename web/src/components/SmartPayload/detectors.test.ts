import { describe, it, expect } from 'vitest';
import { detectEntity } from './detectors';

describe('detectEntity', () => {
  it('returns null for non-objects', () => {
    expect(detectEntity('hello')).toBeNull();
    expect(detectEntity(42)).toBeNull();
    expect(detectEntity(null)).toBeNull();
    expect(detectEntity([1, 2, 3])).toBeNull();
  });

  it('returns null for unrelated objects', () => {
    expect(detectEntity({ foo: 'bar', baz: 1 })).toBeNull();
  });

  it('detects finding', () => {
    expect(detectEntity({
      id: 42,
      severity: 'HIGH',
      title: 'x',
      category: 'process',
    })).toEqual({
      kind: 'finding',
      snapshot: expect.objectContaining({ id: 42, severity: 'HIGH', title: 'x', category: 'process' }),
    });
  });

  it('detects ioc', () => {
    const got = detectEntity({
      kind: 'sha256',
      value: 'a'.repeat(64),
      observation_count: 3,
    });
    expect(got?.kind).toBe('ioc');
    expect(got?.snapshot.kind).toBe('sha256');
  });

  it('detects node (id+hostname, no severity)', () => {
    const got = detectEntity({ id: 7, name: 'edr-1', hostname: 'edr-1.lab', status: 'online' });
    expect(got?.kind).toBe('node');
  });

  it('does not detect node when severity present (would be a finding)', () => {
    const got = detectEntity({
      id: 7,
      hostname: 'h',
      severity: 'HIGH',
      title: 'x',
      category: 'y',
    });
    expect(got?.kind).toBe('finding');
  });

  it('detects daimon', () => {
    const got = detectEntity({ name: 'edr', host: 'h1', agent_id: 11, suspended: false });
    expect(got?.kind).toBe('daimon');
  });

  it('detects run', () => {
    const got = detectEntity({ id: 99, status: 'completed', started_at: '2026-01-01', agent_name: 'edr' });
    expect(got?.kind).toBe('run');
  });

  it('detects investigation, prefers it over finding when no category', () => {
    const got = detectEntity({
      id: 3,
      title: 'inc',
      severity: 'HIGH',
      status: 'open',
      external_key: 'INC-1',
    });
    expect(got?.kind).toBe('investigation');
  });

  it('detects orchestration', () => {
    const got = detectEntity({ id: 5, name: 'triage', version: 2 });
    expect(got?.kind).toBe('orchestration');
  });
});
