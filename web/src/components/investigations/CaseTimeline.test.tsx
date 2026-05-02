import { describe, it, expect, afterEach, beforeAll, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseTimeline } from './CaseTimeline';
import type { InvestigationDetail } from '../../api';
import { api } from '../../api';

// Node 25 ships an experimental top-level `localStorage` that is not
// usable without `--localstorage-file`; it shadows jsdom's
// implementation. Install a small in-memory shim on `window` and
// `globalThis` so the component-under-test (which uses the bare
// `localStorage` global) sees a working Storage.
beforeAll(() => {
  const store = new Map<string, string>();
  const shim = {
    getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => { store.clear(); },
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() { return store.size; },
  } as unknown as Storage;
  Object.defineProperty(window, 'localStorage', { value: shim, configurable: true });
  Object.defineProperty(globalThis, 'localStorage', { value: shim, configurable: true });
});

// The audit lane fetches lazily; mock the api.investigations.audit
// call so toggling audit on doesn't break tests. vi.mock is hoisted
// to the top of the file, so we declare it at module scope rather
// than inside beforeEach.
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        audit: vi.fn().mockResolvedValue([]),
      },
      savedSearches: {
        ...actual.api.savedSearches,
        list: vi.fn().mockResolvedValue([]),
        create: vi.fn(),
        update: vi.fn(),
        delete: vi.fn(),
      },
    },
  };
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});

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

describe('CaseTimeline (D1 skeleton)', () => {
  it('renders four default lanes (lifecycle/findings/runs/notes) on an empty bundle', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    // Lanes appear as labels on the timeline OR as toggle buttons.
    // Toggle bar is always present, so we use the buttons:
    expect(screen.getByRole('button', { name: /^lifecycle$/i }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('button', { name: /^findings$/i }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('button', { name: /^runs$/i }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('button', { name: /^notes$/i }).getAttribute('aria-pressed')).toBe('true');
  });

  it('renders empty-state hint when no events exist beyond the lifecycle "created" marker', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    expect(screen.getByText(/no signals yet/i)).toBeTruthy();
  });

  it('toggle bar starts with iocs/daimons/audit off (aria-pressed=false)', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    expect(screen.getByRole('button', { name: /^iocs$/i }).getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByRole('button', { name: /^daimons$/i }).getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByRole('button', { name: /^audit$/i }).getAttribute('aria-pressed')).toBe('false');
  });

  it('clicking iocs toggle persists to localStorage', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /^iocs$/i }));
    const stored = JSON.parse(localStorage.getItem('investigation:overview:lanes') ?? '[]');
    expect(stored).toContain('iocs');
  });
});

describe('CaseTimeline (D2 events)', () => {
  it('renders one finding dot per finding event with an aria-label including severity', () => {
    const b = bundle({
      findings: [
        { ID: 1, Ts: Date.parse('2026-04-30T11:00:00Z'), Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'CRITICAL', Valid: true }, Title: { String: 'crit', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    expect(screen.getByLabelText(/Finding #1.*CRITICAL/i)).toBeTruthy();
  });

  it('renders a run bar spanning startTs..endTs with status tone', () => {
    const b = bundle({
      runs: [
        { ID: 5, OrchestrationID: 100, OrchestrationName: { String: 'tri', Valid: true }, Status: 'completed', TriggerKind: 'auto', StartedAt: '2026-04-30T11:00:00Z', EndedAt: { String: '2026-04-30T11:30:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
      ],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    expect(screen.getByLabelText(/Run #5.*completed/i)).toBeTruthy();
  });

  it('clicking a finding dot fires entity:open event for the drawer', () => {
    const b = bundle({
      findings: [
        { ID: 7, Ts: Date.parse('2026-04-30T11:00:00Z'), Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const events: { kind: string; identityKey: string }[] = [];
    const handler = (e: Event) => {
      const ce = e as CustomEvent<{ kind: string; identityKey: string }>;
      events.push(ce.detail);
    };
    window.addEventListener('entity:open', handler);
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByLabelText(/Finding #7/));
    window.removeEventListener('entity:open', handler);
    expect(events[0]).toMatchObject({ kind: 'finding', identityKey: '7' });
  });
});

describe('CaseTimeline (clusters + tooltip)', () => {
  it('renders one cluster glyph for 5 findings inside the same x-bucket', () => {
    // 5 findings at very close timestamps so they fall into the same
    // 12px bucket regardless of zoom.
    const baseTs = Date.parse('2026-04-30T12:00:00Z');
    const b = bundle({
      findings: Array.from({ length: 5 }, (_, i) => ({
        ID: i + 1,
        Ts: baseTs + i * 1000, // 1s apart — well within one bucket at any reasonable zoom
        Agent: { String: 'a', Valid: true },
        Host: { String: 'h', Valid: true },
        Severity: { String: i === 0 ? 'CRITICAL' : 'LOW', Valid: true },
        Title: { String: `f${i}`, Valid: true },
        Status: { String: 'open', Valid: true },
        Tags: { String: '', Valid: false },
        Subtype: { String: '', Valid: false },
        LinkedAt: '',
        LinkMethod: { String: '', Valid: false },
        LinkedBy: { String: '', Valid: false },
      })),
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    // The cluster glyph carries an aria-label identifying it as a cluster.
    expect(screen.getByLabelText(/Cluster of 5 findings/i)).toBeTruthy();
    // No individual finding dot labels should be present.
    expect(screen.queryByLabelText(/Finding #1/)).toBeNull();
  });

  it('clicking a cluster glyph opens the popover with one row per finding', () => {
    const baseTs = Date.parse('2026-04-30T12:00:00Z');
    const b = bundle({
      findings: Array.from({ length: 4 }, (_, i) => ({
        ID: i + 10,
        Ts: baseTs + i * 1000,
        Agent: { String: 'a', Valid: true },
        Host: { String: 'h', Valid: true },
        Severity: { String: 'HIGH', Valid: true },
        Title: { String: `f${i + 10}`, Valid: true },
        Status: { String: 'open', Valid: true },
        Tags: { String: '', Valid: false },
        Subtype: { String: '', Valid: false },
        LinkedAt: '',
        LinkMethod: { String: '', Valid: false },
        LinkedBy: { String: '', Valid: false },
      })),
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByLabelText(/Cluster of 4 findings/i));
    // Popover content shows each finding's title.
    expect(screen.getByText(/f10/)).toBeTruthy();
    expect(screen.getByText(/f13/)).toBeTruthy();
  });

  it('clicking a row inside the cluster popover fires entity:open and closes the popover', () => {
    const baseTs = Date.parse('2026-04-30T12:00:00Z');
    const b = bundle({
      findings: Array.from({ length: 4 }, (_, i) => ({
        ID: i + 20,
        Ts: baseTs + i * 1000,
        Agent: { String: 'a', Valid: true },
        Host: { String: 'h', Valid: true },
        Severity: { String: 'LOW', Valid: true },
        Title: { String: `g${i + 20}`, Valid: true },
        Status: { String: 'open', Valid: true },
        Tags: { String: '', Valid: false },
        Subtype: { String: '', Valid: false },
        LinkedAt: '',
        LinkMethod: { String: '', Valid: false },
        LinkedBy: { String: '', Valid: false },
      })),
    });
    const events: { kind: string; identityKey: string }[] = [];
    const handler = (e: Event) => {
      events.push((e as CustomEvent<{ kind: string; identityKey: string }>).detail);
    };
    window.addEventListener('entity:open', handler);
    try {
      render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
      fireEvent.click(screen.getByLabelText(/Cluster of 4 findings/i));
      fireEvent.click(screen.getByText(/g22/));
      expect(events[0]).toMatchObject({ kind: 'finding', identityKey: '22' });
      // Popover closed: g22 no longer findable.
      expect(screen.queryByText(/g22/)).toBeNull();
    } finally {
      window.removeEventListener('entity:open', handler);
    }
  });
});

describe('CaseTimeline filter integration', () => {
  function makeFinding(id: number, severity: string, host: string) {
    return {
      ID: id,
      Severity: { Valid: true, String: severity },
      Title: { Valid: true, String: `f${id}` },
      Agent: { Valid: true, String: 'edr-agent' },
      Host: { Valid: true, String: host },
      Ts: 1700000000000 + id * 1000,
    } as unknown as InvestigationDetail['findings'][number];
  }

  it('clicking a severity chip narrows the rendered events to the matching ones', async () => {
    const b = bundle({
      findings: [makeFinding(1, 'CRITICAL', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(1);
    expect(screen.getByRole('button', { name: /Finding #1/ })).toBeTruthy();
  });

  it('renders zero-matches banner when filter excludes every event', () => {
    const b = bundle({
      findings: [makeFinding(1, 'LOW', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(screen.getByText(/0 of \d+ events match/i)).toBeTruthy();
  });

  it('Clear restores all events', () => {
    const b = bundle({
      findings: [makeFinding(1, 'CRITICAL', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: /clear/i }));
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(2);
  });
});

describe('CaseTimeline default saved search', () => {
  function makeFinding(id: number, severity: string, host: string) {
    return {
      ID: id,
      Severity: { Valid: true, String: severity },
      Title: { Valid: true, String: `f${id}` },
      Agent: { Valid: true, String: 'edr-agent' },
      Host: { Valid: true, String: host },
      Ts: 1700000000000 + id * 1000,
    } as unknown as InvestigationDetail['findings'][number];
  }

  it('auto-applies the default saved search on mount', async () => {
    (api.savedSearches.list as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce([
      {
        id: 5, user_id: 1, name: 'crit-only', scope: 'investigation_timeline',
        config_json: JSON.stringify({ severities: ['CRITICAL'] }),
        is_default: true,
        created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z',
      },
    ]);
    const b = bundle({
      findings: [makeFinding(1, 'CRITICAL', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    // Wait one tick so the savedSearches.list promise resolves.
    await new Promise((r) => setTimeout(r, 0));
    // Only the CRITICAL finding should still be on the timeline.
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(1);
    expect(screen.getByRole('button', { name: /Finding #1/ })).toBeTruthy();
  });
});
