import { useState } from 'react';
import { X } from 'lucide-react';

import { api, type TransportConfigSummary } from '../api';

interface Props {
  bucket: TransportConfigSummary;
  onClose: () => void;
  onUpdated: (tc: TransportConfigSummary) => void;
}

export default function EditBucketModal({ bucket, onClose, onUpdated }: Props) {
  const [name, setName] = useState(bucket.name);
  const [scannerIntervalMs, setScannerIntervalMs] = useState(bucket.scanner_interval_ms ?? 30000);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function submit() {
    setSubmitting(true);
    setError(null);
    api.transportConfigPatch(bucket.id, { name, scanner_interval_ms: scannerIntervalMs })
      .then(onUpdated)
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-panel border border-border rounded-lg shadow-lg w-[420px]">
        <div className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold">Edit bucket</h2>
          <button onClick={onClose} className="text-ink-mute hover:text-ink"><X size={16} /></button>
        </div>
        <div className="px-5 py-4 space-y-3">
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-ink-mute">Display name</span>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-ink-mute">Scanner interval (ms)</span>
            <input
              type="number"
              value={scannerIntervalMs}
              onChange={(e) => setScannerIntervalMs(Number(e.target.value))}
              className="text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
            />
          </label>
          <div className="text-[10px] text-ink-mute">
            Identity fields (bucket, endpoint, access keys) can't be edited — delete + re-add if you need a different bucket.
            Key rotation lands as a separate "Rotate keys" wizard later.
          </div>
        </div>
        <div className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">Cancel</button>
          <button
            onClick={submit}
            disabled={submitting}
            className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
          >
            {submitting ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}
