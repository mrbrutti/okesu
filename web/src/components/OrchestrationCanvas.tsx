// DAG-shaped canvas for an orchestration run.
//
// Phase C v1: linear execution → vertical column. The component
// accepts a graph (nodes + edges), so when v2 brings real fan-out and
// fan-in nodes, the same renderer handles them — we just compute
// different node positions. No third-party graph library; SVG arrows
// + absolute-positioned cards keep the bundle small.
//
// Visual idiom matches the rest of the platform:
//   - Cards use the same panel surface (bg-panel + shadow-card +
//     rounded-md) as Dashboard / Findings / Daimons.
//   - Status colors come from the same palette already used in
//     pages/Orchestrations.tsx (blue / green / red / amber / slate),
//     accessible via stepCanvasTone() so changing one palette ripples
//     through both views.
//   - Approval gates pulse the card border + show an inline Approve
//     button — same affordance as the list view, just laid out on a
//     canvas.

import { Fragment, MouseEvent, useMemo } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  ChevronRight,
  Cpu,
  Loader2,
  Pause,
  Server,
  XCircle,
} from 'lucide-react';
import type {
  OrchestrationStepStatus,
  OrchestrationStepView,
} from '../api';
import { CPSourceChip } from './CPSourceChip';
import { cn } from '../lib/cn';

interface Props {
  steps: OrchestrationStepView[];
  /** Step that's currently approving — disables that card's button. */
  approvingStepID?: string | null;
  /** Operator clicked Approve. The page handles the API call + refresh. */
  onApprove?: (stepID: string) => void;
  /** Operator clicked a card. Parent toggles the expanded body. */
  onSelectStep?: (stepID: string) => void;
  /** Currently-expanded step (or null). The canvas renders the
   *  expanded body inline below the card so the layout stays simple. */
  expandedStepID?: string | null;
}

// Node geometry — picked so a chain of 6 steps fits on a 1080p screen
// without scrolling, and each card has room for title + status + chip.
const CARD_WIDTH = 240;
const CARD_HEIGHT = 86;
const GAP_Y = 28; // vertical space between cards (leaves room for arrow)
const CANVAS_PAD = 16;

interface PositionedStep {
  step: OrchestrationStepView;
  x: number;
  y: number;
}

export default function OrchestrationCanvas({
  steps,
  approvingStepID,
  onApprove,
  onSelectStep,
  expandedStepID,
}: Props) {
  // Phase C v1 layout: pure vertical column. v2 will compute (x, y)
  // from a real DAG layout; the renderer below only cares about
  // positions, so the v2 swap is a `layout()` function change.
  const positioned = useMemo<PositionedStep[]>(() => {
    return steps.map((s, i) => ({
      step: s,
      x: 0,
      y: i * (CARD_HEIGHT + GAP_Y),
    }));
  }, [steps]);

  const expandedIdx = expandedStepID
    ? steps.findIndex((s) => s.step_id === expandedStepID)
    : -1;
  const expandedStep = expandedIdx >= 0 ? steps[expandedIdx] : null;

  const totalHeight = positioned.length * (CARD_HEIGHT + GAP_Y) - GAP_Y;

  // Single-positioned container so SVG + cards share one coordinate
  // space. The dual-div nesting we had before could collapse to 0
  // height inside a flex column layout — this simplification puts an
  // explicit width/height on the only positioned wrapper.
  const canvasW = CARD_WIDTH + CANVAS_PAD * 2;
  const canvasH = totalHeight + CANVAS_PAD * 2;

  return (
    <div className="space-y-3">
      {/* Header — small label so it's obvious the operator is looking
          at the orchestration graph, not the standalone Run page. */}
      <div className="flex items-center gap-2 text-[11px] text-ink-dim">
        <span className="w-1 h-3 rounded-full bg-gradient-to-b from-brand-400 to-brand-600" />
        <span>
          Orchestration graph · <span className="text-ink font-medium">{steps.length}</span> step{steps.length === 1 ? '' : 's'}
        </span>
      </div>

      <div
        data-testid="orchestration-canvas"
        className="relative rounded-lg border border-brand-200 shadow-card overflow-hidden"
        style={{
          width: canvasW,
          height: canvasH,
          // Subtle tinted backdrop with a faint dot grid — gives the
          // canvas a distinct "surface" feel so the cards read as
          // sitting on a workspace, not as floating list items.
          background:
            'linear-gradient(to bottom, #faf8ff, #ffffff), radial-gradient(circle at 8px 8px, #e9d5ff 1px, transparent 1px) 0 0 / 16px 16px',
        }}
      >
        {/* SVG arrows. Same coordinate space as the cards so positions
            line up exactly. */}
        <svg
          width={canvasW}
          height={canvasH}
          className="absolute inset-0 pointer-events-none"
        >
          <defs>
            <marker id="arrow-head" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto">
              <path d="M0,0 L8,4 L0,8 z" fill="#94a3b8" />
            </marker>
          </defs>
          {positioned.slice(0, -1).map((p, i) => {
            const fromY = p.y + CARD_HEIGHT + CANVAS_PAD;
            const toY = positioned[i + 1].y + CANVAS_PAD;
            const x = CANVAS_PAD + CARD_WIDTH / 2;
            return (
              <line
                key={`arrow-${i}`}
                x1={x} y1={fromY}
                x2={x} y2={toY - 6}
                stroke="#94a3b8"
                strokeWidth="2"
                markerEnd="url(#arrow-head)"
              />
            );
          })}
        </svg>

        {positioned.map((p) => (
          <CanvasCard
            key={p.step.step_id}
            step={p.step}
            x={p.x + CANVAS_PAD}
            y={p.y + CANVAS_PAD}
            selected={expandedStepID === p.step.step_id}
            approving={approvingStepID === p.step.step_id}
            onApprove={onApprove}
            onClick={() => onSelectStep?.(p.step.step_id)}
          />
        ))}
      </div>

      {/* Expanded body — rendered below the canvas so it doesn't
          have to fight for space inside the card. Shows rendered
          prompt / result / output / error when an operator clicks a
          card. */}
      {expandedStep && <ExpandedBody step={expandedStep} />}
    </div>
  );
}

interface CardProps {
  step: OrchestrationStepView;
  x: number;
  y: number;
  selected: boolean;
  approving: boolean;
  onClick: () => void;
  onApprove?: (stepID: string) => void;
}

function CanvasCard({ step, x, y, selected, approving, onClick, onApprove }: CardProps) {
  const tone = stepCanvasTone(step.status);
  const handleApprove = (e: MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation();
    onApprove?.(step.step_id);
  };
  return (
    <button
      onClick={onClick}
      className={cn(
        // Same panel idiom as Dashboard StatTile: bg-panel + shadow-card
        // + brand-200 ring on hover. The status-coloured top stripe
        // mirrors the StatTile h-0.5 trick — quick chromatic identity.
        'absolute text-left bg-panel border rounded-md shadow-card overflow-hidden transition-all hover:shadow-md',
        selected ? 'ring-2 ring-brand-300 border-brand-200' : 'border-border hover:border-brand-200',
        // Pulse waiting-for-approval cards so an oncall can't miss
        // them — same affordance the list view uses, just on canvas.
        step.status === 'waiting_approval' && 'ring-2 ring-amber-300 animate-pulse',
      )}
      style={{
        left: x,
        top: y,
        width: CARD_WIDTH,
        height: CARD_HEIGHT,
      }}
    >
      <div className={cn('h-1', tone.stripe)} />
      <div className="p-2.5 h-full flex flex-col justify-between">
        <div className="flex items-start gap-2 min-w-0">
          <StatusIcon status={step.status} />
          <div className="flex-1 min-w-0">
            <div className="text-xs font-medium text-ink truncate">{step.step_id}</div>
            <div className="mt-0.5 flex items-center gap-1 flex-wrap">
              <StatusBadge status={step.status} />
              {step.cp_instance_id && step.cp_instance_id !== 'local' && (
                <CPSourceChip
                  source={{ instance_id: step.cp_instance_id, display_name: step.cp_instance_id.slice(0, 8) }}
                />
              )}
            </div>
          </div>
          {step.status === 'waiting_approval' && (
            <span
              role="button"
              tabIndex={0}
              onClick={handleApprove}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') handleApprove(e as unknown as MouseEvent<HTMLButtonElement>); }}
              className={cn(
                'inline-flex items-center gap-1 bg-amber-500 hover:bg-amber-600 text-white text-[10px] font-medium px-1.5 py-0.5 rounded',
                approving && 'opacity-60',
              )}
            >
              {approving ? <Loader2 size={9} className="animate-spin" /> : <CheckCircle2 size={9} />}
              Approve
            </span>
          )}
        </div>
        <div className="text-[10px] text-ink-mute truncate">
          {stepFooter(step)}
        </div>
      </div>
    </button>
  );
}

function ExpandedBody({ step }: { step: OrchestrationStepView }) {
  return (
    <div className="bg-panel border border-border rounded-md shadow-card p-3 space-y-2">
      <div className="text-[11px] text-ink-dim flex items-center gap-2">
        <ChevronRight size={11} />
        <span className="font-medium text-ink">{step.step_id}</span>
        <StatusBadge status={step.status} />
      </div>
      {step.rendered_prompt && (
        <Section label="Prompt" content={step.rendered_prompt} />
      )}
      {step.result && Object.keys(step.result).length > 0 && (
        <Section label="Result" content={JSON.stringify(step.result, null, 2)} />
      )}
      {step.output_summary && (
        <Section label="Output (tail)" content={step.output_summary} maxHeight={192} />
      )}
      {step.error && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          <span className="font-medium">Error:</span> {step.error}
        </div>
      )}
      {step.run_id && (
        <a
          href={`/runs?id=${step.run_id}`}
          className="text-[11px] text-brand-700 hover:underline inline-flex items-center gap-1"
        >
          Open underlying Run →
        </a>
      )}
    </div>
  );
}

function Section({ label, content, maxHeight }: { label: string; content: string; maxHeight?: number }) {
  return (
    <section>
      <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      <pre
        className="text-[11px] font-mono bg-slate-50 border border-border rounded p-2 whitespace-pre-wrap break-words overflow-auto"
        style={maxHeight ? { maxHeight } : undefined}
      >
        {content}
      </pre>
    </section>
  );
}

function StatusIcon({ status }: { status: OrchestrationStepStatus }) {
  switch (status) {
    case 'running':
      return <Loader2 size={14} className="text-blue-600 animate-spin shrink-0 mt-0.5" />;
    case 'completed':
      return <CheckCircle2 size={14} className="text-green-600 shrink-0 mt-0.5" />;
    case 'failed':
      return <XCircle size={14} className="text-red-600 shrink-0 mt-0.5" />;
    case 'waiting_approval':
      return <Pause size={14} className="text-amber-600 shrink-0 mt-0.5" />;
    case 'skipped':
      return <ChevronRight size={14} className="text-slate-400 shrink-0 mt-0.5" />;
    default:
      return <AlertCircle size={14} className="text-slate-400 shrink-0 mt-0.5" />;
  }
}

function StatusBadge({ status }: { status: OrchestrationStepStatus }) {
  const tone = stepStatusTone(status);
  return (
    <span className={cn('inline-flex items-center gap-0.5 text-[9px] uppercase tracking-wide font-medium px-1 py-0.5 rounded', tone.cls)}>
      {status.replace('_', ' ')}
    </span>
  );
}

// Visual tokens kept here (not hoisted to a shared module) so the
// canvas can be moved between pages without dragging a separate
// stylesheet. If we add an "orchestrations everywhere" view that
// reuses this with a different palette, we hoist then.
function stepCanvasTone(status: OrchestrationStepStatus): { stripe: string } {
  switch (status) {
    case 'running':          return { stripe: 'bg-blue-500' };
    case 'completed':        return { stripe: 'bg-green-500' };
    case 'failed':           return { stripe: 'bg-red-500' };
    case 'waiting_approval': return { stripe: 'bg-amber-400' };
    case 'skipped':          return { stripe: 'bg-slate-300' };
    default:                 return { stripe: 'bg-slate-300' };
  }
}

function stepStatusTone(status: OrchestrationStepStatus) {
  switch (status) {
    case 'running':          return { cls: 'bg-blue-50 text-blue-700' };
    case 'completed':        return { cls: 'bg-green-50 text-green-700' };
    case 'failed':           return { cls: 'bg-red-50 text-red-700' };
    case 'waiting_approval': return { cls: 'bg-amber-50 text-amber-700' };
    case 'skipped':          return { cls: 'bg-slate-100 text-slate-600' };
    default:                 return { cls: 'bg-slate-100 text-slate-600' };
  }
}

function stepFooter(step: OrchestrationStepView): string {
  const parts: string[] = [];
  if (step.started_at) {
    const ageS = Math.floor((Date.now() - new Date(step.started_at).getTime()) / 1000);
    if (ageS < 60) parts.push(`${ageS}s ago`);
    else if (ageS < 3600) parts.push(`${Math.floor(ageS / 60)}m ago`);
    else parts.push(`${Math.floor(ageS / 3600)}h ago`);
  }
  return parts.join(' · ') || '—';
}

// Quiet imports.
const _imp = { Fragment, Cpu, Server };
void _imp;
