import { useEffect, useMemo, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import {
  Activity,
  ArrowLeft,
  CheckCircle2,
  Clock,
  Cpu,
  Fingerprint,
  Hash,
  HardDrive,
  Info,
  Loader2,
  Lock,
  Package,
  Pause,
  Play,
  RefreshCw,
  RotateCcw,
  Server,
  Trash2,
  Upload,
  Wifi,
  WifiOff,
} from 'lucide-react';
import { api, ApiError, type AboutInfo, type DaimonItem, type KnownHostItem, type NodeItem, type User } from '../api';
import { LabelEditor } from '../components/labels/LabelEditor';
import { LabelStrip } from '../components/labels/LabelStrip';
import { cn } from '../lib/cn';
import EventTimeline from '../components/EventTimeline';
import { BinaryUpdateDialog, type BinaryAction } from '../components/BinaryUpdateDialog';

type Tab = 'events' | 'overview';

export default function NodeDetailPage() {
  const { id = '' } = useParams<{ id: string }>();
  const [params] = useSearchParams();
  const cp = params.get('cp') || undefined;
  const [node, setNode] = useState<NodeItem | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('events');
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [binaryAction, setBinaryAction] = useState<BinaryAction | null>(null);
  const [about, setAbout] = useState<AboutInfo | null>(null);
  const [agents, setAgents] = useState<DaimonItem[] | null>(null);
  const [user, setUser] = useState<User | null>(null);

  // Binary version this node is running. All daimons on a node share
  // a single okesu binary, so any agent's reported version is the
  // node's binary version. Empty when no daimon has heartbeated yet.
  const nodeBinaryVersion = useMemo(() => {
    if (!agents || !node) return '';
    const hostMatch = (a: DaimonItem) =>
      (node.daemon_hostname && a.host === node.daemon_hostname) ||
      a.host === node.hostname;
    const versions = agents.filter(hostMatch).map((a) => a.version).filter(Boolean) as string[];
    return versions[0] || '';
  }, [agents, node]);

  const updateAvailable = !!(
    about?.daemon_version &&
    nodeBinaryVersion &&
    about.daemon_version !== nodeBinaryVersion
  );

  async function toggleAutoUpdate() {
    if (!node) return;
    try {
      const next = await api.setNodeAutoUpdatePaused(node.id, !node.auto_update_paused);
      setNode(next);
    } catch (e) {
      setRefreshError(String(e));
    }
  }

  async function refreshMetadata() {
    if (!node) return;
    setRefreshing(true); setRefreshError(null);
    try {
      const updated = await api.refreshNodeMetadata(node.id);
      setNode(updated);
    } catch (e) {
      setRefreshError(String(e));
    } finally {
      setRefreshing(false);
    }
  }

  useEffect(() => {
    let cancelled = false;
    api.about().then((a) => { if (!cancelled) setAbout(a); }).catch(() => { /* ignore */ });
    api.me().then((u) => { if (!cancelled) setUser(u); }).catch(() => { /* ignore */ });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const idNum = Number(id);
    const refresh = () => {
      api.node(idNum, cp).then((n) => { if (!cancelled) setNode(n); }).catch((err) => { if (!cancelled) setError(String(err)); });
      api.connectedNodes().then((arr) => { if (!cancelled) setConnected(arr.includes((node?.name) || '')); }).catch(() => { /* ignore */ });
      api.daimons(500).then((rows) => { if (!cancelled) setAgents(rows); }).catch(() => { /* ignore */ });
    };
    refresh();
    const t = setInterval(refresh, 6_000);
    return () => { cancelled = true; clearInterval(t); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, node?.name]);

  if (error && !node) {
    return (
      <div className="p-6">
        <BackLink />
        <div className="mt-4 text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      </div>
    );
  }

  if (!node) {
    return <div className="p-6 text-ink-mute">Loading…</div>;
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 pt-4 pb-0 border-b border-border bg-panel">
        <BackLink />
        <div className="mt-2 flex items-center gap-4">
          <div className="w-12 h-12 shrink-0 rounded-xl bg-gradient-to-br from-slate-700 to-slate-900 flex items-center justify-center text-white shadow-sm">
            <Server size={18} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 flex-wrap">
              <h1 className="text-lg font-semibold">{node.name}</h1>
              <StatusPill status={node.status} message={node.status_message} />
              {connected && (
                <span className="inline-flex items-center gap-1 text-[11px] text-green-700 bg-green-50 ring-1 ring-green-200 px-1.5 py-0.5 rounded">
                  <Wifi size={9} /> tunnel live
                </span>
              )}
              {!connected && (
                <span className="inline-flex items-center gap-1 text-[11px] text-ink-mute bg-slate-50 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
                  <WifiOff size={9} /> tunnel offline
                </span>
              )}
            </div>
            <p className="text-xs text-ink-dim font-mono mt-0.5">
              {node.ssh_user}@{node.hostname}{node.ssh_port !== 22 ? ':' + node.ssh_port : ''}
            </p>
            <LabelStrip kind="node" idOrKey={node.id} className="mt-1.5" />
          </div>

          <div className="hidden md:flex items-stretch gap-3">
            <Stat label="Daimons" value={String(node.agents_installed.length)} icon={Activity} />
            <Stat
              label="Last deploy"
              value={node.last_deployed_at ? formatAgo(node.last_deployed_at) : '—'}
              icon={Clock}
            />
            <Stat label="Created" value={formatAgo(node.created_at)} icon={Hash} />
          </div>

          <div className="flex items-center gap-2">
            {nodeBinaryVersion && (
              updateAvailable ? (
                <span
                  className="inline-flex items-center gap-1 text-[11px] font-mono text-yellow-800 bg-yellow-50 ring-1 ring-yellow-200 px-2 py-1 rounded"
                  title={`This node runs okesu ${nodeBinaryVersion}; the CP would push ${about?.daemon_version} on Update.`}
                >
                  <Package size={11} /> bin {nodeBinaryVersion}
                  <span className="text-yellow-600">→ {about?.daemon_version}</span>
                </span>
              ) : (
                <span
                  className="inline-flex items-center gap-1 text-[11px] font-mono text-green-800 bg-green-50 ring-1 ring-green-200 px-2 py-1 rounded"
                  title="The node's binary matches the version the CP would push."
                >
                  <CheckCircle2 size={11} /> bin {nodeBinaryVersion}
                </span>
              )
            )}
            {node.auto_update_paused && (
              <span
                className="inline-flex items-center gap-1 text-[11px] font-mono text-slate-700 bg-slate-100 ring-1 ring-slate-300 px-2 py-1 rounded"
                title="Auto-update is paused — daimons on this node will not hot-reload until unpaused."
              >
                <Lock size={11} /> frozen
              </span>
            )}
            <button
              onClick={toggleAutoUpdate}
              title={node.auto_update_paused ? 'Resume auto-update — daimons hot-reload on next poll' : 'Pause auto-update — freeze the daimon definitions on this node'}
              className={cn(
                "inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border bg-panel hover:bg-slate-50",
                node.auto_update_paused ? "border-blue-300 bg-blue-50 text-blue-800 hover:bg-blue-100" : "border-border"
              )}
            >
              {node.auto_update_paused ? <Play size={12} /> : <Pause size={12} />}
              {node.auto_update_paused ? 'Resume' : 'Pause auto-update'}
            </button>
            <button
              onClick={refreshMetadata}
              disabled={refreshing || !connected}
              title={connected ? 'Probe the node over the tunnel and refresh telemetry' : 'Tunnel offline — wait for reconnect'}
              className="inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border border-border bg-panel hover:bg-slate-50 disabled:opacity-50"
            >
              {refreshing ? <Loader2 size={12} className="animate-spin" /> : <RefreshCw size={12} />}
              {refreshing ? 'Probing…' : 'Refresh metadata'}
            </button>
            <button
              onClick={() => setBinaryAction('update')}
              title="Upload the CP's current daemon binary; saves the prior version for rollback"
              className={cn(
                "inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border bg-panel hover:bg-slate-50",
                updateAvailable ? "border-yellow-400 bg-yellow-50 text-yellow-800 hover:bg-yellow-100" : "border-border"
              )}
            >
              <Upload size={12} />
              Update binary
            </button>
            <button
              onClick={() => setBinaryAction('rollback')}
              title="Restore the previous binary saved during the last update"
              className="inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border border-yellow-300 bg-yellow-50 text-yellow-800 hover:bg-yellow-100"
            >
              <RotateCcw size={12} />
              Roll back
            </button>
          </div>
        </div>

        {refreshError && (
          <div className="mt-2 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-1.5 rounded-md">
            {refreshError}
          </div>
        )}

        <nav className="mt-4 -mb-px flex gap-1">
          {([
            ['events',   'Live Events',  Activity],
            ['overview', 'Overview',     Info],
          ] as const).map(([key, label, Icon]) => (
            <button
              key={key}
              onClick={() => setTab(key)}
              className={cn(
                'inline-flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 -mb-px',
                tab === key
                  ? 'border-brand-500 text-brand-700 font-medium'
                  : 'border-transparent text-ink-dim hover:text-ink',
              )}
            >
              <Icon size={14} />
              {label}
            </button>
          ))}
        </nav>
      </header>

      <main className="flex-1 overflow-hidden">
        {tab === 'events' && (
          <EventTimeline
            hostFilter={node.daemon_hostname || node.hostname}
            showHeader={false}
            compact
            defaultMode="all"
            emptyHint={
              node.daemon_hostname
                ? `Events tagged with host=${node.daemon_hostname} will appear here.`
                : `Events tagged with host=${node.hostname} will appear here. This node hasn't been deployed since per-node host capture was added — re-deploy to record the daemon-side hostname automatically.`
            }
          />
        )}
        {tab === 'overview' && <OverviewTab node={node} isAdmin={user?.role === 'admin'} />}
      </main>
      {binaryAction && (
        <BinaryUpdateDialog
          node={node}
          action={binaryAction}
          onClose={() => setBinaryAction(null)}
          onDone={() => setBinaryAction(null)}
        />
      )}
    </div>
  );
}

function BackLink() {
  return (
    <Link to="/nodes" className="inline-flex items-center text-xs text-ink-dim hover:text-ink">
      <ArrowLeft size={12} className="mr-1" /> Nodes
    </Link>
  );
}

function StatusPill({ status, message }: { status: NodeItem['status']; message?: string }) {
  const cfg = {
    pending:   'text-ink-mute bg-slate-50 ring-slate-200',
    deploying: 'text-brand-700 bg-brand-50 ring-brand-100',
    ready:     'text-green-700 bg-green-50 ring-green-200',
    failed:    'text-red-700 bg-red-50 ring-red-200',
  }[status];
  return (
    <span title={message} className={cn('text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1', cfg)}>
      {status}
    </span>
  );
}

function Stat({ label, value, icon: Icon }: { label: string; value: string; icon: typeof Activity }) {
  return (
    <div className="bg-panel border border-border rounded-lg px-3 py-1.5 flex items-center gap-2 shadow-card">
      <Icon size={14} className="text-ink-mute" />
      <div>
        <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium">{label}</div>
        <div className="text-xs font-medium text-ink">{value}</div>
      </div>
    </div>
  );
}

function OverviewTab({ node, isAdmin }: { node: NodeItem; isAdmin: boolean }) {
  return (
    <div className="p-6 grid grid-cols-1 lg:grid-cols-2 gap-4">
      <Card title="Identity">
        <Row label="Name" mono>{node.name}</Row>
        <Row label="Hostname" mono>{node.hostname}</Row>
        <Row label="SSH" mono>{node.ssh_user}@{node.hostname}:{node.ssh_port}</Row>
        <Row label="Status">{node.status}</Row>
        {node.status_message && <Row label="Message">{node.status_message}</Row>}
        {node.notes && <Row label="Notes">{node.notes}</Row>}
      </Card>
      <Card title="Lifecycle">
        <Row label="Created" mono>{node.created_at}</Row>
        <Row label="Last status" mono>{node.last_status_at ?? '—'}</Row>
        <Row label="Last deploy" mono>{node.last_deployed_at ?? 'never'}</Row>
      </Card>
      <KnownHostCard nodeId={node.id} isAdmin={isAdmin} />
      <Card title="Telemetry" wide>
        {node.metadata_at ? (
          <>
            <Row label="Daemon hostname" mono>{node.daemon_hostname || '—'}</Row>
            <Row label="OS" mono>{node.os_release || '—'}</Row>
            <Row label="Kernel" mono>{node.kernel_release || '—'}</Row>
            <Row label="Arch" mono>
              <span className="inline-flex items-center gap-1">
                <Cpu size={11} className="text-ink-mute" /> {node.arch || '—'}
              </span>
            </Row>
            <Row label="CPU / RAM" mono>
              {node.cpu_count ? `${node.cpu_count} cores` : '—'}
              {node.memory_mb ? ` · ${(node.memory_mb / 1024).toFixed(1)} GB RAM` : ''}
            </Row>
            <Row label="Disk free" mono>
              <span className="inline-flex items-center gap-1">
                <HardDrive size={11} className="text-ink-mute" />
                {node.disk_free_mb ? `${(node.disk_free_mb / 1024).toFixed(1)} GB` : '—'}
              </span>
            </Row>
            <Row label="okesu version" mono>{node.okesu_version || '—'}</Row>
            <Row label="Last refreshed" mono>{node.metadata_at}</Row>
          </>
        ) : (
          <p className="text-xs text-ink-mute">
            No telemetry yet. Click <strong>Refresh metadata</strong> above to probe over the tunnel.
          </p>
        )}
      </Card>
      <Card title="Daimons installed" wide>
        {node.agents_installed.length === 0 ? (
          <p className="text-xs text-ink-mute">None yet — click <strong>Deploy</strong> on the Nodes page to install one.</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {node.agents_installed.map((a) => (
              <Link
                key={a}
                to={`/daimons/${encodeURIComponent(a)}`}
                className="text-xs bg-slate-100 hover:bg-slate-200 text-slate-700 px-2 py-0.5 rounded transition-colors"
              >
                {a}
              </Link>
            ))}
          </div>
        )}
      </Card>
      {/* RuntimesCard doesn't need an explicit refresh trigger — the
          NodeDetail page already auto-refreshes every 6s, so the new
          jobs_runtime_seen_at lands in the next tick. */}
      <RuntimesCard node={node} />
      {/* Phase 22.8 PR β — labels for selector-scoped permissions.
          Admins author them; the same selector grammar drives groups
          (PR α) and credential bindings (PR γ). */}
      <LabelsCard nodeID={node.id} isAdmin={isAdmin} />
    </div>
  );
}

// RuntimesCard — Phase 4 surface. Shows the live state of the
// pull-mode jobs runtime + any managed tunnel child, and exposes the
// "Install jobs runtime" button operators use to bootstrap nodes
// without going through the full deploy flow.
function RuntimesCard({ node }: { node: NodeItem }) {
  const [installOpen, setInstallOpen] = useState(false);

  const jobsAge = node.jobs_runtime_seen_at
    ? Math.floor((Date.now() - new Date(node.jobs_runtime_seen_at).getTime()) / 1000)
    : null;
  const jobsFresh = jobsAge !== null && jobsAge < 60;

  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5 lg:col-span-2">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <span className="w-1 h-4 rounded-full bg-gradient-to-b from-brand-400 to-brand-600 shrink-0" />
          <h3 className="text-sm font-semibold">Runtimes</h3>
        </div>
        <button
          onClick={() => setInstallOpen(true)}
          className="inline-flex items-center gap-1 bg-brand-500 hover:bg-brand-600 text-white text-xs font-medium px-3 py-1.5 rounded-md shadow-sm"
        >
          Install jobs runtime
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-3 text-xs">
        <RuntimeTile
          label="Jobs (pull queue)"
          state={jobsFresh ? 'live' : node.jobs_runtime_seen_at ? 'stale' : 'absent'}
          detail={
            jobsAge !== null
              ? `polled ${jobsAge < 60 ? `${jobsAge}s ago` : `${Math.floor(jobsAge / 60)}m ago`}`
              : 'not installed — click Install to provision via SSH'
          }
        />
        <RuntimeTile
          label="Tunnel (reverse mTLS)"
          state={node.tunnel_running ? 'live' : 'absent'}
          detail={
            node.tunnel_running
              ? 'okesu node child running on host'
              : 'on demand — orchestration steps with dispatch=tunnel will start one'
          }
        />
      </div>

      {installOpen && (
        <InstallJobsDialog
          node={node}
          onClose={() => setInstallOpen(false)}
          onCompleted={() => setInstallOpen(false)}
        />
      )}
    </section>
  );
}

// LabelsCard — k/v label editor. Admins can add / edit / delete;
// non-admins see the chips read-only. Selectors authored against
// these labels (group_roles.selector, credential_bindings.selector)
// drive PR β + PR γ scoping.
//
// The previous bespoke implementation has been replaced by the
// generic LabelEditor primitive (kind="node"). Same UX, fewer lines,
// shared code path with every other entity that gets labels.
function LabelsCard({ nodeID, isAdmin }: { nodeID: number; isAdmin: boolean }) {
  return (
    <section
      id="labels-card"
      className="bg-panel border border-border rounded-xl shadow-card p-5 lg:col-span-2 transition-shadow"
    >
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-xs uppercase tracking-wide font-semibold text-ink-mute">Labels</h3>
        {!isAdmin && (
          <span className="text-[11px] text-ink-mute italic">read-only</span>
        )}
      </div>
      <LabelEditor kind="node" idOrKey={nodeID} readonly={!isAdmin} />
    </section>
  );
}

function RuntimeTile({ label, state, detail }: { label: string; state: 'live' | 'stale' | 'absent'; detail: string }) {
  const tone =
    state === 'live'   ? 'bg-green-50 ring-green-200 text-green-800' :
    state === 'stale'  ? 'bg-amber-50 ring-amber-200 text-amber-800' :
                         'bg-slate-50 ring-slate-200 text-slate-600';
  const dot =
    state === 'live'   ? 'bg-green-500' :
    state === 'stale'  ? 'bg-amber-500' :
                         'bg-slate-400';
  return (
    <div className={cn('rounded-lg ring-1 px-3 py-2', tone)}>
      <div className="flex items-center gap-2 font-medium">
        <span className={cn('w-2 h-2 rounded-full', dot)} />
        {label}
      </div>
      <div className="text-[11px] mt-1 opacity-90">{detail}</div>
    </div>
  );
}

function InstallJobsDialog({
  node, onClose, onCompleted,
}: {
  node: NodeItem;
  onClose: () => void;
  onCompleted: () => void;
}) {
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [sudoPassword, setSudoPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [jobLog, setJobLog] = useState<string[]>([]);

  async function start() {
    setBusy(true); setError(null); setJobLog([]);
    try {
      const { job_id } = await api.installJobsRuntime(node.id, {
        private_key: privateKey,
        passphrase: passphrase || undefined,
        sudo_password: sudoPassword || undefined,
      });
      // Poll the job log until terminal.
      let last = 0;
      const t = setInterval(async () => {
        try {
          const snap = await api.job(job_id);
          if (snap.lines.length > last) {
            setJobLog(snap.lines);
            last = snap.lines.length;
          }
          if (snap.status === 'succeeded' || snap.status === 'failed') {
            clearInterval(t);
            setBusy(false);
            if (snap.status === 'succeeded') onCompleted();
            else setError(snap.error ?? 'install failed');
          }
        } catch (e) {
          clearInterval(t);
          setBusy(false);
          setError(String(e));
        }
      }, 1000);
    } catch (e) {
      setBusy(false);
      setError(String(e));
    }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/30 p-4">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl max-h-[80vh] overflow-hidden flex flex-col">
        <header className="px-5 py-3 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
          <h3 className="text-sm font-semibold">Install jobs runtime on {node.name}</h3>
          <p className="text-[11px] text-ink-dim mt-0.5">
            One-shot SSH install of <code className="font-mono">okesu-jobs.service</code>. The CP issues a fresh node mTLS cert; the daemon polls for orchestration step jobs.
          </p>
        </header>
        <div className="p-5 space-y-3 overflow-auto flex-1">
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md whitespace-pre-wrap">
              {error}
            </div>
          )}
          <FormField label="SSH private key" hint="PEM-encoded; same key the deploy flow uses">
            <textarea
              value={privateKey}
              onChange={(e) => setPrivateKey(e.target.value)}
              rows={6}
              spellCheck={false}
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              className="w-full text-[11px] font-mono px-2 py-1.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label="Passphrase (optional)">
              <input
                type="password"
                value={passphrase}
                onChange={(e) => setPassphrase(e.target.value)}
                className="w-full text-xs px-2 py-1.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
              />
            </FormField>
            <FormField label="Sudo password (optional)">
              <input
                type="password"
                value={sudoPassword}
                onChange={(e) => setSudoPassword(e.target.value)}
                className="w-full text-xs px-2 py-1.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
              />
            </FormField>
          </div>
          {jobLog.length > 0 && (
            <div>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">Install log</div>
              <pre className="text-[11px] font-mono bg-slate-50 border border-border rounded p-2 whitespace-pre-wrap break-words max-h-48 overflow-auto">
                {jobLog.join('\n')}
              </pre>
            </div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">
            Close
          </button>
          <button
            onClick={start}
            disabled={busy || !privateKey}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium shadow-sm"
          >
            {busy ? 'Installing…' : 'Install'}
          </button>
        </footer>
      </div>
    </div>
  );
}

function FormField({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
      {hint && <p className="text-[10px] text-ink-mute mt-0.5">{hint}</p>}
    </div>
  );
}

function Card({ title, children, wide }: { title: string; children: React.ReactNode; wide?: boolean }) {
  return (
    <section className={cn('bg-panel border border-border rounded-xl shadow-card p-5', wide && 'lg:col-span-2')}>
      <h3 className="text-sm font-semibold mb-3">{title}</h3>
      <dl className="space-y-1.5">{children}</dl>
    </section>
  );
}

function Row({ label, children, mono }: { label: string; children: React.ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[140px_1fr] text-sm gap-2">
      <dt className="text-ink-dim">{label}</dt>
      <dd className={cn('text-ink truncate', mono && 'font-mono text-xs')}>{children}</dd>
    </div>
  );
}

function KnownHostCard({ nodeId, isAdmin }: { nodeId: number; isAdmin: boolean }) {
  const [kh, setKh] = useState<KnownHostItem | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = () => {
    setError(null);
    api.nodeKnownHost(nodeId)
      .then((v) => setKh(v))
      .catch((e) => {
        setKh(null);
        setError(e instanceof ApiError ? e.message : String(e));
      });
  };

  useEffect(() => { refresh(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [nodeId]);

  async function clear() {
    if (!confirm('Clear the pinned host key for this node? The next deploy will re-establish trust on first connect (TOFU).')) return;
    setBusy(true); setError(null);
    try {
      await api.clearNodeKnownHost(nodeId);
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5 lg:col-span-2">
      <h3 className="text-sm font-semibold mb-3">SSH host key (pinned)</h3>

      {kh === undefined && <p className="text-xs text-ink-mute">Loading…</p>}

      {kh === null && (
        <p className="text-xs text-ink-mute">
          No host key pinned. The first deploy to this node will pin its key automatically (TOFU).
        </p>
      )}

      {kh && (
        <div className="space-y-2 text-sm">
          <div className="flex items-center gap-2 flex-wrap">
            <Fingerprint size={13} className="text-ink-mute shrink-0" />
            <span className="font-mono text-xs break-all">{kh.fingerprint}</span>
            {kh.key_type && (
              <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 px-1.5 py-0.5 rounded">
                {kh.key_type}
              </span>
            )}
          </div>
          <div className="text-[11px] text-ink-mute" title={kh.accepted_at}>
            Pinned {formatAgo(kh.accepted_at)}
            {kh.accepted_by_email && <> by {kh.accepted_by_email}</>}
          </div>
          {isAdmin && (
            <div className="pt-1">
              <button
                onClick={clear}
                disabled={busy}
                className="text-xs px-2 py-1 border border-border rounded-md hover:bg-red-50 hover:text-red-700 inline-flex items-center gap-1 disabled:opacity-50"
              >
                <Trash2 size={11} />
                {busy ? 'Clearing…' : 'Clear'}
              </button>
              <span className="text-[11px] text-ink-mute ml-3">
                After legitimate host-key rotation, click <strong>Clear</strong> and the next deploy will re-pin via TOFU.
              </span>
            </div>
          )}
        </div>
      )}

      {error && (
        <div className="mt-3 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      )}
    </section>
  );
}

function formatAgo(iso: string): string {
  const d = new Date(iso);
  const sec = Math.floor((Date.now() - d.getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
