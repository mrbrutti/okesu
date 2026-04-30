import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Library, Loader2 } from 'lucide-react';

import { api, type IOCRecord } from '../api';
import { cn } from '../lib/cn';
import CatalogDrawer from '../components/CatalogDrawer';

// Mirrors validKinds in controlplane/ioc/catalog/catalog.go.
const ALL_KINDS = ['sha256', 'sha1', 'md5', 'ipv4', 'ipv6', 'domain', 'url', 'cve', 'mitre', 'yara_rule', 'sigma_rule'];

type SourceFilter = '' | 'catalog' | 'observed';
const SOURCE_OPTIONS: { value: SourceFilter; label: string }[] = [
  { value: '',         label: 'All' },
  { value: 'catalog',  label: 'Catalog' },
  { value: 'observed', label: 'Observed' },
];

export default function CatalogPage() {
  const navigate = useNavigate();
  const [rows, setRows] = useState<IOCRecord[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [kindFilter, setKindFilter] = useState<Set<string>>(new Set(ALL_KINDS));
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>('');
  const [query, setQuery] = useState('');
  const [drawerIOC, setDrawerIOC] = useState<IOCRecord | null>(null);

  // Server-side: only source+q push to the API. Kind multi-select narrows
  // client-side because the API's ?kind= takes a single value.
  useEffect(() => {
    setError(null);
    api.iocs({ source: sourceFilter || undefined, q: query || undefined })
      .then(r => setRows(r ?? []))   // Go nil slice → JSON null; coerce to [] so the empty state renders
      .catch(e => {
        setRows([]);
        setError(String(e));
      });
  }, [sourceFilter, query]);

  const filtered = useMemo(() => {
    if (!rows) return null;
    return rows.filter(r => kindFilter.has(r.Kind));
  }, [rows, kindFilter]);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel flex items-start justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
              <Library size={14} />
            </span>
            Catalog
          </h1>
          <p className="text-xs text-ink-dim mt-0.5 ml-9">
            Every IOC the system knows about — catalog-curated and observation-derived.
          </p>
        </div>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-4">
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        <div className="flex flex-wrap items-center gap-3 text-[11px]">
          <div className="flex items-center gap-2">
            <span className="text-ink-mute">Source</span>
            <div className="inline-flex rounded-md ring-1 ring-border bg-white p-0.5">
              {SOURCE_OPTIONS.map(opt => (
                <button
                  key={opt.value || 'all'}
                  onClick={() => setSourceFilter(opt.value)}
                  className={cn(
                    'px-2.5 py-1 rounded text-xs transition-colors',
                    sourceFilter === opt.value
                      ? 'bg-brand-50 text-brand-700 font-medium'
                      : 'text-ink-dim hover:text-ink',
                  )}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          <div className="flex items-center gap-2">
            <span className="text-ink-mute">Kind</span>
            <div className="inline-flex flex-wrap rounded-md ring-1 ring-border bg-white p-0.5 gap-0.5">
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
                    className={cn(
                      'px-2 py-1 rounded text-xs transition-colors',
                      active
                        ? 'bg-brand-50 text-brand-700 font-medium'
                        : 'text-ink-dim hover:text-ink',
                    )}
                  >
                    {k}
                  </button>
                );
              })}
            </div>
          </div>

          <input
            type="search"
            placeholder="Search value, name, tags…"
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="text-xs px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white text-ink placeholder:text-ink-mute focus:ring-brand-500 focus:outline-none flex-1 min-w-[16rem] max-w-md"
          />
        </div>

        {filtered === null ? (
          <div className="flex items-center gap-2 text-ink-dim text-xs">
            <Loader2 size={14} className="animate-spin" /> Loading catalog…
          </div>
        ) : filtered.length === 0 ? (
          <div className="text-sm text-ink-mute border border-border rounded-md p-6 text-center bg-slate-50/50">
            No IOCs match these filters.
          </div>
        ) : (
          <div className="border border-border rounded-md bg-white overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute bg-slate-50 border-b border-border">
                  <th className="px-3 py-2 font-medium w-24">Kind</th>
                  <th className="px-3 py-2 font-medium">Value</th>
                  <th className="px-3 py-2 font-medium hidden md:table-cell">Name</th>
                  <th className="px-3 py-2 font-medium hidden md:table-cell">Tags</th>
                  <th className="px-3 py-2 font-medium w-24">Severity</th>
                  <th className="px-3 py-2 font-medium w-24">Source</th>
                  <th className="px-3 py-2 font-medium text-right w-20">Observed</th>
                  <th className="px-3 py-2 font-medium hidden lg:table-cell w-32">First seen</th>
                  <th className="px-3 py-2 font-medium hidden lg:table-cell w-32">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map(r => (
                  <tr
                    key={r.ID}
                    onClick={() => setDrawerIOC(r)}
                    className="border-b border-border last:border-b-0 hover:bg-slate-50/60 cursor-pointer"
                  >
                    <td className="px-3 py-2 font-mono text-[11px] text-ink-dim">{r.Kind}</td>
                    <td className="px-3 py-2 font-mono text-[11px] text-ink truncate max-w-xs">{r.Value}</td>
                    <td className="px-3 py-2 hidden md:table-cell text-ink">{r.Name || <span className="text-ink-mute">—</span>}</td>
                    <td className="px-3 py-2 hidden md:table-cell"><TagChips tags={r.Tags} /></td>
                    <td className="px-3 py-2"><SeverityChip s={r.SeverityFloor} /></td>
                    <td className="px-3 py-2 text-ink-dim text-xs">{r.Source}</td>
                    <td className="px-3 py-2 text-right text-ink">{r.ObservationCount}</td>
                    <td className="px-3 py-2 hidden lg:table-cell text-ink-dim text-xs font-mono">{fmtDate(r.FirstSeen)}</td>
                    <td className="px-3 py-2 hidden lg:table-cell text-ink-dim text-xs font-mono">{fmtDate(r.LastSeen)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

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

// TagChips renders the comma-separated tags column as up to 3 chips with
// a "+N" overflow indicator. Click the row for the full list in the drawer.
function TagChips({ tags }: { tags: string }) {
  if (!tags) return <span className="text-ink-mute">—</span>;
  const parts = tags.split(',').map(t => t.trim()).filter(Boolean);
  const visible = parts.slice(0, 3);
  const overflow = parts.length - visible.length;
  return (
    <div className="flex flex-wrap gap-1">
      {visible.map(t => (
        <span key={t} className="px-1.5 py-0.5 text-[10px] rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100">{t}</span>
      ))}
      {overflow > 0 && (
        <span className="px-1.5 py-0.5 text-[10px] rounded bg-slate-100 text-ink-mute ring-1 ring-border">+{overflow}</span>
      )}
    </div>
  );
}

function SeverityChip({ s }: { s: string }) {
  if (!s) return <span className="text-ink-mute text-xs">—</span>;
  const styles: Record<string, string> = {
    CRITICAL: 'bg-purple-50 text-purple-700 ring-purple-200',
    HIGH:     'bg-red-50 text-red-700 ring-red-200',
    MEDIUM:   'bg-orange-50 text-orange-700 ring-orange-200',
    LOW:      'bg-yellow-50 text-yellow-700 ring-yellow-200',
    INFO:     'bg-slate-50 text-slate-700 ring-slate-200',
  };
  const cls = styles[s] || 'bg-slate-50 text-slate-700 ring-slate-200';
  return <span className={cn('px-1.5 py-0.5 text-[10px] uppercase font-medium rounded ring-1', cls)}>{s}</span>;
}

function fmtDate(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString(undefined, {
    month: 'short', day: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}
