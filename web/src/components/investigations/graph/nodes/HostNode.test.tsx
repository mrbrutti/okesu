import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { HostNode } from './HostNode';

afterEach(cleanup);

const props = {
  id: 'h:edr-fedora-3',
  type: 'host',
  position: { x: 0, y: 0 },
  data: { label: 'edr-fedora-3' },
  selected: false,
  zIndex: 0,
  isConnectable: false,
  xPos: 0, yPos: 0, dragging: false,
} as any;

describe('HostNode', () => {
  it('renders hostname label', () => {
    render(<ReactFlowProvider><HostNode {...props} /></ReactFlowProvider>);
    expect(screen.getByText('edr-fedora-3')).toBeTruthy();
  });

  it('renders the "host" subtext', () => {
    render(<ReactFlowProvider><HostNode {...props} /></ReactFlowProvider>);
    expect(screen.getByText('host')).toBeTruthy();
  });
});
