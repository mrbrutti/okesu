import { describe, it, expect, afterEach, vi, beforeAll } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseGraph } from './CaseGraph';
import { api } from '../../api';

// jsdom does not implement ResizeObserver, but @xyflow/react relies
// on it inside ZoomPane. Install a no-op shim before any tests run.
beforeAll(() => {
  if (typeof globalThis.ResizeObserver === 'undefined') {
    class RO {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    (globalThis as unknown as { ResizeObserver: typeof RO }).ResizeObserver = RO;
  }
  if (typeof DOMRect === 'undefined' || typeof (globalThis as unknown as { DOMRect: { fromRect?: unknown } }).DOMRect.fromRect === 'undefined') {
    // Some xyflow internals call DOMRect.fromRect; jsdom omits it.
    const D = (globalThis as unknown as { DOMRect: { fromRect: (init?: { x?: number; y?: number; width?: number; height?: number }) => DOMRect } }).DOMRect;
    if (D && typeof D.fromRect !== 'function') {
      D.fromRect = (init = {}) => new DOMRect(init.x ?? 0, init.y ?? 0, init.width ?? 0, init.height ?? 0);
    }
  }
});

// vi.mock is hoisted to the top of the file by vitest. Keeping it
// at module scope (not nested in beforeEach) reflects actual order
// of execution. See CaseTimeline.test.tsx for the same pattern.
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        graph: vi.fn(),
      },
    },
  };
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const baseGraph = {
  total_findings: 1,
  limit_applied: 20,
  nodes: [
    { id: 'f:1', kind: 'finding' as const, label: 'Cron', severity: 'HIGH' },
    { id: 'h:edr-1', kind: 'host' as const, label: 'edr-1' },
  ],
  edges: [{ id: 'f:1|h:edr-1', source: 'f:1', target: 'h:edr-1' }],
};

describe('CaseGraph', () => {
  it('renders empty-state message when bundleFindingsCount === 0', () => {
    render(
      <MemoryRouter>
        <CaseGraph investigationID={1} bundleFindingsCount={0} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/no findings linked yet/i)).toBeTruthy();
  });

  it('fetches and renders nodes from the api', async () => {
    (api.investigations.graph as ReturnType<typeof vi.fn>).mockResolvedValue(baseGraph);
    render(
      <MemoryRouter>
        <CaseGraph investigationID={1} bundleFindingsCount={1} />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText(/Cron/)).toBeTruthy();
    });
    expect(screen.getByText(/edr-1/)).toBeTruthy();
  });

  it('shows the cap banner when total_findings > limit_applied', async () => {
    (api.investigations.graph as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseGraph,
      total_findings: 42,
    });
    render(
      <MemoryRouter>
        <CaseGraph investigationID={1} bundleFindingsCount={42} />
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByText(/20 of 42 findings shown/i)).toBeTruthy();
    });
  });

  it('clicking a finding node fires entity:open', async () => {
    (api.investigations.graph as ReturnType<typeof vi.fn>).mockResolvedValue(baseGraph);
    const events: unknown[] = [];
    const handler = (e: Event) => events.push((e as CustomEvent).detail);
    window.addEventListener('entity:open', handler);
    try {
      render(
        <MemoryRouter>
          <CaseGraph investigationID={1} bundleFindingsCount={1} />
        </MemoryRouter>,
      );
      await waitFor(() => screen.getByText(/Cron/));
      fireEvent.click(screen.getByText(/Cron/));
      expect(events[0]).toMatchObject({ kind: 'finding', identityKey: '1' });
    } finally {
      window.removeEventListener('entity:open', handler);
    }
  });
});
