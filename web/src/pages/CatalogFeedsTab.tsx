// Catalog → Feeds tab — read-only summary of installed IOC feeds.
//
// Each row links back to the Indicators tab with the source filter
// pre-set to feed:<slug>, so an operator can browse exactly what a
// given feed has contributed.
//
// Mutations (install / refresh / uninstall) live in Settings → Feeds.

import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { api, type FeedConfig } from '../api';

interface Props {
  onSelectSlug: (slug: string) => void;
}

export default function CatalogFeedsTab({ onSelectSlug }: Props) {
  const [feeds, setFeeds] = useState<FeedConfig[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.feeds()
      .then(setFeeds)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  if (error) {
    return (
      <div className="p-6 text-xs text-red-700 bg-red-50 border border-red-200 rounded-md">
        {error}
      </div>
    );
  }
  if (!feeds) {
    return (
      <div className="p-6 text-ink-dim text-xs flex items-center gap-2">
        <Loader2 size={14} className="animate-spin" /> Loading feeds…
      </div>
    );
  }
  if (feeds.length === 0) {
    return (
      <div className="p-6 text-sm text-ink-mute">
        No feeds installed. Visit Settings → Feeds to install one.
      </div>
    );
  }

  return (
    <div className="p-6">
      <div className="border border-border rounded-md overflow-hidden bg-white">
        <table className="w-full text-sm">
          <thead className="bg-slate-50 text-[11px] uppercase tracking-wide text-ink-mute">
            <tr>
              <th className="px-3 py-2 text-left">Name</th>
              <th className="px-3 py-2 text-left">Parser</th>
              <th className="px-3 py-2 text-left">Last refresh</th>
              <th className="px-3 py-2 text-right">Entries</th>
              <th className="px-3 py-2 text-right">Browse</th>
            </tr>
          </thead>
          <tbody>
            {feeds.map((f) => (
              <tr key={f.ID} className="border-t border-border">
                <td className="px-3 py-2">
                  <div className="font-medium">{f.Name}</div>
                  <div className="text-[11px] text-ink-mute font-mono">{f.Slug}</div>
                </td>
                <td className="px-3 py-2 text-xs text-ink-dim">{f.Parser}</td>
                <td className="px-3 py-2 text-xs text-ink-dim">
                  {f.LastRefreshAt.Valid
                    ? new Date(f.LastRefreshAt.Time).toLocaleString()
                    : '—'}
                </td>
                <td className="px-3 py-2 text-right text-ink">
                  {f.LastRefreshEntryCount || 0}
                </td>
                <td className="px-3 py-2 text-right">
                  <button
                    onClick={() => onSelectSlug(f.Slug)}
                    className="text-xs px-2 py-1 rounded border border-border hover:bg-slate-50"
                  >
                    Browse rows
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
