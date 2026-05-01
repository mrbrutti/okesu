import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseStatusBar } from './CaseStatusBar';
import type { InvestigationDetail } from '../../api';

afterEach(cleanup);

function makeBundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 7, Title: 't', Status: 'active', Resolution: '', Summary: 'working theory',
      CreatedBy: 'me', CreatedAt: '2026-04-28T00:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

describe('CaseStatusBar', () => {
  it('renders status, severity histogram, freshness, counts, summary on empty case', () => {
    render(
      <MemoryRouter>
        <CaseStatusBar bundle={makeBundle()} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/active/i)).toBeTruthy();
    expect(screen.getByText(/no findings linked yet/i)).toBeTruthy();
    expect(screen.getByText(/working theory/)).toBeTruthy();
  });

  it('severity histogram counts CRITICAL+HIGH+LOW correctly', () => {
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: 1, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'CRITICAL', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 2, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 3, Ts: 3, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    render(<MemoryRouter><CaseStatusBar bundle={bundle} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} /></MemoryRouter>);
    expect(screen.getByLabelText('CRITICAL: 1')).toBeTruthy();
    expect(screen.getByLabelText('HIGH: 1')).toBeTruthy();
    expect(screen.getByLabelText('LOW: 1')).toBeTruthy();
  });

  it('renders war-room badge when war_room is true', () => {
    const bundle = makeBundle({ war_room: true });
    render(<MemoryRouter><CaseStatusBar bundle={bundle} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} /></MemoryRouter>);
    expect(screen.getByText(/war.room/i)).toBeTruthy();
  });

  it('freshness reports the most recent of finding/note/run timestamps', () => {
    vi.setSystemTime(new Date('2026-05-01T01:00:00Z'));
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: Date.parse('2026-04-30T22:00:00Z'), Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
      notes: [
        { ID: 1, InvestigationID: 7, Author: 'me', Body: '...', CreatedAt: '2026-05-01T00:30:00Z' },
      ],
    });
    render(<MemoryRouter><CaseStatusBar bundle={bundle} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} /></MemoryRouter>);
    expect(screen.getByText(/30m ago/i)).toBeTruthy();
    vi.useRealTimers();
  });

  it('Edit button on Summary toggles editing prop', () => {
    const onEditToggle = vi.fn();
    render(<MemoryRouter><CaseStatusBar bundle={makeBundle()} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={onEditToggle} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /edit/i }));
    expect(onEditToggle).toHaveBeenCalledTimes(1);
  });
});
