import { useEffect, useState } from 'react';
import {
  AlertTriangle,
  Archive,
  Database,
  Loader2,
  Trash2,
} from 'lucide-react';
import { api, ApiError, type DBStatsResponse } from '../../api';
import { cn } from '../../lib/cn';

export default function DatabaseSection() {
  const [data, setData] = useState<DBStatsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'vacuum' | 'prune' | null>(null);
  const [pruneDays, setPruneDays] = useState(30);
  const [lastResult, setLastResult] = useState<string | null>(null);

  const refresh = () => {
    api.dbStats().then(setData).catch((e) => setError(String(e)));
  };
  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 15_000);
    return () => clearInterval(t);
  }, []);

  async function vacuum() {
    if (!confirm('Run VACUUM? This rewrites the SQLite file and may take a while on a large DB.')) return;
    setBusy('vacuum'); setError(null); setLastResult(null);
    try {
      const r = await api.dbVacuum();
      setLastResult(`Vacuum done in ${r.duration_ms} ms.`);
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function prune() {
    if (!confirm(`Delete events older than ${pruneDays} days? Findings are kept independently.`)) return;
    setBusy('prune'); setError(null); setLastResult(null);
    try {
      const r = await api.dbPruneEvents(pruneDays);
      setLastResult(`Pruned ${r.deleted.toLocaleString()} event(s) older than ${r.older_than_days} day(s).`);
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="p-6 max-w-4xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Database size={18} className="text-brand-500" />
          Database
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          SQLite footprint, retention policy, and maintenance.
        </p>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md flex items-start gap-2">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      )}
      {lastResult && (
        <div className="bg-green-50 border border-green-200 text-green-800 text-xs px-3 py-2 rounded-md">
          {lastResult}
        </div>
      )}

      <Card title="Footprint">
        {data === null ? (
          <p className="text-sm text-ink-mute">Loading…</p>
        ) : (
          <dl className="grid grid-cols-2 md:grid-cols-3 gap-x-6 gap-y-3 text-sm">
            <Stat label="File"        value={data.stats.path} mono />
            <Stat label="Size"        value={fmtBytes(data.stats.size_bytes)} />
            <Stat label="Events"      value={fmtCount(data.stats.event_count)} />
            <Stat label="Findings"    value={fmtCount(data.stats.finding_count)} />
            <Stat label="Runs"        value={fmtCount(data.stats.run_count)} />
            <Stat label="Sessions"    value={fmtCount(data.stats.session_count)} />
            <Stat label="Deliveries"  value={fmtCount(data.stats.delivery_count)} />
            <Stat label="Oldest event"
                  value={data.stats.oldest_event_ts ? fmtAge(data.stats.oldest_event_ts) : '—'} />
          </dl>
        )}
      </Card>

      <Card title="Retention" subtitle="Auto-prune of the events table.">
        {data === null ? (
          <p className="text-sm text-ink-mute">Loading…</p>
        ) : (
          <>
            <p className="text-sm">
              {data.config.event_ttl_days > 0 ? (
                <>Currently keeping <strong>{data.config.event_ttl_days} days</strong> of event history. Set via <code className="font-mono text-xs bg-slate-100 px-1 py-0.5 rounded">--event-ttl-days</code> or <code className="font-mono text-xs bg-slate-100 px-1 py-0.5 rounded">OKESU_CP_EVENT_TTL_DAYS</code>.</>
              ) : (
                <>Auto-prune is <strong>off</strong>. Events accumulate indefinitely until you prune manually below or set <code className="font-mono text-xs bg-slate-100 px-1 py-0.5 rounded">--event-ttl-days</code> at boot.</>
              )}
            </p>
            <p className="text-xs text-ink-mute mt-1">
              Findings are kept independently — they survive event-table pruning so trend dashboards aren't disturbed.
            </p>
          </>
        )}
      </Card>

      <Card title="Maintenance" subtitle="Manual operations. Both write to the audit log.">
        <div className="space-y-4">
          <div className="flex items-center gap-3 flex-wrap">
            <span className="text-sm font-medium">Prune events older than</span>
            <input
              type="number"
              min={1}
              value={pruneDays}
              onChange={(e) => setPruneDays(Number(e.target.value))}
              className="w-20 px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white"
            />
            <span className="text-sm">days</span>
            <button
              onClick={prune}
              disabled={busy !== null || pruneDays <= 0}
              className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50 inline-flex items-center gap-1.5 disabled:opacity-50"
            >
              {busy === 'prune' ? <Loader2 size={11} className="animate-spin" /> : <Trash2 size={11} />}
              Prune now
            </button>
          </div>

          <div className="flex items-center gap-3 flex-wrap">
            <span className="text-sm font-medium">Reclaim disk space</span>
            <button
              onClick={vacuum}
              disabled={busy !== null}
              className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50 inline-flex items-center gap-1.5 disabled:opacity-50"
            >
              {busy === 'vacuum' ? <Loader2 size={11} className="animate-spin" /> : <Archive size={11} />}
              Vacuum
            </button>
            <span className="text-xs text-ink-mute">Run after a large prune to actually shrink the file on disk.</span>
          </div>
        </div>
      </Card>
    </div>
  );
}

function Card({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5">
      <h3 className="text-sm font-semibold">{title}</h3>
      {subtitle && <p className="text-xs text-ink-dim mt-0.5 mb-4">{subtitle}</p>}
      {children}
    </section>
  );
}

function Stat({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">{label}</dt>
      <dd className={cn('text-sm break-all', mono && 'font-mono text-xs')}>{value}</dd>
    </div>
  );
}

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function fmtCount(n: number): string {
  return n.toLocaleString();
}

function fmtAge(unixMs: number): string {
  const d = new Date(unixMs);
  const sec = (Date.now() - d.getTime()) / 1000;
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago (${d.toLocaleDateString()})`;
}
