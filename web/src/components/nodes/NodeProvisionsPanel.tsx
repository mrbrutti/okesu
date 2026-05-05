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

  async function handleDelete(row: NodeProvision) {
    // Two-tier confirm: row delete is always safe, cloud destroy is
    // opt-in. Failed/cancelled rows almost always have no live VM,
    // so we don't even offer destroy for them — the row delete is
    // the entire intent.
    const hasCloudResource = !!row.cloud_resource_id && row.cloud_resource_id.length > 0;
    const isTerminalNoCloud = row.status === 'failed' || row.status === 'cancelled';
    let destroy = false;

    if (hasCloudResource && !isTerminalNoCloud) {
      // Deploy succeeded or is in flight with a real VM — give the
      // operator the choice.
      const choice = window.confirm(
        `Delete node provision #${row.id} (${row.display_name})?\n\n` +
        `Cloud resource: ${row.cloud_resource_id}\n\n` +
        `OK = ALSO destroy the cloud instance via ${row.cloud}.Destroy()\n` +
        `Cancel = back out without deleting\n\n` +
        `(To delete the row but leave the cloud instance running, click OK on the next prompt instead of this one.)`,
      );
      if (!choice) {
        const recordOnly = window.confirm(
          `Delete node provision #${row.id} record only?\n\n` +
          `The cloud instance ${row.cloud_resource_id} will keep running and you'll need to clean it up via the cloud console.`,
        );
        if (!recordOnly) return;
      } else {
        destroy = true;
      }
    } else {
      // Failed/cancelled or no cloud_resource_id — single confirm.
      const ok = window.confirm(
        `Delete node provision #${row.id} (${row.display_name})?\n\n` +
        (hasCloudResource ? `Cloud resource ${row.cloud_resource_id} (already terminated) will not be touched.` : 'No cloud resource was created — this is a record-only delete.'),
      );
      if (!ok) return;
    }

    try {
      const res = await api.nodeProvisionDelete(row.id, destroy);
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
          <NodeProvisionRow
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

function NodeProvisionRow({ row, expanded, onToggle, onDelete }: {
  row: NodeProvision;
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
            <button
              onClick={onDelete}
              title="Delete this node-provision row (and optionally destroy the cloud instance)"
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
