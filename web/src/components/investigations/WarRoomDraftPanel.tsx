// WarRoomDraftPanel — collaborative draft composer mounted under
// NotesPanel. Textarea bound to a Yjs Y.Text via the textarea
// binding; presence chips driven by awareness; Send POSTs to
// /finalize and closes the panel on success.

import { useEffect, useMemo, useState } from 'react';
import { applyTextDelta } from './warRoomDraft/textareaBinding';
import type { YjsConnection } from './warRoomDraft/yjsConnection';

interface Props {
  conn: YjsConnection;
  currentEmail: string;
  onClose: () => void;
}

export function WarRoomDraftPanel({ conn, currentEmail, onClose }: Props) {
  const [text, setText] = useState<string>(() => conn.ydoc.getText('body').toString());
  const [participants, setParticipants] = useState(conn.participants);
  const [status, setStatus] = useState(conn.status);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Sync Yjs → React state for the textarea value.
  useEffect(() => {
    const body = conn.ydoc.getText('body');
    const observer = () => setText(body.toString());
    body.observe(observer);
    return () => body.unobserve(observer);
  }, [conn.ydoc]);

  // Subscribe to status / awareness changes through the connection wrapper.
  useEffect(() => {
    return conn.subscribe(() => {
      setParticipants(conn.participants);
      setStatus(conn.status);
    });
  }, [conn]);

  function onTextareaChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const desired = e.target.value;
    setText(desired); // optimistic local
    const body = conn.ydoc.getText('body');
    applyTextDelta(body, desired);
  }

  async function send() {
    if (busy || status !== 'connected') return;
    setBusy(true); setError(null);
    try {
      await conn.sendFinalize(currentEmail);
      conn.close();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  function cancel() {
    conn.close();
    onClose();
  }

  // Deterministic chip color from a hash of the email.
  const chipColor = (email: string) => {
    let h = 0;
    for (let i = 0; i < email.length; i++) h = (h * 31 + email.charCodeAt(i)) | 0;
    const hue = ((h % 360) + 360) % 360;
    return `hsl(${hue}, 60%, 45%)`;
  };

  const chips = useMemo(() => participants.map((p) => (
    <span key={p.clientID} className="inline-flex items-center gap-1 text-[11px] px-2 py-0.5 rounded-full border border-border">
      <span className="w-2 h-2 rounded-full" style={{ backgroundColor: chipColor(p.email) }} />
      <span className="font-mono">{p.email === currentEmail ? `you (${p.email})` : p.email}</span>
    </span>
  )), [participants, currentEmail]);

  return (
    <div className="border border-border rounded-md bg-white p-3 space-y-2 mt-3">
      <div className="flex items-center gap-2 flex-wrap">
        {chips}
        <span className={`ml-auto text-[11px] ${status === 'connected' ? 'text-emerald-700' : 'text-amber-700'}`}>
          {status}
        </span>
      </div>
      <textarea
        value={text}
        onChange={onTextareaChange}
        rows={6}
        className="w-full px-2.5 py-1.5 rounded-md border border-border text-sm focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        placeholder="Draft together — observations, hypotheses, next steps…"
      />
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] text-ink-mute">
          Plain text · {text.length} chars
        </span>
        <div className="flex items-center gap-2">
          {error && <span className="text-[11px] text-red-700">{error}</span>}
          <button onClick={cancel} className="text-xs px-2.5 py-1 rounded-md border border-border hover:bg-slate-50">
            Cancel
          </button>
          <button
            onClick={send}
            disabled={busy || status !== 'connected'}
            className="text-xs px-2.5 py-1 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
          >
            Send
          </button>
        </div>
      </div>
    </div>
  );
}
