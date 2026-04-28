// Multi-node binary update. Same SSH credentials apply to every selected
// node — operators typically use a single bastion-style key across the
// fleet, so prompting once and fanning out is the right shape.
//
// Job log streaming is intentionally NOT done here; with N nodes that
// would be an N-way SSE storm. Instead we list per-node terminal status
// (succeeded / failed / pending) once each job resolves. Operators
// who want a live log open the single-node dialog from the row.

import { useEffect, useState } from 'react';
import { Loader2, Upload, X, CheckCircle2, AlertCircle } from 'lucide-react';
import { api, subscribeJobLog, type NodeItem } from '../api';
import { cn } from '../lib/cn';

type JobState = 'pending' | 'running' | 'succeeded' | 'failed';
type Row = {
  node: NodeItem;
  state: JobState;
  message?: string;
  jobId?: string;
};

export function BulkBinaryUpdateDialog({
  nodes, onClose, onDone,
}: {
  nodes: NodeItem[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [sudoPassword, setSudoPassword] = useState('');
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rows, setRows] = useState<Row[]>(nodes.map((n) => ({ node: n, state: 'pending' })));

  // Track unsubscribe handles so we can tear down all SSE streams when
  // the dialog closes (or when every job has terminated).
  const [unsubs, setUnsubs] = useState<Array<() => void>>([]);
  useEffect(() => () => { for (const u of unsubs) u(); }, [unsubs]);

  async function start() {
    if (!privateKey) {
      setError('private key is required');
      return;
    }
    setRunning(true); setError(null);
    setRows((prev) => prev.map((r) => ({ ...r, state: 'running', message: 'queued' })));

    const fired: Array<() => void> = [];
    const results = await Promise.allSettled(
      nodes.map((n) =>
        api.updateNodeBinary(n.id, {
          private_key: privateKey,
          passphrase: passphrase || undefined,
          sudo_password: sudoPassword || undefined,
        }),
      ),
    );

    // Wire each successful job up to its log stream so we know when it
    // terminates (succeeded / failed). Failed POSTs (e.g. unreachable
    // host before the job even started) are marked failed immediately.
    setRows((prev) => prev.map((r, i) => {
      const res = results[i];
      if (res.status === 'rejected') {
        return { ...r, state: 'failed', message: String(res.reason) };
      }
      const jobId = res.value.job_id;
      const unsub = subscribeJobLog(
        jobId,
        () => { /* drop log lines — no live tail in bulk mode */ },
        (status) => {
          setRows((cur) => cur.map((cr) =>
            cr.node.id === r.node.id
              ? { ...cr, state: status === 'succeeded' ? 'succeeded' : 'failed', message: status }
              : cr,
          ));
        },
      );
      fired.push(unsub);
      return { ...r, jobId, state: 'running', message: 'updating…' };
    }));
    setUnsubs(fired);
  }

  const total = rows.length;
  const succeeded = rows.filter((r) => r.state === 'succeeded').length;
  const failed = rows.filter((r) => r.state === 'failed').length;
  const inflight = rows.filter((r) => r.state === 'running').length;
  const allDone = running && inflight === 0;

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl max-h-[85vh] flex flex-col">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold flex items-center gap-2">
              <Upload size={14} className="text-brand-500" />
              Update binary on {total} node{total === 1 ? '' : 's'}
            </h2>
            <p className="text-[11px] text-ink-mute mt-0.5">
              Same SSH credentials applied to every selected node. Each node runs the
              same update path as the single-node dialog (preserves <code>okesu.previous</code>,
              bounces every running daimon).
            </p>
          </div>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>

        <div className="flex-1 overflow-auto p-5 space-y-3 text-sm">
          {!running && (
            <>
              <Field label="SSH private key (PEM)">
                <textarea
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  rows={4}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
                  className="w-full px-2.5 py-1.5 text-xs font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white"
                />
                <p className="text-[11px] text-ink-mute mt-1">Used once for this batch. Not stored.</p>
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Passphrase (optional)">
                  <input
                    type="password"
                    value={passphrase}
                    onChange={(e) => setPassphrase(e.target.value)}
                    className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
                  />
                </Field>
                <Field label="Sudo password (optional)">
                  <input
                    type="password"
                    value={sudoPassword}
                    onChange={(e) => setSudoPassword(e.target.value)}
                    placeholder="blank for root / NOPASSWD"
                    className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
                  />
                </Field>
              </div>
            </>
          )}

          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}

          {running && (
            <div className="space-y-1">
              <div className="text-[11px] text-ink-dim flex items-center gap-3 mb-1">
                <span className="text-green-700">{succeeded} succeeded</span>
                {failed > 0 && <span className="text-red-700">{failed} failed</span>}
                {inflight > 0 && <span className="text-ink-dim">{inflight} in flight</span>}
              </div>
              {rows.map((r) => (
                <div key={r.node.id} className="flex items-center gap-2 text-xs px-2 py-1.5 border border-border rounded-md bg-white">
                  <span className="w-4 shrink-0">
                    {r.state === 'running' && <Loader2 size={12} className="animate-spin text-brand-500" />}
                    {r.state === 'succeeded' && <CheckCircle2 size={12} className="text-green-600" />}
                    {r.state === 'failed' && <AlertCircle size={12} className="text-red-600" />}
                  </span>
                  <span className="font-medium truncate flex-1">{r.node.name}</span>
                  <span className="font-mono text-[11px] text-ink-mute truncate">{r.message ?? ''}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button
            onClick={() => { if (allDone) onDone(); else onClose(); }}
            className="text-xs px-3 py-1.5 border border-border rounded-md"
          >
            {allDone ? 'Close' : 'Cancel'}
          </button>
          {!running && (
            <button
              onClick={start}
              disabled={!privateKey}
              className={cn(
                'text-xs px-3 py-1.5 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600',
              )}
            >
              <Upload size={12} />
              Update {total} node{total === 1 ? '' : 's'}
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
    </div>
  );
}
