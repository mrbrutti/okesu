import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import EntityDrawerHost from './EntityDrawerHost';

afterEach(cleanup);

// Mock FindingDrawer to a sentinel so we can assert it gets rendered
// without pulling in the real Findings page (which has its own
// network deps).
vi.mock('../pages/Findings', () => ({
  FindingDrawer: ({ id }: { id: number }) => <div data-testid="finding-drawer">id={id}</div>,
}));

describe('EntityDrawerHost', () => {
  it('renders nothing initially', () => {
    render(<MemoryRouter><EntityDrawerHost /></MemoryRouter>);
    expect(screen.queryByTestId('finding-drawer')).toBeNull();
  });

  it('opens FindingDrawer on entity:open event with kind=finding', () => {
    render(<MemoryRouter><EntityDrawerHost /></MemoryRouter>);
    act(() => {
      window.dispatchEvent(new CustomEvent('entity:open', {
        detail: { kind: 'finding', identityKey: '42' },
      }));
    });
    expect(screen.getByTestId('finding-drawer')).toBeTruthy();
    expect(screen.getByText('id=42')).toBeTruthy();
  });

  it('ignores non-finding kinds (no drawer opens)', () => {
    render(<MemoryRouter><EntityDrawerHost /></MemoryRouter>);
    act(() => {
      window.dispatchEvent(new CustomEvent('entity:open', {
        detail: { kind: 'ioc', identityKey: 'sha256:abc' },
      }));
    });
    expect(screen.queryByTestId('finding-drawer')).toBeNull();
  });

  it('ignores entity:open with non-numeric identityKey for finding', () => {
    render(<MemoryRouter><EntityDrawerHost /></MemoryRouter>);
    act(() => {
      window.dispatchEvent(new CustomEvent('entity:open', {
        detail: { kind: 'finding', identityKey: 'not-a-number' },
      }));
    });
    expect(screen.queryByTestId('finding-drawer')).toBeNull();
  });
});
