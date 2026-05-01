import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import { Tooltip } from './Tooltip';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('Tooltip uncontrolled', () => {
  it('shows on mouseenter, hides on mouseleave', () => {
    render(
      <Tooltip content={<span>tip body</span>}>
        <button>anchor</button>
      </Tooltip>,
    );
    expect(screen.queryByText('tip body')).toBeNull();
    fireEvent.mouseEnter(screen.getByRole('button'));
    expect(screen.getByText('tip body')).toBeTruthy();
    fireEvent.mouseLeave(screen.getByRole('button'));
    expect(screen.queryByText('tip body')).toBeNull();
  });

  it('honors hover delay', () => {
    vi.useFakeTimers();
    render(
      <Tooltip content={<span>tip</span>} delay={250}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.mouseEnter(screen.getByRole('button'));
    expect(screen.queryByText('tip')).toBeNull();
    act(() => { vi.advanceTimersByTime(250); });
    expect(screen.getByText('tip')).toBeTruthy();
  });

  it('shows on focus and hides on blur', () => {
    render(
      <Tooltip content={<span>tip</span>}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.focus(screen.getByRole('button'));
    expect(screen.getByText('tip')).toBeTruthy();
    fireEvent.blur(screen.getByRole('button'));
    expect(screen.queryByText('tip')).toBeNull();
  });

  it('renders content into document.body via portal', () => {
    render(
      <Tooltip content={<span data-testid="tip-body">tip</span>}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.mouseEnter(screen.getByRole('button'));
    const tip = screen.getByTestId('tip-body');
    // Walk up: tip is inside a portaled <div> that sits as a direct
    // child of document.body.
    let node: HTMLElement | null = tip;
    let foundBodyChild = false;
    while (node && node.parentElement) {
      if (node.parentElement === document.body) { foundBodyChild = true; break; }
      node = node.parentElement;
    }
    expect(foundBodyChild).toBe(true);
  });
});

describe('Tooltip controlled', () => {
  it('renders only when open=true', () => {
    const { rerender } = render(
      <Tooltip content={<span>tip</span>} open={false} onOpenChange={() => {}}>
        <button>anchor</button>
      </Tooltip>,
    );
    expect(screen.queryByText('tip')).toBeNull();
    rerender(
      <Tooltip content={<span>tip</span>} open={true} onOpenChange={() => {}}>
        <button>anchor</button>
      </Tooltip>,
    );
    expect(screen.getByText('tip')).toBeTruthy();
  });

  it('outside-click fires onOpenChange(false)', () => {
    const onOpenChange = vi.fn();
    render(
      <div>
        <button data-testid="outside">outside</button>
        <Tooltip content={<span>tip</span>} open={true} onOpenChange={onOpenChange}>
          <button>anchor</button>
        </Tooltip>
      </div>,
    );
    fireEvent.mouseDown(screen.getByTestId('outside'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('Escape fires onOpenChange(false)', () => {
    const onOpenChange = vi.fn();
    render(
      <Tooltip content={<span>tip</span>} open={true} onOpenChange={onOpenChange}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('does not show on hover when in controlled mode', () => {
    const onOpenChange = vi.fn();
    render(
      <Tooltip content={<span>tip</span>} open={false} onOpenChange={onOpenChange}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.mouseEnter(screen.getByRole('button'));
    expect(screen.queryByText('tip')).toBeNull();
  });
});
