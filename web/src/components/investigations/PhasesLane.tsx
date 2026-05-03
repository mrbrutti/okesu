// PhasesLane — sibling of the timeline SVG; not inside it. Operators
// drag inside the lane background to mark a time range, type a name,
// hit Enter. Click a pill to rename inline; hover to reveal an X
// delete button. Color is derived from hash(name); no picker UI.
import { useMemo, useRef, useState } from 'react';
import { api, type InvestigationPhase } from '../../api';
import { layoutPhases } from './timeline/phasesLayer';
import { xToT, type Range } from './timeline/scale';

const LANE_HEIGHT = 28;
const MIN_DRAG_PX = 5;

interface Props {
  invID: number;
  cpInstanceID?: string;
  phases: InvestigationPhase[];
  range: Range;
  drawableWidth: number;
  laneLabelWidth: number;
  onPhasesChange: () => void;
}

type DragState =
  | { kind: 'idle' }
  | { kind: 'dragging'; startX: number; currentX: number }
  | { kind: 'naming'; startX: number; endX: number; value: string };

export function PhasesLane({
  invID, cpInstanceID, phases, range, drawableWidth, laneLabelWidth, onPhasesChange,
}: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [drag, setDrag] = useState<DragState>({ kind: 'idle' });
  const [editingID, setEditingID] = useState<number | null>(null);
  const [editValue, setEditValue] = useState('');
  const [hoveredID, setHoveredID] = useState<number | null>(null);

  const layout = useMemo(
    () => layoutPhases(phases, range, drawableWidth, laneLabelWidth),
    [phases, range, drawableWidth, laneLabelWidth],
  );
  const rowCount = Math.max(1, ...layout.map((l) => l.row + 1));
  const containerWidth = laneLabelWidth + drawableWidth;

  function localX(e: React.PointerEvent): number {
    const rect = containerRef.current!.getBoundingClientRect();
    return e.clientX - rect.left;
  }

  function onPointerDownLane(e: React.PointerEvent) {
    if ((e.target as HTMLElement).dataset.role === 'phase-pill') return;
    if ((e.target as HTMLElement).dataset.role === 'phase-delete') return;
    const x = localX(e);
    if (x < laneLabelWidth) return;
    (e.target as Element).setPointerCapture?.(e.pointerId);
    setDrag({ kind: 'dragging', startX: x, currentX: x });
  }

  function onPointerMoveLane(e: React.PointerEvent) {
    if (drag.kind !== 'dragging') return;
    setDrag({ ...drag, currentX: localX(e) });
  }

  function onPointerUpLane(e: React.PointerEvent) {
    if (drag.kind !== 'dragging') return;
    const endX = localX(e);
    if (Math.abs(endX - drag.startX) < MIN_DRAG_PX) {
      setDrag({ kind: 'idle' });
      return;
    }
    setDrag({
      kind: 'naming',
      startX: Math.min(drag.startX, endX),
      endX:   Math.max(drag.startX, endX),
      value:  '',
    });
  }

  function commitNew() {
    if (drag.kind !== 'naming') return;
    const name = drag.value.trim();
    if (!name) {
      setDrag({ kind: 'idle' });
      return;
    }
    const startTs = xToT(drag.startX - laneLabelWidth, range.tMin, range.tMax, drawableWidth);
    const endTs   = xToT(drag.endX   - laneLabelWidth, range.tMin, range.tMax, drawableWidth);
    setDrag({ kind: 'idle' });
    api.investigations.phases.create(invID, {
      name,
      start_ts: Math.round(startTs),
      end_ts:   Math.round(endTs),
    }, cpInstanceID).then(onPhasesChange).catch(() => {});
  }

  function startEdit(p: InvestigationPhase) {
    setEditingID(p.ID);
    setEditValue(p.Name);
  }

  function commitEdit(p: InvestigationPhase) {
    const name = editValue.trim();
    setEditingID(null);
    if (!name || name === p.Name) return;
    api.investigations.phases.update(invID, p.ID, { name }, cpInstanceID)
      .then(onPhasesChange).catch(() => {});
  }

  function deletePhase(p: InvestigationPhase) {
    if (!window.confirm(`Delete phase "${p.Name}"?`)) return;
    api.investigations.phases.delete(invID, p.ID, cpInstanceID)
      .then(onPhasesChange).catch(() => {});
  }

  return (
    <div
      ref={containerRef}
      className="relative border-b border-border bg-slate-50/40"
      style={{ width: containerWidth, height: rowCount * LANE_HEIGHT }}
      onPointerDown={onPointerDownLane}
      onPointerMove={onPointerMoveLane}
      onPointerUp={onPointerUpLane}
    >
      <span className="absolute left-1.5 top-1.5 text-[11px] text-ink-mute">phases</span>

      {phases.length === 0 && drag.kind === 'idle' && (
        <span
          className="absolute inset-0 flex items-center justify-center text-[11px] text-ink-mute italic pointer-events-none"
          style={{ paddingLeft: laneLabelWidth }}
        >
          drag here to mark a phase
        </span>
      )}

      {drag.kind === 'dragging' && (
        <div
          className="absolute rounded bg-blue-200/60 ring-1 ring-blue-500"
          style={{
            left:   Math.min(drag.startX, drag.currentX),
            width:  Math.abs(drag.currentX - drag.startX),
            top:    4,
            height: LANE_HEIGHT - 8,
          }}
        />
      )}

      {drag.kind === 'naming' && (
        <input
          autoFocus
          value={drag.value}
          onChange={(e) => setDrag({ ...drag, value: e.target.value })}
          onBlur={commitNew}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commitNew();
            if (e.key === 'Escape') setDrag({ kind: 'idle' });
          }}
          placeholder="phase name"
          className="absolute rounded border border-blue-500 px-1 text-[11px] bg-white"
          style={{
            left:   drag.startX,
            width:  Math.max(80, drag.endX - drag.startX),
            top:    4,
            height: LANE_HEIGHT - 8,
          }}
        />
      )}

      {layout.map((entry) => {
        const p = entry.phase;
        const isEditing = editingID === p.ID;
        const isHovered = hoveredID === p.ID;
        const hue = hashHue(p.Name);
        const bg     = `hsl(${hue}, 70%, 92%)`;
        const ring   = `hsl(${hue}, 60%, 65%)`;
        const text   = `hsl(${hue}, 55%, 28%)`;
        return (
          <div key={p.ID} style={{ position: 'absolute', left: entry.x, top: entry.row * LANE_HEIGHT + 4, width: Math.max(entry.width, 4), height: LANE_HEIGHT - 8 }}>
            {isEditing ? (
              <input
                autoFocus
                value={editValue}
                onChange={(e) => setEditValue(e.target.value)}
                onBlur={() => commitEdit(p)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commitEdit(p);
                  if (e.key === 'Escape') setEditingID(null);
                }}
                className="w-full h-full rounded border border-blue-500 px-1 text-[11px] bg-white"
              />
            ) : (
              <>
                <div
                  data-role="phase-pill"
                  role="button"
                  className="rounded ring-1 px-1.5 text-[10px] font-medium truncate cursor-pointer"
                  style={{ backgroundColor: bg, color: text, boxShadow: `inset 0 0 0 1px ${ring}`, height: '100%', display: 'flex', alignItems: 'center' }}
                  onClick={() => startEdit(p)}
                  onMouseEnter={() => setHoveredID(p.ID)}
                  onMouseLeave={() => setHoveredID(null)}
                >
                  {p.Name}
                </div>
                {isHovered && (
                  <button
                    data-role="phase-delete"
                    onClick={(e) => { e.stopPropagation(); deletePhase(p); }}
                    className="absolute right-0.5 top-1/2 -translate-y-1/2 w-4 h-4 rounded-full bg-white border border-red-300 text-red-600 text-[10px] leading-none hover:bg-red-50"
                    aria-label={`Delete phase ${p.Name}`}
                  >
                    ×
                  </button>
                )}
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}

function hashHue(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return ((h % 360) + 360) % 360;
}
