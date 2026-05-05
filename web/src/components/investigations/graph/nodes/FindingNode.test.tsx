import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { FindingNode } from './FindingNode';

afterEach(cleanup);

const baseProps = {
  id: 'f:42',
  type: 'finding',
  position: { x: 0, y: 0 },
  data: {
    id: 'f:42',
    numericID: 42,
    title: 'Suspicious cron job spawning tcp connections',
    severity: 'CRITICAL',
    host: 'edr-fedora-3',
    agent: 'edr-agent',
  },
  selected: false,
  zIndex: 0,
  isConnectable: false,
  xPos: 0,
  yPos: 0,
  dragging: false,
} as any;

function withProvider(ui: React.ReactNode) {
  return <ReactFlowProvider>{ui}</ReactFlowProvider>;
}

describe('FindingNode', () => {
  it('renders the title, severity badge, agent, and host', () => {
    render(withProvider(<FindingNode {...baseProps} />));
    expect(screen.getByText(/Suspicious cron job/)).toBeTruthy();
    expect(screen.getByText('CRITICAL')).toBeTruthy();
    expect(screen.getByText('edr-agent')).toBeTruthy();
    expect(screen.getByText('edr-fedora-3')).toBeTruthy();
    expect(screen.getByText(/^#42$/)).toBeTruthy();
  });

  it('uses severity-critical class for CRITICAL', () => {
    render(withProvider(<FindingNode {...baseProps} />));
    const badge = screen.getByText('CRITICAL');
    expect(badge.className).toContain('severity-critical');
  });

  it('falls back to severity-info for missing severity', () => {
    const props = { ...baseProps, data: { ...baseProps.data, severity: '' } };
    render(withProvider(<FindingNode {...props} />));
    const badge = screen.getByText('INFO');
    expect(badge.className).toContain('severity-info');
  });

  it('shows "(no title)" when title is empty', () => {
    const props = { ...baseProps, data: { ...baseProps.data, title: '' } };
    render(withProvider(<FindingNode {...props} />));
    expect(screen.getByText('(no title)')).toBeTruthy();
  });

  it('omits agent chip when agent is empty', () => {
    const props = { ...baseProps, data: { ...baseProps.data, agent: '' } };
    render(withProvider(<FindingNode {...props} />));
    expect(screen.queryByText('edr-agent')).toBeNull();
  });
});
