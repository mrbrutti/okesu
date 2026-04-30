// WarBridgeBanner — dashboard alert strip for active war-bridge findings.
//
// Phase 22.3 introduced the war-bridge tag for findings that need
// real-time cross-team escalation (incident-bridge calls, paging,
// etc.). The endpoint /api/findings/war-bridge returns up to 25 of
// the most recent open ones; we render up to 5 inline and link the
// rest into the regular findings list.
//
// The endpoint encodes through controlplane/api/findings.go's
// findingJSON wrapper — note the lowercase JSON tags (id, title,
// host, severity) which differ from the capitalized field names
// used by tag-less Go types like Investigation.
//
// Polling cadence: 30s. The banner self-hides when the list is empty,
// so it's invisible in the common case and only surfaces during an
// active escalation.

import { useEffect, useState } from 'react';
import { AlertOctagon } from 'lucide-react';

interface WarBridgeFinding {
  id: number;
  title?: string;
  host?: string;
  severity?: string;
}

const POLL_INTERVAL_MS = 30_000;

export function WarBridgeBanner() {
  const [items, setItems] = useState<WarBridgeFinding[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const res = await fetch('/api/findings/war-bridge', {
          credentials: 'same-origin',
        });
        if (!res.ok) {
          // Silently ignore errors — this is a peripheral surface.
          // 401 (logged out) just means the layout will redirect us
          // shortly anyway.
          if (!cancelled) setItems([]);
          return;
        }
        const data = (await res.json()) as WarBridgeFinding[] | null;
        if (!cancelled) setItems(Array.isArray(data) ? data : []);
      } catch {
        // Network error / parse failure — keep the prior value to
        // avoid flicker; we'll retry on the next interval tick.
      }
    };
    load();
    const t = setInterval(load, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (items.length === 0) return null;

  return (
    <div className="bg-gradient-to-r from-red-700 to-red-600 text-white rounded-md px-4 py-3 shadow-md ring-1 ring-red-800">
      <div className="flex items-center gap-2 font-semibold text-sm">
        <AlertOctagon size={16} className="shrink-0" />
        <span>War bridge — {items.length} active</span>
      </div>
      <ul className="mt-2 space-y-1 text-xs">
        {items.slice(0, 5).map((f) => (
          <li key={f.id}>
            <a
              href={`/findings?id=${f.id}`}
              className="underline decoration-red-200/80 underline-offset-2 hover:decoration-white"
            >
              <span className="font-medium">{f.title || `Finding #${f.id}`}</span>
              {f.severity && (
                <span className="text-red-100/90"> ({f.severity})</span>
              )}
              {f.host && (
                <span className="text-red-100/90"> · {f.host}</span>
              )}
            </a>
          </li>
        ))}
        {items.length > 5 && (
          <li className="text-red-100/80 italic">+ {items.length - 5} more — see Findings</li>
        )}
      </ul>
    </div>
  );
}
