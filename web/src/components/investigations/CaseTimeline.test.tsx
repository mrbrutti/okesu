import { describe, it, expect, afterEach, beforeAll, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseTimeline } from './CaseTimeline';
import type { InvestigationDetail } from '../../api';

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
