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
import { api, type CloudCredential, type CloudKind, type FederationPeer } from '../api';
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
  // Three flows live under the same dialog because they share the same
  // mental model ("get a child CP into this federation"):
  //   • "Managed deploy" — parent provisions the VM via cloud APIs +
  //     the new CP auto-registers (Phase 21.3+).
  //   • "Generate bundle" — parent emits a tar.gz the operator drops
  //     on a host they manage (Phase 21.1).
  //   • "Connect existing" — the child is already running with a
  //     federation token; we just probe + add the peer row.
  const [mode, setMode] = useState<'managed' | 'generate' | 'connect'>('managed');

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

        <div className="px-5 pt-3 flex items-center gap-1 border-b border-border/60">
          <ModeTab active={mode === 'managed'}  onClick={() => setMode('managed')}  label="Managed deploy" />
          <ModeTab active={mode === 'generate'} onClick={() => setMode('generate')} label="Generate bundle" />
          <ModeTab active={mode === 'connect'}  onClick={() => setMode('connect')}  label="Connect existing" />
        </div>

        {mode === 'managed'  && <ManagedDeployPanel onClose={onClose} />}
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
  const [paramsJSON, setParamsJSON] = useState('{}');
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState<{ id: number; status: string } | null>(null);

  useEffect(() => {
    api.cpProvisionersList()
      .then((r) => setProvisioners(r.clouds))
      .catch((e) => setError(String(e)));
    api.cloudCredentialsList()
      .then(setCredentials)
      .catch((e) => setError(String(e)));
  }, []);

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

  async function submit() {
    if (!cloud || !credentialID) return;
    setBusy(true); setError(null);
    let cloudParams: Record<string, unknown>;
    try {
      cloudParams = paramsJSON.trim() ? JSON.parse(paramsJSON) : {};
    } catch (e) {
      setError('cloud_params is not valid JSON: ' + String(e));
      setBusy(false);
      return;
    }
    try {
      const row = await api.cpProvisionCreate({
        display_name: displayName,
        region,
        cloud,
        credential_id: credentialID,
        cloud_params: cloudParams,
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
            Watch its progress in the Federation page's "Managed deploys" panel.
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
        <Field label="Cloud params (JSON)" hint="Per-cloud knobs the Provisioner needs (subnet OCID, AMI id, shape, ...). Schema is provisioner-specific.">
          <textarea
            value={paramsJSON} onChange={(e) => setParamsJSON(e.target.value)}
            rows={4}
            className="w-full px-3 py-1.5 text-xs font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            placeholder='{ "subnet_id": "ocid1.subnet.oc1..xxx", "shape": "VM.Standard.E4.Flex" }'
          />
        </Field>
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
        <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
        <button
          onClick={submit}
          disabled={busy || !displayName || !cloud || !credentialID || !region}
          className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
        >
          {busy && <Loader2 size={12} className="animate-spin" />}
          Provision
        </button>
      </footer>
    </>
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
        parent_url: parentURL || undefined,
      });
      // Trigger the browser's save-as flow.
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
          auto-registers with this parent on first boot — no manual peer add required.
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
            <label className="flex items-start gap-2 cursor-pointer text-ink-dim">
              <input type="radio" checked={format === 'terraform'} onChange={() => setFormat('terraform')} className="mt-0.5" disabled />
              <div className="text-xs">
                <div className="font-medium">Terraform module</div>
                <div className="text-ink-mute">Coming in Phase 21.4 — IaC-friendly export.</div>
              </div>
            </label>
          </div>
        </Field>
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
          disabled={busy || !displayName || !region || format === 'terraform'}
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
