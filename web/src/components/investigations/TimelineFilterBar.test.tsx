import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { TimelineFilterBar } from './TimelineFilterBar';
import type { InvestigationDetail } from '../../api';

afterEach(cleanup);

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-04-30T10:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

describe('TimelineFilterBar', () => {
  it('renders five severity chips, four run-status chips, host & agent inputs, and Clear', () => {
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={() => {}} />);
    expect(screen.getByRole('button', { name: /^CRITICAL$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^HIGH$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^MEDIUM$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^LOW$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^INFO$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^completed$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^failed$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^cancelled$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^running$/ })).toBeTruthy();
    expect(screen.getByLabelText(/host/i)).toBeTruthy();
    expect(screen.getByLabelText(/agent/i)).toBeTruthy();
    expect(screen.getByRole('button', { name: /clear/i })).toBeTruthy();
  });

  it('clicking a severity chip calls onChange with that severity added', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(onChange).toHaveBeenCalledWith({ severities: ['CRITICAL'] });
  });

  it('clicking an already-selected severity chip removes it', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{ severities: ['CRITICAL', 'HIGH'] }} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(onChange).toHaveBeenCalledWith({ severities: ['HIGH'] });
  });

  it('removing the last severity drops the field instead of leaving an empty array', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{ severities: ['LOW'] }} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^LOW$/ }));
    expect(onChange).toHaveBeenCalledWith({ severities: undefined });
  });

  it('typing in the host input calls onChange with that host', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText(/host/i), { target: { value: 'edr-fedora-3' } });
    expect(onChange).toHaveBeenCalledWith({ host: 'edr-fedora-3' });
  });

  it('clearing the host input drops the field', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{ host: 'edr-fedora-3' }} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText(/host/i), { target: { value: '' } });
    expect(onChange).toHaveBeenCalledWith({ host: undefined });
  });

  it('clicking a run-status chip toggles run_statuses', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^failed$/ }));
    expect(onChange).toHaveBeenCalledWith({ run_statuses: ['failed'] });
  });

  it('Clear button calls onChange({})', () => {
    const onChange = vi.fn();
    render(
      <TimelineFilterBar
        bundle={bundle()}
        filter={{ severities: ['HIGH'], host: 'h', agent: 'a', run_statuses: ['failed'] }}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /clear/i }));
    expect(onChange).toHaveBeenCalledWith({});
  });

  it('trims surrounding whitespace from host input before storing', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText(/host/i), { target: { value: '  edr-fedora-3  ' } });
    expect(onChange).toHaveBeenCalledWith({ host: 'edr-fedora-3' });
  });
});
