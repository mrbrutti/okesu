import { useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  ClipboardList,
  ExternalLink,
  Filter,
  RefreshCw,
} from 'lucide-react';
import { api, type AuditEntry } from '../../api';
import { cn } from '../../lib/cn';
import { useInfiniteScroll } from '../../lib/useInfiniteScroll';

const AUDIT_PAGE_SIZE = 250;

export default function AuditLogSection() {
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const [actor, setActor] = useState('');
  const [action, setAction] = useState('');
  const [result, setResult] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(true);

  const refresh = useMemo(() => () => {
    api.audit({
      actor: actor || undefined,
      action: action || undefined,
      result: result || undefined,
      limit: AUDIT_PAGE_SIZE,
    })
      .then((list) => {
        setRows(list);
        setHasMore(list.length >= AUDIT_PAGE_SIZE);
      })
      .catch((e) => setError(String(e)));
  }, [actor, action, result]);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 12_000);
    return () => clearInterval(t);
  }, [refresh]);

  const auditScroll = useInfiniteScroll({
    hasMore,
    loadMore: async () => {
      if (!rows) return;
      const next = await api.audit({
        actor: actor || undefined,
        action: action || undefined,
        result: result || undefined,
        limit: AUDIT_PAGE_SIZE,
        offset: rows.length,
      });
      if (next.length === 0) {
        setHasMore(false);
        return;
      }
      setRows((prev) => [...(prev ?? []), ...next]);
      if (next.length < AUDIT_PAGE_SIZE) setHasMore(false);
    },
  });

  return (
    <div className="p-6 max-w-6xl space-y-4">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <ClipboardList size={18} className="text-brand-500" />
          Audit log
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Every administrative action — logins, role changes, deploys, acks — is recorded here.
        </p>
      </header>

      <div className="bg-panel border border-border rounded-xl shadow-card">
        <header className="px-4 py-2.5 border-b border-border flex items-center gap-2 flex-wrap">
          <Filter size={13} className="text-ink-mute" />
          <input
            value={actor}
            onChange={(e) => setActor(e.target.value)}
            placeholder="actor email"
            className="px-2 py-1 text-xs border border-border rounded-md w-40 focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
          <input
            value={action}
            onChange={(e) => setAction(e.target.value)}
            placeholder='action — e.g. user.* or finding.acknowledge'
            className="px-2 py-1 text-xs border border-border rounded-md w-72 focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
          <select
            value={result}
            onChange={(e) => setResult(e.target.value)}
            className="px-2 py-1 text-xs border border-border rounded-md bg-white"
          >
            <option value="">any result</option>
            <option value="ok">ok</option>
            <option value="denied">denied</option>
            <option value="error">error</option>
          </select>
          <button
            onClick={refresh}
            className="ml-auto inline-flex items-center gap-1 text-[11px] text-ink-dim hover:text-ink px-2 py-1 rounded-md hover:bg-slate-100"
          >
            <RefreshCw size={11} />
            refresh
          </button>
        </header>

        {error && (
          <div className="m-4 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
        )}

        {rows === null ? (
          <p className="p-4 text-ink-mute">Loading…</p>
        ) : rows.length === 0 ? (
          <p className="p-4 text-ink-mute">No matching audit entries.</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-slate-50 border-b border-border sticky top-0">
              <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute">
                <th className="px-4 py-2 font-medium w-44">When</th>
                <th className="px-3 py-2 font-medium w-44">Actor</th>
                <th className="px-3 py-2 font-medium w-56">Action</th>
                <th className="px-3 py-2 font-medium w-44">Target</th>
                <th className="px-3 py-2 font-medium w-20">Result</th>
                <th className="px-3 py-2 font-medium">Metadata</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id} className="border-b border-border/60 last:border-0 hover:bg-slate-50/40 align-top">
                  <td className="px-4 py-2 text-xs font-mono text-ink-dim whitespace-nowrap">
                    {fmt(r.ts)}
                  </td>
                  <td className="px-3 py-2 text-xs">
                    {r.actor_email ? (
                      <>
                        <div className="text-ink truncate">{r.actor_email}</div>
                        <div className="text-[10px] text-ink-mute capitalize">{r.actor_role}</div>
                      </>
                    ) : (
                      <span className="text-ink-mute">system</span>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <code className="text-[11px] bg-slate-100 px-1.5 py-0.5 rounded text-ink-dim">
                      {r.action}
                    </code>
                  </td>
                  <td className="px-3 py-2 text-xs font-mono text-ink-dim truncate">
                    {r.target || '—'}
                  </td>
                  <td className="px-3 py-2">
                    <ResultPill result={r.result} />
                  </td>
                  <td className="px-3 py-2 text-[11px] text-ink-dim font-mono break-all max-w-md">
                    {r.metadata ? JSON.stringify(r.metadata) : '—'}
                    {r.actor_ip && (
                      <span className="block mt-0.5 text-[10px] text-ink-mute inline-flex items-center gap-1">
                        <ExternalLink size={9} /> {r.actor_ip}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {rows && rows.length > 0 && (
          <div ref={auditScroll.sentinelRef} className="px-4 py-2 text-[11px] text-ink-mute text-center border-t border-border/60">
            {auditScroll.loading
              ? 'loading older entries…'
              : hasMore
                ? 'scroll for more'
                : `— end of ${rows.length} entries —`}
          </div>
        )}
      </div>
    </div>
  );
}

function ResultPill({ result }: { result: string }) {
  const cfg = {
    ok:     { icon: CheckCircle2,  cls: 'text-green-700 bg-green-50 ring-green-200' },
    denied: { icon: Ban,           cls: 'text-red-700 bg-red-50 ring-red-200' },
    error:  { icon: AlertTriangle, cls: 'text-orange-700 bg-orange-50 ring-orange-200' },
  }[result] ?? { icon: CheckCircle2, cls: 'text-ink-dim bg-slate-50 ring-slate-200' };
  return (
    <span className={cn('inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1', cfg.cls)}>
      <cfg.icon size={9} />
      {result}
    </span>
  );
}

function fmt(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  });
}
