// Phase 9.5 — Federation page. Lists registered child CPs with their
// last-cached introspect snapshot. Admin-only (the API enforces); the
// UI surfaces it under Settings → Federation.

import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  AlertCircle,
  CheckCircle2,
  ExternalLink,
  Loader2,
  Network,
  Plus,
  RefreshCcw,
  Server,
  Trash2,
  X,
} from 'lucide-react';
import { api, type FederationPeer } from '../api';
import { cn } from '../lib/cn';

export default function FederationPage() {
  const [peers, setPeers] = useState<FederationPeer[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [refreshing, setRefreshing] = useState<Set<number>>(new Set());

  // Refresh peer list every 8s — the poller writes to the DB on its
  // own 30s tick, so this just picks up whatever cached state is
  // freshest.
  useEffect(() => {
    let cancelled = false;
    const refresh = () => {
      api.federationPeers()
        .then((list) => { if (!cancelled) { setPeers(list); setError(null); } })
        .catch((err) => { if (!cancelled) setError(String(err)); });
    };
    refresh();
    const t = setInterval(refresh, 8_000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  async function handleRefresh(peer: FederationPeer) {
    setRefreshing((s) => new Set(s).add(peer.id));
    try {
      const updated = await api.refreshFederationPeer(peer.id);
      setPeers((prev) => (prev ?? []).map((p) => p.id === peer.id ? updated : p));
    } catch (e) {
      setError(String(e));
    } finally {
      setRefreshing((s) => {
        const n = new Set(s);
        n.delete(peer.id);
        return n;
      });
    }
  }

  async function handleDelete(peer: FederationPeer) {
    if (!confirm(`Remove federation peer "${peer.display_name || peer.url}"?\n\nThe child CP keeps running; this only stops aggregating from it.`)) return;
    try {
      await api.deleteFederationPeer(peer.id);
      setPeers((prev) => (prev ?? []).filter((p) => p.id !== peer.id));
    } catch (e) {
      setError(String(e));
    }
  }

  // Aggregate row at the top — sums daimons / nodes / open findings
  // across every child for the operator's "global posture" answer.
  const totals = (peers ?? []).reduce((acc, p) => {
    const c = p.introspect?.counts ?? {};
    acc.daimons += c.daimons ?? 0;
    acc.daimonsHealthy += c.daimons_healthy ?? 0;
    acc.nodes += c.nodes ?? 0;
    acc.openFindings += c.open_findings ?? 0;
    if (p.healthy) acc.healthyChildren++;
    return acc;
  }, { daimons: 0, daimonsHealthy: 0, nodes: 0, openFindings: 0, healthyChildren: 0 });

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
              <Network size={14} />
            </span>
            Federation
          </h1>
          <p className="text-xs text-ink-dim mt-0.5 ml-9">
            Child CPs this CP aggregates from. Each peer is polled every 30 seconds.
          </p>
        </div>
        <button
          onClick={() => setShowAdd(true)}
          className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-3 py-1.5 rounded-md"
        >
          <Plus size={14} /> Add child CP
        </button>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-6">
        {error && (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {peers === null && <div className="text-ink-mute">Loading…</div>}

        {peers && peers.length === 0 && (
          <div className="text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <Network size={32} className="mx-auto mb-2 opacity-40" />
            <p className="mb-1">No federation peers registered.</p>
            <p className="text-xs max-w-md mx-auto">
              Add a child CP to start aggregating its daimons, nodes, and findings into this view.
              The child must have <code className="bg-slate-100 px-1 rounded">--federation-token</code> set.
            </p>
          </div>
        )}

        {peers && peers.length > 0 && (
          <>
            <section className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <SumTile label="Children" value={`${totals.healthyChildren}/${peers.length}`} sub={totals.healthyChildren === peers.length ? 'all healthy' : `${peers.length - totals.healthyChildren} stale`} accent={totals.healthyChildren === peers.length ? 'good' : 'warn'} />
              <SumTile label="Daimons (federated)" value={`${totals.daimonsHealthy}/${totals.daimons}`} sub="heartbeating" accent="info" />
              <SumTile label="Nodes (federated)" value={`${totals.nodes}`} sub="across all children" accent="info" />
              <SumTile label="Open findings" value={`${totals.openFindings}`} sub="across all children" accent={totals.openFindings > 0 ? 'warn' : 'good'} />
            </section>

            <section className="bg-panel border border-border rounded-xl shadow-card overflow-hidden">
              <ul className="divide-y divide-border">
                {peers.map((p) => (
                  <PeerRow
                    key={p.id}
                    peer={p}
                    refreshing={refreshing.has(p.id)}
                    onRefresh={() => handleRefresh(p)}
                    onDelete={() => handleDelete(p)}
                  />
                ))}
              </ul>
            </section>
          </>
        )}
      </div>

      {showAdd && (
        <AddPeerDialog
          onClose={() => setShowAdd(false)}
          onAdded={(p) => {
            setShowAdd(false);
            setPeers((prev) => [...(prev ?? []), p]);
          }}
        />
      )}
    </div>
  );
}

function PeerRow({
  peer, refreshing, onRefresh, onDelete,
}: {
  peer: FederationPeer;
  refreshing: boolean;
  onRefresh: () => void;
  onDelete: () => void;
}) {
  const intro = peer.introspect;
  const counts = intro?.counts ?? {};
  const ageS = peer.heartbeat_age_sec ?? 0;
  const ageLabel =
    !peer.last_seen_at ? 'never' :
    ageS < 60 ? `${ageS}s ago` :
    ageS < 3600 ? `${Math.round(ageS / 60)}m ago` :
    `${Math.round(ageS / 3600)}h ago`;
  const displayName = peer.display_name || intro?.display_name || peer.url;

  return (
    <li className="px-4 py-3 flex items-center gap-4 hover:bg-slate-50/40">
      <div className={cn(
        'w-10 h-10 shrink-0 rounded-lg flex items-center justify-center text-white shadow-sm',
        peer.healthy
          ? 'bg-gradient-to-br from-brand-500 to-brand-700'
          : 'bg-gradient-to-br from-slate-500 to-slate-700',
      )}>
        <Server size={16} />
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium text-ink truncate">{displayName}</span>
          {intro?.region && (
            <span className="text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded">
              {intro.region}
            </span>
          )}
          {intro?.role && intro.role !== 'standalone' && (
            <span className="text-[10px] uppercase tracking-wide text-slate-700 bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
              {intro.role}
            </span>
          )}
          {peer.healthy ? (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-700 bg-green-50 ring-1 ring-green-200 px-1.5 py-0.5 rounded">
              <CheckCircle2 size={9} /> healthy
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-red-700 bg-red-50 ring-1 ring-red-200 px-1.5 py-0.5 rounded">
              <AlertCircle size={9} /> stale
            </span>
          )}
        </div>
        <div className="text-xs text-ink-dim font-mono truncate flex items-center gap-2 mt-0.5">
          <a href={peer.url} target="_blank" rel="noreferrer" className="hover:underline inline-flex items-center gap-1">
            {peer.url} <ExternalLink size={9} />
          </a>
          {intro?.instance_id && <span className="text-ink-mute">· id {intro.instance_id.slice(0, 8)}</span>}
          {intro?.version && <span className="text-ink-mute">· v{intro.version}</span>}
        </div>
        {peer.last_error && (
          <div className="text-[11px] text-red-700 mt-0.5 truncate" title={peer.last_error}>
            {peer.last_error}
          </div>
        )}
      </div>

      <div className="hidden md:flex items-baseline gap-4 text-xs text-ink-dim shrink-0">
        <Stat label="daimons" value={`${counts.daimons_healthy ?? 0}/${counts.daimons ?? 0}`} />
        <Stat label="nodes" value={`${counts.nodes ?? 0}`} />
        <Stat label="findings" value={`${counts.open_findings ?? 0}`} accent={(counts.open_findings ?? 0) > 0 ? 'warn' : undefined} />
      </div>

      <div className="text-[11px] text-ink-mute tabular-nums shrink-0 hidden lg:block">
        {ageLabel}
      </div>

      <div className="flex items-center gap-0.5 shrink-0">
        <button
          onClick={onRefresh}
          disabled={refreshing}
          title="Force refresh now"
          className="p-1.5 text-ink-mute hover:text-brand-700 hover:bg-brand-50 rounded-md inline-flex items-center"
        >
          {refreshing ? <Loader2 size={13} className="animate-spin" /> : <RefreshCcw size={13} />}
        </button>
        <button
          onClick={onDelete}
          title="Remove peer"
          className="p-1.5 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded-md inline-flex items-center"
        >
          <Trash2 size={13} />
        </button>
      </div>
    </li>
  );
}

function Stat({ label, value, accent }: { label: string; value: string; accent?: 'warn' | 'good' }) {
  const cls = accent === 'warn' ? 'text-yellow-700' : accent === 'good' ? 'text-green-700' : 'text-ink';
  return (
    <div className="flex flex-col items-end">
      <span className={cn('text-sm font-semibold tabular-nums', cls)}>{value}</span>
      <span className="text-[10px] uppercase tracking-wide text-ink-mute">{label}</span>
    </div>
  );
}

type SumAccent = 'good' | 'warn' | 'info';
function SumTile({ label, value, sub, accent }: { label: string; value: string; sub?: string; accent: SumAccent }) {
  const stripe = {
    good: 'bg-green-500',
    warn: 'bg-yellow-500',
    info: 'bg-brand-500',
  }[accent];
  return (
    <Link to="#" className="group relative block bg-panel border border-border rounded-xl shadow-card overflow-hidden">
      <div className={cn('h-0.5', stripe)} />
      <div className="p-4">
        <div className="text-[11px] uppercase tracking-wider text-ink-mute font-medium">{label}</div>
        <div className="mt-2 text-2xl font-semibold text-ink tabular-nums leading-tight">{value}</div>
        {sub && <div className="text-[11px] text-ink-mute mt-1 truncate">{sub}</div>}
      </div>
    </Link>
  );
}

function AddPeerDialog({ onClose, onAdded }: { onClose: () => void; onAdded: (p: FederationPeer) => void }) {
  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true); setError(null);
    try {
      const peer = await api.addFederationPeer({ url, token, display_name: displayName });
      onAdded(peer);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-lg">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Network size={14} className="text-brand-500" /> Add child CP
          </h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <p className="text-xs text-ink-dim">
            The child CP must have <code className="bg-slate-100 px-1 rounded">--federation-token</code> set.
            We'll verify the URL + token before saving.
          </p>
          <Field label="Child CP URL">
            <input
              type="url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://child.example:8443"
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          <Field label="Federation token">
            <input
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="shared-secret"
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          <Field label="Display name (optional)">
            <input
              type="text"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="defaults to remote display_name"
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={busy || !url || !token}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy && <Loader2 size={12} className="animate-spin" />}
            Verify & add
          </button>
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
