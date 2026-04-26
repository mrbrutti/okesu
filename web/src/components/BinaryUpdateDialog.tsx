// Binary update / rollback dialog. Operator pastes their SSH key (same
// UX as the deploy form), submits, watches the job log stream live.
//
// Two modes — "update" uploads the CP's current daemon binary and
// preserves the previous one at /usr/local/bin/okesu.previous; "rollback"
// flips them back. Both bounce every okesu-agent@* unit on the host.

import { useEffect, useState } from 'react';
import { Loader2, Play, RotateCcw, Upload, X } from 'lucide-react';
import { api, subscribeJobLog, type NodeItem } from '../api';
import { cn } from '../lib/cn';

export type BinaryAction = 'update' | 'rollback';

export function BinaryUpdateDialog({
  node,
  action,
  onClose,
  onDone,
}: {
  node: NodeItem;
  action: BinaryAction;
  onClose: () => void;
  onDone: () => void;
}) {
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [logs, setLogs] = useState<string[]>([]);
  const [running, setRunning] = useState(false);
  const [doneStatus, setDoneStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    return () => {
      // No explicit unsubscribe — subscribeJobLog returns a cleanup that
      // runs on done, and this dialog being unmounted while a job is
      // running just means the operator closed the modal mid-run. The
      // job continues server-side; the next opener will see it via the
      // job-log replay (existing behaviour).
    };
  }, []);

  async function start() {
    if (!privateKey) {
      setError('private key is required');
      return;
    }
    setRunning(true); setError(null); setLogs([]); setDoneStatus(null);
    try {
      const fn = action === 'update' ? api.updateNodeBinary : api.rollbackNodeBinary;
      const { job_id } = await fn(node.id, {
        private_key: privateKey,
        passphrase: passphrase || undefined,
      });
      const unsubscribe = subscribeJobLog(
        job_id,
        (line) => setLogs((prev) => [...prev, line]),
        (status) => { setDoneStatus(status); setRunning(false); unsubscribe(); },
      );
    } catch (e) {
      setError(String(e));
      setRunning(false);
    }
  }

  const title = action === 'update' ? 'Update binary' : 'Roll back binary';
  const subtitle = action === 'update'
    ? 'Uploads the CP\'s current daemon binary to /usr/local/bin/okesu, saves the previous version as okesu.previous, and bounces every running daimon service.'
    : 'Restores /usr/local/bin/okesu.previous to /usr/local/bin/okesu and bounces every running daimon service. Fails if there\'s no previous binary on disk.';

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl max-h-[85vh] flex flex-col">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold flex items-center gap-2">
              {action === 'update' ? <Upload size={14} className="text-brand-500" /> : <RotateCcw size={14} className="text-yellow-600" />}
              {title} — {node.name}
            </h2>
            <p className="text-[11px] text-ink-mute font-mono mt-0.5">
              {node.ssh_user}@{node.hostname}{node.ssh_port !== 22 ? ':' + node.ssh_port : ''}
              {node.okesu_version ? ` · current: ${node.okesu_version}` : ''}
            </p>
          </div>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>

        <div className="flex-1 overflow-auto p-5 space-y-3 text-sm">
          <p className="text-xs text-ink-dim">{subtitle}</p>

          {!doneStatus && (
            <>
              <Field label="SSH private key (PEM)">
                <textarea
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  rows={5}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
                  className="w-full px-2.5 py-1.5 text-xs font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white"
                />
                <p className="text-[11px] text-ink-mute mt-1">Used once for this action. Not stored.</p>
              </Field>
              <Field label="Passphrase (optional)">
                <input
                  type="password"
                  value={passphrase}
                  onChange={(e) => setPassphrase(e.target.value)}
                  className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
                />
              </Field>
            </>
          )}

          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}

          {(running || logs.length > 0 || doneStatus) && (
            <div className="bg-slate-900 text-slate-100 rounded-md p-3 text-xs font-mono max-h-72 overflow-auto">
              {logs.map((line, i) => (
                <div key={i} className="whitespace-pre-wrap">{line}</div>
              ))}
              {doneStatus && (
                <div className={cn('mt-1 font-medium', doneStatus === 'succeeded' ? 'text-green-300' : 'text-red-300')}>
                  ── {doneStatus.toUpperCase()} ──
                </div>
              )}
            </div>
          )}
        </div>

        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button
            onClick={() => { if (doneStatus) onDone(); else onClose(); }}
            className="text-xs px-3 py-1.5 border border-border rounded-md"
          >
            {doneStatus ? 'Close' : 'Cancel'}
          </button>
          {!doneStatus && (
            <button
              onClick={start}
              disabled={running || !privateKey}
              className={cn(
                'text-xs px-3 py-1.5 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5',
                action === 'update' ? 'bg-brand-500 hover:bg-brand-600' : 'bg-yellow-600 hover:bg-yellow-700',
              )}
            >
              {running ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}
              {running
                ? (action === 'update' ? 'Updating…' : 'Rolling back…')
                : (action === 'update' ? 'Start update' : 'Start rollback')}
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
