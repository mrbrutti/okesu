import { useEffect } from 'react';
import { X, Server, ExternalLink, AlertCircle } from 'lucide-react';
import { cn } from '../lib/cn';
import type { OrchestrationStepView, StepNodeDispatchView } from '../api';

interface Props {
  /** The parent step (used for the breadcrumb + the rendered prompt
   *  the host shared with all other hosts). */
  step: OrchestrationStepView;
  /** Which host's detail to show. The drawer pulls the matching row
   *  from step.per_node by host name; that row is the source of truth
   *  for status / output_tail / findings_count / agent_run_id. */
  host: string;
  onClose: () => void;
}

export default function FanoutHostDrawer({ step, host, onClose }: Props) {
  const hostRow: StepNodeDispatchView | undefined =
    step.per_node?.find((h) => h.host === host);

  // ESC closes the drawer (matches the existing patterns in this app).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Per-host findings come from result_json.byNode[host].findings —
  // the engine keeps per-host findings under the byNode map at step
  // completion. For mid-flight runs the byNode map isn't populated yet;
  // we fall back to displaying the count from per_node.
  const result = step.result as Record<string, unknown> | undefined;
  const byNode = result?.byNode as Record<string, { findings?: unknown[]; output_tail?: string }> | undefined;
  const hostByNode = byNode?.[host];
  const findingsList = (hostByNode?.findings ?? []) as unknown[];
  const hostOutputTail = hostRow?.output_tail || hostByNode?.output_tail || '';

  return (
    <>
      {/* backdrop */}
      <div
        className="fixed inset-0 bg-slate-900/30 z-40"
        onClick={onClose}
      />
      <aside
        className="fixed top-0 right-0 h-full w-[480px] max-w-[90vw] bg-panel border-l border-border z-50 shadow-xl flex flex-col"
        role="dialog"
        aria-label={`Host detail for ${host}`}
      >
        <header className="flex items-center justify-between px-4 py-3 border-b border-border bg-gradient-to-r from-brand-50/40 via-panel to-panel">
          <div className="flex items-center gap-2 min-w-0">
            <Server size={14} className="text-brand-700 shrink-0" />
            <span className="text-[11px] text-ink-dim font-mono truncate">
              {step.step_id} / <span className="text-ink font-semibold">{host}</span>
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-ink-dim hover:text-ink p-1 rounded"
            aria-label="Close"
          >
            <X size={14} />
          </button>
        </header>

        <div className="flex-1 overflow-y-auto px-4 py-3 space-y-4 text-[12px]">
          {!hostRow && (
            <div className="text-ink-mute italic flex items-center gap-2">
              <AlertCircle size={12} /> No dispatch row recorded for this host yet.
            </div>
          )}

          {hostRow && (
            <section>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">Status</div>
              <div className={cn(
                'inline-block text-[11px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded',
                hostRow.status === 'completed' && 'bg-green-50 text-green-700',
                hostRow.status === 'failed' && 'bg-red-50 text-red-700',
                hostRow.status === 'running' && 'bg-blue-50 text-blue-700',
                hostRow.status === 'pending' && 'bg-slate-100 text-slate-600',
              )}>{hostRow.status}</div>
              <dl className="grid grid-cols-2 gap-x-3 gap-y-1 mt-2 text-[11px]">
                {hostRow.started_at && (<><dt className="text-ink-mute">started</dt><dd className="font-mono">{hostRow.started_at}</dd></>)}
                {hostRow.ended_at   && (<><dt className="text-ink-mute">ended</dt>  <dd className="font-mono">{hostRow.ended_at}</dd></>)}
                {hostRow.error      && (<><dt className="text-ink-mute">error</dt>  <dd className="text-red-700 break-words">{hostRow.error}</dd></>)}
              </dl>
            </section>
          )}

          {step.rendered_prompt && (
            <section>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">Rendered prompt</div>
              <pre className="bg-slate-50 ring-1 ring-slate-200 rounded p-2 text-[10px] font-mono whitespace-pre-wrap break-words">{step.rendered_prompt}</pre>
            </section>
          )}

          {hostOutputTail && (
            <section>
              <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">Output tail</div>
              <pre className="bg-slate-50 ring-1 ring-slate-200 rounded p-2 text-[10px] font-mono whitespace-pre-wrap max-h-[260px] overflow-auto">{hostOutputTail}</pre>
            </section>
          )}

          <section>
            <div className="text-[10px] uppercase tracking-wide text-ink-mute font-semibold mb-1.5">
              Findings ({hostRow?.findings_count ?? 0})
            </div>
            {findingsList.length === 0 && (hostRow?.findings_count ?? 0) > 0 && (
              <div className="text-ink-mute italic text-[11px]">(populated when run completes)</div>
            )}
            {findingsList.length === 0 && (hostRow?.findings_count ?? 0) === 0 && (
              <div className="text-ink-mute italic text-[11px]">No findings.</div>
            )}
            {findingsList.length > 0 && (
              <pre className="bg-slate-50 ring-1 ring-slate-200 rounded p-2 text-[10px] font-mono whitespace-pre-wrap max-h-[260px] overflow-auto">{JSON.stringify(findingsList, null, 2)}</pre>
            )}
          </section>
        </div>

        {hostRow?.agent_run_id && (
          <footer className="px-4 py-3 border-t border-border bg-slate-50/40">
            <a
              href={`/runs/${encodeURIComponent(hostRow.agent_run_id)}`}
              className="inline-flex items-center gap-1 text-[11px] text-brand-700 hover:text-brand-900 font-medium"
            >
              <ExternalLink size={11} />
              Open agent run {hostRow.agent_run_id}
            </a>
          </footer>
        )}
      </aside>
    </>
  );
}
