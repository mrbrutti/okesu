import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { IOCNode, shortValue } from './IOCNode';

afterEach(cleanup);

function mkProps(kind: string, value: string, obs = 5, hosts = 2) {
  return {
    id: `i:${kind}:${value}`,
    type: 'ioc',
    position: { x: 0, y: 0 },
    data: { ioc_kind: kind, ioc_value: value, obs_count: obs, host_count: hosts },
    selected: false,
    zIndex: 0,
    isConnectable: false,
    xPos: 0, yPos: 0, dragging: false,
  } as any;
}

describe('IOCNode', () => {
  it('renders kind in uppercase + truncated value + counts', () => {
    render(<ReactFlowProvider><IOCNode {...mkProps('sha256', 'a'.repeat(64))} /></ReactFlowProvider>);
    expect(screen.getByText(/^sha256$/i)).toBeTruthy();
    expect(screen.getByText(/5 obs/)).toBeTruthy();
    expect(screen.getByText(/2 hosts/)).toBeTruthy();
  });

  it('singular host', () => {
    render(<ReactFlowProvider><IOCNode {...mkProps('ip', '10.0.0.1', 3, 1)} /></ReactFlowProvider>);
    expect(screen.getByText(/1 host$/)).toBeTruthy();
  });
});

describe('shortValue', () => {
  it('passes through values ≤18 chars', () => {
    expect(shortValue('10.0.0.1')).toBe('10.0.0.1');
    expect(shortValue('a'.repeat(18))).toBe('a'.repeat(18));
  });

  it('elides long values to prefix…suffix (13 chars)', () => {
    const v = 'aaaaaa1234567890bbbbbb';
    const out = shortValue(v);
    expect(out.length).toBe(13);
    expect(out.startsWith('aaaaaa')).toBe(true);
    expect(out.endsWith('bbbbbb')).toBe(true);
    expect(out.includes('…')).toBe(true);
  });

  it('elides 64-char hash predictably', () => {
    const hash = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
    const out = shortValue(hash);
    expect(out).toBe('012345…abcdef');
  });
});
