import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseStructure } from './CaseStructure';
import { api, ApiError, type InvestigationDetail, type InvestigationStructure } from '../../api';

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  const { ApiError: ActualApiError } = actual;
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        structure: vi.fn().mockRejectedValue(new ActualApiError(404, 'not implemented')),
      },
    },
  };
});

afterEach(cleanup);

function makeBundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-05-01T00:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [],
    runs: [],
    iocs: [],
    daimons: [],
    orchestrations: [],
    notes: [],
    war_room: false,
    ...over,
  };
}

describe('CaseStructure', () => {
  it('renders four cards with zero counts on an empty bundle', () => {
    render(<MemoryRouter><CaseStructure bundle={makeBundle()} /></MemoryRouter>);
    expect(screen.getByText(/Hosts/i)).toBeTruthy();
    expect(screen.getByText(/IOCs/i)).toBeTruthy();
    expect(screen.getByText(/Daimons/i)).toBeTruthy();
    expect(screen.getByText(/Runs/i)).toBeTruthy();
    expect(screen.getAllByText(/no .* linked/i).length).toBe(4);
  });

  it('counts distinct hosts and surfaces the heavy hitter', () => {
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: 1, Agent: { String: 'a', Valid: true }, Host: { String: 'h1', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 2, Agent: { String: 'a', Valid: true }, Host: { String: 'h1', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 3, Ts: 3, Agent: { String: 'a', Valid: true }, Host: { String: 'h2', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    render(<MemoryRouter><CaseStructure bundle={bundle} /></MemoryRouter>);
    expect(screen.getByText(/Hosts \(2\)/)).toBeTruthy();
    expect(screen.getByText(/h1/)).toBeTruthy();
  });

  it('counts IOCs from bundle.iocs by ObservationCount', () => {
    const bundle = makeBundle({
      iocs: [
        { ID: 1, Kind: 'sha256', Value: 'a'.repeat(64), Severity: { String: 'HIGH', Valid: true }, ObservationCount: 5, HostCount: 2, FirstSeen: '', LastSeen: '' },
        { ID: 2, Kind: 'ip', Value: '1.2.3.4', Severity: { String: 'LOW', Valid: true }, ObservationCount: 23, HostCount: 4, FirstSeen: '', LastSeen: '' },
      ],
    });
    render(<MemoryRouter><CaseStructure bundle={bundle} /></MemoryRouter>);
    expect(screen.getByText(/IOCs \(2\)/)).toBeTruthy();
    expect(screen.getByText(/1\.2\.3\.4/)).toBeTruthy();
  });

  it('counts daimons by FindingCount with last-seen tie break', () => {
    const bundle = makeBundle({
      daimons: [
        { Agent: 'edr-agent', FindingCount: 9, LastSeenTs: 1000 },
        { Agent: 'compliance', FindingCount: 9, LastSeenTs: 2000 },
        { Agent: 'sre', FindingCount: 1, LastSeenTs: 500 },
      ],
    });
    render(<MemoryRouter><CaseStructure bundle={bundle} /></MemoryRouter>);
    expect(screen.getByText(/Daimons \(3\)/)).toBeTruthy();
    expect(screen.getByText(/compliance/)).toBeTruthy();
  });

  it('counts orchestrations and per-orchestration run-status breakdown', () => {
    const bundle = makeBundle({
      runs: [
        { ID: 1, OrchestrationID: 100, OrchestrationName: { String: 'triage', Valid: true }, Status: 'completed', TriggerKind: 'auto', StartedAt: '2026-05-01T00:00:00Z', EndedAt: { String: '2026-05-01T00:01:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
        { ID: 2, OrchestrationID: 100, OrchestrationName: { String: 'triage', Valid: true }, Status: 'completed', TriggerKind: 'auto', StartedAt: '2026-05-01T00:01:00Z', EndedAt: { String: '2026-05-01T00:02:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
        { ID: 3, OrchestrationID: 100, OrchestrationName: { String: 'triage', Valid: true }, Status: 'failed',    TriggerKind: 'auto', StartedAt: '2026-05-01T00:02:00Z', EndedAt: { String: '2026-05-01T00:03:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: 'oops', Valid: true }, LinkedAt: '' },
      ],
      orchestrations: [
        { OrchestrationID: { Int64: 100, Valid: true }, OrchestrationName: 'triage', RunCount: 3, Completed: 2, Failed: 1, Cancelled: 0, Running: 0, LastStartedAt: '2026-05-01T00:02:00Z' },
      ],
    });
    render(<MemoryRouter><CaseStructure bundle={bundle} /></MemoryRouter>);
    expect(screen.getByText(/Runs \(3 .* 1 orch/)).toBeTruthy();
    expect(screen.getByText(/triage/)).toBeTruthy();
    expect(screen.getByText(/2 ✓/)).toBeTruthy();
    expect(screen.getByText(/1 ✗/)).toBeTruthy();
  });
});

describe('CaseStructure (server-side path)', () => {
  it('renders host count from /structure response', async () => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      hosts:          [{ Host: 'edr-fedora-3', Count: 7 }, { Host: 'web-1', Count: 2 }],
      iocs:           [],
      daimons:        [],
      orchestrations: [],
    } satisfies InvestigationStructure);
    render(<MemoryRouter><CaseStructure bundle={makeBundle()} /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText(/Hosts \(2\)/)).toBeTruthy();
      expect(screen.getByText(/edr-fedora-3 — 7 findings/)).toBeTruthy();
    });
  });
});

describe('CaseStructure (fallback path)', () => {
  it('falls back to bundle-derived numbers on 404', async () => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new ApiError(404, 'not found'),
    );
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: 0, Severity: { Valid: true, String: 'HIGH' }, Title: { Valid: true, String: 't' }, Agent: { Valid: true, String: 'a' }, Host: { Valid: true, String: 'h-a' }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 0, Severity: { Valid: true, String: 'HIGH' }, Title: { Valid: true, String: 't' }, Agent: { Valid: true, String: 'a' }, Host: { Valid: true, String: 'h-a' }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ] as unknown as InvestigationDetail['findings'],
    });
    render(<MemoryRouter><CaseStructure bundle={bundle} /></MemoryRouter>);
    // Bundle-derived hosts: 1 distinct host with count 2.
    await waitFor(() => {
      expect(screen.getByText(/Hosts \(1\)/)).toBeTruthy();
      expect(screen.getByText(/h-a — 2 findings/)).toBeTruthy();
    });
  });

  it('falls back on network error', async () => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error('network down'),
    );
    render(<MemoryRouter><CaseStructure bundle={makeBundle()} /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText(/Hosts \(0\)/)).toBeTruthy();
    });
  });
});

describe('CaseStructure (invID change + cancellation)', () => {
  beforeEach(() => {
    (api.investigations.structure as ReturnType<typeof vi.fn>).mockClear();
  });

  it('refetches /structure when the invID prop changes', async () => {
    const m = api.investigations.structure as ReturnType<typeof vi.fn>;
    m.mockResolvedValueOnce({
      hosts:          [{ Host: 'first-host', Count: 1 }],
      iocs:           [], daimons: [], orchestrations: [],
    } satisfies InvestigationStructure);
    m.mockResolvedValueOnce({
      hosts:          [{ Host: 'second-host', Count: 2 }],
      iocs:           [], daimons: [], orchestrations: [],
    } satisfies InvestigationStructure);

    const b1 = makeBundle();
    b1.investigation.ID = 1;
    const { rerender } = render(<MemoryRouter><CaseStructure bundle={b1} /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText(/first-host/)).toBeTruthy());

    const b2 = makeBundle();
    b2.investigation.ID = 2;
    rerender(<MemoryRouter><CaseStructure bundle={b2} /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText(/second-host/)).toBeTruthy());

    expect(m).toHaveBeenCalledTimes(2);
    expect(m).toHaveBeenNthCalledWith(1, 1, undefined);
    expect(m).toHaveBeenNthCalledWith(2, 2, undefined);
  });

  it('discards a slow first response if the bundle changes before it lands', async () => {
    const m = api.investigations.structure as ReturnType<typeof vi.fn>;

    // First call resolves slowly with a "stale" host.
    let resolveSlow!: (v: InvestigationStructure) => void;
    const slow = new Promise<InvestigationStructure>((resolve) => { resolveSlow = resolve; });
    m.mockReturnValueOnce(slow);

    // Second call resolves immediately with a "fresh" host.
    m.mockResolvedValueOnce({
      hosts:          [{ Host: 'fresh-host', Count: 1 }],
      iocs:           [], daimons: [], orchestrations: [],
    } satisfies InvestigationStructure);

    const b1 = makeBundle();
    b1.investigation.ID = 1;
    const { rerender } = render(<MemoryRouter><CaseStructure bundle={b1} /></MemoryRouter>);

    const b2 = makeBundle();
    b2.investigation.ID = 2;
    rerender(<MemoryRouter><CaseStructure bundle={b2} /></MemoryRouter>);

    // Fresh fetch resolves first.
    await waitFor(() => expect(screen.getByText(/fresh-host/)).toBeTruthy());

    // Now resolve the slow first call. The cancelled flag should
    // prevent it from clobbering the fresh data.
    resolveSlow({
      hosts:          [{ Host: 'stale-host', Count: 99 }],
      iocs:           [], daimons: [], orchestrations: [],
    });
    // Give React one microtask to process if anything were to leak through.
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.queryByText(/stale-host/)).toBeNull();
    expect(screen.getByText(/fresh-host/)).toBeTruthy();
  });
});
