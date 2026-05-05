// Phase 21.7 — Managed deploys panel for the Nodes page. Direct port
// of Federation.tsx::ManagedDeploysPanel; lists in-flight + recent
// node_provisions rows below the node list. Auto-refreshes every 5s
// while any row is non-terminal so the operator sees `queued →
// starting → cloud_init_running → bootstrap_pending → ready` progress
// live without leaving the page. Each row is expandable to surface
// the worker's streamed log, the cloud-resource id (with console-URL
// link), the auto-enrolled node link (once s3scanner sees its first
// heartbeat), and any error message on a failed deploy.
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Archive,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Server,
  Trash2,
} from 'lucide-react';
import { api, type NodeProvision } from '../../api';
import { cn } from '../../lib/cn';

export function NodeProvisionsPanel() {
  const [rows, setRows] = useState<NodeProvision[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  useEffect(() => {
    let cancelled = false;
    let timer: number | undefined;
    async function tick() {
      try {
        const data = await api.nodeProvisionsList(50);
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
    // Don't render an empty section — the +Add Node modal teaches the
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

  function pruneRow(id: number) {
    setRows((prev) => (prev ?? []).filter((r) => r.id !== id));
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
          <NodeProvisionRow
            key={r.id}
            row={r}
            expanded={expanded.has(r.id)}
            onToggle={() => toggleExpand(r.id)}
            onPruned={() => pruneRow(r.id)}
            onError={setError}
          />
        ))}
      </ul>
    </section>
  );
}

function NodeProvisionRow({ row, expanded, onToggle, onPruned, onError }: {
  row: NodeProvision;
  expanded: boolean;
  onToggle: () => void;
  onPruned: () => void;
  onError: (msg: string | null) => void;
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
          {row.node_id != null && (
            <div className="text-ink-dim">
              node: <Link to={`/nodes/${row.node_id}`} className="text-brand-700 hover:underline">#{row.node_id}</Link>
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
            <RetireMenu row={row} onPruned={onPruned} onError={onError} />
          </div>
        </div>
      )}
    </li>
  );
}

// RetireMenu — replaces the prior single "Delete" button. Operators
// have two distinct intents on a node-provision:
//
//   1. Archive (clean offboarding) — VM is gone, but we keep the row
//      and its provenance/history. One click; no extra confirm beyond
//      opening the popover.
//   2. Delete & purge history (irreversible nuke) — VM gone, row gone,
//      and all events/findings/runs/agents for the host plus the
//      bucket prefix are swept. Requires the operator to type the
//      display name as a guardrail.
//
// Both surface server errors inline so the operator sees destroy
// failures (cloud instance still alive) without losing context.
function RetireMenu({ row, onPruned, onError }: {
  row: NodeProvision;
  onPruned: () => void;
  onError: (msg: string | null) => void;
}) {
  const [open, setOpen] = useState(false);
  const [purgeMode, setPurgeMode] = useState(false);
  const [confirmText, setConfirmText] = useState('');
  const [busy, setBusy] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  function reset() {
    setOpen(false);
    setPurgeMode(false);
    setConfirmText('');
    setLocalError(null);
  }

  async function archive() {
    setBusy(true);
    setLocalError(null);
    onError(null);
    try {
      const res = await api.archiveNodeProvision(row.id);
      // Surface destroy errors inline — the row IS archived but the
      // cloud instance may still be running, which is operator-actionable.
      if (res && typeof res === 'object' && 'destroy_error' in res && res.destroy_error) {
        onError(`row archived, but destroy failed: ${res.destroy_error} — clean up via cloud console`);
      }
      onPruned();
      reset();
    } catch (e) {
      setLocalError(String(e));
    } finally {
      setBusy(false);
    }
  }

  async function deleteAndPurge() {
    if (confirmText !== row.display_name) {
      setLocalError(`type "${row.display_name}" exactly to confirm`);
      return;
    }
    setBusy(true);
    setLocalError(null);
    onError(null);
    try {
      const res = await api.nodeProvisionDelete(row.id, { destroy: true, purge: true });
      if (res && typeof res === 'object' && 'destroy_error' in res && res.destroy_error) {
        onError(`row deleted, but destroy failed: ${res.destroy_error} — clean up ${res.cloud_resource} via cloud console`);
      }
      onPruned();
      reset();
    } catch (e) {
      setLocalError(String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        title="Retire this node-provision"
        className="text-[11px] px-2 py-1 border border-border hover:bg-slate-50 rounded inline-flex items-center gap-1"
      >
        Retire
        <ChevronDown size={11} />
      </button>
    );
  }

  return (
    <div className="relative">
      <button
        onClick={reset}
        className="text-[11px] px-2 py-1 border border-border bg-slate-50 rounded inline-flex items-center gap-1"
      >
        Retire
        <ChevronDown size={11} />
      </button>
      <div
        className="absolute right-0 top-full mt-1 z-30 bg-white border border-border rounded-md shadow-md w-72 p-1"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Archive — single-click, default action */}
        <button
          onClick={archive}
          disabled={busy}
          className="w-full text-left px-2.5 py-2 rounded-md hover:bg-slate-50 disabled:opacity-50 inline-flex items-start gap-2"
        >
          <Archive size={12} className="mt-0.5 text-ink-mute shrink-0" />
          <span className="flex-1">
            <span className="block text-[12px] font-medium text-ink">Archive</span>
            <span className="block text-[10px] text-ink-mute leading-snug">
              Terminate cloud VM. Keep node row + history.
            </span>
          </span>
        </button>

        {/* Delete & purge — two-step typed confirm */}
        {!purgeMode ? (
          <button
            onClick={() => { setPurgeMode(true); setLocalError(null); }}
            disabled={busy}
            className="w-full text-left px-2.5 py-2 rounded-md hover:bg-red-50 disabled:opacity-50 inline-flex items-start gap-2 text-red-700"
          >
            <Trash2 size={12} className="mt-0.5 shrink-0" />
            <span className="flex-1">
              <span className="block text-[12px] font-medium">Delete &amp; purge history</span>
              <span className="block text-[10px] leading-snug text-red-600/80">
                Terminate VM, delete row, purge events / findings / runs / agents for this host, sweep bucket prefix. Irreversible.
              </span>
            </span>
          </button>
        ) : (
          <div className="px-2.5 py-2 space-y-1.5">
            <div className="text-[11px] font-medium text-red-700 inline-flex items-center gap-1">
              <Trash2 size={11} /> Delete &amp; purge history
            </div>
            <div className="text-[10px] text-ink-mute leading-snug">
              Type the display name <code className="bg-slate-100 px-1 rounded">{row.display_name}</code> to confirm.
            </div>
            <input
              autoFocus
              type="text"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder={row.display_name}
              disabled={busy}
              className="w-full text-[11px] px-2 py-1 border border-red-300 rounded outline-none focus:ring-2 focus:ring-red-500/30 font-mono"
            />
            <div className="flex gap-1.5 justify-end">
              <button
                onClick={() => { setPurgeMode(false); setConfirmText(''); setLocalError(null); }}
                disabled={busy}
                className="text-[11px] px-2 py-1 border border-border rounded hover:bg-slate-50 disabled:opacity-50"
              >
                Back
              </button>
              <button
                onClick={deleteAndPurge}
                disabled={busy || confirmText !== row.display_name}
                className="text-[11px] px-2 py-1 bg-red-600 text-white rounded hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed inline-flex items-center gap-1"
              >
                <Trash2 size={11} /> Delete &amp; purge
              </button>
            </div>
          </div>
        )}

        {localError && (
          <div className="mx-1 my-1 px-2 py-1 text-[10px] text-red-700 bg-red-50 border border-red-200 rounded">
            {localError}
          </div>
        )}
        <div className="px-2.5 py-1 border-t border-border mt-1">
          <button
            onClick={reset}
            disabled={busy}
            className="text-[10px] text-ink-mute hover:text-ink"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
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
