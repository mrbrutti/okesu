# Timeline Clusters + Portal'd Tooltip Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cluster overlapping point events on the investigation timeline into `+N` glyphs that expand into a clickable mini-list, and replace SVG `<title>` hover tooltips with a portal'd styled `<div>` for instant appearance + richer rendering.

**Architecture:** Two new files (a pure cluster algorithm + a generic portal'd Tooltip component) plus one modified component (EventsLayer in CaseTimeline.tsx). Clustering is per-lane bucketed by x-pixel; bars (runs/IOCs) skip clustering. Tooltip has uncontrolled (hover) and controlled (cluster popover) modes.

**Tech Stack:** React 18 + TypeScript + Tailwind, Vitest + @testing-library/react + jsdom (already configured).

---

## File structure

### New

- `web/src/components/investigations/timeline/cluster.ts` — pure clustering function.
- `web/src/components/investigations/timeline/cluster.test.ts`
- `web/src/components/Tooltip.tsx` — generic portal'd tooltip (uncontrolled hover + controlled popover modes).
- `web/src/components/Tooltip.test.tsx`

### Modified

- `web/src/components/investigations/CaseTimeline.tsx` — `EventsLayer` rewrites: pipe through `clusterEvents`; new `renderSingle` wraps each shape in `<Tooltip>` and removes `<title>`; new `renderCluster` for cluster glyphs; new `ClusterList` inline; new `tooltipFor` per-kind content function; new `useState<ClusterOrEvent | null>(null)` for `openCluster`.
- `web/src/components/investigations/CaseTimeline.test.tsx` — extend with cluster + tooltip integration tests.

---

## Task Group A — `cluster.ts` pure helper

### Task A1: clusterEvents

**Files:**
- Create: `web/src/components/investigations/timeline/cluster.ts`
- Create: `web/src/components/investigations/timeline/cluster.test.ts`

- [ ] **Step A1.1: Write the failing test**

```ts
// web/src/components/investigations/timeline/cluster.test.ts
import { describe, it, expect } from 'vitest';
import { clusterEvents, type ClusterOrEvent } from './cluster';
import type { TimelineEvent, TimelineLane } from './types';

const VISIBLE: TimelineLane[] = ['lifecycle', 'findings', 'runs', 'notes'];

function finding(id: number, ts: number): TimelineEvent {
  return { kind: 'finding', ts, id, severity: 'LOW', title: `f${id}`, agent: 'a', host: 'h' };
}

function run(id: number, startTs: number, endTs: number): TimelineEvent {
  return { kind: 'run', startTs, endTs, running: false, id, status: 'completed', orchestrationName: 'o' };
}

// Identity tToX so x === t for easy bucket reasoning.
const idScale = (t: number) => t;

describe('clusterEvents', () => {
  it('returns empty output for empty input', () => {
    expect(clusterEvents([], VISIBLE, idScale)).toEqual([]);
  });

  it('passes events through as singletons when below threshold (default 4)', () => {
    const evs = [finding(1, 0), finding(2, 1), finding(3, 2)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(3);
    out.forEach((c) => expect(c.kind).toBe('single'));
  });

  it('clusters when 4+ point events fall in the same bucket', () => {
    // bucketPx default 12 → bucket 0 = [0..11]; place 5 dots within that range
    const evs = [finding(1, 0), finding(2, 2), finding(3, 4), finding(4, 6), finding(5, 8)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(1);
    expect(out[0].kind).toBe('cluster');
    if (out[0].kind === 'cluster') {
      expect(out[0].lane).toBe('findings');
      expect(out[0].events.length).toBe(5);
      // cx is the bucket midpoint = bucketPx * (bucketIndex + 0.5) = 6
      expect(out[0].cx).toBe(6);
    }
  });

  it('does not cluster across adjacent buckets', () => {
    // bucket 0 = [0..11] (3 events), bucket 1 = [12..23] (2 events)
    const evs = [finding(1, 0), finding(2, 4), finding(3, 8), finding(4, 13), finding(5, 16)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(5);
    out.forEach((c) => expect(c.kind).toBe('single'));
  });

  it('passes bars (runs, iocs) through as singletons even when their x bucket would cluster', () => {
    // 4 runs all starting in bucket 0; bars must NOT cluster.
    const evs = [run(1, 0, 5), run(2, 2, 6), run(3, 4, 8), run(4, 6, 10)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(4);
    out.forEach((c) => expect(c.kind).toBe('single'));
  });

  it('keeps separate clusters per lane (findings + daimons in same x bucket)', () => {
    const evs: TimelineEvent[] = [
      finding(1, 0), finding(2, 2), finding(3, 4), finding(4, 6),  // 4 findings → cluster
      { kind: 'daimon', ts: 0, agent: 'a', findingID: 1 },
      { kind: 'daimon', ts: 2, agent: 'a', findingID: 2 },
      { kind: 'daimon', ts: 4, agent: 'a', findingID: 3 },
      { kind: 'daimon', ts: 6, agent: 'a', findingID: 4 },         // 4 daimons → cluster
    ];
    const out = clusterEvents(evs, ['findings', 'daimons'], idScale);
    expect(out.length).toBe(2);
    const lanes = out.map((c) => c.kind === 'cluster' ? c.lane : null);
    expect(lanes).toContain('findings');
    expect(lanes).toContain('daimons');
  });

  it('cluster events array is sorted ascending by primary timestamp', () => {
    const evs = [finding(1, 8), finding(2, 0), finding(3, 4), finding(4, 2)];
    const out = clusterEvents(evs, VISIBLE, idScale);
    expect(out.length).toBe(1);
    if (out[0].kind === 'cluster') {
      expect(out[0].events.map((e) => e.kind === 'finding' ? e.id : 0)).toEqual([2, 4, 3, 1]);
    }
  });

  it('hides clustered events when their lane is not visible', () => {
    const evs = [finding(1, 0), finding(2, 2), finding(3, 4), finding(4, 6)];
    // Only 'lifecycle' visible — findings should not appear at all
    const out = clusterEvents(evs, ['lifecycle'], idScale);
    expect(out).toEqual([]);
  });

  it('respects a custom threshold and bucketPx', () => {
    const evs = [finding(1, 0), finding(2, 5)];
    const out = clusterEvents(evs, VISIBLE, idScale, 10, 2);
    // bucketPx=10 → both in bucket 0; threshold=2 → cluster
    expect(out.length).toBe(1);
    expect(out[0].kind).toBe('cluster');
  });
});
```

- [ ] **Step A1.2: Run test, expect FAIL**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-timeline-clusters/web
npm test -- --run src/components/investigations/timeline/cluster.test.ts
```

Expected: FAIL — module doesn't exist.

- [ ] **Step A1.3: Implement cluster.ts**

```ts
// Pure clustering of timeline point events into +N stacks. Buckets
// by (lane, x-bucket) and emits either a single-event entry or a
// cluster entry per bucket. Bars (run, ioc) skip clustering and pass
// through as singletons — their duration is itself the visual signal.

import type { TimelineEvent, TimelineLane } from './types';

export type ClusterOrEvent =
  | { kind: 'single'; event: TimelineEvent }
  | { kind: 'cluster'; lane: TimelineLane; events: TimelineEvent[]; cx: number };

const POINT_LANE: Record<string, TimelineLane | null> = {
  lifecycle: 'lifecycle',
  finding:   'findings',
  note:      'notes',
  ioc:       null,    // bar
  run:       null,    // bar
  daimon:    'daimons',
  audit:     'audit',
};

function primaryTs(e: TimelineEvent): number {
  if (e.kind === 'run' || e.kind === 'ioc') return e.startTs;
  return e.ts;
}

export function clusterEvents(
  events: TimelineEvent[],
  visibleLanes: TimelineLane[],
  tToX: (t: number) => number,
  bucketPx = 12,
  threshold = 4,
): ClusterOrEvent[] {
  const visible = new Set(visibleLanes);
  const bars: ClusterOrEvent[] = [];
  const buckets = new Map<string, { lane: TimelineLane; events: TimelineEvent[] }>();

  for (const e of events) {
    const lane = POINT_LANE[e.kind];
    if (lane === null) {
      // Bar — pass through as a singleton if its lane is visible.
      const barLane = e.kind === 'run' ? 'runs' : 'iocs';
      if (!visible.has(barLane as TimelineLane)) continue;
      bars.push({ kind: 'single', event: e });
      continue;
    }
    if (!lane || !visible.has(lane)) continue;
    const bucketIdx = Math.floor(tToX(primaryTs(e)) / bucketPx);
    const key = `${lane}:${bucketIdx}`;
    let b = buckets.get(key);
    if (!b) {
      b = { lane, events: [] };
      buckets.set(key, b);
    }
    b.events.push(e);
  }

  const out: ClusterOrEvent[] = [...bars];
  for (const [key, b] of buckets) {
    const bucketIdx = Number(key.slice(key.lastIndexOf(':') + 1));
    if (b.events.length < threshold) {
      for (const e of b.events) out.push({ kind: 'single', event: e });
      continue;
    }
    const sorted = [...b.events].sort((a, c) => primaryTs(a) - primaryTs(c));
    out.push({
      kind: 'cluster',
      lane: b.lane,
      events: sorted,
      cx: bucketPx * (bucketIdx + 0.5),
    });
  }
  return out;
}
```

- [ ] **Step A1.4: Run, expect PASS**

```bash
npm test -- --run src/components/investigations/timeline/cluster.test.ts
node_modules/.bin/tsc -b
```

Expected: 9 PASS, tsc clean.

- [ ] **Step A1.5: Commit**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-timeline-clusters
git add web/src/components/investigations/timeline/cluster.ts \
        web/src/components/investigations/timeline/cluster.test.ts
git commit -m "web(timeline): pure cluster algorithm for point events"
```

---

## Task Group B — `Tooltip` portal'd component

### Task B1: Tooltip with uncontrolled + controlled modes

**Files:**
- Create: `web/src/components/Tooltip.tsx`
- Create: `web/src/components/Tooltip.test.tsx`

- [ ] **Step B1.1: Write the failing test**

```tsx
// web/src/components/Tooltip.test.tsx
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import { Tooltip } from './Tooltip';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('Tooltip uncontrolled', () => {
  it('shows on mouseenter, hides on mouseleave', () => {
    render(
      <Tooltip content={<span>tip body</span>}>
        <button>anchor</button>
      </Tooltip>,
    );
    expect(screen.queryByText('tip body')).toBeNull();
    fireEvent.mouseEnter(screen.getByRole('button'));
    expect(screen.getByText('tip body')).toBeTruthy();
    fireEvent.mouseLeave(screen.getByRole('button'));
    expect(screen.queryByText('tip body')).toBeNull();
  });

  it('honors hover delay', () => {
    vi.useFakeTimers();
    render(
      <Tooltip content={<span>tip</span>} delay={250}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.mouseEnter(screen.getByRole('button'));
    expect(screen.queryByText('tip')).toBeNull();
    act(() => { vi.advanceTimersByTime(250); });
    expect(screen.getByText('tip')).toBeTruthy();
  });

  it('shows on focus and hides on blur', () => {
    render(
      <Tooltip content={<span>tip</span>}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.focus(screen.getByRole('button'));
    expect(screen.getByText('tip')).toBeTruthy();
    fireEvent.blur(screen.getByRole('button'));
    expect(screen.queryByText('tip')).toBeNull();
  });

  it('renders content into document.body via portal', () => {
    render(
      <Tooltip content={<span data-testid="tip-body">tip</span>}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.mouseEnter(screen.getByRole('button'));
    const tip = screen.getByTestId('tip-body');
    // Walk up: tip is inside a portaled <div> that sits as a direct
    // child of document.body.
    let node: HTMLElement | null = tip;
    let foundBodyChild = false;
    while (node && node.parentElement) {
      if (node.parentElement === document.body) { foundBodyChild = true; break; }
      node = node.parentElement;
    }
    expect(foundBodyChild).toBe(true);
  });
});

describe('Tooltip controlled', () => {
  it('renders only when open=true', () => {
    const { rerender } = render(
      <Tooltip content={<span>tip</span>} open={false} onOpenChange={() => {}}>
        <button>anchor</button>
      </Tooltip>,
    );
    expect(screen.queryByText('tip')).toBeNull();
    rerender(
      <Tooltip content={<span>tip</span>} open={true} onOpenChange={() => {}}>
        <button>anchor</button>
      </Tooltip>,
    );
    expect(screen.getByText('tip')).toBeTruthy();
  });

  it('outside-click fires onOpenChange(false)', () => {
    const onOpenChange = vi.fn();
    render(
      <div>
        <button data-testid="outside">outside</button>
        <Tooltip content={<span>tip</span>} open={true} onOpenChange={onOpenChange}>
          <button>anchor</button>
        </Tooltip>
      </div>,
    );
    fireEvent.mouseDown(screen.getByTestId('outside'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('Escape fires onOpenChange(false)', () => {
    const onOpenChange = vi.fn();
    render(
      <Tooltip content={<span>tip</span>} open={true} onOpenChange={onOpenChange}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('does not show on hover when in controlled mode', () => {
    const onOpenChange = vi.fn();
    render(
      <Tooltip content={<span>tip</span>} open={false} onOpenChange={onOpenChange}>
        <button>anchor</button>
      </Tooltip>,
    );
    fireEvent.mouseEnter(screen.getByRole('button'));
    expect(screen.queryByText('tip')).toBeNull();
  });
});
```

- [ ] **Step B1.2: Run, expect FAIL**

```bash
npm test -- --run src/components/Tooltip.test.tsx
```

Expected: FAIL.

- [ ] **Step B1.3: Implement Tooltip.tsx**

```tsx
// Generic portal'd tooltip. Two modes:
//
//   Uncontrolled (no `open` prop) — opens on mouseenter/focus,
//   closes on mouseleave/blur. `delay` defaults to 0 for instant
//   appearance (vs the ~1s native browser-tooltip delay).
//
//   Controlled (`open` + `onOpenChange` set) — caller drives
//   open/close. Used by the cluster popover. Closes on outside
//   click, Escape, or any descendant calling onOpenChange(false).
//
// Portal target is document.body so the tooltip is never clipped by
// SVG viewport or parent overflow:hidden. Position computed from the
// anchor's getBoundingClientRect(); re-positioned via window scroll
// listener and a ResizeObserver on the anchor.

import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

interface TooltipProps {
  content: ReactNode;
  placement?: 'top' | 'bottom';
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  delay?: number;
  children: ReactNode;
}

export function Tooltip({
  content,
  placement = 'top',
  open: controlledOpen,
  onOpenChange,
  delay = 0,
  children,
}: TooltipProps) {
  const isControlled = controlledOpen !== undefined;
  const [hoverOpen, setHoverOpen] = useState(false);
  const open = isControlled ? !!controlledOpen : hoverOpen;

  const anchorRef = useRef<HTMLSpanElement | null>(null);
  const tooltipRef = useRef<HTMLDivElement | null>(null);
  const timerRef = useRef<number | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  const id = useId();

  function clearDelay() {
    if (timerRef.current != null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }

  function showHover() {
    if (isControlled) return;
    clearDelay();
    if (delay <= 0) { setHoverOpen(true); return; }
    timerRef.current = window.setTimeout(() => setHoverOpen(true), delay);
  }

  function hideHover() {
    if (isControlled) return;
    clearDelay();
    setHoverOpen(false);
  }

  // Position relative to the anchor whenever open changes or the
  // viewport scrolls/resizes.
  useEffect(() => {
    if (!open) { setPos(null); return; }
    function update() {
      const a = anchorRef.current;
      if (!a) return;
      const r = a.getBoundingClientRect();
      const left = r.left + r.width / 2;
      const top = placement === 'top' ? r.top : r.bottom;
      setPos({ left, top });
    }
    update();
    window.addEventListener('scroll', update, true);
    window.addEventListener('resize', update);
    let ro: ResizeObserver | null = null;
    if (anchorRef.current && typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(update);
      ro.observe(anchorRef.current);
    }
    return () => {
      window.removeEventListener('scroll', update, true);
      window.removeEventListener('resize', update);
      if (ro) ro.disconnect();
    };
  }, [open, placement]);

  // Outside-click + Escape for controlled mode.
  useEffect(() => {
    if (!isControlled || !open) return;
    function onDown(ev: MouseEvent) {
      const t = ev.target as Node | null;
      if (!t) return;
      if (anchorRef.current?.contains(t)) return;
      if (tooltipRef.current?.contains(t)) return;
      onOpenChange?.(false);
    }
    function onKey(ev: KeyboardEvent) {
      if (ev.key === 'Escape') onOpenChange?.(false);
    }
    document.addEventListener('mousedown', onDown);
    window.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [isControlled, open, onOpenChange]);

  // Cleanup any pending hover timer on unmount.
  useEffect(() => clearDelay, []);

  const tooltipNode = open && pos ? (
    <div
      ref={tooltipRef}
      role={isControlled ? 'dialog' : 'tooltip'}
      id={id}
      style={{
        position: 'fixed',
        left: pos.left,
        top: pos.top,
        transform: placement === 'top'
          ? 'translate(-50%, calc(-100% - 6px))'
          : 'translate(-50%, 6px)',
        pointerEvents: isControlled ? 'auto' : 'none',
        zIndex: 50,
      }}
      className="bg-panel border border-border rounded-md shadow-lg px-2 py-1.5 text-xs text-ink max-w-sm"
    >
      {content}
    </div>
  ) : null;

  return (
    <>
      <span
        ref={anchorRef}
        aria-describedby={!isControlled && open ? id : undefined}
        onMouseEnter={showHover}
        onMouseLeave={hideHover}
        onFocus={showHover}
        onBlur={hideHover}
        style={{ display: 'contents' }}
      >
        {children}
      </span>
      {tooltipNode && createPortal(tooltipNode, document.body)}
    </>
  );
}
```

- [ ] **Step B1.4: Run, expect PASS**

```bash
npm test -- --run src/components/Tooltip.test.tsx
node_modules/.bin/tsc -b
```

Expected: 8 PASS, tsc clean.

- [ ] **Step B1.5: Commit**

```bash
git add web/src/components/Tooltip.tsx web/src/components/Tooltip.test.tsx
git commit -m "web(tooltip): portal'd Tooltip with hover + controlled modes"
```

---

## Task Group C — Integrate into `EventsLayer`

### Task C1: replace `<title>` with `<Tooltip>` everywhere; add cluster rendering

**Files:**
- Modify: `web/src/components/investigations/CaseTimeline.tsx`
- Modify: `web/src/components/investigations/CaseTimeline.test.tsx`

- [ ] **Step C1.1: Add cluster + tooltip integration tests**

Append to `web/src/components/investigations/CaseTimeline.test.tsx` inside its top-level `describe(...)` block (or at the end of the file in a new `describe`):

```tsx
describe('CaseTimeline (clusters + tooltip)', () => {
  it('renders one cluster glyph for 5 findings inside the same x-bucket', () => {
    // 5 findings at very close timestamps so they fall into the same
    // 12px bucket regardless of zoom.
    const baseTs = Date.parse('2026-04-30T12:00:00Z');
    const b = bundle({
      findings: Array.from({ length: 5 }, (_, i) => ({
        ID: i + 1,
        Ts: baseTs + i * 1000, // 1s apart — well within one bucket at any reasonable zoom
        Agent: { String: 'a', Valid: true },
        Host: { String: 'h', Valid: true },
        Severity: { String: i === 0 ? 'CRITICAL' : 'LOW', Valid: true },
        Title: { String: `f${i}`, Valid: true },
        Status: { String: 'open', Valid: true },
        Tags: { String: '', Valid: false },
        Subtype: { String: '', Valid: false },
        LinkedAt: '',
        LinkMethod: { String: '', Valid: false },
        LinkedBy: { String: '', Valid: false },
      })),
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    // The cluster glyph carries an aria-label identifying it as a cluster.
    expect(screen.getByLabelText(/Cluster of 5 findings/i)).toBeTruthy();
    // No individual finding dot labels should be present.
    expect(screen.queryByLabelText(/Finding #1/)).toBeNull();
  });

  it('clicking a cluster glyph opens the popover with one row per finding', () => {
    const baseTs = Date.parse('2026-04-30T12:00:00Z');
    const b = bundle({
      findings: Array.from({ length: 4 }, (_, i) => ({
        ID: i + 10,
        Ts: baseTs + i * 1000,
        Agent: { String: 'a', Valid: true },
        Host: { String: 'h', Valid: true },
        Severity: { String: 'HIGH', Valid: true },
        Title: { String: `f${i + 10}`, Valid: true },
        Status: { String: 'open', Valid: true },
        Tags: { String: '', Valid: false },
        Subtype: { String: '', Valid: false },
        LinkedAt: '',
        LinkMethod: { String: '', Valid: false },
        LinkedBy: { String: '', Valid: false },
      })),
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByLabelText(/Cluster of 4 findings/i));
    // Popover content shows each finding's title.
    expect(screen.getByText(/f10/)).toBeTruthy();
    expect(screen.getByText(/f13/)).toBeTruthy();
  });

  it('clicking a row inside the cluster popover fires entity:open and closes the popover', () => {
    const baseTs = Date.parse('2026-04-30T12:00:00Z');
    const b = bundle({
      findings: Array.from({ length: 4 }, (_, i) => ({
        ID: i + 20,
        Ts: baseTs + i * 1000,
        Agent: { String: 'a', Valid: true },
        Host: { String: 'h', Valid: true },
        Severity: { String: 'LOW', Valid: true },
        Title: { String: `g${i + 20}`, Valid: true },
        Status: { String: 'open', Valid: true },
        Tags: { String: '', Valid: false },
        Subtype: { String: '', Valid: false },
        LinkedAt: '',
        LinkMethod: { String: '', Valid: false },
        LinkedBy: { String: '', Valid: false },
      })),
    });
    const events: { kind: string; identityKey: string }[] = [];
    const handler = (e: Event) => {
      events.push((e as CustomEvent<{ kind: string; identityKey: string }>).detail);
    };
    window.addEventListener('entity:open', handler);
    try {
      render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
      fireEvent.click(screen.getByLabelText(/Cluster of 4 findings/i));
      fireEvent.click(screen.getByText(/g22/));
      expect(events[0]).toMatchObject({ kind: 'finding', identityKey: '22' });
      // Popover closed: g22 no longer findable.
      expect(screen.queryByText(/g22/)).toBeNull();
    } finally {
      window.removeEventListener('entity:open', handler);
    }
  });
});
```

(Re-uses the existing `bundle()` helper + `MemoryRouter` import from the existing test file. Add `fireEvent` to the existing `@testing-library/react` import if not already there.)

- [ ] **Step C1.2: Run, expect FAIL**

```bash
npm test -- --run src/components/investigations/CaseTimeline.test.tsx
```

Expected: FAIL — cluster rendering not yet implemented.

- [ ] **Step C1.3: Add the helpers + ClusterList in CaseTimeline.tsx**

Add these top-level helpers near the existing `SEV_FILL` / `RUN_FILL` constants in `CaseTimeline.tsx`:

```tsx
import { Tooltip } from '../Tooltip';
import { clusterEvents, type ClusterOrEvent } from './timeline/cluster';

const SEV_RANK: Record<string, number> = { CRITICAL: 5, HIGH: 4, MEDIUM: 3, LOW: 2, INFO: 1 };

function highestSeverityColor(events: TimelineEvent[]): string {
  let bestRank = 0;
  let best: string = SEV_FILL.INFO;
  for (const e of events) {
    if (e.kind !== 'finding') continue;
    const r = SEV_RANK[e.severity] ?? 0;
    if (r > bestRank) {
      bestRank = r;
      best = SEV_FILL[e.severity] ?? SEV_FILL.INFO;
    }
  }
  return best;
}

function tooltipFor(e: TimelineEvent): React.ReactNode {
  switch (e.kind) {
    case 'finding':
      return (
        <div className="space-y-0.5">
          <div className="flex items-center gap-1">
            <span className={`inline-block px-1 py-0.5 text-[10px] uppercase font-medium rounded ring-1 ${sevTone(e.severity)}`}>{e.severity}</span>
            <span className="font-mono">Finding #{e.id}</span>
          </div>
          <div className="font-medium">{e.title}</div>
          <div className="text-ink-mute">{e.agent} on {e.host}</div>
          <div className="text-ink-mute italic text-[10px]">Click to open drawer</div>
        </div>
      );
    case 'run': {
      const dur = formatDuration(e.startTs, e.endTs, e.running);
      return (
        <div className="space-y-0.5">
          <div className="flex items-center gap-1">
            <span className={`inline-block px-1 py-0.5 text-[10px] uppercase font-medium rounded ring-1 ${runTone(e.status)}`}>{e.status}</span>
            <span className="font-mono">Run #{e.id}</span>
          </div>
          <div className="font-medium">{e.orchestrationName}</div>
          <div className="text-ink-mute">{dur}</div>
        </div>
      );
    }
    case 'note':
      return (
        <div className="space-y-0.5">
          <div className="text-ink-mute text-[10px]">{e.author}</div>
          <div className="font-mono whitespace-pre-wrap line-clamp-4">{e.body}</div>
        </div>
      );
    case 'ioc': {
      const display = e.value.length > 32 ? `…${e.value.slice(-4)}` : e.value;
      return (
        <div className="space-y-0.5">
          <div className="font-mono">{e.iocKind}:{display}</div>
        </div>
      );
    }
    case 'daimon':
      return <div className="font-mono">{e.agent} → Finding #{e.findingID}</div>;
    case 'audit':
      return (
        <div className="space-y-0.5">
          <div className="text-ink-mute text-[10px]">{e.by}</div>
          <div>{e.auditKind}: {e.title}</div>
        </div>
      );
    case 'lifecycle':
      return <div>{e.title}</div>;
  }
}

function sevTone(sev: string): string {
  if (sev === 'CRITICAL') return 'text-red-700 bg-red-50 ring-red-200';
  if (sev === 'HIGH') return 'text-orange-700 bg-orange-50 ring-orange-200';
  if (sev === 'MEDIUM') return 'text-amber-700 bg-amber-50 ring-amber-200';
  if (sev === 'LOW') return 'text-blue-700 bg-blue-50 ring-blue-200';
  return 'text-slate-700 bg-slate-50 ring-slate-200';
}

function runTone(status: string): string {
  if (status === 'completed') return 'text-emerald-700 bg-emerald-50 ring-emerald-200';
  if (status === 'failed') return 'text-red-700 bg-red-50 ring-red-200';
  if (status === 'cancelled') return 'text-slate-700 bg-slate-50 ring-slate-200';
  return 'text-blue-700 bg-blue-50 ring-blue-200';
}

function formatDuration(startTs: number, endTs: number, running: boolean): string {
  if (running) return 'in progress';
  const ms = Math.max(0, endTs - startTs);
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function ClusterList({ events, onPick, cpInstanceID }: {
  events: TimelineEvent[];
  onPick: () => void;
  cpInstanceID?: string;
}) {
  function fire(kind: string, identityKey: string) {
    window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind, identityKey, cpInstanceID } }));
    onPick();
  }
  return (
    <ul className="divide-y divide-border min-w-[16rem]">
      {events.map((e, i) => {
        const { kind, key, label } = clusterRowDetails(e);
        const clickable = kind !== null;
        return (
          <li key={i} className="py-1.5">
            <button
              type="button"
              onClick={clickable ? () => fire(kind!, key!) : undefined}
              disabled={!clickable}
              className={`w-full text-left ${clickable ? 'hover:bg-slate-50 cursor-pointer' : 'cursor-default'} px-1`}
            >
              {label}
            </button>
          </li>
        );
      })}
    </ul>
  );
}

function clusterRowDetails(e: TimelineEvent): { kind: string | null; key: string | null; label: React.ReactNode } {
  switch (e.kind) {
    case 'finding': return { kind: 'finding', key: String(e.id), label: tooltipFor(e) };
    case 'note':    return { kind: null, key: null, label: tooltipFor(e) };
    case 'audit':   return { kind: null, key: null, label: tooltipFor(e) };
    case 'lifecycle': return { kind: null, key: null, label: tooltipFor(e) };
    case 'daimon':  return { kind: 'finding', key: String(e.findingID), label: tooltipFor(e) };
    default:        return { kind: null, key: null, label: tooltipFor(e) };
  }
}
```

**Update the existing import** at the top of `CaseTimeline.tsx` from:

```tsx
import { ALL_LANES, DEFAULT_LANES_ON, type TimelineLane } from './timeline/types';
```

To:

```tsx
import { ALL_LANES, DEFAULT_LANES_ON, type TimelineLane, type TimelineEvent } from './timeline/types';
```

- [ ] **Step C1.4: Rewrite EventsLayer**

Replace the existing `EventsLayer` function body with the cluster-aware version. The new function adds `useState<ClusterOrEvent | null>(null)` for `openCluster`, computes `clustered` via `clusterEvents`, and dispatches each entry through `renderSingle` or `renderCluster`. Replace lines 150 → end-of-EventsLayer with:

```tsx
function EventsLayer({ events, visibleLanes, range, laneLabelWidth, drawableWidth, cpInstanceID }: {
  events: ReturnType<typeof buildEvents>;
  visibleLanes: TimelineLane[];
  range: Range;
  laneLabelWidth: number;
  drawableWidth: number;
  cpInstanceID?: string;
}) {
  const laneIndex = (l: TimelineLane) => visibleLanes.indexOf(l);
  const x = (t: number) => laneLabelWidth + tToX(t, range.tMin, range.tMax, drawableWidth);
  const yMid = (l: TimelineLane) => laneIndex(l) * LANE_HEIGHT + LANE_HEIGHT / 2;

  const clustered = useMemo(
    () => clusterEvents(events, visibleLanes, (t) => tToX(t, range.tMin, range.tMax, drawableWidth)),
    [events, visibleLanes, range, drawableWidth],
  );

  const [openCluster, setOpenCluster] = useState<ClusterOrEvent | null>(null);

  // Close orphaned popover when re-clustering produces a different
  // set of cluster objects.
  useEffect(() => {
    if (openCluster && !clustered.includes(openCluster)) setOpenCluster(null);
  }, [clustered, openCluster]);

  function fire(kind: string, identityKey: string) {
    window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind, identityKey, cpInstanceID } }));
  }

  return (
    <g>
      {clustered.map((c, i) => c.kind === 'single'
        ? renderSingle(c.event, i, { x, yMid, laneIndex, fire })
        : renderCluster(c, i, { yMid, openCluster, setOpenCluster, cpInstanceID }))}
    </g>
  );
}

function renderSingle(
  e: TimelineEvent,
  i: number,
  ctx: {
    x: (t: number) => number;
    yMid: (l: TimelineLane) => number;
    laneIndex: (l: TimelineLane) => number;
    fire: (kind: string, identityKey: string) => void;
  },
): React.ReactNode {
  const { x, yMid, laneIndex, fire } = ctx;
  switch (e.kind) {
    case 'lifecycle': {
      if (laneIndex('lifecycle') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <text x={x(e.ts)} y={yMid('lifecycle') + 4} fontSize="14" textAnchor="middle" aria-label={e.title}>
            {e.marker === 'created' ? '⊕' : '⊗'}
          </text>
        </Tooltip>
      );
    }
    case 'finding': {
      if (laneIndex('findings') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <circle
            cx={x(e.ts)} cy={yMid('findings')} r={5}
            fill={SEV_FILL[e.severity] ?? SEV_FILL.INFO}
            role="button"
            aria-label={`Finding #${e.id}: ${e.severity} ${e.title} on ${e.host}`}
            tabIndex={0}
            style={{ cursor: 'pointer' }}
            onClick={() => fire('finding', String(e.id))}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('finding', String(e.id)); }}
          />
        </Tooltip>
      );
    }
    case 'run': {
      if (laneIndex('runs') < 0) return null;
      const x0 = x(e.startTs);
      const x1 = x(e.endTs);
      const w = Math.max(2, x1 - x0);
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <rect
            x={x0} y={yMid('runs') - 6} width={w} height={12} rx={2}
            fill={RUN_FILL[e.status] ?? RUN_FILL.cancelled}
            role="button"
            aria-label={`Run #${e.id}: ${e.status} (${e.orchestrationName})`}
            tabIndex={0}
            style={{ cursor: 'pointer', opacity: e.running ? 0.85 : 1 }}
            onClick={() => fire('run', String(e.id))}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('run', String(e.id)); }}
          />
        </Tooltip>
      );
    }
    case 'note': {
      if (laneIndex('notes') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <text x={x(e.ts)} y={yMid('notes') + 4} fontSize="12" textAnchor="middle" aria-label={`Note by ${e.author}`}>
            ✎
          </text>
        </Tooltip>
      );
    }
    case 'ioc': {
      if (laneIndex('iocs') < 0) return null;
      const x0 = x(e.startTs);
      const x1 = x(e.endTs);
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <rect
            x={x0} y={yMid('iocs') - 2} width={Math.max(2, x1 - x0)} height={4}
            fill="#a855f7"
            role="button"
            aria-label={`IOC ${e.iocKind}:${e.value.slice(-4)}`}
            tabIndex={0}
            style={{ cursor: 'pointer' }}
            onClick={() => fire('ioc', `${e.iocKind}:${e.value}`)}
            onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('ioc', `${e.iocKind}:${e.value}`); }}
          />
        </Tooltip>
      );
    }
    case 'daimon': {
      if (laneIndex('daimons') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <rect
            x={x(e.ts) - 2} y={yMid('daimons') - 2} width={4} height={4}
            fill="#6366f1"
            role="button"
            aria-label={`Daimon ${e.agent}`}
            tabIndex={0}
            style={{ cursor: 'pointer' }}
            onClick={() => fire('finding', String(e.findingID))}
          />
        </Tooltip>
      );
    }
    case 'audit': {
      if (laneIndex('audit') < 0) return null;
      return (
        <Tooltip key={i} content={tooltipFor(e)}>
          <text x={x(e.ts)} y={yMid('audit') + 4} fontSize="11" textAnchor="middle" fill="#64748b" aria-label={`Audit ${e.auditKind} by ${e.by}`}>
            ↗
          </text>
        </Tooltip>
      );
    }
  }
}

function renderCluster(
  c: { kind: 'cluster'; lane: TimelineLane; events: TimelineEvent[]; cx: number },
  i: number,
  ctx: {
    yMid: (l: TimelineLane) => number;
    openCluster: ClusterOrEvent | null;
    setOpenCluster: (c: ClusterOrEvent | null) => void;
    cpInstanceID?: string;
  },
): React.ReactNode {
  const { yMid, openCluster, setOpenCluster, cpInstanceID } = ctx;
  const isOpen = openCluster === c;
  const cy = yMid(c.lane);
  const fill = c.lane === 'findings' ? highestSeverityColor(c.events) : '#475569';
  const r = 9;
  return (
    <Tooltip
      key={i}
      open={isOpen}
      onOpenChange={(o) => setOpenCluster(o ? c : null)}
      content={<ClusterList events={c.events} onPick={() => setOpenCluster(null)} cpInstanceID={cpInstanceID} />}
    >
      <g
        role="button"
        aria-label={`Cluster of ${c.events.length} ${c.lane}`}
        tabIndex={0}
        style={{ cursor: 'pointer' }}
        onClick={() => setOpenCluster(c)}
        onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') setOpenCluster(c); }}
      >
        <circle cx={c.cx} cy={cy} r={r} fill={fill} />
        <text x={c.cx} y={cy + 3} fontSize="9" textAnchor="middle" fill="white" fontWeight="bold" pointerEvents="none">
          +{c.events.length}
        </text>
      </g>
    </Tooltip>
  );
}
```

Add `useMemo`, `useEffect`, `useState` to the existing React import line at the top of the file if any are missing.

Remove the now-unused inline `<title>` elements that were attached to the old per-shape returns (they're gone in `renderSingle`).

- [ ] **Step C1.5: Run + commit**

```bash
npm test -- --run src/components/investigations/CaseTimeline.test.tsx
node_modules/.bin/tsc -b
```

Expected: existing CaseTimeline tests still pass + 3 new cluster tests pass.

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-timeline-clusters
git add web/src/components/investigations/CaseTimeline.tsx \
        web/src/components/investigations/CaseTimeline.test.tsx
git commit -m "web(timeline): cluster overlapping point events; portal'd hover tooltip"
```

---

## Task Group Z — docs + sweep + PR body

### Task Z1: Architecture doc append

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

```markdown
## Investigation Overview — timeline collision avoidance + portal'd tooltip

Building on the layered Investigation Overview, the timeline component (`web/src/components/investigations/CaseTimeline.tsx`) clusters overlapping point events into `+N` glyphs and replaces SVG `<title>` hover tooltips with a portal'd styled `<div>`.

The clustering algorithm (`web/src/components/investigations/timeline/cluster.ts`) is a pure function that buckets point events on `(lane, x-bucket)` with a default 12px bucket width and 4-event threshold. Bars (runs / IOCs) skip clustering — their duration is the visual signal. Cluster click opens a popover (controlled `<Tooltip>`) listing each event with row-level click-through to the matching detail drawer.

The shared `<Tooltip>` component (`web/src/components/Tooltip.tsx`) is portal'd to `document.body` so it never gets clipped by SVG viewport or parent `overflow:hidden`. Two modes — uncontrolled hover/focus (replacing SVG `<title>` with instant `delay=0` appearance + styled rendering) and controlled (used as the cluster popover with outside-click + Escape dismissal). Reusable beyond the timeline.
```

```bash
git add docs/architecture.md
git commit -m "docs(architecture): timeline clusters + portal'd tooltip"
```

### Task Z2: Test sweep

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-timeline-clusters/web
node_modules/.bin/tsc -b
npm test -- --run 2>&1 | tail -8
```

All must pass. (No Go code touched in this PR.)

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-05-01-timeline-clusters-pr-body.md`

```markdown
## Summary

Follow-up #7 from the Investigation Overview backlog. Two improvements to the case timeline:

1. **Cluster overlapping point events.** When ≥4 finding/note/audit/lifecycle/daimon events fall in the same 12px-wide x-bucket on the same lane, render a single `+N` glyph instead of an unreadable smudge of dots. Click the glyph → controlled-mode `<Tooltip>` popover with one row per clustered event; click a row → opens that event's detail drawer (per-kind `entity:open`) and dismisses the popover. Bars (runs / IOCs) skip clustering — their duration carries the signal.

2. **Portal'd `<Tooltip>` replaces SVG `<title>`.** New shared component at `web/src/components/Tooltip.tsx`. Renders into `document.body` so it can't be clipped by SVG viewport or parent overflow. Uncontrolled mode (hover/focus, instant `delay=0`) replaces the existing SVG `<title>` text with styled multi-line content — severity chips, status pills, monospace IDs, click-to-open hints. Controlled mode (open/onOpenChange) drives the cluster popover.

Both pieces are reusable: the cluster algorithm is a pure function (`cluster.ts`), and the Tooltip is generic enough that other parts of the page can adopt it.

## Test plan

Unit tests in this PR:
- `timeline/cluster.test.ts` — empty input, below-threshold passthrough, at-threshold cluster, no cross-bucket merging, bars skip clustering, per-lane separation, sort order, lane visibility, custom thresholds (9 tests).
- `Tooltip.test.tsx` — uncontrolled show/hide on hover + focus, hover delay, portal target = `document.body`, controlled `open`, outside-click + Escape dismissal, hover suppression in controlled mode (8 tests).
- `CaseTimeline.test.tsx` — extends existing tests with cluster glyph rendering, cluster popover open + row-click dispatch + popover dismissal (3 new tests).

Manual lab smoke (post-merge):
- [ ] Open an investigation with ≥ 4 findings within a few minutes of each other → single cluster glyph at the bucket midpoint.
- [ ] Click cluster → popover lists each finding; row click opens FindingDrawer + popover dismisses.
- [ ] Hover an isolated finding → styled tooltip appears instantly (no native delay).
- [ ] Resize the page; tooltip + popover reposition correctly via the ResizeObserver/scroll listeners.
- [ ] Press Escape with the cluster popover open → dismisses.

## Files

**New:**
- `web/src/components/investigations/timeline/cluster.ts` (+ test)
- `web/src/components/Tooltip.tsx` (+ test)

**Modified:**
- `web/src/components/investigations/CaseTimeline.tsx` — `EventsLayer` rewrites, new helpers (`tooltipFor`, `highestSeverityColor`, `formatDuration`, `sevTone`/`runTone`, `ClusterList`).
- `docs/architecture.md` — new section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-timeline-clusters-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-timeline-clusters.md`

## Closes / refs

Implements item #7 from `investigation_overview_followups.md`. Six remaining backlog items unchanged.
```

```bash
git add docs/superpowers/plans/2026-05-01-timeline-clusters-pr-body.md
git commit -m "docs: timeline clusters PR body"
```

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.
