import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { WarRoomDraftPanel } from './WarRoomDraftPanel';
import type { YjsConnection, Participant } from './warRoomDraft/yjsConnection';
import * as Y from 'yjs';
import { Awareness } from 'y-protocols/awareness';

afterEach(cleanup);

function fakeConn(over: Partial<YjsConnection> = {}): YjsConnection {
  const ydoc = new Y.Doc();
  const awareness = new Awareness(ydoc);
  const subs = new Set<() => void>();
  return {
    ydoc,
    awareness,
    status: 'connected',
    participants: [],
    sendFinalize: vi.fn().mockResolvedValue({ noteID: 99 }),
    close: vi.fn(),
    subscribe: (cb: () => void) => { subs.add(cb); return () => { subs.delete(cb); }; },
    ...over,
  } as YjsConnection;
}

describe('WarRoomDraftPanel', () => {
  it('renders presence chips for connected participants', () => {
    const conn = fakeConn({
      participants: [
        { clientID: 1, email: 'alice@x', displayName: 'alice' },
        { clientID: 2, email: 'bob@x', displayName: 'bob' },
      ] as Participant[],
    });
    render(<WarRoomDraftPanel conn={conn} currentEmail="bob@x" onClose={() => {}} />);
    expect(screen.getByText(/alice/i)).toBeTruthy();
    expect(screen.getByText(/bob/i)).toBeTruthy();
  });

  it('disables Send when status is connecting', () => {
    const conn = fakeConn({ status: 'connecting' });
    render(<WarRoomDraftPanel conn={conn} currentEmail="bob@x" onClose={() => {}} />);
    const send = screen.getByRole('button', { name: /send/i });
    expect((send as HTMLButtonElement).disabled).toBe(true);
  });

  it('calls sendFinalize on Send and onClose on success', async () => {
    const onClose = vi.fn();
    const conn = fakeConn();
    conn.ydoc.getText('body').insert(0, 'hello world');
    render(<WarRoomDraftPanel conn={conn} currentEmail="bob@x" onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: /send/i }));
    // Wait for the promise chain.
    await new Promise((r) => setTimeout(r, 0));
    expect(conn.sendFinalize).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('Cancel calls conn.close and onClose', () => {
    const onClose = vi.fn();
    const conn = fakeConn();
    render(<WarRoomDraftPanel conn={conn} currentEmail="bob@x" onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    expect(conn.close).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });
});
