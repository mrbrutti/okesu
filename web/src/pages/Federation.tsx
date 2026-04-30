// Phase 9.5 — Federation page. Lists registered child CPs with their
// last-cached introspect snapshot. Admin-only (the API enforces); the
// UI surfaces it under Settings → Federation.

import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Loader2,
  Network,
  Plus,
  RefreshCcw,
  Server,
  Trash2,
  X,
} from 'lucide-react';
import { api, type CloudCredential, type CloudKind, type CPProvision, type CPProvisionEstimate, type DiscoveryItem, type FederationPeer, type TransportConfigSummary } from '../api';
import { cn } from '../lib/cn';

export default function FederationPage() {
  const [peers, setPeers] = useState<FederationPeer[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [refreshing, setRefreshing] = useState<Set<number>>(new Set());
  // Per-peer detail drawer. Holds the id of the peer the operator
  // clicked on; we re-resolve to the live row each render so the
  // drawer reflects the most recent poll state without stashing a
  // stale snapshot.
  const [openPeerID, setOpenPeerID] = useState<number | null>(null);

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
                    onOpen={() => setOpenPeerID(p.id)}
                  />
                ))}
              </ul>
            </section>

            <ManagedDeploysPanel />
          </>
        )}

        {/* Show managed deploys even without peers — a row in flight
            here is exactly what's about to BECOME a peer. */}
        {peers && peers.length === 0 && <ManagedDeploysPanel />}
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

      {openPeerID !== null && peers && (() => {
        const peer = peers.find((p) => p.id === openPeerID);
        if (!peer) return null;
        return (
          <PeerDetailDrawer
            peer={peer}
            refreshing={refreshing.has(peer.id)}
            onRefresh={() => handleRefresh(peer)}
            onClose={() => setOpenPeerID(null)}
          />
        );
      })()}
    </div>
  );
}

function PeerRow({
  peer, refreshing, onRefresh, onDelete, onOpen,
}: {
  peer: FederationPeer;
  refreshing: boolean;
  onRefresh: () => void;
  onDelete: () => void;
  /** Click anywhere on the row except action buttons → drawer. */
  onOpen: () => void;
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

  // Polling-vs-success split. When last_polled_at is more recent than
  // last_seen_at by more than a poll interval (~30s), the peer is
  // being polled but not responding — surface this as "polling, last
  // success Xs ago" so a flapping peer is visible at a glance.
  const polledStr = peer.last_polled_at;
  const seenStr = peer.last_seen_at;
  const polledMs = polledStr ? new Date(polledStr).getTime() : 0;
  const seenMs = seenStr ? new Date(seenStr).getTime() : 0;
  const stalePolling = polledMs > 0 && seenMs > 0 && polledMs - seenMs > 60_000;

  return (
    <li
      className="px-4 py-3 flex items-center gap-4 hover:bg-slate-50/40 cursor-pointer"
      onClick={onOpen}
    >
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
          <TransportChip transport={peer.transport} />
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
          ) : stalePolling ? (
            <span
              className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-orange-700 bg-orange-50 ring-1 ring-orange-200 px-1.5 py-0.5 rounded"
              title="Parent is polling but the peer isn't responding"
            >
              <AlertCircle size={9} /> flapping
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-red-700 bg-red-50 ring-1 ring-red-200 px-1.5 py-0.5 rounded">
              <AlertCircle size={9} /> stale
            </span>
          )}
        </div>
        <div className="text-xs text-ink-dim font-mono truncate flex items-center gap-2 mt-0.5">
          <a
            href={peer.url}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            className="hover:underline inline-flex items-center gap-1"
          >
            {peer.transport === 's3_dead_drop' && peer.bucket_prefix
              ? <>s3:// {peer.bucket_prefix}</>
              : <>{peer.url}</>
            }
            <ExternalLink size={9} />
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

      <div className="text-[11px] text-ink-mute tabular-nums shrink-0 hidden lg:flex flex-col items-end">
        <span title={`Last successful introspect: ${peer.last_seen_at ?? 'never'}`}>seen {ageLabel}</span>
        {stalePolling && peer.last_polled_at && (
          <span
            className="text-orange-700"
            title={`Parent last attempted poll: ${peer.last_polled_at}`}
          >
            polled {fmtAgeStr(peer.last_polled_at)}
          </span>
        )}
      </div>

      <div className="flex items-center gap-0.5 shrink-0" onClick={(e) => e.stopPropagation()}>
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

// TransportChip — visual marker for HTTPS-pull vs S3 dead-drop peers.
// Same shape across the page, sized to fit alongside region/role chips.
function TransportChip({ transport }: { transport: string }) {
  const t = transport || 'https_pull';
  const isS3 = t === 's3_dead_drop';
  const cls = isS3
    ? 'bg-amber-50 text-amber-800 ring-amber-200'
    : 'bg-cyan-50 text-cyan-800 ring-cyan-200';
  const label = isS3 ? 'S3' : 'HTTPS';
  return (
    <span
      className={`text-[10px] uppercase tracking-wide ring-1 px-1.5 py-0.5 rounded ${cls}`}
      title={t}
    >
      {label}
    </span>
  );
}

// PeerDetailDrawer — right-side drawer with the full picture for
// one federation peer. Shows transport, polling timestamps,
// introspect counts + features, federation URL/bucket details, and
// the most recent error. Opens on row click; closes via X or the
// dimmed backdrop.
//
// The peer prop is re-resolved from the parent's live list on every
// render, so 8s-tick refreshes flow into the drawer automatically
// without a separate subscription.
function PeerDetailDrawer({
  peer,
  refreshing,
  onRefresh,
  onClose,
}: {
  peer: FederationPeer;
  refreshing: boolean;
  onRefresh: () => void;
  onClose: () => void;
}) {
  const intro = peer.introspect;
  const counts = intro?.counts ?? {};
  const features = intro?.features ?? {};
  const seenAge = peer.heartbeat_age_sec ?? 0;
  const seenLabel =
    !peer.last_seen_at ? 'never'
    : seenAge < 60 ? `${seenAge}s ago`
    : seenAge < 3600 ? `${Math.round(seenAge / 60)}m ago`
    : `${Math.round(seenAge / 3600)}h ago`;
  const polledLabel = peer.last_polled_at ? fmtAgeStr(peer.last_polled_at) : 'never';

  const polledMs = peer.last_polled_at ? new Date(peer.last_polled_at).getTime() : 0;
  const seenMs = peer.last_seen_at ? new Date(peer.last_seen_at).getTime() : 0;
  const stalePolling = polledMs > 0 && seenMs > 0 && polledMs - seenMs > 60_000;

  return (
    <div
      className="fixed inset-0 bg-black/30 z-40 flex justify-end"
      onClick={onClose}
    >
      <aside
        className="w-[520px] h-full bg-panel border-l border-border shadow-xl flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-5 py-4 border-b border-border flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <h2 className="text-sm font-semibold truncate">
                {peer.display_name || intro?.display_name || peer.url}
              </h2>
              <TransportChip transport={peer.transport} />
              {intro?.region && (
                <span className="text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded">
                  {intro.region}
                </span>
              )}
              {peer.healthy
                ? <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-700 bg-green-50 ring-1 ring-green-200 px-1.5 py-0.5 rounded"><CheckCircle2 size={9} />healthy</span>
                : stalePolling
                ? <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-orange-700 bg-orange-50 ring-1 ring-orange-200 px-1.5 py-0.5 rounded"><AlertCircle size={9} />flapping</span>
                : <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-red-700 bg-red-50 ring-1 ring-red-200 px-1.5 py-0.5 rounded"><AlertCircle size={9} />stale</span>
              }
            </div>
            {intro?.instance_id && (
              <div className="text-[11px] text-ink-mute font-mono mt-0.5 truncate">
                id {intro.instance_id}
              </div>
            )}
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <button
              onClick={onRefresh}
              disabled={refreshing}
              title="Force refresh now"
              className="p-1.5 text-ink-mute hover:text-brand-700 hover:bg-brand-50 rounded-md inline-flex items-center"
            >
              {refreshing ? <Loader2 size={13} className="animate-spin" /> : <RefreshCcw size={13} />}
            </button>
            <button
              onClick={onClose}
              className="p-1.5 text-ink-mute hover:text-ink hover:bg-slate-100 rounded-md"
              title="Close"
            >
              <span className="text-base leading-none">×</span>
            </button>
          </div>
        </header>

        <div className="flex-1 overflow-auto p-5 space-y-4 text-sm">
          {peer.last_error && (
            <div className="border border-red-200 bg-red-50 rounded-md p-3 text-xs text-red-800">
              <div className="font-semibold mb-1">Last error</div>
              <div className="font-mono whitespace-pre-wrap break-all">{peer.last_error}</div>
            </div>
          )}

          <DrawerCard title="Connection">
            <DrawerKV label="Transport" mono>{peer.transport || 'https_pull'}</DrawerKV>
            {peer.transport === 's3_dead_drop' && peer.bucket_prefix && (
              <DrawerKV label="Bucket prefix" mono>{peer.bucket_prefix}</DrawerKV>
            )}
            {peer.transport_config_id && peer.transport_config_id > 0 && (
              <DrawerKV label="Transport config" mono>#{peer.transport_config_id}</DrawerKV>
            )}
            <DrawerKV label="URL" mono>
              <a href={peer.url} target="_blank" rel="noreferrer" className="hover:underline inline-flex items-center gap-1">
                {peer.url} <ExternalLink size={9} />
              </a>
            </DrawerKV>
            <DrawerKV label="Last seen">{seenLabel}</DrawerKV>
            <DrawerKV label="Last polled">
              <span className={stalePolling ? 'text-orange-700' : ''}>{polledLabel}</span>
            </DrawerKV>
            <DrawerKV label="Added">{fmtAgeStr(peer.added_at)}</DrawerKV>
          </DrawerCard>

          <DrawerCard title="Aggregated counts">
            <DrawerKV label="Daimons">
              {`${counts.daimons_healthy ?? 0} healthy / ${counts.daimons ?? 0} total`}
            </DrawerKV>
            <DrawerKV label="Nodes">{counts.nodes ?? 0}</DrawerKV>
            <DrawerKV label="Open findings">
              <span className={(counts.open_findings ?? 0) > 0 ? 'text-yellow-700 font-semibold' : ''}>
                {counts.open_findings ?? 0}
              </span>
            </DrawerKV>
          </DrawerCard>

          {(intro?.role || intro?.version || intro?.daemon_version) && (
            <DrawerCard title="Identity">
              {intro?.role && <DrawerKV label="Role" mono>{intro.role}</DrawerKV>}
              {intro?.version && <DrawerKV label="CP version" mono>{intro.version}</DrawerKV>}
              {intro?.daemon_version && <DrawerKV label="Daemon version" mono>{intro.daemon_version}</DrawerKV>}
              {intro?.webhook_public_url && <DrawerKV label="Webhook URL" mono>{intro.webhook_public_url}</DrawerKV>}
              {intro?.mgmt_public_url && <DrawerKV label="Mgmt URL" mono>{intro.mgmt_public_url}</DrawerKV>}
            </DrawerCard>
          )}

          {Object.keys(features).length > 0 && (
            <DrawerCard title="Features">
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(features).map(([k, v]) => (
                  <span
                    key={k}
                    className={`text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1 ${
                      v
                        ? 'bg-emerald-50 text-emerald-800 ring-emerald-200'
                        : 'bg-slate-50 text-slate-600 ring-slate-200'
                    }`}
                  >
                    {k}{!v && ' (off)'}
                  </span>
                ))}
              </div>
            </DrawerCard>
          )}
        </div>
      </aside>
    </div>
  );
}

function DrawerCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-border rounded-md bg-white p-4">
      <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2">
        {title}
      </h3>
      <div className="space-y-1.5 text-sm">{children}</div>
    </div>
  );
}

function DrawerKV({ label, children, mono }: { label: string; children: React.ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[140px_1fr] gap-2 items-baseline">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute">{label}</div>
      <div className={`min-w-0 break-all ${mono ? 'font-mono text-xs' : 'text-sm'}`}>{children}</div>
    </div>
  );
}

// fmtAgeStr renders an ISO timestamp as "Ns / Nm / Nh ago". Same
// granularity as the heartbeat-age-sec UI so the two timestamps read
// consistently next to each other.
function fmtAgeStr(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  return `${Math.round(s / 3600)}h ago`;
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
  // Four flows under the same dialog because they share the same
  // mental model ("get a child CP into this federation"):
  //   • "Managed deploy" — parent provisions the VM via cloud APIs +
  //     the new CP auto-registers (Phase 21.3+).
  //   • "S3 dead-drop" — bucket-pipe transport, no inbound HTTPS to
  //     the child needed. Mirrors Nodes' Add → S3 dead-drop tab.
  //   • "Generate bundle" — parent emits a tar.gz the operator drops
  //     on a host they manage (Phase 21.1) — HTTPS bootstrap formats.
  //   • "Connect existing" — the child is already running with a
  //     federation token; we just probe + add the peer row.
  const [mode, setMode] = useState<'managed' | 's3' | 'generate' | 'connect'>('managed');

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

        <div className="px-5 pt-3 flex items-center gap-1 border-b border-border/60 flex-wrap">
          <ModeTab active={mode === 'managed'}  onClick={() => setMode('managed')}  label="Managed deploy" />
          <ModeTab active={mode === 's3'}       onClick={() => setMode('s3')}       label="S3 dead-drop" />
          <ModeTab active={mode === 'generate'} onClick={() => setMode('generate')} label="Generate bundle" />
          <ModeTab active={mode === 'connect'}  onClick={() => setMode('connect')}  label="Connect existing" />
        </div>

        {mode === 'managed'  && <ManagedDeployPanel onClose={onClose} />}
        {mode === 's3'       && <S3DeadDropPanel    onClose={onClose} />}
        {mode === 'generate' && <GenerateBundlePanel onClose={onClose} />}
        {mode === 'connect'  && <ConnectExistingPanel onClose={onClose} onAdded={onAdded} />}
      </div>
    </div>
  );
}

// ManagedDeployPanel collects {cloud, credential, region, cloud-specific
// params} and submits a /api/federation/cp-provision request that
// kicks the per-cloud Provisioner. Phase 21.3a ships the framework:
// the cloud picker is gated on the parent's provisioner registry,
// which is empty in 21.3a — operators see a "no clouds available
// yet" panel with a pointer to the bundle / connect flows.
function ManagedDeployPanel({ onClose }: { onClose: () => void }) {
  const [provisioners, setProvisioners] = useState<string[] | null>(null);
  const [credentials, setCredentials] = useState<CloudCredential[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [cloud, setCloud] = useState<CloudKind | ''>('');
  const [credentialID, setCredentialID] = useState<number | null>(null);
  const [displayName, setDisplayName] = useState('');
  const [region, setRegion] = useState('');
  // Phase 21.6 — transport choice. 'https' uses the existing mTLS
  // bootstrap callback; 's3_dead_drop' uses the bucket pipe so the
  // new CP doesn't need an inbound connection back to the parent.
  const [transport, setTransport] = useState<'https' | 's3_dead_drop'>('https');
  const [transportConfigID, setTransportConfigID] = useState<number | null>(null);
  const [transportConfigs, setTransportConfigs] = useState<TransportConfigSummary[] | null>(null);
  // Structured cloud_params — populated by per-cloud form components.
  // Submitting an empty object hits the worker's validator instantly,
  // so the form below blocks submit until required keys are present.
  const [cloudParams, setCloudParams] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState<{ id: number; status: string } | null>(null);

  // Phase 21.5 — live cost preview + budget context. Re-fetched
  // (debounced) on every change to (cloud, credential, params).
  const [estimate, setEstimate] = useState<CPProvisionEstimate | null>(null);
  const [overrideBudget, setOverrideBudget] = useState(false);

  // Reset cloud_params when cloud changes — different schemas, no
  // sense carrying OCI fields into an AWS submit.
  useEffect(() => {
    setCloudParams({});
  }, [cloud]);

  useEffect(() => {
    if (!cloud || !credentialID) {
      setEstimate(null);
      return;
    }
    let cancelled = false;
    const handle = setTimeout(() => {
      api.cpProvisionEstimate({ cloud, credential_id: credentialID, cloud_params: cloudParams })
        .then((est) => { if (!cancelled) setEstimate(est); })
        .catch(() => { if (!cancelled) setEstimate(null); });
    }, 250);
    return () => { cancelled = true; clearTimeout(handle); };
  }, [cloud, credentialID, cloudParams]);

  // Re-allow submit when budget context shifts back under the cap.
  useEffect(() => {
    if (estimate && !estimate.would_exceed_budget) {
      setOverrideBudget(false);
    }
  }, [estimate]);

  useEffect(() => {
    api.cpProvisionersList()
      .then((r) => setProvisioners(r.clouds))
      .catch((e) => setError(String(e)));
    api.cloudCredentialsList()
      .then(setCredentials)
      .catch((e) => setError(String(e)));
  }, []);

  // Phase 21.6 — load transport_configs lazily once the operator
  // picks the s3_dead_drop transport. The list is small.
  useEffect(() => {
    if (transport === 's3_dead_drop' && transportConfigs === null) {
      api.transportConfigs()
        .then((rows) => {
          setTransportConfigs(rows);
          if (rows.length > 0 && transportConfigID === null) {
            setTransportConfigID(rows[0].id);
          }
        })
        .catch((e) => setError('load transport configs: ' + String(e)));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [transport]);

  // Auto-pick the first registered cloud + first credential of that
  // cloud as a usability nicety.
  useEffect(() => {
    if (cloud === '' && provisioners && provisioners.length > 0) {
      setCloud(provisioners[0] as CloudKind);
    }
  }, [provisioners, cloud]);
  useEffect(() => {
    if (cloud && credentials) {
      const match = credentials.find((c) => c.cloud === cloud);
      if (match) {
        setCredentialID(match.id);
        if (!region && match.region) setRegion(match.region);
      } else {
        setCredentialID(null);
      }
    }
  }, [cloud, credentials, region]);

  const usableCreds = (credentials ?? []).filter((c) => c.cloud === cloud);

  // Per-cloud required-key list. Mirrors the validators in
  // controlplane/cpprovision/{oci,aws}/*.go so the UI blocks the
  // submit before the worker rejects it.
  const missing = cloudParamsMissingFields(cloud, cloudParams);

  async function submit() {
    if (!cloud || !credentialID) return;
    if (missing.length > 0) {
      setError(`missing required cloud_params: ${missing.join(', ')}`);
      return;
    }
    if (transport === 's3_dead_drop' && !transportConfigID) {
      setError('pick a bucket (transport_config) for the s3_dead_drop transport');
      return;
    }
    setBusy(true); setError(null);
    try {
      const row = await api.cpProvisionCreate({
        display_name: displayName,
        region,
        cloud,
        credential_id: credentialID,
        cloud_params: cloudParams,
        transport: transport === 'https' ? undefined : transport,
        transport_config_id: transport === 's3_dead_drop' ? (transportConfigID ?? undefined) : undefined,
        force_over_budget: overrideBudget || undefined,
      });
      setSubmitted({ id: row.id, status: row.status });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // Loading state — registry + creds in flight.
  if (provisioners === null || credentials === null) {
    return (
      <div className="p-5 text-xs text-ink-mute flex items-center gap-2">
        <Loader2 size={12} className="animate-spin" /> Loading provisioners…
      </div>
    );
  }

  // No registered clouds — Phase 21.3a default state. Help the operator
  // toward the working alternatives.
  if (provisioners.length === 0) {
    return (
      <>
        <div className="p-5 text-sm space-y-3">
          <div className="text-xs text-ink-dim">
            No cloud provisioners are registered on this CP yet. Phase 21.3a ships the framework;
            per-cloud impls land in upcoming releases (OCI in 21.3b, AWS in 21.3c).
          </div>
          <div className="text-xs bg-amber-50 border border-amber-200 rounded-md px-3 py-2 text-ink">
            For now, use <strong>Generate bundle</strong> to download a tar.gz you can run on a
            VM you provisioned manually, or <strong>Connect existing</strong> if the child CP
            is already running.
          </div>
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        </footer>
      </>
    );
  }

  // Submitted — show the job-id + status, point at the Federation page
  // for the live job log.
  if (submitted) {
    return (
      <>
        <div className="p-5 text-sm space-y-3">
          <div className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 rounded-md px-3 py-2">
            Provision job <strong>#{submitted.id}</strong> created (status: <code>{submitted.status}</code>).
            Close this dialog and watch progress in the <strong>Managed deploys</strong> panel below
            the federation peers list — it auto-refreshes while the deploy is in flight.
          </div>
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        </footer>
      </>
    );
  }

  return (
    <>
      <div className="p-5 space-y-3 text-sm max-h-[70vh] overflow-y-auto">
        <p className="text-xs text-ink-dim">
          Provisions a child CP via your stored cloud credentials. The new VM auto-registers
          with this parent on first boot — no manual intervention.
        </p>
        <Field label="Display name">
          <input
            type="text" value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="us-east-prod"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Cloud">
          <select
            value={cloud}
            onChange={(e) => setCloud(e.target.value as CloudKind)}
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
          >
            {provisioners.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </Field>
        <Field label="Credential">
          <select
            value={credentialID ?? ''}
            onChange={(e) => setCredentialID(e.target.value ? Number(e.target.value) : null)}
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
          >
            <option value="">— select —</option>
            {usableCreds.map((c) => (
              <option key={c.id} value={c.id}>{c.name}{c.region ? ` (${c.region})` : ''}</option>
            ))}
          </select>
          {usableCreds.length === 0 && (
            <div className="text-[11px] text-amber-700 mt-1">
              No credentials saved for {cloud}. Add one under Settings → Cloud first.
            </div>
          )}
        </Field>
        <Field label="Region">
          <input
            type="text" value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder="defaults to credential's region"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Federation transport" hint="How the new CP talks back to this parent. HTTPS works when the new VM has inbound access to this CP; S3 dead-drop is the bucket pipe — slower (~60s round trip) but works through any NAT / firewall.">
          <div className="space-y-1.5">
            <label className="flex items-start gap-2 cursor-pointer">
              <input
                type="radio"
                checked={transport === 'https'}
                onChange={() => setTransport('https')}
                className="mt-0.5"
              />
              <div className="text-xs">
                <div className="font-medium text-ink">HTTPS (mTLS bootstrap)</div>
                <div className="text-ink-mute">New CP calls back to <code>/api/v1/cp/bootstrap</code> after first boot. Fastest; needs the parent reachable from the new VM.</div>
              </div>
            </label>
            <label className="flex items-start gap-2 cursor-pointer">
              <input
                type="radio"
                checked={transport === 's3_dead_drop'}
                onChange={() => setTransport('s3_dead_drop')}
                className="mt-0.5"
              />
              <div className="text-xs">
                <div className="font-medium text-ink">S3 dead-drop</div>
                <div className="text-ink-mute">Bucket pipe; both ends only need outbound HTTPS to the bucket. Pre-registers the federation peer at bundle time — no inbound bootstrap callback.</div>
              </div>
            </label>
          </div>
        </Field>
        {transport === 's3_dead_drop' && (
          <Field label="Bucket (transport_config)" hint="Which bucket the new CP publishes to + this parent reads from. Pick an existing transport_config that both ends can reach.">
            {transportConfigs === null ? (
              <div className="text-xs text-ink-mute">loading…</div>
            ) : transportConfigs.length === 0 ? (
              <div className="text-xs text-amber-700">
                No transport_configs configured. Add one under Settings → Cloud → Object storage buckets,
                or via Nodes → Add Node → S3 dead-drop, before continuing.
              </div>
            ) : (
              <select
                value={transportConfigID ?? ''}
                onChange={(e) => setTransportConfigID(e.target.value ? Number(e.target.value) : null)}
                className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
              >
                {transportConfigs.map((tc) => (
                  <option key={tc.id} value={tc.id}>
                    {tc.name} · {tc.bucket} @ {tc.endpoint}
                  </option>
                ))}
              </select>
            )}
          </Field>
        )}
        {cloud && credentialID && (
          <CloudParamsForm
            cloud={cloud}
            credentialID={credentialID}
            region={region}
            value={cloudParams}
            onChange={setCloudParams}
          />
        )}
        {estimate && <CostEstimateLine estimate={estimate} />}
        {estimate?.would_exceed_budget && (
          <label className="flex items-start gap-2 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md cursor-pointer">
            <input
              type="checkbox"
              checked={overrideBudget}
              onChange={(e) => setOverrideBudget(e.target.checked)}
              className="mt-0.5"
            />
            <span>
              I acknowledge this would push the credential's projected monthly spend to{' '}
              <strong>${estimate.projected_monthly_usd?.toFixed(2)}</strong>, over the{' '}
              <strong>${estimate.monthly_budget_usd?.toFixed(2)}</strong> cap. Provision anyway.
            </span>
          </label>
        )}
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex items-center justify-between gap-2">
        <div className="text-[11px] text-ink-mute">
          {missing.length > 0
            ? <>missing: <span className="text-red-700">{missing.join(', ')}</span></>
            : <>all required fields populated</>
          }
        </div>
        <div className="flex items-center gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={
              busy || !displayName || !cloud || !credentialID || !region ||
              missing.length > 0 ||
              (estimate?.would_exceed_budget === true && !overrideBudget)
            }
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy && <Loader2 size={12} className="animate-spin" />}
            Provision
          </button>
        </div>
      </footer>
    </>
  );
}

// cloudParamsMissingFields enumerates which keys the per-cloud
// Provisioner.Launch validator will reject if absent. Mirrors the
// requireKeys() / decodeLaunchParams() logic on the server so the UI
// blocks the submit instead of letting the worker fail post-token-mint.
function cloudParamsMissingFields(cloud: CloudKind | '', params: Record<string, unknown>): string[] {
  const has = (k: string) => {
    const v = params[k];
    if (v === undefined || v === null) return false;
    if (typeof v === 'string') return v.trim() !== '';
    if (Array.isArray(v)) return v.length > 0;
    return true;
  };
  switch (cloud) {
    case 'oci':
      return ['compartment_id', 'availability_domain', 'subnet_id', 'image_id', 'shape']
        .filter((k) => !has(k));
    case 'aws':
      return ['ami_id', 'instance_type', 'subnet_id', 'security_group_ids']
        .filter((k) => !has(k));
    default:
      return [];
  }
}

function CostEstimateLine({ estimate }: { estimate: CPProvisionEstimate }) {
  const fmt = (v?: number) => v != null ? `$${v.toFixed(2)}` : '—';
  const noEstimate = estimate.hourly_usd == null;
  return (
    <div className={cn(
      'text-xs px-3 py-2 rounded-md border',
      estimate.would_exceed_budget
        ? 'bg-red-50 border-red-200 text-red-700'
        : 'bg-slate-50 border-border text-ink-dim',
    )}>
      <div className="flex items-center justify-between gap-3">
        <span>
          <strong className="text-ink">Estimated cost:</strong>{' '}
          {noEstimate
            ? <span className="italic">no catalog entry for {estimate.instance_shape || 'this shape'} — submit will not enforce budget.</span>
            : <>{fmt(estimate.hourly_usd)}/hr · {fmt(estimate.monthly_usd)}/mo</>
          }
        </span>
        <span className="text-[11px] text-ink-mute">catalog {estimate.catalog_version}</span>
      </div>
      {estimate.monthly_budget_usd != null && (
        <div className="mt-1 text-[11px]">
          credential budget: {fmt(estimate.current_monthly_usd)} current
          {' '}+ {fmt(estimate.monthly_usd ?? 0)} new
          {' '}= <strong>{fmt(estimate.projected_monthly_usd)}</strong>
          {' '}of {fmt(estimate.monthly_budget_usd)} cap
          {estimate.unknown_active_count ? ` (+${estimate.unknown_active_count} active w/o catalog data)` : ''}
        </div>
      )}
      {estimate.note && !estimate.would_exceed_budget && (
        <div className="mt-1 text-[11px] text-ink-mute">{estimate.note}</div>
      )}
    </div>
  );
}

function ModeTab({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'px-3 py-2 text-xs font-medium border-b-2 -mb-px',
        active ? 'border-brand-500 text-brand-700' : 'border-transparent text-ink-dim hover:text-ink',
      )}
    >
      {label}
    </button>
  );
}

function ConnectExistingPanel({ onClose, onAdded }: { onClose: () => void; onAdded: (p: FederationPeer) => void }) {
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
    <>
      <div className="p-5 space-y-3 text-sm">
        <p className="text-xs text-ink-dim">
          The child CP must already be running with <code className="bg-slate-100 px-1 rounded">--federation-token</code> set.
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
    </>
  );
}

function GenerateBundlePanel({ onClose }: { onClose: () => void }) {
  const [displayName, setDisplayName] = useState('');
  const [region, setRegion] = useState('');
  const [format, setFormat] = useState<'dockerfile-tarball' | 'compose-tarball' | 'terraform'>('dockerfile-tarball');
  const [cloud, setCloud] = useState<'oci' | 'aws'>('oci');
  const [parentURL, setParentURL] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  async function submit() {
    setBusy(true); setError(null); setDone(null);
    try {
      const { blob, filename } = await api.cpBundle({
        display_name: displayName,
        region,
        format,
        cloud: format === 'terraform' ? cloud : undefined,
        parent_url: parentURL || undefined,
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url; a.download = filename;
      document.body.appendChild(a); a.click(); a.remove();
      URL.revokeObjectURL(url);
      setDone(filename);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="p-5 space-y-3 text-sm">
        <p className="text-xs text-ink-dim">
          Mints a one-time-use bootstrap token + drops it in a downloadable bundle. The new CP
          auto-registers with this parent on first boot via inbound HTTPS — no manual peer add
          required. For air-gapped / NAT'd children, use the <strong>S3 dead-drop</strong> tab
          instead.
        </p>
        <Field label="Display name">
          <input
            type="text"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="us-east-prod"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Region">
          <input
            type="text"
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder="us-east-1"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Format">
          <div className="space-y-1.5">
            <label className="flex items-start gap-2 cursor-pointer">
              <input type="radio" checked={format === 'dockerfile-tarball'} onChange={() => setFormat('dockerfile-tarball')} className="mt-0.5" />
              <div className="text-xs">
                <div className="font-medium text-ink">Dockerfile + binary</div>
                <div className="text-ink-mute">Operator's host builds the image locally. Works air-gapped.</div>
              </div>
            </label>
            <label className="flex items-start gap-2 cursor-pointer">
              <input type="radio" checked={format === 'compose-tarball'} onChange={() => setFormat('compose-tarball')} className="mt-0.5" />
              <div className="text-xs">
                <div className="font-medium text-ink">docker-compose + image tarball</div>
                <div className="text-ink-mute"><code>docker load</code> ships the parent's image — fastest first-boot.</div>
              </div>
            </label>
            <label className="flex items-start gap-2 cursor-pointer">
              <input type="radio" checked={format === 'terraform'} onChange={() => setFormat('terraform')} className="mt-0.5" />
              <div className="text-xs">
                <div className="font-medium text-ink">Terraform module</div>
                <div className="text-ink-mute">IaC export — operator runs <code>terraform apply</code> with their own cloud creds.</div>
              </div>
            </label>
          </div>
        </Field>
        {format === 'terraform' && (
          <Field label="Cloud" hint="Provider the rendered module targets. The cloud-init script is identical; only the instance resource shape differs.">
            <select
              value={cloud}
              onChange={(e) => setCloud(e.target.value as 'oci' | 'aws')}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
            >
              <option value="oci">OCI (oracle/oci)</option>
              <option value="aws">AWS (hashicorp/aws)</option>
            </select>
          </Field>
        )}
        <Field label="Parent URL (optional)">
          <input
            type="url"
            value={parentURL}
            onChange={(e) => setParentURL(e.target.value)}
            placeholder="defaults to this CP's --mgmt-public-url"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}
        {done && (
          <div className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 px-3 py-2 rounded-md">
            Downloaded <code>{done}</code>. Bootstrap token expires in 24h — apply it on the new host before then.
          </div>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
        <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        <button
          onClick={submit}
          disabled={busy || !displayName || !region}
          className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
        >
          {busy && <Loader2 size={12} className="animate-spin" />}
          Generate & download
        </button>
      </footer>
    </>
  );
}

// S3DeadDropPanel — promote-to-top-level tab for the bucket-pipe
// transport. Mirrors the Generate bundle flow but always emits the
// s3-dead-drop format and includes the transport_config picker
// inline. Once the operator clicks "Generate", the bundle is
// downloaded; first boot auto-registers via the bucket — same shape
// as Nodes' Add → S3 dead-drop tab.
function S3DeadDropPanel({ onClose }: { onClose: () => void }) {
  const [displayName, setDisplayName] = useState('');
  const [region, setRegion] = useState('');
  const [transportConfigID, setTransportConfigID] = useState<number | null>(null);
  const [transportConfigs, setTransportConfigs] = useState<TransportConfigSummary[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  useEffect(() => {
    api.transportConfigs()
      .then((rows) => {
        setTransportConfigs(rows);
        if (rows.length > 0) setTransportConfigID(rows[0].id);
      })
      .catch((e) => setError('load transport configs: ' + String(e)));
  }, []);

  async function submit() {
    if (!transportConfigID) {
      setError('pick a transport_config (which bucket the child publishes to)');
      return;
    }
    setBusy(true); setError(null); setDone(null);
    try {
      const { blob, filename } = await api.cpBundle({
        display_name: displayName,
        region,
        format: 's3-dead-drop',
        transport_config_id: transportConfigID,
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url; a.download = filename;
      document.body.appendChild(a); a.click(); a.remove();
      URL.revokeObjectURL(url);
      setDone(filename);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="p-5 space-y-3 text-sm">
        <p className="text-xs text-ink-dim">
          Bucket-pipe transport. Both CPs only need outbound HTTPS to the shared bucket — no
          inbound port required on either side. Worst-case round-trip ~60s vs ~2s for the
          mTLS path; acceptable for an air-gapped or NAT'd child where the alternative is
          unreachability.
        </p>
        <Field label="Display name">
          <input
            type="text"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="us-edge-prod"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Region">
          <input
            type="text"
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder="us-east-1"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Bucket (transport_config)" hint="Which bucket the child publishes to + the parent reads from. Pick an existing transport_config that both ends can reach.">
          {transportConfigs === null ? (
            <div className="text-xs text-ink-mute">loading…</div>
          ) : transportConfigs.length === 0 ? (
            <div className="text-xs text-amber-700">
              No transport_configs configured. Add one under <strong>Settings → Transport</strong>{' '}
              (or Nodes → Add Node → S3 dead-drop) first.
            </div>
          ) : (
            <select
              value={transportConfigID ?? ''}
              onChange={(e) => setTransportConfigID(e.target.value ? Number(e.target.value) : null)}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
            >
              {transportConfigs.map((tc) => (
                <option key={tc.id} value={tc.id}>
                  {tc.name} · {tc.bucket} @ {tc.endpoint}
                </option>
              ))}
            </select>
          )}
        </Field>
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}
        {done && (
          <div className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 px-3 py-2 rounded-md">
            Downloaded <code>{done}</code>. The new CP auto-registers on first boot — give it
            ~60s after start and check the Federation page for a fresh peer row.
          </div>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
        <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Close</button>
        <button
          onClick={submit}
          disabled={busy || !displayName || !region || !transportConfigID}
          className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
        >
          {busy && <Loader2 size={12} className="animate-spin" />}
          Generate & download
        </button>
      </footer>
    </>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
      {hint && <div className="text-[11px] text-ink-mute mt-1">{hint}</div>}
    </div>
  );
}

// ── Managed deploys panel ──────────────────────────────────────────
//
// Lists in-flight + recent cp_provisions rows below the peers list.
// Auto-refreshes every 5s while any row is non-terminal so the
// operator sees `queued → starting → cloud_init_running →
// bootstrap_pending → ready` progress live without having to leave
// the page. Each row is expandable to surface the worker's
// streamed log, the cloud-resource id (with console-URL link), and
// any error message on a failed deploy.

function ManagedDeploysPanel() {
  const [rows, setRows] = useState<CPProvision[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;
    async function tick() {
      try {
        const data = await api.cpProvisionsList(50);
        if (cancelled) return;
        setRows(data);
        setError(null);
        // Reschedule fast (5s) only if at least one row is still
        // moving; otherwise relax to 30s so we don't poll forever
        // on a fleet that isn't deploying.
        const live = data.some((r) => isNonTerminal(r.status));
        timer = window.setTimeout(tick, live ? 5000 : 30_000);
      } catch (e) {
        if (cancelled) return;
        setError(String(e));
        timer = window.setTimeout(tick, 30_000);
      }
    }
    tick();
    return () => { cancelled = true; if (timer) clearTimeout(timer); };
  }, []);

  if (rows === null) {
    return null; // first load — quiet so we don't flash a loading bar
  }
  if (rows.length === 0) {
    // Don't render an empty section — the +Add CP modal teaches the
    // operator about managed deploys; the panel only shows up once
    // there's history.
    return null;
  }

  function toggleExpand(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  }

  async function handleDelete(row: CPProvision) {
    // Two-tier confirm: row delete is always safe, cloud destroy is
    // opt-in. Failed/cancelled rows almost always have no live VM,
    // so we don't even offer destroy for them — the row delete is
    // the entire intent.
    const hasCloudResource = row.cloud_resource_id && row.cloud_resource_id.length > 0;
    const isTerminalNoCloud = row.status === 'failed' || row.status === 'cancelled';
    let destroy = false;

    if (hasCloudResource && !isTerminalNoCloud) {
      // Deploy succeeded or is in flight with a real VM — give the
      // operator the choice.
      const choice = window.confirm(
        `Delete provision #${row.id} (${row.display_name})?\n\n` +
        `Cloud resource: ${row.cloud_resource_id}\n\n` +
        `OK = ALSO destroy the cloud instance via ${row.cloud}.Destroy()\n` +
        `Cancel = back out without deleting\n\n` +
        `(To delete the row but leave the cloud instance running, click OK on the next prompt instead of this one.)`,
      );
      if (!choice) {
        const recordOnly = window.confirm(
          `Delete provision #${row.id} record only?\n\n` +
          `The cloud instance ${row.cloud_resource_id} will keep running and you'll need to clean it up via the cloud console.`,
        );
        if (!recordOnly) return;
      } else {
        destroy = true;
      }
    } else {
      // Failed/cancelled or no cloud_resource_id — single confirm.
      const ok = window.confirm(
        `Delete provision #${row.id} (${row.display_name})?\n\n` +
        (hasCloudResource ? `Cloud resource ${row.cloud_resource_id} (already terminated) will not be touched.` : 'No cloud resource was created — this is a record-only delete.'),
      );
      if (!ok) return;
    }

    try {
      const res = await api.cpProvisionDelete(row.id, destroy);
      // Optimistically prune from the visible list.
      setRows((prev) => (prev ?? []).filter((r) => r.id !== row.id));
      // Surface destroy errors inline — the row IS gone but the cloud
      // instance may still be running, which is operator-actionable.
      if (res && typeof res === 'object' && 'destroy_error' in res && res.destroy_error) {
        setError(`row deleted, but destroy failed: ${res.destroy_error} — clean up ${res.cloud_resource} via cloud console`);
      }
    } catch (e) {
      setError(`delete: ${String(e)}`);
    }
  }

  return (
    <section className="bg-panel border border-border rounded-xl shadow-card overflow-hidden">
      <header className="px-4 py-2.5 border-b border-border flex items-center justify-between">
        <div className="text-sm font-semibold flex items-center gap-2">
          <Server size={14} className="text-brand-500" />
          Managed deploys
        </div>
        <div className="text-[11px] text-ink-mute">{rows.length} job{rows.length === 1 ? '' : 's'}</div>
      </header>
      {error && (
        <div className="px-4 py-2 text-xs text-red-700 bg-red-50 border-b border-red-200">{error}</div>
      )}
      <ul className="divide-y divide-border">
        {rows.map((r) => (
          <ManagedDeployRow
            key={r.id}
            row={r}
            expanded={expanded.has(r.id)}
            onToggle={() => toggleExpand(r.id)}
            onDelete={() => handleDelete(r)}
          />
        ))}
      </ul>
    </section>
  );
}

function ManagedDeployRow({ row, expanded, onToggle, onDelete }: {
  row: CPProvision;
  expanded: boolean;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const Icon = expanded ? ChevronDown : ChevronRight;
  return (
    <li className="text-sm">
      <button
        onClick={onToggle}
        className="w-full px-4 py-2.5 flex items-center gap-3 hover:bg-slate-50 text-left"
      >
        <Icon size={14} className="text-ink-mute shrink-0" />
        <span className="font-medium truncate flex-1">{row.display_name}</span>
        <span className="text-[11px] text-ink-mute font-mono">{row.cloud}{row.region ? ` · ${row.region}` : ''}</span>
        <ProvisionStatusBadge status={row.status} />
        <span className="text-[11px] text-ink-mute hidden md:block w-32 text-right">
          {new Date(row.created_at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
        </span>
      </button>
      {expanded && (
        <div className="px-10 pb-3 space-y-2 text-xs">
          {row.error && (
            <div className="text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md font-mono whitespace-pre-wrap">
              {row.error}
            </div>
          )}
          {row.cloud_resource_id && (
            <div className="text-ink-dim">
              instance: <code className="bg-slate-100 px-1 rounded">{row.cloud_resource_id}</code>
              {row.cloud_resource_url && (
                <> · <a href={row.cloud_resource_url} target="_blank" rel="noreferrer" className="text-brand-700 hover:underline inline-flex items-center gap-0.5">cloud console <ExternalLink size={10} /></a></>
              )}
            </div>
          )}
          {row.instance_shape && (
            <div className="text-ink-mute">
              shape: <code className="bg-slate-100 px-1 rounded">{row.instance_shape}</code>
              {row.est_cost_per_hour_usd != null && <> · ~${(row.est_cost_per_hour_usd * 730).toFixed(2)}/mo est.</>}
            </div>
          )}
          {row.log && (
            <pre className="bg-slate-50 border border-border rounded-md p-2 text-[10px] font-mono whitespace-pre-wrap max-h-64 overflow-y-auto">
              {row.log.trim() || '(worker has not written any log lines yet)'}
            </pre>
          )}
          {!row.log && !row.error && row.status === 'queued' && (
            <div className="text-ink-mute italic">queued — waiting for the worker to pick this up.</div>
          )}
          <div className="flex justify-end pt-1">
            <button
              onClick={onDelete}
              title="Delete this provision row (and optionally destroy the cloud instance)"
              className="text-[11px] px-2 py-1 border border-border hover:bg-red-50 hover:text-red-700 rounded inline-flex items-center gap-1"
            >
              <Trash2 size={11} /> Delete
            </button>
          </div>
        </div>
      )}
    </li>
  );
}

function ProvisionStatusBadge({ status }: { status: string }) {
  const cfg = PROVISION_STATUS_CFG[status] ?? PROVISION_STATUS_CFG.unknown;
  return (
    <span className={cn(
      'inline-flex items-center gap-1 text-[11px] uppercase tracking-wide font-medium px-2 py-0.5 rounded-md ring-1 shrink-0',
      cfg.cls,
    )}>
      {cfg.label}
    </span>
  );
}

const PROVISION_STATUS_CFG: Record<string, { label: string; cls: string }> = {
  queued:               { label: 'queued',      cls: 'text-ink-mute bg-slate-50 ring-slate-200' },
  starting:             { label: 'starting',    cls: 'text-brand-700 bg-brand-50 ring-brand-100' },
  cloud_init_running:   { label: 'cloud-init',  cls: 'text-brand-700 bg-brand-50 ring-brand-100' },
  bootstrap_pending:    { label: 'bootstrap',   cls: 'text-amber-700 bg-amber-50 ring-amber-100' },
  ready:                { label: 'ready',       cls: 'text-emerald-700 bg-emerald-50 ring-emerald-200' },
  failed:               { label: 'failed',      cls: 'text-red-700 bg-red-50 ring-red-200' },
  cancelled:            { label: 'cancelled',   cls: 'text-ink-mute bg-slate-50 ring-slate-200' },
  unknown:              { label: 'unknown',     cls: 'text-ink-mute bg-slate-50 ring-slate-200' },
};

function isNonTerminal(status: string): boolean {
  switch (status) {
    case 'queued':
    case 'starting':
    case 'cloud_init_running':
    case 'bootstrap_pending':
      return true;
    default:
      return false;
  }
}

// ── Cloud-params structured form ───────────────────────────────────
//
// Replaces the old JSON textarea. Per cloud, surfaces labelled
// inputs/dropdowns and (for OCI) auto-discovers tenant resources via
// the /api/cloud-credentials/{id}/oci/* endpoints. All writes go
// through the parent component's setCloudParams so the cost-estimate
// + missing-fields validators see the same value at all times.

function CloudParamsForm({
  cloud, credentialID, region, value, onChange,
}: {
  cloud: CloudKind;
  credentialID: number;
  region: string;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  if (cloud === 'oci') {
    return <OciCloudParamsForm credentialID={credentialID} region={region} value={value} onChange={onChange} />;
  }
  if (cloud === 'aws') {
    return <AwsCloudParamsFormStub value={value} onChange={onChange} />;
  }
  return null;
}

function OciCloudParamsForm({
  credentialID, region, value, onChange,
}: {
  credentialID: number;
  region: string;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const compartmentID = (value.compartment_id as string) ?? '';
  const ad = (value.availability_domain as string) ?? '';
  const subnetID = (value.subnet_id as string) ?? '';
  const imageID = (value.image_id as string) ?? '';
  const shape = (value.shape as string) ?? '';
  const ocpus = value.ocpus as number | undefined;
  const memoryGB = value.memory_in_gbs as number | undefined;
  const sshKeys = (value.ssh_authorized_keys as string) ?? '';

  const set = (patch: Record<string, unknown>) => onChange({ ...value, ...patch });

  // Discovery state. Each list loads when its prerequisites are set.
  const [compartments, setCompartments] = useState<DiscoveryItem[] | null>(null);
  const [ads, setAds] = useState<DiscoveryItem[] | null>(null);
  const [subnets, setSubnets] = useState<DiscoveryItem[] | null>(null);
  const [images, setImages] = useState<DiscoveryItem[] | null>(null);
  const [shapes, setShapes] = useState<DiscoveryItem[] | null>(null);
  const [discoveryError, setDiscoveryError] = useState<string | null>(null);

  // Compartments load on credential/region change.
  useEffect(() => {
    let cancelled = false;
    setCompartments(null); setDiscoveryError(null);
    api.cloudDiscoveryOCI.compartments(credentialID, region || undefined)
      .then((items) => { if (!cancelled) setCompartments(items); })
      .catch((e) => { if (!cancelled) setDiscoveryError(`compartments: ${e}`); });
    return () => { cancelled = true; };
  }, [credentialID, region]);

  // ADs / subnets / shapes load when compartment is set. Images
  // depend ALSO on the selected shape — see the next effect — so an
  // operator can't accidentally pair an ARM image with an x86 shape
  // (OCI silently terminates the launch when arches don't match).
  useEffect(() => {
    if (!compartmentID) {
      setAds(null); setSubnets(null); setShapes(null);
      return;
    }
    let cancelled = false;
    api.cloudDiscoveryOCI.availabilityDomains(credentialID, compartmentID, region || undefined)
      .then((items) => { if (!cancelled) setAds(items); }).catch(() => {});
    api.cloudDiscoveryOCI.subnets(credentialID, compartmentID, { region: region || undefined })
      .then((items) => { if (!cancelled) setSubnets(items); }).catch(() => {});
    api.cloudDiscoveryOCI.shapes(credentialID, compartmentID, { region: region || undefined })
      .then((items) => { if (!cancelled) setShapes(items); }).catch(() => {});
    return () => { cancelled = true; };
  }, [credentialID, compartmentID, region]);

  // Images list is shape-scoped — OCI only returns images compatible
  // with the requested shape, so the operator can't pick a wrong-arch
  // image. Refires when shape changes.
  useEffect(() => {
    if (!compartmentID) {
      setImages(null);
      return;
    }
    let cancelled = false;
    api.cloudDiscoveryOCI.images(credentialID, compartmentID, {
      region: region || undefined,
      shape: shape || undefined,
    })
      .then((items) => { if (!cancelled) setImages(items); }).catch(() => {});
    return () => { cancelled = true; };
  }, [credentialID, compartmentID, region, shape]);

  // Shape attrs gate the OCPU/memory inputs (.Flex shapes only) and
  // give us min/max bounds for the number fields.
  const flexShape = shape.endsWith('.Flex') || shape.includes('.Flex.');
  const shapeAttrs = shapes?.find((s) => s.id === shape)?.attrs as
    | { ocpus_min?: number; ocpus_max?: number; memory_min_gb?: number; memory_max_gb?: number }
    | undefined;

  // Fill sensible defaults when shape changes — and clear the
  // image because the new shape's image list is about to refresh
  // and the previously-picked id may not be in it (different arch).
  useEffect(() => {
    if (shape && imageID) {
      // Only clear if the image is no longer in the latest list.
      // The list itself updates via the shape-scoped effect; this
      // local reset just keeps the form in a coherent state during
      // the brief gap between shape change and list refresh.
      if (images && !images.some((i) => i.id === imageID)) {
        set({ image_id: '' });
      }
    }
    if (flexShape && shapeAttrs && (ocpus == null || memoryGB == null)) {
      set({
        ocpus: ocpus ?? shapeAttrs.ocpus_min ?? 1,
        memory_in_gbs: memoryGB ?? Math.min(shapeAttrs.memory_max_gb ?? 8, Math.max(shapeAttrs.memory_min_gb ?? 8, 8)),
      });
    }
    if (!flexShape && (ocpus != null || memoryGB != null)) {
      // Strip flex-only fields when leaving a flex shape.
      const { ocpus: _o, memory_in_gbs: _m, ...rest } = value;
      void _o; void _m;
      onChange(rest);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shape]);

  return (
    <div className="bg-slate-50 border border-border rounded-md p-3 space-y-3">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium flex items-center justify-between">
        <span>OCI deployment target</span>
        <span className="normal-case text-ink-mute">auto-discovered from credential</span>
      </div>
      {discoveryError && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2 py-1 rounded">{discoveryError}</div>
      )}

      <Field label="Compartment" hint="Where the VM will be created. The tenancy root is the safe default if you don't have sub-compartments.">
        <DiscoverySelect
          items={compartments}
          value={compartmentID}
          onChange={(v) => set({
            compartment_id: v,
            // Resetting downstream selections on compartment change —
            // their valid values depend on it.
            availability_domain: '', subnet_id: '', image_id: '', shape: '',
          })}
          placeholder={compartments === null ? 'loading…' : '— pick compartment —'}
        />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="Availability domain">
          <DiscoverySelect
            items={ads} value={ad}
            onChange={(v) => set({ availability_domain: v })}
            placeholder={!compartmentID ? 'pick compartment first' : ads === null ? 'loading…' : '— pick AD —'}
            disabled={!compartmentID}
          />
        </Field>
        <Field label="Shape">
          <DiscoverySelect
            items={shapes} value={shape}
            onChange={(v) => set({ shape: v })}
            placeholder={!compartmentID ? 'pick compartment first' : shapes === null ? 'loading…' : '— pick shape —'}
            disabled={!compartmentID}
          />
        </Field>
      </div>

      <Field label="Subnet" hint="The VNIC attaches here. Subnets that prohibit public IPs are still selectable; provisioning will succeed but the bootstrap callback path needs your own NAT.">
        <DiscoverySelect
          items={subnets} value={subnetID}
          onChange={(v) => set({ subnet_id: v })}
          placeholder={!compartmentID ? 'pick compartment first' : subnets === null ? 'loading…' : '— pick subnet —'}
          disabled={!compartmentID}
          renderItem={(s) => `${s.name}${(s.attrs?.cidr_block as string) ? `  (${s.attrs!.cidr_block})` : ''}`}
        />
      </Field>

      <Field label="Image" hint="Latest build per OS+version is shown.">
        <DiscoverySelect
          items={images} value={imageID}
          onChange={(v) => set({ image_id: v })}
          placeholder={!compartmentID ? 'pick compartment first' : images === null ? 'loading…' : '— pick image —'}
          disabled={!compartmentID}
        />
      </Field>

      {flexShape && (
        <div className="grid grid-cols-2 gap-3">
          <Field label="OCPUs" hint={shapeAttrs ? `${shapeAttrs.ocpus_min}–${shapeAttrs.ocpus_max} for ${shape}` : ''}>
            <input
              type="number"
              value={ocpus ?? ''}
              min={shapeAttrs?.ocpus_min ?? 1}
              max={shapeAttrs?.ocpus_max ?? 64}
              step="0.25"
              onChange={(e) => set({ ocpus: e.target.value === '' ? undefined : Number(e.target.value) })}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
          <Field label="Memory (GB)" hint={shapeAttrs ? `${shapeAttrs.memory_min_gb}–${shapeAttrs.memory_max_gb} for ${shape}` : ''}>
            <input
              type="number"
              value={memoryGB ?? ''}
              min={shapeAttrs?.memory_min_gb ?? 1}
              max={shapeAttrs?.memory_max_gb ?? 1024}
              onChange={(e) => set({ memory_in_gbs: e.target.value === '' ? undefined : Number(e.target.value) })}
              className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </Field>
        </div>
      )}

      <Field label="SSH authorized keys (optional)" hint="One public key per line. Used for break-glass debugging only — the CP itself doesn't need SSH to function.">
        <textarea
          value={sshKeys}
          onChange={(e) => set({ ssh_authorized_keys: e.target.value })}
          rows={2}
          placeholder="ssh-ed25519 AAAA…"
          className="w-full px-3 py-1.5 text-xs font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
      </Field>
    </div>
  );
}

function DiscoverySelect({
  items, value, onChange, placeholder, disabled, renderItem,
}: {
  items: DiscoveryItem[] | null;
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  disabled?: boolean;
  renderItem?: (item: DiscoveryItem) => string;
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled || items === null}
      className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel disabled:opacity-50"
    >
      <option value="">{placeholder}</option>
      {(items ?? []).map((it) => (
        <option key={it.id} value={it.id}>
          {renderItem ? renderItem(it) : it.name}
        </option>
      ))}
    </select>
  );
}

// AwsCloudParamsFormStub — auto-discovery for AWS lands in a follow-up.
// Until then operators on AWS get the same JSON-textarea fallback they
// had pre-this-PR, with up-front validation so the worker can't
// fail-on-empty-payload.
function AwsCloudParamsFormStub({
  value, onChange,
}: {
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  // Edit each required field as a labelled input — no discovery yet.
  const set = (patch: Record<string, unknown>) => onChange({ ...value, ...patch });
  const sgs = (value.security_group_ids as string[] | undefined)?.join(',') ?? '';
  return (
    <div className="bg-slate-50 border border-border rounded-md p-3 space-y-3">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium flex items-center justify-between">
        <span>AWS deployment target</span>
        <span className="normal-case text-amber-700">auto-discovery for AWS lands in a follow-up</span>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="AMI id">
          <input value={(value.ami_id as string) ?? ''} onChange={(e) => set({ ami_id: e.target.value })}
            placeholder="ami-0abcd1234efgh"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
        </Field>
        <Field label="Instance type">
          <input value={(value.instance_type as string) ?? ''} onChange={(e) => set({ instance_type: e.target.value })}
            placeholder="t3.small"
            className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
        </Field>
      </div>
      <Field label="Subnet id">
        <input value={(value.subnet_id as string) ?? ''} onChange={(e) => set({ subnet_id: e.target.value })}
          placeholder="subnet-0abc1234"
          className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
      </Field>
      <Field label="Security group ids" hint="Comma-separated list of sg-… ids.">
        <input value={sgs}
          onChange={(e) => set({ security_group_ids: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })}
          placeholder="sg-0abc1234,sg-0def5678"
          className="w-full px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30" />
      </Field>
    </div>
  );
}
