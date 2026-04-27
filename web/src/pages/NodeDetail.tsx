import { useEffect, useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  Activity,
  ArrowLeft,
  CheckCircle2,
  Clock,
  Cpu,
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
  Upload,
  Wifi,
  WifiOff,
} from 'lucide-react';
import { api, type AboutInfo, type DaimonItem, type NodeItem } from '../api';
import { cn } from '../lib/cn';
import EventTimeline from '../components/EventTimeline';
import { BinaryUpdateDialog, type BinaryAction } from '../components/BinaryUpdateDialog';

type Tab = 'events' | 'overview';

export default function NodeDetailPage() {
  const { id = '' } = useParams<{ id: string }>();
  const [node, setNode] = useState<NodeItem | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('events');
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [binaryAction, setBinaryAction] = useState<BinaryAction | null>(null);
  const [about, setAbout] = useState<AboutInfo | null>(null);
  const [agents, setAgents] = useState<DaimonItem[] | null>(null);

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
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const idNum = Number(id);
    const refresh = () => {
      api.node(idNum).then((n) => { if (!cancelled) setNode(n); }).catch((err) => { if (!cancelled) setError(String(err)); });
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
        {tab === 'overview' && <OverviewTab node={node} />}
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

function OverviewTab({ node }: { node: NodeItem }) {
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

function formatAgo(iso: string): string {
  const d = new Date(iso);
  const sec = Math.floor((Date.now() - d.getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
