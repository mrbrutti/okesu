import { useState } from 'react';
import { X } from 'lucide-react';
import { Link } from 'react-router-dom';

import { api, ApiError, type TransportConfigSummary, type TransportConfigDeleteConflict } from '../api';

interface Props {
  bucket: TransportConfigSummary;
  onClose: () => void;
  onDeleted: () => void;
}

export default function DeleteBucketDialog({ bucket, onClose, onDeleted }: Props) {
  const [submitting, setSubmitting] = useState(false);
  const [conflict, setConflict] = useState<TransportConfigDeleteConflict | null>(null);
  const [error, setError] = useState<string | null>(null);

  function submit() {
    setSubmitting(true);
    setError(null);
    setConflict(null);
    api.transportConfigDelete(bucket.id)
      .then(() => onDeleted())
      .catch((e) => {
        if (e instanceof ApiError && e.status === 409) {
          try {
            const parsed = JSON.parse(e.body) as TransportConfigDeleteConflict;
            setConflict(parsed);
          } catch {
            setError(e.message);
          }
        } else {
          setError(String(e));
        }
      })
      .finally(() => setSubmitting(false));
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-panel border border-border rounded-lg shadow-lg w-[480px]">
        <div className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold">Delete bucket?</h2>
          <button onClick={onClose} className="text-ink-mute hover:text-ink"><X size={16} /></button>
        </div>
        <div className="px-5 py-4 space-y-3">
          <div className="text-sm">
            This removes the transport configuration for <code className="font-mono text-xs">{bucket.bucket}</code> at{' '}
            <code className="font-mono text-xs">{bucket.endpoint}</code> from Okesu.
            The bucket itself in your cloud account is NOT deleted.
          </div>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
          {conflict && (
            <div className="text-xs text-yellow-700 bg-yellow-50 border border-yellow-200 px-3 py-2 rounded-md space-y-2">
              <div className="font-medium">{conflict.message}</div>
              {conflict.referenced_by.Nodes.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">Nodes</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.Nodes.map((r) => (
                      <li key={r.id}><Link to="/nodes" className="text-brand-700 hover:underline">{r.name || `#${r.id}`}</Link></li>
                    ))}
                  </ul>
                </div>
              )}
              {conflict.referenced_by.EnrollmentPackages.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">Enrollment packages</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.EnrollmentPackages.map((r) => (
                      <li key={r.id}><Link to="/federation" className="text-brand-700 hover:underline">{r.name || `#${r.id}`}</Link></li>
                    ))}
                  </ul>
                </div>
              )}
              {conflict.referenced_by.FederationPeers.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">Federation peers (will be set to NULL)</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.FederationPeers.map((r) => (
                      <li key={r.id}>{r.name || `#${r.id}`}</li>
                    ))}
                  </ul>
                </div>
              )}
              {conflict.referenced_by.CPProvisions.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">CP provisions (will be set to NULL)</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.CPProvisions.map((r) => (
                      <li key={r.id}>{r.name || `#${r.id}`}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}
        </div>
        <div className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">Cancel</button>
          <button
            onClick={submit}
            disabled={submitting}
            className="text-xs px-3 py-1.5 bg-red-600 text-white rounded-md hover:bg-red-700 disabled:opacity-50"
          >
            {submitting ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      </div>
    </div>
  );
}
