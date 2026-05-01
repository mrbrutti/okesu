import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

afterEach(() => {
  cleanup();
});
import { FindingChip } from './FindingChip';
import { IOCChip } from './IOCChip';
import { NodeChip } from './NodeChip';
import { DaimonChip } from './DaimonChip';
import { RunChip } from './RunChip';
import { InvestigationChip } from './InvestigationChip';
import { OrchestrationChip } from './OrchestrationChip';

describe('chips render', () => {
  it('FindingChip shows title + severity', () => {
    render(<FindingChip snapshot={{ id: 42, title: 'Cron', severity: 'HIGH', category: 'process' }} />);
    expect(screen.getByText(/Cron/)).toBeTruthy();
    expect(screen.getByText('HIGH')).toBeTruthy();
  });

  it('IOCChip shows kind + last4', () => {
    render(<IOCChip snapshot={{ kind: 'sha256', value: 'a'.repeat(64), last4: 'aaaa' }} />);
    expect(screen.getByText(/sha256/)).toBeTruthy();
    expect(screen.getByText(/aaaa/)).toBeTruthy();
  });

  it('NodeChip shows hostname when no name', () => {
    render(<NodeChip snapshot={{ hostname: 'edr-1.lab', status: 'online' }} />);
    expect(screen.getByText('edr-1.lab')).toBeTruthy();
  });

  it('DaimonChip shows name', () => {
    render(<DaimonChip snapshot={{ name: 'edr-agent', host: 'h1' }} />);
    expect(screen.getByText('edr-agent')).toBeTruthy();
  });

  it('RunChip shows Run #id and status', () => {
    render(<RunChip snapshot={{ id: 99, status: 'completed' }} />);
    expect(screen.getByText(/Run #99/)).toBeTruthy();
    expect(screen.getByText(/completed/)).toBeTruthy();
  });

  it('InvestigationChip shows title and severity', () => {
    render(<InvestigationChip snapshot={{ id: 3, title: 'inc', severity: 'HIGH' }} />);
    expect(screen.getByText(/inc/)).toBeTruthy();
    expect(screen.getByText('HIGH')).toBeTruthy();
  });

  it('OrchestrationChip shows name and version', () => {
    render(<OrchestrationChip snapshot={{ id: 5, name: 'triage', version: 2 }} />);
    expect(screen.getByText('triage')).toBeTruthy();
    expect(screen.getByText(/v2/)).toBeTruthy();
  });
});
