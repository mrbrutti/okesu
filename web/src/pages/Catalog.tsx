import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Library } from 'lucide-react';

import { api, type IOCRecord } from '../api';
import CatalogDrawer from '../components/CatalogDrawer';

// Fixed list of kinds operators can filter by. Mirrors validKinds from
// controlplane/ioc/catalog/catalog.go — kept in sync by hand for now.
const ALL_KINDS = ['sha256', 'sha1', 'md5', 'ipv4', 'ipv6', 'domain', 'url', 'cve', 'mitre', 'yara_rule', 'sigma_rule'];

export default function CatalogPage() {
  const navigate = useNavigate();
  const [rows, setRows] = useState<IOCRecord[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [kindFilter, setKindFilter] = useState<Set<string>>(new Set(ALL_KINDS));
  const [sourceFilter, setSourceFilter] = useState<'' | 'catalog' | 'observed'>('');
  const [query, setQuery] = useState('');
  const [drawerIOC, setDrawerIOC] = useState<IOCRecord | null>(null);

  // Server-side: only source+q filters are pushed (the API takes a
  // single kind param). Client-side we further narrow by the kind
  // multi-select since the API only accepts a single kind value.
  useEffect(() => {
    setError(null);
    api.iocs({ source: sourceFilter || undefined, q: query || undefined })
      .then(setRows)
      .catch(e => {
        // Empty list on error so the table area renders the no-match
        // empty state alongside the error banner instead of a stuck
        // "Loading…" spinner.
        setRows([]);
        setError(String(e));
      });
  }, [sourceFilter, query]);

  const filtered = useMemo(() => {
    if (!rows) return null;
    return rows.filter(r => kindFilter.has(r.Kind));
  }, [rows, kindFilter]);

  return (
    <div className="p-6 space-y-4">
      <header className="flex items-center gap-3">
        <Library className="w-6 h-6 text-zinc-400" />
        <div>
          <h1 className="text-xl font-semibold text-zinc-100">Catalog</h1>
          <p className="text-sm text-zinc-400">Every IOC the system knows about — catalog-curated and observation-derived.</p>
        </div>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-wrap gap-1">
          {ALL_KINDS.map(k => {
            const active = kindFilter.has(k);
            return (
              <button
                key={k}
                onClick={() => {
                  const next = new Set(kindFilter);
                  if (active) next.delete(k); else next.add(k);
                  setKindFilter(next);
                }}
                className={
                  'px-2 py-0.5 text-xs rounded border ' +
                  (active
                    ? 'bg-zinc-700 text-zinc-100 border-zinc-600'
                    : 'bg-zinc-900 text-zinc-500 border-zinc-800')
                }
              >
                {k}
              </button>
            );
          })}
        </div>
        <select
          value={sourceFilter}
          onChange={e => setSourceFilter(e.target.value as '' | 'catalog' | 'observed')}
          className="text-sm bg-zinc-900 border border-zinc-800 rounded px-2 py-1 text-zinc-200"
        >
          <option value="">All sources</option>
          <option value="catalog">Catalog</option>
          <option value="observed">Observed</option>
        </select>
        <input
          type="search"
          placeholder="Search value, name, tags…"
          value={query}
          onChange={e => setQuery(e.target.value)}
          className="text-sm bg-zinc-900 border border-zinc-800 rounded px-2 py-1 text-zinc-200 flex-1 max-w-md"
        />
      </div>

      {error && <div className="text-red-400 text-sm">{error}</div>}

      {filtered === null ? (
        <div className="text-sm text-zinc-500">Loading…</div>
      ) : filtered.length === 0 ? (
        <div className="text-sm text-zinc-500">No IOCs match these filters.</div>
      ) : (
        <table className="w-full text-sm border-collapse">
          <thead className="text-zinc-400 border-b border-zinc-800">
            <tr>
              <th className="text-left py-2 pr-3">Kind</th>
              <th className="text-left py-2 pr-3">Value</th>
              <th className="text-left py-2 pr-3 hidden md:table-cell">Name</th>
              <th className="text-left py-2 pr-3 hidden md:table-cell">Tags</th>
              <th className="text-left py-2 pr-3">Severity</th>
              <th className="text-left py-2 pr-3">Source</th>
              <th className="text-right py-2 pr-3">Observed</th>
              <th className="text-left py-2 pr-3 hidden lg:table-cell">First seen</th>
              <th className="text-left py-2 pr-3 hidden lg:table-cell">Last seen</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map(r => (
              <tr
                key={r.ID}
                onClick={() => setDrawerIOC(r)}
                className="border-b border-zinc-900 hover:bg-zinc-900/40 cursor-pointer"
              >
                <td className="py-2 pr-3 font-mono text-xs text-zinc-400">{r.Kind}</td>
                <td className="py-2 pr-3 font-mono text-xs text-zinc-200 truncate max-w-xs">{r.Value}</td>
                <td className="py-2 pr-3 hidden md:table-cell text-zinc-300">{r.Name || '—'}</td>
                <td className="py-2 pr-3 hidden md:table-cell">
                  <TagChips tags={r.Tags} />
                </td>
                <td className="py-2 pr-3 text-zinc-300">{r.SeverityFloor || '—'}</td>
                <td className="py-2 pr-3 text-zinc-300">{r.Source}</td>
                <td className="py-2 pr-3 text-right text-zinc-300">{r.ObservationCount}</td>
                <td className="py-2 pr-3 hidden lg:table-cell text-zinc-500 text-xs">{r.FirstSeen}</td>
                <td className="py-2 pr-3 hidden lg:table-cell text-zinc-500 text-xs">{r.LastSeen}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {drawerIOC && (
        <CatalogDrawer
          ioc={drawerIOC}
          onClose={() => setDrawerIOC(null)}
          onOpenFullPage={() => navigate(`/catalog/${drawerIOC.ID}`)}
        />
      )}
    </div>
  );
}

// TagChips renders the comma-separated tags column as up to 3 small
// chips with a "+N more" overflow indicator (non-interactive — click
// the row for the full list in the drawer).
function TagChips({ tags }: { tags: string }) {
  if (!tags) return <span className="text-zinc-600">—</span>;
  const parts = tags.split(',').map(t => t.trim()).filter(Boolean);
  const visible = parts.slice(0, 3);
  const overflow = parts.length - visible.length;
  return (
    <div className="flex flex-wrap gap-1">
      {visible.map(t => (
        <span key={t} className="px-1.5 py-0.5 text-xs rounded bg-zinc-800 text-zinc-300">{t}</span>
      ))}
      {overflow > 0 && (
        <span className="px-1.5 py-0.5 text-xs rounded bg-zinc-900 text-zinc-500 border border-zinc-800">+{overflow}</span>
      )}
    </div>
  );
}
