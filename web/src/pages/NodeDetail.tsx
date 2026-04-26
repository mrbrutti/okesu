import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  Activity,
  ArrowLeft,
  Clock,
  Hash,
  Info,
  Server,
  Wifi,
  WifiOff,
} from 'lucide-react';
import { api, type NodeItem } from '../api';
import { cn } from '../lib/cn';
import EventTimeline from '../components/EventTimeline';

type Tab = 'events' | 'overview';

export default function NodeDetailPage() {
  const { id = '' } = useParams<{ id: string }>();
  const [node, setNode] = useState<NodeItem | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('events');

  useEffect(() => {
    let cancelled = false;
    const idNum = Number(id);
    const refresh = () => {
      api.node(idNum).then((n) => { if (!cancelled) setNode(n); }).catch((err) => { if (!cancelled) setError(String(err)); });
      api.connectedNodes().then((arr) => { if (!cancelled) setConnected(arr.includes((node?.name) || '')); }).catch(() => { /* ignore */ });
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
        </div>

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
