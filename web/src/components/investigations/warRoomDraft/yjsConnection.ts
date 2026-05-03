// yjsConnection — wraps Y.Doc + WebSocket transport + Awareness.
// Encapsulates the y-websocket protocol details so the React
// component is purely UX. Reconnects with exponential backoff. The
// component talks only to the YjsConnection interface; tests mock
// it without dragging real Yjs into the component test environment.

import * as Y from 'yjs';
import { Awareness, encodeAwarenessUpdate, removeAwarenessStates, applyAwarenessUpdate } from 'y-protocols/awareness';
import * as syncProtocol from 'y-protocols/sync';
import * as encoding from 'lib0/encoding';
import * as decoding from 'lib0/decoding';
import { api } from '../../../api';

const MSG_SYNC = 0;
const MSG_AWARENESS = 1;
const MSG_SESSION_END = 2;

const BACKOFF_MS = [250, 500, 1000, 2000, 5000, 5000, 5000];

export interface Participant {
  clientID: number;
  email: string;
  displayName: string;
  cursor?: { from: number; to: number };
}

export interface YjsConnection {
  ydoc: Y.Doc;
  awareness: Awareness;
  status: 'connecting' | 'connected' | 'closed';
  participants: Participant[];
  sendFinalize: (author?: string) => Promise<{ noteID: number }>;
  close: () => void;
  /** Subscribe to status / participant changes; returns unsubscribe. */
  subscribe: (cb: () => void) => () => void;
}

export function connectWarRoomDraft(
  invID: number,
  cpInstanceID: string | undefined,
  user: { email: string; displayName: string },
): YjsConnection {
  const ydoc = new Y.Doc();
  const awareness = new Awareness(ydoc);
  awareness.setLocalStateField('user', user);

  let ws: WebSocket | null = null;
  let attempt = 0;
  let closed = false;
  let status: YjsConnection['status'] = 'connecting';
  const subscribers = new Set<() => void>();

  const notify = () => subscribers.forEach((cb) => cb());

  const url = api.investigations.draftWSURL(invID, cpInstanceID);

  const send = (msg: Uint8Array) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(msg);
    }
  };

  const onLocalUpdate = (update: Uint8Array, _origin: unknown) => {
    const enc = encoding.createEncoder();
    encoding.writeVarUint(enc, MSG_SYNC);
    syncProtocol.writeUpdate(enc, update);
    send(encoding.toUint8Array(enc));
  };
  ydoc.on('update', onLocalUpdate);

  const onAwarenessUpdate = ({
    added,
    updated,
    removed,
  }: {
    added: number[];
    updated: number[];
    removed: number[];
  }) => {
    const ids = added.concat(updated, removed);
    const enc = encoding.createEncoder();
    encoding.writeVarUint(enc, MSG_AWARENESS);
    encoding.writeVarUint8Array(enc, encodeAwarenessUpdate(awareness, ids));
    send(encoding.toUint8Array(enc));
    notify();
  };
  awareness.on('update', onAwarenessUpdate);

  const handleMessage = (data: Uint8Array) => {
    const dec = decoding.createDecoder(data);
    const msgType = decoding.readVarUint(dec);
    switch (msgType) {
      case MSG_SYNC: {
        const enc = encoding.createEncoder();
        encoding.writeVarUint(enc, MSG_SYNC);
        syncProtocol.readSyncMessage(dec, enc, ydoc, null);
        // If the message was a step1 from the server (asking for our
        // state), the encoder now has a step2 reply to send back.
        if (encoding.length(enc) > 1) {
          send(encoding.toUint8Array(enc));
        }
        break;
      }
      case MSG_AWARENESS: {
        const update = decoding.readVarUint8Array(dec);
        applyAwarenessUpdate(awareness, update, null);
        notify();
        break;
      }
      case MSG_SESSION_END: {
        // Body is plain JSON containing { note_id }.
        // We do not parse it here — the component's finalize callback
        // already returned with the note ID. Just close the WS.
        status = 'closed';
        notify();
        if (ws) ws.close();
        break;
      }
    }
  };

  const reconnect = () => {
    if (closed) return;
    if (ws) {
      try { ws.close(); } catch { /* ignore */ }
    }
    status = 'connecting';
    notify();

    ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      attempt = 0;
      status = 'connected';
      // Send our state vector — y-protocols/sync step 1.
      const enc = encoding.createEncoder();
      encoding.writeVarUint(enc, MSG_SYNC);
      syncProtocol.writeSyncStep1(enc, ydoc);
      send(encoding.toUint8Array(enc));
      notify();
    };
    ws.onmessage = (e) => {
      const data = new Uint8Array(e.data as ArrayBuffer);
      handleMessage(data);
    };
    ws.onclose = () => {
      if (status === 'closed') return;
      status = 'connecting';
      notify();
      // Drop our awareness state on disconnect (others see us leave).
      removeAwarenessStates(awareness, [ydoc.clientID], null);
      const delay = BACKOFF_MS[Math.min(attempt, BACKOFF_MS.length - 1)];
      attempt += 1;
      setTimeout(reconnect, delay);
    };
    ws.onerror = () => { /* onclose will fire too */ };
  };

  reconnect();

  const conn: YjsConnection = {
    ydoc,
    awareness,
    get status() { return status; },
    get participants(): Participant[] {
      const out: Participant[] = [];
      awareness.getStates().forEach((state, clientID) => {
        if (typeof state !== 'object' || state == null) return;
        const u = (state as { user?: { email: string; displayName: string } }).user;
        if (!u) return;
        out.push({
          clientID,
          email: u.email,
          displayName: u.displayName,
          cursor: (state as { cursor?: { from: number; to: number } }).cursor,
        });
      });
      return out;
    },
    sendFinalize: async (author?: string) => {
      const r = await api.investigations.finalizeDraft(invID, { author }, cpInstanceID);
      return { noteID: r.note_id };
    },
    close: () => {
      closed = true;
      status = 'closed';
      ydoc.off('update', onLocalUpdate);
      awareness.off('update', onAwarenessUpdate);
      if (ws) ws.close();
      notify();
    },
    subscribe: (cb) => {
      subscribers.add(cb);
      return () => subscribers.delete(cb);
    },
  };
  return conn;
}
