import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { DaimonNode } from './DaimonNode';

afterEach(cleanup);

function mkProps(label: string, finding_count: number) {
  return {
    id: `d:${label}`,
    type: 'daimon',
    position: { x: 0, y: 0 },
    data: { label, finding_count },
    selected: false,
    zIndex: 0,
    isConnectable: false,
    xPos: 0, yPos: 0, dragging: false,
  } as any;
}

describe('DaimonNode', () => {
  it('renders the agent label and count', () => {
    render(<ReactFlowProvider><DaimonNode {...mkProps('edr-agent', 7)} /></ReactFlowProvider>);
    expect(screen.getByText('edr-agent')).toBeTruthy();
    expect(screen.getByText(/daimon · 7 findings/)).toBeTruthy();
  });

  it('uses singular "finding" for count of 1', () => {
    render(<ReactFlowProvider><DaimonNode {...mkProps('sre-health', 1)} /></ReactFlowProvider>);
    expect(screen.getByText(/daimon · 1 finding$/)).toBeTruthy();
  });

  it('treats undefined count as 0', () => {
    const p = mkProps('edr-agent', 0);
    p.data.finding_count = undefined;
    render(<ReactFlowProvider><DaimonNode {...p} /></ReactFlowProvider>);
    expect(screen.getByText(/daimon · 0 findings/)).toBeTruthy();
  });
});
