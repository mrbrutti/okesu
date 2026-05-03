import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { connectWarRoomDraft } from './yjsConnection';

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static lastURL = '';

  url: string;
  readyState = 0;
  onopen?: () => void;
  onmessage?: (e: { data: ArrayBufferLike }) => void;
  onclose?: () => void;
  onerror?: () => void;

  static OPEN = 1;
  static CLOSED = 3;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.lastURL = url;
    FakeWebSocket.instances.push(this);
  }
  send = vi.fn();
  close = vi.fn(() => {
    this.readyState = 3;
    if (this.onclose) this.onclose();
  });

  open() { this.readyState = 1; if (this.onopen) this.onopen(); }
  receive(data: Uint8Array) { if (this.onmessage) this.onmessage({ data: data.buffer }); }
}

beforeEach(() => {
  FakeWebSocket.instances = [];
  // @ts-expect-error: replace global WebSocket
  global.WebSocket = FakeWebSocket;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('connectWarRoomDraft', () => {
  it('starts in connecting state', () => {
    const c = connectWarRoomDraft(1, undefined, { email: 'a@x', displayName: 'a' });
    expect(c.status).toBe('connecting');
    c.close();
  });

  it('transitions to connected when the WS opens', () => {
    const c = connectWarRoomDraft(1, undefined, { email: 'a@x', displayName: 'a' });
    FakeWebSocket.instances[0].open();
    expect(c.status).toBe('connected');
    c.close();
  });

  it('reconnects on unexpected close with backoff', async () => {
    vi.useFakeTimers();
    const c = connectWarRoomDraft(1, undefined, { email: 'a@x', displayName: 'a' });
    FakeWebSocket.instances[0].open();
    FakeWebSocket.instances[0].close(); // unexpected close
    expect(FakeWebSocket.instances).toHaveLength(1); // not reconnected immediately

    vi.advanceTimersByTime(300); // first backoff is 250ms
    await Promise.resolve();
    expect(FakeWebSocket.instances).toHaveLength(2);
    c.close();
    vi.useRealTimers();
  });

  it('builds the expected WS URL with cp param', () => {
    Object.defineProperty(window, 'location', {
      value: { protocol: 'http:', host: 'localhost:8080' },
      configurable: true,
    });
    const c = connectWarRoomDraft(42, 'remote-cp', { email: 'a@x', displayName: 'a' });
    expect(FakeWebSocket.lastURL).toBe('ws://localhost:8080/api/investigations/42/draft/ws?cp=remote-cp');
    c.close();
  });
});
