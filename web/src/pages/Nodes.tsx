import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  AlertCircle,
  Archive,
  CheckCircle2,
  ChevronRight,
  Circle,
  Loader2,
  Play,
  Plus,
  Server,
  Tag,
  Trash2,
  Upload,
  Wifi,
  X,
} from 'lucide-react';
import { api, subscribeJobLog, type EnrollmentPackageSummary, type FederationPeer, type NodeItem, type TransportConfigSummary } from '../api';
import { cn } from '../lib/cn';
import { SectionHeader, type SectionTone } from '../components/lists/SectionHeader';
import { ListCard } from '../components/lists/ListCard';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';
import { useSelection } from '../lib/useSelection';
import { BulkActionBar, BulkActionButton } from '../components/BulkActionBar';
import { BulkBinaryUpdateDialog } from '../components/BulkBinaryUpdateDialog';
import { CPSourceChip } from '../components/CPSourceChip';
import { AddNodeManaged } from '../components/nodes/AddNodeManaged';
import { NodeProvisionsPanel } from '../components/nodes/NodeProvisionsPanel';

const NODES_PAGE_SIZE = 500;

type Bucket = 'ready' | 'deploying' | 'failed' | 'pending' | 'archived';

const BUCKET_ORDER: Bucket[] = ['ready', 'deploying', 'failed', 'pending', 'archived'];
const BUCKET_LABEL: Record<Bucket, string> = {
  ready:     'Ready',
  deploying: 'Deploying',
  failed:    'Failed',
  pending:   'Pending',
  archived:  'Archived',
};
const BUCKET_TONE: Record<Bucket, SectionTone> = {
  ready:     'good',
  deploying: 'progress',
  failed:    'bad',
  pending:   'muted',
  archived:  'muted',
};

export default function NodesPage() {
  const [nodes, setNodes] = useState<NodeItem[] | null>(null);
  const [connected, setConnected] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [deployingNode, setDeployingNode] = useState<NodeItem | null>(null);
  const [hasMore, setHasMore] = useState(true);
  const sel = useSelection<string>();
  const [bulkUpdate, setBulkUpdate] = useState<NodeItem[] | null>(null);
  const [bulkLabel, setBulkLabel] = useState<NodeItem[] | null>(null);
  const [bulkBusy, setBulkBusy] = useState<null | 'delete'>(null);

  // Refresh the first page; merge by id so already-loaded older pages stay
  // visible. Newest entries overwrite tail dups.
  const refresh = () => {
    api.nodes(NODES_PAGE_SIZE, 0).then((list) => {
      setNodes((prev) => {
        const newest = list;
        const seen = new Set(newest.map((n) => n.id));
        const tail = (prev ?? []).filter((n) => !seen.has(n.id));
        return [...newest, ...tail];
      });
      setHasMore(list.length >= NODES_PAGE_SIZE);
    }).catch((e) => setError(String(e)));
    api.connectedNodes().then((arr) => setConnected(new Set(arr))).catch(() => { /* ignore */ });
  };

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 6_000);
    return () => clearInterval(t);
  }, []);

  const scroll = useInfiniteScroll({
    hasMore,
    loadMore: async () => {
      if (!nodes) return;
      const next = await api.nodes(NODES_PAGE_SIZE, nodes.length);
      if (next.length === 0) {
        setHasMore(false);
        return;
      }
      setNodes((prev) => [...(prev ?? []), ...next]);
      if (next.length < NODES_PAGE_SIZE) setHasMore(false);
    },
  });

  const buckets = useMemo(() => groupNodes(nodes ?? []), [nodes]);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <Server size={18} className="text-brand-500" />
            Nodes
          </h1>
          <p className="text-xs text-ink-dim">
            Remote hosts the Control Plane can deploy daemons to and run ad-hoc agents on.
          </p>
        </div>
        <div className="flex items-center gap-3">
          {connected.size > 0 && (
            <span className="text-xs text-green-700 inline-flex items-center gap-1">
              <Wifi size={12} /> {connected.size} live tunnel{connected.size === 1 ? '' : 's'}
            </span>
          )}
          <button
            onClick={() => setShowAdd(true)}
            className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-3 py-1.5 rounded-md"
          >
            <Plus size={14} />
            Add Node
          </button>
        </div>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-6">
        {error && (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {nodes === null && <div className="text-ink-mute">Loading…</div>}

        {nodes && nodes.length === 0 && (
          <div className="text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <Server size={32} className="mx-auto mb-2 opacity-40" />
            <p className="mb-1">No nodes registered.</p>
            <p className="text-xs">Click <span className="text-ink">Add Node</span> to register a remote host.</p>
          </div>
        )}

        {BUCKET_ORDER.map((bucket) => {
          const list = buckets[bucket];
          if (list.length === 0) return null;
          const keys = list.map((n) => String(n.id));
          const allSelected = keys.length > 0 && keys.every((k) => sel.isSelected(k));
          const someSelected = keys.some((k) => sel.isSelected(k));
          return (
            <section key={bucket}>
              <SectionHeader
                tone={BUCKET_TONE[bucket]}
                label={BUCKET_LABEL[bucket]}
                count={list.length}
                leading={
                  <input
                    type="checkbox"
                    checked={allSelected}
                    ref={(el) => { if (el) el.indeterminate = !allSelected && someSelected; }}
                    onChange={(e) => sel.setMany(keys, e.target.checked)}
                    title={allSelected ? `Deselect all ${list.length}` : `Select all ${list.length}`}
                    className="rounded border-border text-brand-500 focus:ring-brand-500/30 cursor-pointer"
                  />
                }
              />
              <ListCard>
                {list.map((n) => (
                  <NodeRow
                    key={n.id}
                    node={n}
                    connected={connected.has(n.name)}
                    selected={sel.isSelected(String(n.id))}
                    onToggleSelected={(on) => sel.set(String(n.id), on)}
                    onDeploy={() => setDeployingNode(n)}
                    onDelete={async () => {
                      if (confirm(`Remove node "${n.name}"?`)) {
                        await api.deleteNode(n.id);
                        refresh();
                      }
                    }}
                  />
                ))}
              </ListCard>
            </section>
          );
        })}

        {/* Bulk action bar — appears when ≥ 1 node selected. */}
        <BulkActionBar count={sel.count} onClear={sel.clear}>
          <BulkActionButton
            tone="neutral"
            icon={Tag}
            label="Add label"
            disabled={!!bulkBusy}
            title={`Apply a label to ${sel.count} node${sel.count === 1 ? '' : 's'}`}
            onClick={() => {
              const ids = new Set(sel.all);
              const targets = (nodes ?? []).filter((n) => ids.has(String(n.id)));
              setBulkLabel(targets);
            }}
          />
          <BulkActionButton
            tone="neutral"
            icon={Upload}
            label="Update binary"
            disabled={!!bulkBusy}
            title={`SSH-update the okesu binary on ${sel.count} node${sel.count === 1 ? '' : 's'}`}
            onClick={() => {
              const ids = new Set(sel.all);
              const targets = (nodes ?? []).filter((n) => ids.has(String(n.id)));
              setBulkUpdate(targets);
            }}
          />
          <BulkActionButton
            tone="bad"
            icon={Trash2}
            label="Delete"
            busy={bulkBusy === 'delete'}
            disabled={!!bulkBusy}
            title={`Remove ${sel.count} node${sel.count === 1 ? '' : 's'} from the Control Plane`}
            onClick={async () => {
              if (!confirm(`Remove ${sel.count} node${sel.count === 1 ? '' : 's'}? Their daemons will stop heartbeating but the remote okesu binary stays installed.`)) return;
              setBulkBusy('delete');
              const ids = sel.all.map(Number).filter((n) => Number.isFinite(n));
              await Promise.allSettled(ids.map((id) => api.deleteNode(id)));
              sel.clear();
              setBulkBusy(null);
              refresh();
            }}
          />
        </BulkActionBar>

        {nodes && nodes.length > 0 && (
          <div ref={scroll.sentinelRef} className="text-[11px] text-ink-mute text-center py-2">
            {scroll.loading
              ? 'loading more nodes…'
              : hasMore
                ? 'scroll for more'
                : `— end of ${nodes.length} nodes —`}
          </div>
        )}

        {/* Phase 21.7 — in-flight + recent managed-deploy provisions.
            Self-renders only when ≥1 row exists, so it stays out of
            the way on fleets that never use the managed-deploy tab. */}
        <NodeProvisionsPanel />
      </div>

      {showAdd && (
        <AddNodeModal
          onClose={() => setShowAdd(false)}
          onCreated={(n) => { setShowAdd(false); refresh(); setDeployingNode(n); }}
        />
      )}
      {deployingNode && (
        <DeployDrawer
          node={deployingNode}
          onClose={() => { setDeployingNode(null); refresh(); }}
        />
      )}
      {bulkUpdate && bulkUpdate.length > 0 && (
        <BulkBinaryUpdateDialog
          nodes={bulkUpdate}
          onClose={() => setBulkUpdate(null)}
          onDone={() => { setBulkUpdate(null); sel.clear(); refresh(); }}
        />
      )}
      {bulkLabel && bulkLabel.length > 0 && (
        <BulkLabelDialog
          nodes={bulkLabel}
          onClose={() => setBulkLabel(null)}
          onDone={() => { setBulkLabel(null); sel.clear(); refresh(); }}
        />
      )}
    </div>
  );
}

// BulkLabelDialog — apply a key=value label to N selected nodes via
// per-node /api/labels/node/{id} PUT calls. Dispatches in parallel
// and surfaces per-node failures so a partial deploy is visible.
function BulkLabelDialog({
  nodes,
  onClose,
  onDone,
}: {
  nodes: NodeItem[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ ok: number; failed: { id: number; name: string; err: string }[] } | null>(null);

  async function submit() {
    const k = key.trim();
    if (!k) return;
    setBusy(true); setResults(null);
    const settled = await Promise.allSettled(
      nodes.map((n) => api.setLabel('node', n.id, k, value.trim())),
    );
    const failed: { id: number; name: string; err: string }[] = [];
    let ok = 0;
    settled.forEach((s, i) => {
      if (s.status === 'fulfilled') {
        ok++;
      } else {
        failed.push({
          id: nodes[i].id,
          name: nodes[i].name,
          err: s.reason instanceof Error ? s.reason.message : String(s.reason),
        });
      }
    });
    setResults({ ok, failed });
    setBusy(false);
    if (failed.length === 0) {
      // Auto-close on full success after a brief flash so the user sees the count.
      setTimeout(onDone, 600);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 z-50 flex items-center justify-center p-4" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold flex items-center gap-2">
            <Tag size={14} className="text-brand-500" />
            Add label to {nodes.length} node{nodes.length === 1 ? '' : 's'}
          </h3>
          <button onClick={onClose} className="p-1 text-ink-mute hover:text-ink rounded-md"><X size={14} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <p className="text-[11px] text-ink-mute">
            Existing values under the same key are overwritten on each node.
          </p>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">Key</div>
              <input
                type="text"
                autoFocus
                value={key}
                onChange={(e) => setKey(e.target.value)}
                placeholder="e.g. env"
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
              />
            </div>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">Value</div>
              <input
                type="text"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder="e.g. prod"
                onKeyDown={(e) => { if (e.key === 'Enter') submit(); }}
                className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
              />
            </div>
          </div>
          <details className="text-[11px] text-ink-mute">
            <summary className="cursor-pointer hover:text-ink">Targets ({nodes.length})</summary>
            <ul className="mt-1 max-h-32 overflow-auto pl-4 list-disc font-mono text-[11px]">
              {nodes.map((n) => <li key={n.id}>{n.name}</li>)}
            </ul>
          </details>
          {results && (
            <div className="text-xs space-y-1">
              <div className="text-green-700">✓ {results.ok} applied</div>
              {results.failed.length > 0 && (
                <div className="text-red-700">
                  ✗ {results.failed.length} failed:
                  <ul className="mt-0.5 pl-4 list-disc text-[11px]">
                    {results.failed.slice(0, 5).map((f) => (
                      <li key={f.id}>{f.name}: {f.err}</li>
                    ))}
                    {results.failed.length > 5 && <li>+{results.failed.length - 5} more</li>}
                  </ul>
                </div>
              )}
            </div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">
            {results ? 'Close' : 'Cancel'}
          </button>
          <button
            onClick={submit}
            disabled={busy || !key.trim()}
            className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md font-medium hover:bg-brand-700 disabled:opacity-50 inline-flex items-center gap-1"
          >
            <Tag size={12} />
            {busy ? 'Applying…' : 'Apply'}
          </button>
        </footer>
      </div>
    </div>
  );
}

function NodeRow({
  node,
  connected,
  selected,
  onToggleSelected,
  onDeploy,
  onDelete,
}: {
  node: NodeItem;
  connected: boolean;
  selected: boolean;
  onToggleSelected: (on: boolean) => void;
  onDeploy: () => void;
  onDelete: () => void;
}) {
  // Click anywhere on the row navigates to /nodes/:id, matching the Agents
  // page pattern. Inline action buttons stop propagation so they don't
  // hijack navigation.
  const navigate = useNavigate();
  const stop = (e: React.MouseEvent) => { e.stopPropagation(); e.preventDefault(); };
  return (
    <Link
      to={`/nodes/${node.id}${node.cp_source ? `?cp=${node.cp_source.instance_id}` : ''}`}
      onClick={(e) => {
        // Keep modifier-clicks (cmd/ctrl/shift/middle) for new-tab behavior.
        if (e.defaultPrevented) {
          const q = node.cp_source ? `?cp=${node.cp_source.instance_id}` : '';
          navigate(`/nodes/${node.id}${q}`);
        }
      }}
      className="block px-4 py-3 hover:bg-slate-50/60 transition-colors"
    >
      <div className="flex items-center gap-4">
        <input
          type="checkbox"
          checked={selected}
          onChange={(e) => onToggleSelected(e.target.checked)}
          onClick={(e) => e.stopPropagation()}
          onMouseDown={(e) => e.stopPropagation()}
          aria-label={`Select ${node.name}`}
          className="shrink-0 rounded border-border text-brand-500 focus:ring-brand-500/30 cursor-pointer"
        />
        {/* avatar */}
        <div className="w-10 h-10 shrink-0 rounded-lg bg-gradient-to-br from-slate-700 to-slate-900 flex items-center justify-center text-white shadow-sm">
          <Server size={16} />
        </div>

        {/* identity */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-ink truncate">{node.name}</span>
            {connected && (
              <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-700 bg-green-50 ring-1 ring-green-200 px-1.5 py-0.5 rounded">
                <Wifi size={9} className="-mt-px" />
                tunnel live
              </span>
            )}
            <CPSourceChip source={node.cp_source} />
          </div>
          <div className="text-xs text-ink-dim font-mono truncate">
            {node.ssh_user}@{node.hostname}{node.ssh_port !== 22 ? ':' + node.ssh_port : ''}
          </div>
        </div>

        {/* status */}
        <StatusPill status={node.status} message={node.status_message} />

        {/* agents installed */}
        <div className="hidden md:block w-44 shrink-0">
          {node.agents_installed.length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {node.agents_installed.slice(0, 3).map((a) => (
                <span key={a} className="text-[10px] bg-slate-100 text-slate-700 px-1.5 py-0.5 rounded">
                  {a}
                </span>
              ))}
              {node.agents_installed.length > 3 && (
                <span className="text-[10px] text-ink-mute">+{node.agents_installed.length - 3}</span>
              )}
            </div>
          ) : (
            <span className="text-[11px] text-ink-mute">no agents</span>
          )}
        </div>

        {/* last deployed */}
        <div className="hidden lg:block w-32 shrink-0 text-[11px] text-ink-mute">
          {node.last_deployed_at ? new Date(node.last_deployed_at).toLocaleString(undefined, {
            month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
          }) : '—'}
        </div>

        {/* actions — icon-only with stopPropagation so they don't hijack the row click */}
        <div className="flex items-center gap-0.5 shrink-0">
          <button
            onClick={(e) => { stop(e); onDeploy(); }}
            className="p-1.5 text-ink-mute hover:text-brand-700 hover:bg-brand-50 rounded-md inline-flex items-center"
            title="Deploy a daemon to this node"
          >
            <Play size={13} />
          </button>
          <button
            onClick={(e) => { stop(e); onDelete(); }}
            className="p-1.5 text-ink-mute hover:text-red-600 hover:bg-red-50 rounded-md inline-flex items-center"
            title="Remove node"
          >
            <Trash2 size={13} />
          </button>
          <ChevronRight size={14} className="text-ink-mute mx-0.5" />
        </div>
      </div>
    </Link>
  );
}

function StatusPill({ status, message }: { status: NodeItem['status']; message?: string }) {
  const config: Record<NodeItem['status'], { label: string; icon: typeof Circle; cls: string }> = {
    pending:   { label: 'pending',   icon: Circle,        cls: 'text-ink-mute bg-slate-50 ring-slate-200' },
    deploying: { label: 'deploying', icon: Loader2,       cls: 'text-brand-700 bg-brand-50 ring-brand-100' },
    ready:     { label: 'ready',     icon: CheckCircle2,  cls: 'text-green-700 bg-green-50 ring-green-200' },
    failed:    { label: 'failed',    icon: AlertCircle,   cls: 'text-red-700 bg-red-50 ring-red-200' },
    archived:  { label: 'archived',  icon: Archive,       cls: 'text-ink-mute bg-slate-100 ring-slate-200' },
  };
  const c = config[status] ?? config.pending;
  return (
    <span title={message}
      className={cn(
        'inline-flex items-center gap-1 text-[11px] uppercase tracking-wide font-medium px-2 py-0.5 rounded-md ring-1 w-24 shrink-0 justify-center',
        c.cls,
      )}
    >
      <c.icon size={10} className={status === 'deploying' ? 'animate-spin' : ''} />
      {c.label}
    </span>
  );
}

function groupNodes(list: NodeItem[]): Record<Bucket, NodeItem[]> {
  const out: Record<Bucket, NodeItem[]> = { ready: [], deploying: [], failed: [], pending: [], archived: [] };
  for (const n of list) {
    const bucket = out[n.status as Bucket];
    if (bucket) bucket.push(n);
  }
  return out;
}

// ── Add Node modal ──────────────────────────────────────────────────────────
//
// Two enrollment paths in one dialog:
//
//   1. SSH push  — operator supplies host + SSH creds, CP installs via SSH.
//                  Best for hosts the CP can reach and for the existing
//                  fleet operators have already provisioned with sshd.
//   2. S3 dead-drop — operator generates a fleet package against a
//                  configured S3-compatible bucket, downloads it, runs
//                  install.sh on N machines. The hosts self-register via
//                  the bucket; no inbound reachability to the CP is
//                  required.
//
// Dropping in / out of either tab keeps the form-state private to that
// tab so a half-filled SSH form isn't lost when the operator peeks at
// the package generator.

type AddTab = 'ssh' | 's3' | 'managed';

function AddNodeModal({ onClose, onCreated }: { onClose: () => void; onCreated: (n: NodeItem) => void }) {
  const [tab, setTab] = useState<AddTab>('ssh');
  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <div className="flex items-center gap-3">
            <h2 className="text-sm font-semibold">Add Node</h2>
            <nav className="flex gap-1 text-xs">
              <button
                onClick={() => setTab('ssh')}
                className={cn(
                  'px-2.5 py-1 rounded-md font-medium',
                  tab === 'ssh'
                    ? 'bg-brand-50 text-brand-700 ring-1 ring-brand-200'
                    : 'text-ink-dim hover:bg-slate-100',
                )}
              >
                SSH push
              </button>
              <button
                onClick={() => setTab('s3')}
                className={cn(
                  'px-2.5 py-1 rounded-md font-medium',
                  tab === 's3'
                    ? 'bg-brand-50 text-brand-700 ring-1 ring-brand-200'
                    : 'text-ink-dim hover:bg-slate-100',
                )}
              >
                S3 dead-drop
              </button>
              <button
                onClick={() => setTab('managed')}
                className={cn(
                  'px-2.5 py-1 rounded-md font-medium',
                  tab === 'managed'
                    ? 'bg-brand-50 text-brand-700 ring-1 ring-brand-200'
                    : 'text-ink-dim hover:bg-slate-100',
                )}
              >
                Managed deploy
              </button>
            </nav>
          </div>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
        </header>
        {tab === 'ssh' && <AddNodeSSH onClose={onClose} onCreated={onCreated} />}
        {tab === 's3' && <AddNodeS3 onClose={onClose} />}
        {tab === 'managed' && <AddNodeManaged onClose={onClose} />}
      </div>
    </div>
  );
}

// SSH push tab — original Add Node form, unchanged in behaviour.
function AddNodeSSH({ onClose, onCreated }: { onClose: () => void; onCreated: (n: NodeItem) => void }) {
  const [name, setName] = useState('');
  const [hostname, setHostname] = useState('');
  const [sshUser, setSshUser] = useState('root');
  const [sshPort, setSshPort] = useState(22);
  const [notes, setNotes] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [peers, setPeers] = useState<FederationPeer[]>([]);
  const [targetCP, setTargetCP] = useState('');

  useEffect(() => {
    api.federationPeers()
      .then((list) => setPeers(list.filter((p) => p.healthy)))
      .catch(() => { /* no peers configured — picker hides */ });
  }, []);

  async function handleSubmit() {
    setBusy(true); setError(null);
    try {
      const node = await api.createNode({
        name, hostname, ssh_user: sshUser, ssh_port: sshPort, notes,
        target_cp_instance_id: targetCP || undefined,
      });
      onCreated(node);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="p-5 space-y-3 text-sm">
        {peers.length > 0 && (
          <Field label="Target CP">
            <select
              value={targetCP}
              onChange={(e) => setTargetCP(e.target.value)}
              className={inputCls}
              title="Which Control Plane should own this node? Defaults to this (local) CP."
            >
              <option value="">This CP (local)</option>
              {peers.map((p) => {
                const id = p.introspect?.instance_id ?? '';
                const label = p.display_name || p.introspect?.display_name || p.url;
                const region = p.introspect?.region;
                return (
                  <option key={id} value={id}>
                    {label}{region ? ` · ${region}` : ''}
                  </option>
                );
              })}
            </select>
          </Field>
        )}
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="prod-web-01" className={inputCls} required />
        </Field>
        <Field label="Hostname or IP">
          <input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="10.0.1.42" className={inputCls} required />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="SSH user">
            <input value={sshUser} onChange={(e) => setSshUser(e.target.value)} className={inputCls} />
          </Field>
          <Field label="SSH port">
            <input type="number" value={sshPort} onChange={(e) => setSshPort(Number(e.target.value))} className={inputCls} />
          </Field>
        </div>
        <Field label="Notes (optional)">
          <input value={notes} onChange={(e) => setNotes(e.target.value)} className={inputCls} />
        </Field>
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1.5 rounded-md">{error}</div>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
        <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
        <button
          onClick={handleSubmit}
          disabled={busy || !name || !hostname}
          className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium"
        >
          {busy ? 'Creating…' : 'Create & Deploy'}
        </button>
      </footer>
    </>
  );
}

// S3 dead-drop tab — generate an enrollment package the operator
// can drop on N hosts. Each host self-registers via the bucket.
//
// The form picks: a transport_config (or inline-creates one),
// display name, target platforms, default agents, archive format.
// Output is a download link the operator clicks.
function AddNodeS3({ onClose }: { onClose: () => void }) {
  // Operator inputs.
  const [transportID, setTransportID] = useState<number | null>(null);
  const [displayName, setDisplayName] = useState('');
  const [agentsCsv, setAgentsCsv] = useState('instance-integrity');
  const [pollMs, setPollMs] = useState(10_000);
  const [format, setFormat] = useState<'tar.gz' | 'deb' | 'rpm' | 'pkg' | 'msi'>('tar.gz');

  // Available transport configs + agents (for the autocomplete).
  const [configs, setConfigs] = useState<TransportConfigSummary[]>([]);
  const [agentLib, setAgentLib] = useState<string[]>([]);
  const [showCreateConfig, setShowCreateConfig] = useState(false);

  // Generation state — once a package is minted we surface the
  // download link (operator can re-download for any format) and the
  // raw curl recipe for scripting.
  const [pkg, setPkg] = useState<EnrollmentPackageSummary | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    refreshConfigs();
    api.nodeLibrary().then((lib) => setAgentLib(lib.agents)).catch(() => { /* ignore */ });
  }, []);

  function refreshConfigs() {
    api.transportConfigs()
      .then((rows) => {
        setConfigs(rows);
        if (rows.length > 0 && transportID == null) {
          setTransportID(rows[0].id);
        }
      })
      .catch((e) => setError(String(e)));
  }

  async function handleGenerate() {
    if (!transportID) { setError('pick a transport config first'); return; }
    setBusy(true); setError(null);
    try {
      const agents = agentsCsv.split(',').map((s) => s.trim()).filter(Boolean);
      const created = await api.enrollmentPackageCreate({
        display_name: displayName || `pkg-${new Date().toISOString().slice(0, 10)}`,
        transport_config_id: transportID,
        defaults: {
          agents,
          poll_interval_ms: pollMs,
        },
      });
      setPkg(created);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  const downloadURL = pkg ? api.enrollmentPackageDownloadURL(pkg.id, format) : null;

  return (
    <>
      <div className="p-5 space-y-4 text-sm max-h-[70vh] overflow-y-auto">
        {/* Transport config picker — null state nudges the operator
            into creating one inline. */}
        <Field label="S3 bucket / transport config">
          {configs.length === 0 ? (
            <div className="space-y-2">
              <p className="text-xs text-ink-mute">
                No S3 transport configured. Add one to mint enrollment packages.
              </p>
              <button
                onClick={() => setShowCreateConfig(true)}
                className="text-xs text-brand-700 hover:underline inline-flex items-center gap-1"
              >
                <Plus size={11} /> Create transport config
              </button>
            </div>
          ) : (
            <div className="flex gap-2 items-center">
              <select
                value={transportID ?? ''}
                onChange={(e) => setTransportID(Number(e.target.value))}
                className={inputCls}
              >
                {configs.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} · {c.bucket} {c.has_fleet_pubkey ? '' : '(no fleet keys yet)'}
                  </option>
                ))}
              </select>
              <button
                onClick={() => setShowCreateConfig(true)}
                title="Add another transport config"
                className="text-xs text-brand-700 hover:underline shrink-0 inline-flex items-center gap-0.5"
              >
                <Plus size={11} /> Add
              </button>
            </div>
          )}
        </Field>

        {showCreateConfig && (
          <CreateTransportInline
            onCreated={(c) => {
              setShowCreateConfig(false);
              setConfigs((prev) => [...prev, c]);
              setTransportID(c.id);
            }}
            onCancel={() => setShowCreateConfig(false)}
          />
        )}

        <Field label="Package name">
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="prod-fleet-2026-04"
            className={inputCls}
          />
        </Field>

        <Field label="Default agents (comma-separated)">
          <input
            value={agentsCsv}
            onChange={(e) => setAgentsCsv(e.target.value)}
            placeholder="instance-integrity,edr"
            className={inputCls}
          />
          {agentLib.length > 0 && (
            <p className="text-[10px] text-ink-mute mt-1">
              Available: {agentLib.join(', ')}
            </p>
          )}
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="Poll interval (ms)">
            <input
              type="number"
              value={pollMs}
              onChange={(e) => setPollMs(Number(e.target.value))}
              min={5000}
              max={300_000}
              step={1000}
              className={inputCls}
            />
          </Field>
          <Field label="Archive format">
            <select
              value={format}
              onChange={(e) => setFormat(e.target.value as typeof format)}
              className={inputCls}
            >
              <option value="tar.gz">tar.gz · linux / macos / bsd</option>
              <option value="deb">.deb · debian / ubuntu (coming soon)</option>
              <option value="rpm">.rpm · rhel / fedora (coming soon)</option>
              <option value="pkg">.pkg · macos installer (coming soon)</option>
              <option value="msi">.msi · windows (coming soon)</option>
            </select>
          </Field>
        </div>

        <p className="text-[11px] text-ink-mute">
          Multi-arch binaries baked in: linux-amd64, linux-arm64, darwin-amd64,
          darwin-arm64, freebsd-amd64. The install.sh script picks the right
          one for each host based on <code>uname -s</code> / <code>uname -m</code>.
        </p>

        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1.5 rounded-md">
            {error}
          </div>
        )}

        {pkg && downloadURL && (
          <section className="bg-green-50 border border-green-200 rounded-md p-3 space-y-2">
            <div className="flex items-center gap-2 text-xs font-semibold text-green-800">
              <CheckCircle2 size={14} /> Package #{pkg.id} ready
            </div>
            <p className="text-[11px] text-green-900/80">
              Drop on any host and run <code>sudo ./install.sh</code>. The host
              auto-registers via the bucket.
            </p>
            <div className="flex flex-wrap gap-2">
              <a
                href={downloadURL}
                download
                className="text-xs px-3 py-1.5 bg-green-700 hover:bg-green-800 text-white rounded-md font-medium inline-flex items-center gap-1"
              >
                <Upload size={12} className="rotate-180" /> Download {format}
              </a>
              <button
                onClick={() => navigator.clipboard.writeText(`curl -sk -OJ ${window.location.origin}${downloadURL}`)}
                className="text-xs px-3 py-1.5 border border-green-300 text-green-800 hover:bg-green-100 rounded-md font-medium"
                title="Copy a curl command for scripted download"
              >
                Copy curl
              </button>
            </div>
          </section>
        )}
      </div>
      <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
        <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">
          {pkg ? 'Done' : 'Cancel'}
        </button>
        {!pkg && (
          <button
            onClick={handleGenerate}
            disabled={busy || !transportID}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium"
          >
            {busy ? 'Generating…' : 'Generate package'}
          </button>
        )}
      </footer>
    </>
  );
}

// CreateTransportInline — minimal form to add a new bucket config
// without leaving the Add Node dialog. Mints a fresh fleet keypair
// by default since v1 has no separate keypair-management UI.
function CreateTransportInline({
  onCreated,
  onCancel,
}: {
  onCreated: (c: TransportConfigSummary) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState('');
  const [bucket, setBucket] = useState('');
  const [endpoint, setEndpoint] = useState('s3.us-west-2.amazonaws.com');
  const [endpointInternal, setEndpointInternal] = useState('');
  const [region, setRegion] = useState('us-west-2');
  const [accessKey, setAccessKey] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [useSSL, setUseSSL] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true); setError(null);
    try {
      const c = await api.transportConfigCreate({
        name, kind: 's3', bucket, endpoint,
        endpoint_internal: endpointInternal || undefined,
        region,
        use_ssl: useSSL, access_key: accessKey, secret_key: secretKey,
        generate_fleet_keys: true,
        scanner_interval_ms: 10_000,
        cp_id: 'global',
      });
      onCreated(c);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="bg-slate-50 border border-border rounded-md p-3 space-y-2">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">
        New transport config
      </div>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="prod-bucket" className={inputCls} />
        </Field>
        <Field label="Bucket">
          <input value={bucket} onChange={(e) => setBucket(e.target.value)} placeholder="okesu-prod" className={inputCls} />
        </Field>
        <Field label="Endpoint (host:port)">
          <input value={endpoint} onChange={(e) => setEndpoint(e.target.value)} className={inputCls} />
        </Field>
        <Field label="Region">
          <input value={region} onChange={(e) => setRegion(e.target.value)} className={inputCls} />
        </Field>
        <div className="col-span-2">
          <Field label="Internal endpoint (optional)">
            <input
              value={endpointInternal}
              onChange={(e) => setEndpointInternal(e.target.value)}
              placeholder="leave blank to dial the public endpoint above"
              className={inputCls}
            />
            <p className="text-[10px] text-ink-mute mt-1">
              CP scanner uses this when set; nodes always embed the public endpoint. Use for split-horizon DNS / VPC private endpoints (e.g.{' '}
              <code>vpce-…-s3.s3.us-east-1.vpce.amazonaws.com</code>).
            </p>
          </Field>
        </div>
        <Field label="Access key">
          <input value={accessKey} onChange={(e) => setAccessKey(e.target.value)} className={inputCls} />
        </Field>
        <Field label="Secret key">
          <input type="password" value={secretKey} onChange={(e) => setSecretKey(e.target.value)} className={inputCls} />
        </Field>
      </div>
      <label className="text-[11px] inline-flex items-center gap-1.5 text-ink-dim">
        <input type="checkbox" checked={useSSL} onChange={(e) => setUseSSL(e.target.checked)} />
        Use TLS (uncheck only for local MinIO over plain HTTP)
      </label>
      {error && <div className="text-xs text-red-700">{error}</div>}
      <div className="flex justify-end gap-2 pt-1">
        <button onClick={onCancel} className="text-xs px-2.5 py-1 border border-border rounded">Cancel</button>
        <button
          onClick={submit}
          disabled={busy || !name || !bucket || !endpoint}
          className="text-xs px-2.5 py-1 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded font-medium"
        >
          {busy ? 'Creating…' : 'Create + mint fleet keys'}
        </button>
      </div>
    </section>
  );
}

// ── Deploy drawer ───────────────────────────────────────────────────────────

function DeployDrawer({ node, onClose }: { node: NodeItem; onClose: () => void }) {
  const [agentLib, setAgentLib] = useState<string[]>([]);
  const [agents, setAgents] = useState<string[]>([]);
  const [privateKey, setPrivateKey] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [sudoPassword, setSudoPassword] = useState('');
  const [anthropicKey, setAnthropicKey] = useState('');
  const [openaiKey, setOpenaiKey] = useState('');
  const [includeWebhook, setIncludeWebhook] = useState(true);
  const [includeMgmtCert, setIncludeMgmtCert] = useState(true);
  const [logs, setLogs] = useState<string[]>([]);
  const [running, setRunning] = useState(false);
  const [doneStatus, setDoneStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.nodeLibrary().then((lib) => setAgentLib(lib.agents)).catch(() => { /* ignore */ });
  }, []);

  function toggleAgent(a: string) {
    setAgents((prev) => prev.includes(a) ? prev.filter((x) => x !== a) : [...prev, a]);
  }

  async function startDeploy() {
    if (running) return;
    setLogs([]); setDoneStatus(null); setError(null);
    setRunning(true);
    try {
      const { job_id } = await api.deployNode(node.id, {
        agents,
        private_key: privateKey,
        passphrase: passphrase || undefined,
        sudo_password: sudoPassword || undefined,
        anthropic_api_key: anthropicKey || undefined,
        openai_api_key: openaiKey || undefined,
        include_webhook: includeWebhook,
        include_mgmt_cert: includeMgmtCert,
        // Phase 9.7: when this node belongs to a federated child, the
        // parent's ForwardingNodeDeploy proxies the request there. The
        // node id is preserved (federated rows keep their child-side id).
        target_cp_instance_id: node.cp_source?.instance_id,
      });
      const unsubscribe = subscribeJobLog(
        job_id,
        (line) => setLogs((prev) => [...prev, line]),
        (status) => { setDoneStatus(status); setRunning(false); unsubscribe(); },
      );
    } catch (e) {
      setError(String(e));
      setRunning(false);
    }
  }

  return (
    <aside className="fixed inset-y-0 right-0 w-[640px] bg-panel border-l border-border shadow-card flex flex-col z-40">
      <header className="px-5 py-3 border-b border-border flex items-start justify-between">
        <div>
          <h2 className="text-sm font-semibold">Deploy to {node.name}</h2>
          <p className="text-xs text-ink-dim font-mono">{node.ssh_user}@{node.hostname}:{node.ssh_port}</p>
        </div>
        <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
      </header>

      <div className="flex-1 overflow-auto p-5 space-y-4 text-sm">
        <Field label="Agents to install">
          {agentLib.length === 0 ? (
            <p className="text-xs text-ink-mute">
              No agent files found. Configure <code className="bg-slate-100 px-1 rounded">--agent-files-dir</code> on the CP.
            </p>
          ) : (
            <div className="grid grid-cols-2 gap-1.5">
              {agentLib.map((a) => (
                <label key={a} className="flex items-center gap-2 px-2 py-1.5 border border-border rounded-md hover:bg-slate-50 cursor-pointer">
                  <input type="checkbox" checked={agents.includes(a)} onChange={() => toggleAgent(a)} />
                  <span className="text-xs">{a}</span>
                </label>
              ))}
            </div>
          )}
        </Field>

        <Field label="SSH private key (PEM)">
          <textarea
            value={privateKey}
            onChange={(e) => setPrivateKey(e.target.value)}
            rows={5}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
            className={`${inputCls} font-mono text-xs`}
          />
          <p className="text-[11px] text-ink-mute mt-1">Used once for this deploy. Not stored.</p>
        </Field>

        <Field label="Passphrase (optional)">
          <input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} className={inputCls} />
        </Field>

        <Field label="Sudo password (optional)">
          <input
            type="password"
            value={sudoPassword}
            onChange={(e) => setSudoPassword(e.target.value)}
            placeholder="leave blank for root login or NOPASSWD sudo"
            className={inputCls}
          />
          <p className="text-[11px] text-ink-mute mt-1">
            Needed for Mac developer machines and other targets where the SSH user
            isn't root and doesn't have passwordless sudo. Used once; not stored.
          </p>
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="Anthropic API key">
            <input type="password" value={anthropicKey} onChange={(e) => setAnthropicKey(e.target.value)} className={inputCls} placeholder="sk-ant-..." />
          </Field>
          <Field label="OpenAI API key">
            <input type="password" value={openaiKey} onChange={(e) => setOpenaiKey(e.target.value)} className={inputCls} placeholder="sk-..." />
          </Field>
        </div>

        <div className="space-y-2">
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={includeWebhook} onChange={(e) => setIncludeWebhook(e.target.checked)} />
            Wire up webhook output (uses CP&apos;s webhook secret)
          </label>
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={includeMgmtCert} onChange={(e) => setIncludeMgmtCert(e.target.checked)} />
            Issue mTLS client certs (per agent) for the management plane
          </label>
        </div>

        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
        )}

        {(running || logs.length > 0 || doneStatus) && (
          <div className="bg-slate-900 text-slate-100 rounded-md p-3 text-xs font-mono max-h-64 overflow-auto">
            {logs.map((line, i) => (
              <div key={i} className="whitespace-pre-wrap">{line}</div>
            ))}
            {doneStatus && (
              <div className={cn('mt-1 font-medium', doneStatus === 'succeeded' ? 'text-green-300' : 'text-red-300')}>
                ── {doneStatus.toUpperCase()} ──
              </div>
            )}
          </div>
        )}
      </div>

      <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
        <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">
          {doneStatus ? 'Close' : 'Cancel'}
        </button>
        {!doneStatus && (
          <button
            onClick={startDeploy}
            disabled={running || !privateKey || agents.length === 0}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {running ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}
            {running ? 'Deploying…' : 'Start Deploy'}
          </button>
        )}
      </footer>
    </aside>
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

const inputCls = "w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30";
