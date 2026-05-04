# Site Feature Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refresh okesu.to to cover April–May platform features — investigations workspace (timeline + graph + war-room + PDF), new daimons, selector-based fan-out, and a refreshed home page + tour.

**Architecture:** Static Astro 5 site under `site/`. New concept page (`investigations.mdx`) plus 5 new CSS-only animated demos under `site/src/components/diagrams/`. Existing pages get targeted edits. The tour grows from 6 → 10 screens. A new seed script drives a reproducible demo state on the local CP, screenshots are captured via Playwright. The site has no test runner; verification is `npm run dev` + visual eyeball + `npm run build`. Each task ends with a commit.

**Tech Stack:** Astro 5, Tailwind 3, MDX, vanilla CSS keyframes (no JS animation), Playwright (capture only), `sharp` (WebP optimization).

---

## File Map

```
NEW
  site/src/pages/concepts/investigations.mdx
  site/src/components/diagrams/TimelineLaneDemo.astro
  site/src/components/diagrams/GraphFanDemo.astro
  site/src/components/diagrams/WarRoomCursorsDemo.astro
  site/src/components/diagrams/PDFReportDemo.astro
  site/src/components/diagrams/SelectorFanoutDemo.astro
  site/src/data/recipes/t2-medium-noise-triage.yaml
  site/scripts/seed-investigation-demo.mjs

MODIFY
  site/src/styles/animations.css            (+3 keyframes)
  site/src/components/Nav.astro             (+ Investigations link, but Concepts hub already covers it)
  site/src/pages/concepts/agents.mdx        (LLM keys + RBAC bindings sentence)
  site/src/pages/concepts/daimons.mdx       (+ log-sigma-hunter, IOC feeds note)
  site/src/pages/concepts/orchestrations.mdx (+ Selectors & labels section)
  site/src/pages/daimons.astro              (+ Sigma daimon section)
  site/src/pages/recipes.astro              (+ t2-medium-noise-triage entry)
  site/src/components/ConceptCard.astro     (4-up sizing tweak)
  site/src/components/FeatureGrid.astro     (rewrite Investigations entry, add Real-time war room)
  site/src/pages/index.astro                (4-up grid, + Investigations card)
  site/src/pages/tour.astro                 (+ screens 7-10)
  site/scripts/capture-screenshots.mjs      (+ 6 investigation captures)
  site/scripts/capture-tour.mjs             (+ 4 tail captures, 2-context war-room)

NEW SCREENSHOT ASSETS (produced by capture scripts)
  site/public/screenshots/investigation-overview.{png,webp}
  site/public/screenshots/investigation-timeline.{png,webp}
  site/public/screenshots/investigation-graph.{png,webp}
  site/public/screenshots/investigation-war-room.{png,webp}
  site/public/screenshots/investigation-war-room-thumb.{png,webp}
  site/public/screenshots/investigation-pdf-page1.{png,webp}
  site/public/screenshots/tour-investigation-timeline.{png,webp}
  site/public/screenshots/tour-investigation-graph.{png,webp}
  site/public/screenshots/tour-investigation-war-room.{png,webp}
  site/public/screenshots/tour-investigation-pdf.{png,webp}
```

---

## Task 1: Extend `animations.css` with new keyframes

**Files:**
- Modify: `site/src/styles/animations.css`

- [ ] **Step 1: Append new keyframes**

Open `site/src/styles/animations.css`. Inside the existing `@media (prefers-reduced-motion: no-preference)` block, before the closing `}`, append:

```css
  /* Slide a row of dots/bars in left-to-right — used on timeline lanes. */
  @keyframes okesu-lane-slide {
    0%, 5%   { transform: translateX(-12px); opacity: 0; }
    20%, 90% { transform: translateX(0);     opacity: 1; }
    95%, 100% { transform: translateX(-12px); opacity: 0; }
  }

  /* 1-second blink for a faux text cursor. */
  @keyframes okesu-cursor-blink {
    0%, 49%   { opacity: 1; }
    50%, 100% { opacity: 0; }
  }

  /* Selector pill resolves into N chips — used for SelectorFanoutDemo. */
  @keyframes okesu-chip-resolve {
    0%, 10%  { opacity: 0; transform: scale(0.8); }
    25%, 90% { opacity: 1; transform: scale(1); }
    95%, 100% { opacity: 0; transform: scale(0.8); }
  }
```

- [ ] **Step 2: Verify Astro builds**

Run from `site/`:
```bash
npm run build
```
Expected: build succeeds, `dist/` populated, no warnings about unknown CSS.

- [ ] **Step 3: Commit**

```bash
git add site/src/styles/animations.css
git commit -m "$(cat <<'EOF'
feat(site): add lane-slide, cursor-blink, chip-resolve keyframes

Foundation keyframes for upcoming TimelineLaneDemo, WarRoomCursorsDemo,
and SelectorFanoutDemo components.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: TimelineLaneDemo

**Files:**
- Create: `site/src/components/diagrams/TimelineLaneDemo.astro`

- [ ] **Step 1: Create the component**

```astro
---
// Investigations / Timeline. 8-second loop:
//   three horizontal lanes (findings dots, runs bars, IOC dots) slide
//   in left-to-right; a "filter chip" cycles between all/findings/runs
//   to dim the un-selected lanes.
---

<div class="bg-panel border border-border rounded-lg shadow-card p-4 max-w-md" role="img"
     aria-label="Animated demo: a case timeline with three lanes — findings, runs, IOCs. Dots and bars slide in left-to-right.">
  <div class="flex gap-1.5 mb-3">
    <span class="filter-chip filter-chip-all">all</span>
    <span class="filter-chip filter-chip-findings">findings</span>
    <span class="filter-chip filter-chip-runs">runs</span>
    <span class="filter-chip filter-chip-iocs">IOCs</span>
  </div>

  <div class="space-y-2">
    <div class="lane lane-findings">
      <span class="lane-label">findings</span>
      <div class="lane-track">
        <span class="lane-dot lane-dot-1 bg-rose-500"></span>
        <span class="lane-dot lane-dot-2 bg-amber-500"></span>
        <span class="lane-dot lane-dot-3 bg-rose-500"></span>
        <span class="lane-dot lane-dot-4 bg-amber-500"></span>
        <span class="lane-dot lane-dot-5 bg-rose-500"></span>
      </div>
    </div>
    <div class="lane lane-runs">
      <span class="lane-label">runs</span>
      <div class="lane-track">
        <span class="lane-bar lane-bar-1"></span>
        <span class="lane-bar lane-bar-2"></span>
      </div>
    </div>
    <div class="lane lane-iocs">
      <span class="lane-label">IOCs</span>
      <div class="lane-track">
        <span class="lane-dot lane-dot-6 bg-violet-500"></span>
        <span class="lane-dot lane-dot-7 bg-violet-500"></span>
        <span class="lane-dot lane-dot-8 bg-violet-500"></span>
      </div>
    </div>
  </div>
</div>

<style>
  .filter-chip { font-size: 10px; padding: 2px 7px; border-radius: 9999px; background: #f1f5f9; color: #64748b; border: 1px solid #e2e8f0; }
  .filter-chip-all { background: #ede9fe; color: #5b21b6; border-color: #ddd6fe; }

  .lane { display: flex; align-items: center; gap: 8px; }
  .lane-label { font-size: 10px; color: #94a3b8; width: 56px; flex-shrink: 0; text-align: right; }
  .lane-track { flex: 1; position: relative; height: 14px; display: flex; align-items: center; gap: 6px; padding-left: 4px; }

  .lane-dot { width: 8px; height: 8px; border-radius: 9999px; opacity: 0; }
  .lane-bar { height: 6px; border-radius: 3px; background: #3b82f6; opacity: 0; }
  .lane-bar-1 { width: 60px; }
  .lane-bar-2 { width: 90px; margin-left: 16px; background: #22c55e; }

  /* Static fallback for reduced-motion: show everything at full opacity. */
  @media (prefers-reduced-motion: reduce) {
    .lane-dot, .lane-bar { opacity: 1; }
  }

  @media (prefers-reduced-motion: no-preference) {
    .lane-dot-1 { animation: okesu-lane-slide 8s ease-in-out 0.2s infinite; }
    .lane-dot-2 { animation: okesu-lane-slide 8s ease-in-out 0.6s infinite; }
    .lane-dot-3 { animation: okesu-lane-slide 8s ease-in-out 1.0s infinite; }
    .lane-dot-4 { animation: okesu-lane-slide 8s ease-in-out 1.4s infinite; }
    .lane-dot-5 { animation: okesu-lane-slide 8s ease-in-out 1.8s infinite; }
    .lane-bar-1 { animation: okesu-lane-slide 8s ease-in-out 0.8s infinite; }
    .lane-bar-2 { animation: okesu-lane-slide 8s ease-in-out 1.6s infinite; }
    .lane-dot-6 { animation: okesu-lane-slide 8s ease-in-out 0.4s infinite; }
    .lane-dot-7 { animation: okesu-lane-slide 8s ease-in-out 1.0s infinite; }
    .lane-dot-8 { animation: okesu-lane-slide 8s ease-in-out 1.6s infinite; }

    /* Filter cycle: 0-3s "all", 3-5s "findings" (dim runs+iocs), 5-7s "runs" (dim findings+iocs), 7-8s "all". */
    .lane-runs, .lane-iocs { animation: filter-dim-runs-iocs 8s ease-in-out infinite; }
    .lane-findings, .lane-iocs { animation: filter-dim-findings-iocs 8s ease-in-out infinite; }

    @keyframes filter-dim-runs-iocs {
      0%, 37%, 88%, 100% { opacity: 1; }
      40%, 60% { opacity: 0.25; }
    }
    @keyframes filter-dim-findings-iocs {
      0%, 62%, 88%, 100% { opacity: 1; }
      65%, 85% { opacity: 0.25; }
    }
  }
</style>
```

- [ ] **Step 2: Verify build**

```bash
npm run build
```
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/diagrams/TimelineLaneDemo.astro
git commit -m "$(cat <<'EOF'
feat(site): TimelineLaneDemo — case timeline with filter cycle

Three lanes (findings, runs, IOCs) slide in left-to-right; filter
chips dim the un-selected lanes on a cycle.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: GraphFanDemo

**Files:**
- Create: `site/src/components/diagrams/GraphFanDemo.astro`

- [ ] **Step 1: Create the component**

```astro
---
// Investigations / Graph. 6-second loop:
//   bipartite layout — 4 hosts (left) and 5 IOCs (right). Edges fade
//   in one-by-one via stroke-dashoffset; final state has every edge
//   drawn so the relationship topology is legible.
---

<div class="bg-panel border border-border rounded-lg shadow-card p-4 max-w-md" role="img"
     aria-label="Animated demo: bipartite graph — four hosts on the left, five IOCs on the right. Edges fade in one by one to reveal the relationship topology.">
  <div class="flex justify-between mb-2 text-[10px] text-ink-mute font-mono">
    <span>HOSTS</span>
    <span>IOCs</span>
  </div>
  <svg viewBox="0 0 320 200" class="w-full">
    <!-- Edges first so they sit beneath nodes -->
    <line class="edge edge-1" x1="48"  y1="20"  x2="272" y2="20"/>
    <line class="edge edge-2" x1="48"  y1="20"  x2="272" y2="60"/>
    <line class="edge edge-3" x1="48"  y1="65"  x2="272" y2="20"/>
    <line class="edge edge-4" x1="48"  y1="65"  x2="272" y2="100"/>
    <line class="edge edge-5" x1="48"  y1="110" x2="272" y2="60"/>
    <line class="edge edge-6" x1="48"  y1="110" x2="272" y2="140"/>
    <line class="edge edge-7" x1="48"  y1="155" x2="272" y2="100"/>
    <line class="edge edge-8" x1="48"  y1="155" x2="272" y2="180"/>

    <!-- Host nodes -->
    <g class="nodes-host">
      <circle cx="40" cy="20"  r="6"/>
      <circle cx="40" cy="65"  r="6"/>
      <circle cx="40" cy="110" r="6"/>
      <circle cx="40" cy="155" r="6"/>
      <text x="20" y="24"  class="node-label">h1</text>
      <text x="20" y="69"  class="node-label">h2</text>
      <text x="20" y="114" class="node-label">h3</text>
      <text x="20" y="159" class="node-label">h4</text>
    </g>

    <!-- IOC nodes -->
    <g class="nodes-ioc">
      <rect x="274" y="14"  width="12" height="12" rx="2"/>
      <rect x="274" y="54"  width="12" height="12" rx="2"/>
      <rect x="274" y="94"  width="12" height="12" rx="2"/>
      <rect x="274" y="134" width="12" height="12" rx="2"/>
      <rect x="274" y="174" width="12" height="12" rx="2"/>
    </g>
  </svg>
  <div class="text-[10px] text-ink-mute mt-1">4 hosts share 8 relationships with 5 IOCs</div>
</div>

<style>
  .edge { stroke: #94a3b8; stroke-width: 1.2; stroke-dasharray: 280; stroke-dashoffset: 280; }
  .nodes-host circle { fill: #5b21b6; }
  .nodes-ioc rect    { fill: #f59e0b; }
  .node-label        { font-size: 9px; fill: #64748b; font-family: ui-monospace, monospace; }

  @media (prefers-reduced-motion: reduce) {
    .edge { stroke-dashoffset: 0; }
  }

  @media (prefers-reduced-motion: no-preference) {
    .edge-1 { animation: edge-draw 6s ease-out 0.0s infinite; }
    .edge-2 { animation: edge-draw 6s ease-out 0.4s infinite; }
    .edge-3 { animation: edge-draw 6s ease-out 0.8s infinite; }
    .edge-4 { animation: edge-draw 6s ease-out 1.2s infinite; }
    .edge-5 { animation: edge-draw 6s ease-out 1.6s infinite; }
    .edge-6 { animation: edge-draw 6s ease-out 2.0s infinite; }
    .edge-7 { animation: edge-draw 6s ease-out 2.4s infinite; }
    .edge-8 { animation: edge-draw 6s ease-out 2.8s infinite; }

    @keyframes edge-draw {
      0%, 5%    { stroke-dashoffset: 280; }
      40%, 90%  { stroke-dashoffset: 0; }
      95%, 100% { stroke-dashoffset: 280; }
    }
  }
</style>
```

- [ ] **Step 2: Verify build**

```bash
npm run build
```
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/diagrams/GraphFanDemo.astro
git commit -m "$(cat <<'EOF'
feat(site): GraphFanDemo — bipartite host↔IOC relationship graph

Pure-SVG component, edges fade in via stroke-dashoffset to teach the
topology of the graph view.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: WarRoomCursorsDemo

**Files:**
- Create: `site/src/components/diagrams/WarRoomCursorsDemo.astro`

- [ ] **Step 1: Create the component**

```astro
---
// Investigations / War room. 10-second loop:
//   Faux textarea bound to a shared draft. Two presence chips visible.
//   Operator A types the first line; operator B inserts a follow-up.
//   At the end, "Send →" pulses to suggest the buffer becomes one
//   immutable note row. Cursors blink in their respective colors.
---

<div class="bg-panel border border-border rounded-lg shadow-card p-4 max-w-md" role="img"
     aria-label="Animated demo: a war-room draft panel with two operators editing the same buffer in real time. Two presence chips and two coloured cursors are visible.">
  <div class="flex items-center gap-1.5 mb-2">
    <span class="presence-chip presence-chip-a">A</span>
    <span class="presence-chip presence-chip-b">B</span>
    <span class="text-[10px] text-ink-mute ml-auto">2 editing · auto-saving</span>
  </div>

  <div class="war-room-text">
    <div class="war-room-line">
      <span class="war-room-typed-a">callback to 8.8.8.8 from web-prod-01 — netflow confirms</span>
      <span class="war-room-cursor war-room-cursor-a"></span>
    </div>
    <div class="war-room-line war-room-line-b">
      <span class="war-room-typed-b">EDR shows pid 4421 spawned by sshd — pulling memory snapshot now</span>
      <span class="war-room-cursor war-room-cursor-b"></span>
    </div>
  </div>

  <div class="flex gap-2 mt-3">
    <button class="war-room-send" type="button" disabled>Send →</button>
    <button class="war-room-cancel" type="button" disabled>Cancel</button>
  </div>
</div>

<style>
  .presence-chip {
    font-size: 10px; width: 18px; height: 18px; border-radius: 9999px;
    display: inline-flex; align-items: center; justify-content: center;
    color: white; font-weight: 600; font-family: ui-monospace, monospace;
  }
  .presence-chip-a { background: #5b21b6; }
  .presence-chip-b { background: #0ea5e9; }

  .war-room-text {
    background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 6px;
    padding: 10px 12px; min-height: 80px; font-size: 12px; line-height: 1.55;
    font-family: ui-monospace, monospace; color: #334155;
  }
  .war-room-line { display: block; min-height: 18px; position: relative; }
  .war-room-line-b { color: #0ea5e9; }

  .war-room-typed-a, .war-room-typed-b {
    display: inline-block; overflow: hidden; white-space: nowrap;
    vertical-align: bottom; max-width: 100%;
  }

  .war-room-cursor {
    display: inline-block; width: 1.5px; height: 14px; vertical-align: text-bottom;
    margin-left: 1px;
  }
  .war-room-cursor-a { background: #5b21b6; }
  .war-room-cursor-b { background: #0ea5e9; }

  .war-room-send, .war-room-cancel {
    font-size: 11px; padding: 4px 10px; border-radius: 4px; cursor: not-allowed;
  }
  .war-room-send { background: #5b21b6; color: white; opacity: 0.5; }
  .war-room-cancel { background: white; color: #64748b; border: 1px solid #e2e8f0; }

  @media (prefers-reduced-motion: reduce) {
    .war-room-typed-a, .war-room-typed-b { width: 100%; }
  }

  @media (prefers-reduced-motion: no-preference) {
    .war-room-typed-a { width: 0; animation: type-a 10s steps(48, end) infinite; }
    .war-room-typed-b { width: 0; animation: type-b 10s steps(56, end) infinite; }
    .war-room-cursor-a { animation: okesu-cursor-blink 1s steps(2, end) infinite; }
    .war-room-cursor-b { animation: okesu-cursor-blink 1s steps(2, end) 0.5s infinite; }
    .war-room-send { animation: send-pulse 10s ease-in-out infinite; }

    @keyframes type-a {
      0%, 5%   { width: 0; }
      35%, 95% { width: 100%; }
      100%     { width: 0; }
    }
    @keyframes type-b {
      0%, 35%  { width: 0; }
      65%, 95% { width: 100%; }
      100%     { width: 0; }
    }
    @keyframes send-pulse {
      0%, 80%  { opacity: 0.5; }
      85%, 95% { opacity: 1; box-shadow: 0 0 0 3px rgba(91, 33, 182, 0.3); }
      100%     { opacity: 0.5; }
    }
  }
</style>
```

- [ ] **Step 2: Verify build**

```bash
npm run build
```
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/diagrams/WarRoomCursorsDemo.astro
git commit -m "$(cat <<'EOF'
feat(site): WarRoomCursorsDemo — two-operator collaborative draft

CSS-only animation: operator A types, operator B inserts follow-up,
Send button pulses. Two colored cursors and presence chips communicate
the Yjs/CRDT real-time editing concept.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: PDFReportDemo

**Files:**
- Create: `site/src/components/diagrams/PDFReportDemo.astro`

- [ ] **Step 1: Create the component**

```astro
---
// Investigations / PDF report. 4-second loop:
//   stack of three faux page outlines + a download icon that pulses.
//   Compact — meant for an inline section diagram, not the section's
//   centerpiece (the screenshot does that job).
---

<div class="bg-panel border border-border rounded-lg shadow-card p-4 max-w-xs" role="img"
     aria-label="Animated demo: a stack of three PDF page outlines with a pulsing download icon, indicating one-click report export.">
  <div class="flex items-center gap-3">
    <div class="pdf-stack relative w-16 h-20">
      <div class="pdf-page pdf-page-3"></div>
      <div class="pdf-page pdf-page-2"></div>
      <div class="pdf-page pdf-page-1">
        <div class="pdf-line w-8"></div>
        <div class="pdf-line w-10"></div>
        <div class="pdf-line w-7"></div>
      </div>
    </div>
    <div class="flex-1">
      <div class="text-[12px] font-semibold text-ink">incident-31.pdf</div>
      <div class="text-[10px] text-ink-mute mb-2">12 pages · 240 KB</div>
      <button class="pdf-dl-btn" type="button" disabled>
        <span aria-hidden="true">↓</span> Download
      </button>
    </div>
  </div>
</div>

<style>
  .pdf-page {
    position: absolute; width: 56px; height: 72px; background: white;
    border: 1px solid #e2e8f0; border-radius: 3px; padding: 6px;
  }
  .pdf-page-1 { top: 0; left: 0; z-index: 3; }
  .pdf-page-2 { top: 4px; left: 4px; z-index: 2; opacity: 0.7; }
  .pdf-page-3 { top: 8px; left: 8px; z-index: 1; opacity: 0.4; }

  .pdf-line { height: 2px; background: #cbd5e1; border-radius: 2px; margin-bottom: 3px; }

  .pdf-dl-btn {
    font-size: 10px; padding: 3px 8px; border-radius: 4px;
    background: #5b21b6; color: white; cursor: not-allowed;
  }

  @media (prefers-reduced-motion: no-preference) {
    .pdf-dl-btn { animation: dl-pulse 4s ease-in-out infinite; }
    @keyframes dl-pulse {
      0%, 70%   { box-shadow: 0 0 0 0 rgba(91, 33, 182, 0); }
      80%       { box-shadow: 0 0 0 4px rgba(91, 33, 182, 0.4); }
      90%, 100% { box-shadow: 0 0 0 0 rgba(91, 33, 182, 0); }
    }
  }
</style>
```

- [ ] **Step 2: Verify build**

```bash
npm run build
```
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/diagrams/PDFReportDemo.astro
git commit -m "$(cat <<'EOF'
feat(site): PDFReportDemo — small stacked-page diagram for PDF section

Compact inline diagram suggesting one-click PDF export.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: SelectorFanoutDemo

**Files:**
- Create: `site/src/components/diagrams/SelectorFanoutDemo.astro`

- [ ] **Step 1: Create the component**

```astro
---
// Orchestrations / Selectors. 8-second loop:
//   selector pill `label:env=prod` shows briefly, then 5 host chips
//   pop into existence with a "matching…" hint, then a fan-out strip
//   below dispatches across them.
---

<div class="bg-panel border border-border rounded-lg shadow-card p-4 max-w-md" role="img"
     aria-label="Animated demo: a label selector resolves into 5 host chips and a fan-out strip dispatches across them.">
  <div class="flex items-center gap-2 mb-3">
    <span class="text-[10px] uppercase tracking-wide text-ink-mute font-bold">selector</span>
    <span class="selector-pill">label:env=prod</span>
    <span class="selector-arrow">→</span>
    <span class="selector-count">5 hosts</span>
  </div>

  <div class="flex flex-wrap gap-1 mb-3">
    <span class="host-chip host-chip-1">web-01</span>
    <span class="host-chip host-chip-2">web-02</span>
    <span class="host-chip host-chip-3">api-01</span>
    <span class="host-chip host-chip-4">api-02</span>
    <span class="host-chip host-chip-5">api-03</span>
  </div>

  <div class="border-t border-border pt-2">
    <div class="flex items-center gap-2">
      <div class="flex-1 h-1.5 bg-slate-200 rounded overflow-hidden">
        <div class="sel-hist-bar h-full bg-green-500"></div>
      </div>
      <span class="sel-strip-count text-[10px] font-mono text-ink-dim">0/5</span>
    </div>
    <div class="flex gap-1 mt-1.5">
      <span class="sel-dot sel-dot-1 w-2 h-2 rounded-full bg-slate-300"></span>
      <span class="sel-dot sel-dot-2 w-2 h-2 rounded-full bg-slate-300"></span>
      <span class="sel-dot sel-dot-3 w-2 h-2 rounded-full bg-slate-300"></span>
      <span class="sel-dot sel-dot-4 w-2 h-2 rounded-full bg-slate-300"></span>
      <span class="sel-dot sel-dot-5 w-2 h-2 rounded-full bg-slate-300"></span>
    </div>
  </div>
</div>

<style>
  .selector-pill {
    font-size: 11px; padding: 2px 8px; border-radius: 4px;
    background: #ede9fe; color: #5b21b6; font-family: ui-monospace, monospace;
    border: 1px solid #ddd6fe;
  }
  .selector-arrow { color: #94a3b8; font-size: 12px; }
  .selector-count { font-size: 10px; color: #64748b; }

  .host-chip {
    font-size: 10px; padding: 2px 6px; border-radius: 4px;
    background: #f1f5f9; color: #334155; opacity: 0;
  }

  .sel-hist-bar { width: 0%; }

  @media (prefers-reduced-motion: reduce) {
    .host-chip { opacity: 1; }
    .sel-hist-bar { width: 100%; }
    .sel-dot { background: #22c55e; }
  }

  @media (prefers-reduced-motion: no-preference) {
    .host-chip-1 { animation: okesu-chip-resolve 8s ease-in-out 0.6s infinite; }
    .host-chip-2 { animation: okesu-chip-resolve 8s ease-in-out 0.9s infinite; }
    .host-chip-3 { animation: okesu-chip-resolve 8s ease-in-out 1.2s infinite; }
    .host-chip-4 { animation: okesu-chip-resolve 8s ease-in-out 1.5s infinite; }
    .host-chip-5 { animation: okesu-chip-resolve 8s ease-in-out 1.8s infinite; }

    .sel-hist-bar { animation: sel-hist-grow 8s linear infinite; }
    @keyframes sel-hist-grow {
      0%, 25%  { width: 0; }
      85%, 95% { width: 100%; }
      100%     { width: 0; }
    }

    .sel-dot-1 { animation: sel-dot-fill 8s linear 2.6s infinite; }
    .sel-dot-2 { animation: sel-dot-fill 8s linear 3.0s infinite; }
    .sel-dot-3 { animation: sel-dot-fill 8s linear 3.4s infinite; }
    .sel-dot-4 { animation: sel-dot-fill 8s linear 3.8s infinite; }
    .sel-dot-5 { animation: sel-dot-fill 8s linear 4.2s infinite; }
    @keyframes sel-dot-fill {
      0%, 5%   { background-color: #cbd5e1; }
      6%, 90%  { background-color: #22c55e; }
      95%, 100% { background-color: #cbd5e1; }
    }
  }
</style>
```

- [ ] **Step 2: Verify build**

```bash
npm run build
```
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/diagrams/SelectorFanoutDemo.astro
git commit -m "$(cat <<'EOF'
feat(site): SelectorFanoutDemo — label selector → host chips → fan-out

Teaches the nodes_selector concept: a label expression resolves to
N hosts, then dispatches with the standard fan-out strip beneath.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Investigations concept page + Nav entry

**Files:**
- Create: `site/src/pages/concepts/investigations.mdx`
- Modify: `site/src/components/Nav.astro` (add `/concepts/investigations` to active prefix matching — already covers via `/concepts` prefix; one-line change to add a direct link not strictly required)

- [ ] **Step 1: Create `investigations.mdx`**

```mdx
---
layout: ../../layouts/Concept.astro
title: Investigations
subtitle: The case-file layer. A workspace that groups findings, IOCs, runs, and notes into one closed loop per incident.
glyph: ◈
description: An investigation is the case file Okesu builds around an incident — a timeline, a graph view, a real-time war-room for notes, and a one-click PDF report.
toc:
  - { id: what,        label: "What an investigation is" }
  - { id: timeline,    label: "Timeline" }
  - { id: graph,       label: "Graph view" }
  - { id: war-room,    label: "War room" }
  - { id: pdf,         label: "PDF report" }
  - { id: next,        label: "Where to next" }
---

import TimelineLaneDemo from '../../components/diagrams/TimelineLaneDemo.astro';
import GraphFanDemo from '../../components/diagrams/GraphFanDemo.astro';
import WarRoomCursorsDemo from '../../components/diagrams/WarRoomCursorsDemo.astro';
import PDFReportDemo from '../../components/diagrams/PDFReportDemo.astro';
import OperatorView from '../../components/OperatorView.astro';

<div class="bg-bg border border-border rounded-xl p-6 md:p-8 my-8">
  <TimelineLaneDemo />
</div>

<h2 id="what">What an investigation is</h2>

<p>An <span class="term">investigation</span> is the case file Okesu builds around an incident. It groups the findings that triggered the case, the IOCs they reference, the orchestration runs you spent investigating, the daimons that reported in, and the operators' notes — one closed loop per incident.</p>

<p>The point is replayability. Six months later you can open the case and see exactly what happened, who did what when, and which evidence was used to close it. Investigations exist because findings alone aren't enough: a critical alert is the start of a story, not the end.</p>

<div class="my-6">
  <OperatorView
    src="investigation-overview"
    alt="Investigation detail in the Okesu Control Plane — header with status, summary cards (hosts/IOCs/daimons/runs), and the eight-tab navigation across the top."
    url="okesu / Investigations / #31"
    caption="The case detail header — status, summary cards, and the eight tabs (Overview, Timeline, Graph, Findings, Runs, IOCs, Daimons, Notes, Audit)."
  />
</div>

<h2 id="timeline">Timeline</h2>

<p>The Timeline tab is a horizontal lane view of everything that's happened in the case. Findings dots, run bars, IOC markers, daimon ticks, and notes — all drawn against the same time axis. A filter bar at the top lets you scope by severity, run-status, host, or agent. Saved searches let you stash a particular slice and come back to it.</p>

<p>For long-running cases, an outlier-resistant <em>autoFit</em> keeps the most active cluster of events visible without zooming you out to a useless bird's-eye view.</p>

<div class="my-6">
  <OperatorView
    src="investigation-timeline"
    alt="Investigation timeline — three lanes (findings, runs, IOCs) drawn against a time axis with filter chips and a search bar above."
    url="okesu / Investigations / #31 / Timeline"
    caption="The timeline view, filtered to severity ≥ HIGH and run-status = approved."
  />
</div>

<h2 id="graph">Graph view</h2>

<p>The Graph tab renders the case's relationships as a bipartite layout — hosts on one side, IOCs on the other, edges showing which host saw which IOC. Hover an edge to see when and how the relationship was established (which finding observed it, which run confirmed it).</p>

<p>The data comes from a server-side aggregation endpoint (<code>/api/investigations/&#123;id&#125;/structure</code>) — the CP rolls up host/IOC/daimon/run counts so the frontend doesn't have to walk the entity graph itself.</p>

<div class="my-6">
  <GraphFanDemo />
</div>

<div class="my-6">
  <OperatorView
    src="investigation-graph"
    alt="Investigation graph view — bipartite layout, four hosts on the left, five IOCs on the right, edges drawn between them."
    url="okesu / Investigations / #31 / Graph"
    caption="The graph view — hosts on the left, IOCs on the right, relationships as edges."
  />
</div>

<h2 id="war-room">War room</h2>

<p>When two or more operators are working a live incident, the <span class="term">war room</span> gives them a shared draft buffer to think out loud together. Open the war room from the case detail page; everyone connected sees each other's cursors, types into the same textarea, and reads each other's contributions in real time.</p>

<p>Under the hood it's a <a href="https://yjs.dev" target="_blank" rel="noopener">Yjs</a> CRDT — operations are eventually consistent across all clients with no central serialiser, so a brief network blip doesn't lose anyone's input. The CP runs a thin <em>relay</em> that forwards Yjs sync messages between connected clients and persists the snapshot every five seconds.</p>

<p>When the team's done thinking, one operator hits <strong>Send</strong> and the current draft becomes one immutable <code>InvestigationNote</code> row — preserving the existing audit chain. The simple-note path stays available for quick comments; the war room is the right tool when multiple people need to think together.</p>

<div class="my-6">
  <WarRoomCursorsDemo />
</div>

<div class="my-6">
  <OperatorView
    src="investigation-war-room"
    alt="War room panel mid-edit — two presence chips visible, partial draft text in the textarea, two coloured cursors."
    url="okesu / Investigations / #31 / Notes"
    caption="A war-room session in flight: two operators editing the same buffer, presence chips up top, Send produces one note row."
  />
</div>

<h2 id="pdf">PDF report</h2>

<p>One click, one PDF. The report stitches together the case's executive summary, the chronological narrative, every linked finding/IOC/run/audit-event, and a final reference table — formatted for the kind of stakeholder who'll never log into Okesu.</p>

<p>The report endpoint is federated: a parent CP serves a report for a child's case by proxying the request through the federation token. A consistent header/footer frame and page numbering keep the document stakeholder-ready.</p>

<div class="my-6">
  <PDFReportDemo />
</div>

<div class="my-6">
  <OperatorView
    src="investigation-pdf-page1"
    alt="Page one of an investigation PDF report — executive summary, status, host counts, finding counts."
    url="okesu / Investigations / #31 / Export"
    caption="Page one of the export — executive summary plus key counts."
  />
</div>

<h2 id="next">Where to next</h2>

<ul>
  <li><a href="/concepts/orchestrations">Orchestrations</a> — runs hang off cases; an orchestration's run-detail links back to its case.</li>
  <li><a href="/concepts/daimons">Daimons</a> — continuous monitoring is what produces the findings cases get built around.</li>
  <li><a href="/recipes">Recipes</a> — every recipe on the playbook list is the kind of run that ends up linked to an investigation.</li>
</ul>
```

- [ ] **Step 2: Verify build + view**

```bash
cd site
npm run dev
```
Open `http://localhost:4321/concepts/investigations` in a browser. Verify:
- All four animated demos render and animate
- All four `<OperatorView>` blocks render (broken-image OK for now — screenshots come later)
- TOC sidebar shows the 6 sections
- Brand glyph `◈` shows in the heading

Stop the dev server.

- [ ] **Step 3: Commit**

```bash
git add site/src/pages/concepts/investigations.mdx
git commit -m "$(cat <<'EOF'
feat(site): investigations concept page

New /concepts/investigations covering case-file model, timeline,
graph, war-room (Yjs CRDT), and PDF report. Four inline animated
demos plus four screenshot placeholders (filled by capture pass).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Edits to existing concept pages (agents + daimons)

**Files:**
- Modify: `site/src/pages/concepts/agents.mdx`
- Modify: `site/src/pages/concepts/daimons.mdx`

- [ ] **Step 1: Edit `agents.mdx`**

In `site/src/pages/concepts/agents.mdx`, find the section with id `in-platform` (search for `<h2 id="in-platform">`). After the existing first paragraph in that section, insert a new paragraph:

```mdx
<p>Agents run with <strong>per-fleet LLM credentials</strong> — the CP threads provider keys (Claude / Codex / others) into <code>/etc/okesu/jobs.env</code> on each daimon at deploy time, so the agent picks them up without per-host secret juggling. <strong>RBAC credential bindings</strong> let an orchestration step request scoped, time-boxed credentials from the CP at dispatch — useful for steps that hit cloud APIs or other production systems where you want a paper trail.</p>
```

- [ ] **Step 2: Edit `daimons.mdx`**

In `site/src/pages/concepts/daimons.mdx`, find the `built-in agents` mention in the `<h2 id="what">` section (line ~37). The current sentence ends with `(<code>edr</code>, <code>instance-integrity</code>, <code>instance-threat</code>, <code>sre-health</code>)`. Replace that parenthetical with the longer roster:

OLD:
```
(<code>edr</code>, <code>instance-integrity</code>, <code>instance-threat</code>, <code>sre-health</code>)
```

NEW:
```
(<code>edr</code>, <code>instance-integrity</code>, <code>instance-threat</code>, <code>sre-health</code>, <code>log-investigator</code>, <code>log-sigma-hunter</code>)
```

Then in the same section, after the existing roster paragraph, append:

```mdx
<p>The <code>log-sigma-hunter</code> daimon ships paired with an <strong>IOC feeds + Sigma rules</strong> catalog: the CP curates Sigma detection rules per fleet, the daimon evaluates them against local logs, and matches surface as ordinary findings. Cross-references <code>log-investigator</code> for deeper analysis on whatever it surfaces.</p>
```

- [ ] **Step 3: Verify build + view**

```bash
npm run dev
```
- Open `/concepts/agents`, scroll to "In the platform" — new paragraph reads cleanly.
- Open `/concepts/daimons`, scroll to "What a daimon is" — roster mentions log-sigma-hunter; the IOC feeds paragraph follows.

Stop dev server.

- [ ] **Step 4: Commit**

```bash
git add site/src/pages/concepts/agents.mdx site/src/pages/concepts/daimons.mdx
git commit -m "$(cat <<'EOF'
feat(site): note LLM keys, RBAC bindings, log-sigma-hunter, IOC feeds

agents.mdx — call out per-fleet LLM credentials and RBAC credential
bindings on agent_run dispatch.

daimons.mdx — extend built-in roster with log-investigator and
log-sigma-hunter; mention IOC feeds + Sigma rules pipeline.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Orchestrations — Selectors & labels section

**Files:**
- Modify: `site/src/pages/concepts/orchestrations.mdx`

- [ ] **Step 1: Add SelectorFanoutDemo import + TOC entry**

In the frontmatter, add `selectors` to the `toc` list between `branching` and `triggers`:

```mdx
toc:
  - { id: what,        label: "What an orchestration is" }
  - { id: spec,        label: "The YAML spec" }
  - { id: fanout,      label: "Fan-out across hosts" }
  - { id: gates,       label: "Approval gates" }
  - { id: branching,   label: "Branching on results" }
  - { id: selectors,   label: "Selectors & labels" }
  - { id: triggers,    label: "Triggers" }
  - { id: in-platform, label: "In the platform" }
  - { id: next,        label: "Where to next" }
```

In the import block (lines 18-23), add SelectorFanoutDemo:

```mdx
import SelectorFanoutDemo from '../../components/diagrams/SelectorFanoutDemo.astro';
```

- [ ] **Step 2: Insert the new section**

Insert the following section between the `<h2 id="branching">` block (which ends with the `byNode` paragraph) and the `<h2 id="triggers">` block:

```mdx
<h2 id="selectors">Selectors & labels</h2>

<p>Hard-coded host arrays don't scale. Okesu's <span class="term">labels</span> are a generic, cross-entity tagging system — every entity (hosts, daimons, agents, findings, investigations, IOCs) has a labels editor on its detail page. The label index lives in one table; the labels themselves are arbitrary key/value strings (<code>env=prod</code>, <code>tier=web</code>, <code>compliance=pci</code>).</p>

<p>An orchestration step can replace the explicit <code>nodes:</code> array with a selector expression — the engine resolves the selector at dispatch time and fans out across whatever matches:</p>

<div class="code-block">
&nbsp;&nbsp;- <span class="key">id</span>: scan<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">agent</span>: edr-triage<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">nodes_selector</span>: <span class="str">"label:env=prod AND label:tier=web"</span><br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">prompt</span>: <span class="str">"sweep for known IOCs"</span>
</div>

<p>Move a host between environments and the selector picks up the change automatically — no orchestration edit required. The same selectors drive notification rules and the cmd-K shortcut (<code>label:tier=web</code>) for jumping to filtered list pages.</p>

<div class="my-6">
  <SelectorFanoutDemo />
</div>
```

- [ ] **Step 3: Verify build + view**

```bash
npm run dev
```
Open `/concepts/orchestrations`. Verify:
- New "Selectors & labels" section appears between Branching and Triggers
- TOC shows the new entry
- The SelectorFanoutDemo component renders, resolves labels, dispatches

Stop dev server.

- [ ] **Step 4: Commit**

```bash
git add site/src/pages/concepts/orchestrations.mdx
git commit -m "$(cat <<'EOF'
feat(site): orchestrations — Selectors & labels section

Cover the label system + nodes_selector. Includes the
SelectorFanoutDemo animation.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Daimon deep-dive — Sigma section

**Files:**
- Modify: `site/src/pages/daimons.astro`

- [ ] **Step 1: Locate the daimon roster section**

Open `site/src/pages/daimons.astro`. Search for the section heading that lists the built-in daimons (typically titled something like "The built-in roster" or "Daimons that ship in the box"). It is structured as a series of named subsections.

Identify the last subsection — likely <code>sre-health</code>. Append a new subsection after it:

```astro
    <section id="log-sigma-hunter" class="border-t border-border pt-10">
      <h3 class="text-[20px] font-semibold text-ink mb-2">log-sigma-hunter</h3>
      <p class="text-[13px] uppercase tracking-wide text-ink-mute font-bold mb-3">Continuous detection · Sigma rules · Logs</p>

      <p class="text-[14px] text-ink leading-relaxed mb-4">
        <strong>log-sigma-hunter</strong> is a detection daimon that evaluates
        <a href="https://github.com/SigmaHQ/sigma" target="_blank" rel="noopener" class="text-brand-700 font-medium hover:underline">Sigma</a>
        rules against the host's local logs (auth, audit, application). When a
        rule fires, the daimon emits a finding with the rule id, matched event,
        and severity carried over from the rule definition itself.
      </p>

      <p class="text-[14px] text-ink leading-relaxed mb-4">
        Rule curation lives at the CP, not the host. The <strong>IOC feeds</strong>
        admin page (Settings → IOC feeds) lets you subscribe a fleet to
        upstream Sigma rule sources or load your own. Rules are pushed to
        <code class="bg-brand-50 text-brand-700 px-1 rounded text-[12px] font-mono">log-sigma-hunter</code>
        daimons via the standard tunnel; offline daimons pick them up on
        their next bucket pull. No daimon-side rule editing — everything
        flows from the CP outward.
      </p>

      <p class="text-[14px] text-ink leading-relaxed">
        Pairs cleanly with <a href="#log-investigator" class="text-brand-700 font-medium hover:underline">log-investigator</a>:
        the hunter detects on a known pattern, the investigator does the
        deeper analysis on the surrounding context.
      </p>
    </section>
```

If the page does not have a roster section (the deep-dive may be structured differently), instead append the section near the end of the page before the closing `</article>`/`</section>` markers, using the same structural HTML.

- [ ] **Step 2: Verify build + view**

```bash
npm run dev
```
Open `/daimons` in the browser. Scroll to the new section. Verify:
- Heading reads "log-sigma-hunter"
- Three paragraphs render with correct copy
- Internal anchor link to `#log-investigator` (if it exists in this page; if not, dropping the link is acceptable — verify it doesn't 404 on hover)

Stop dev server.

- [ ] **Step 3: Commit**

```bash
git add site/src/pages/daimons.astro
git commit -m "$(cat <<'EOF'
feat(site): daimon deep-dive — log-sigma-hunter section

Cover the Sigma-rules pipeline (IOC feeds → rules → daimon → findings)
and the cross-reference to log-investigator.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: New recipe — `t2-medium-noise-triage`

**Files:**
- Create: `site/src/data/recipes/t2-medium-noise-triage.yaml`
- Modify: `site/src/pages/recipes.astro`

- [ ] **Step 1: Pull the YAML from the running CP**

Either run from the demo CP, or copy from the orchestrations seed directory in the repo. Try the demo CP first:

```bash
curl -sk -b /tmp/okesu-cookie -c /tmp/okesu-cookie \
  -X POST https://localhost:7443/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@local","password":"okesu-demo"}'

curl -sk -b /tmp/okesu-cookie \
  https://localhost:7443/api/orchestrations/t2-medium-noise-triage/spec \
  | jq -r .spec > site/src/data/recipes/t2-medium-noise-triage.yaml
```

If the API path differs in your build, fall back to copying from the repo's seed directory:

```bash
find . -name "t2-medium-noise-triage*" -type f 2>/dev/null
```

The expected file is in `controlplane/orchestrations/seeds/` or similar; copy its contents into `site/src/data/recipes/t2-medium-noise-triage.yaml` verbatim.

- [ ] **Step 2: Add the recipe entry**

In `site/src/pages/recipes.astro`, find the `recipes: Recipe[] = [...]` array (line ~39). Append a new entry to the array, before the closing `];`:

```typescript
  {
    id: 't2-medium-noise-triage',
    title: 'Medium-Severity Noise Triage (T2)',
    tier: 'T2',
    tagline: 'Conservative auto-classifier for MEDIUM findings — only auto-resolves what is unambiguously safe; the rest escalate with context attached.',
    whenToUse: 'You\'re drowning in MEDIUM findings that are mostly background radiation but occasionally bury something real. You want a careful auto-triage layer that resolves the obvious noise without ever closing something that should have been escalated.',
    highlights: [
      'Refuses to auto-close anything ambiguous — defaults to escalate, not resolve. Optimised against false-resolutions, not against analyst time.',
      'Uses `actions:` allowlist to set status, severity overrides, and tags — no approval gate needed because the action surface is tightly bounded.',
      'Demonstrates how to wire severity-rule overrides + label-driven escalation into one playbook so a misconfigured fleet doesn\'t need to live with a wrong-severity baseline forever.',
    ],
  },
```

- [ ] **Step 3: Verify build + view**

```bash
npm run dev
```
Open `/recipes`. Verify:
- New recipe section appears at the bottom of the list
- TOC shows it
- The collapsed `<details>` block contains the YAML

Stop dev server.

- [ ] **Step 4: Commit**

```bash
git add site/src/data/recipes/t2-medium-noise-triage.yaml site/src/pages/recipes.astro
git commit -m "$(cat <<'EOF'
feat(site): recipes — t2-medium-noise-triage worked example

Adds the conservative MEDIUM auto-classifier orchestration as the
seventh worked recipe.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: ConceptCard — 4-up sizing

**Files:**
- Modify: `site/src/components/ConceptCard.astro`

- [ ] **Step 1: Tighten padding for narrow column**

Open `site/src/components/ConceptCard.astro`. Replace the anchor's class string:

OLD:
```astro
class="group block bg-panel border border-border rounded-lg shadow-card p-5 hover:border-brand-100 hover:shadow-md transition-all"
```

NEW:
```astro
class="group block bg-panel border border-border rounded-lg shadow-card p-4 lg:p-5 hover:border-brand-100 hover:shadow-md transition-all"
```

Replace the description paragraph's class:

OLD:
```astro
<p class="text-xs text-ink-dim leading-relaxed mb-3">{description}</p>
```

NEW:
```astro
<p class="text-xs text-ink-dim leading-relaxed mb-3 line-clamp-3 lg:line-clamp-none">{description}</p>
```

(The line-clamp keeps cards even-height at the narrowest 4-up breakpoint; full text shows once there's room.)

- [ ] **Step 2: Verify build**

```bash
npm run build
```
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/ConceptCard.astro
git commit -m "$(cat <<'EOF'
feat(site): ConceptCard — tighter padding + clamp at 4-up

Prepare the card chrome for the 4-up home-page grid. Padding tightens
on smaller breakpoints; description clamps to keep card heights even.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: FeatureGrid — rewrite + add war room

**Files:**
- Modify: `site/src/components/FeatureGrid.astro`

- [ ] **Step 1: Rewrite the Investigations entry, add Real-time war room**

Open `site/src/components/FeatureGrid.astro`. Replace the entry whose title is "Investigations as a workspace":

OLD:
```typescript
  {
    title: 'Investigations as a workspace',
    body: 'Group findings into a case file. Notes, timeline, and orchestrations all hang off one investigation entity.',
  },
```

NEW:
```typescript
  {
    title: 'Investigations as a workspace',
    body: 'A case file with timeline, graph view, collaborative notes, and one-click PDF export. Findings, IOCs, runs, and audit events all hang off one entity.',
  },
```

In the same `features` array, insert a new entry after the existing `Per-host fan-out` entry, before the closing `];`:

```typescript
  {
    title: 'Real-time war room',
    body: 'Multiple operators co-edit one case-scoped draft buffer in real time, see each other\'s cursors, then Send produces one immutable note. Yjs CRDT under the hood.',
  },
```

The list now has 7 entries. The `md:grid-cols-2` layout handles an odd row.

- [ ] **Step 2: Verify build + view**

```bash
npm run dev
```
Open `/`. Scroll to the feature list. Verify:
- "Investigations as a workspace" body reads with the new copy
- "Real-time war room" entry appears
- Grid has 4 rows × 2 cols, with the last row holding only one entry

Stop dev server.

- [ ] **Step 3: Commit**

```bash
git add site/src/components/FeatureGrid.astro
git commit -m "$(cat <<'EOF'
feat(site): FeatureGrid — refresh Investigations, add Real-time war room

Updates the Investigations entry to mention the timeline, graph, and
PDF export. Adds a dedicated entry for the Yjs-backed collaborative
war room.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Home page — 4-up concept cards + Investigations card

**Files:**
- Modify: `site/src/pages/index.astro`

- [ ] **Step 1: Make the grid 4-up and add the Investigations card**

Open `site/src/pages/index.astro`. Find the concept-card section (line ~25):

OLD:
```astro
  <section class="max-w-page mx-auto px-6 mb-4">
    <div class="grid md:grid-cols-3 gap-4">
      <ConceptCard
        href="/concepts/daimons"
        title="Daimons"
        glyph="▦"
        description="The long-running daemon installed on each host. Listens for run dispatches, executes agents, streams findings back."
      />
      <ConceptCard
        href="/concepts/agents"
        title="Agents"
        glyph="✦"
        description="Prompt-driven workers (Claude, Codex, your own). They run on the daimon, emit structured findings as JSONL."
      />
      <ConceptCard
        href="/concepts/orchestrations"
        title="Orchestrations"
        glyph="⊞"
        description="YAML specs that sequence agents into playbooks. Fan out across hosts, gate on approval, branch on results."
      />
    </div>
  </section>
```

NEW:
```astro
  <section class="max-w-page mx-auto px-6 mb-4">
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
      <ConceptCard
        href="/concepts/daimons"
        title="Daimons"
        glyph="▦"
        description="The long-running daemon installed on each host. Listens for run dispatches, executes agents, streams findings back."
      />
      <ConceptCard
        href="/concepts/agents"
        title="Agents"
        glyph="✦"
        description="Prompt-driven workers (Claude, Codex, your own). They run on the daimon, emit structured findings as JSONL."
      />
      <ConceptCard
        href="/concepts/orchestrations"
        title="Orchestrations"
        glyph="⊞"
        description="YAML specs that sequence agents into playbooks. Fan out across hosts, gate on approval, branch on results."
      />
      <ConceptCard
        href="/concepts/investigations"
        title="Investigations"
        glyph="◈"
        description="The case workspace. Timeline, graph view, real-time war-room notes, and a one-click PDF report — one closed loop per incident."
      />
    </div>
  </section>
```

- [ ] **Step 2: Verify build + view**

```bash
npm run dev
```
Open `/`. Verify:
- 4 concept cards render in a row at desktop width (resize down to confirm 2-up at sm and 1-up on phones)
- Investigations card has glyph `◈` and links to `/concepts/investigations`
- Card heights are visually even

Stop dev server.

- [ ] **Step 3: Commit**

```bash
git add site/src/pages/index.astro
git commit -m "$(cat <<'EOF'
feat(site): home — 4-up concept cards + Investigations card

Add Investigations to the concept-card row; grid goes from 3-up to
4-up at lg and 2-up at sm.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Tour — extend with screens 7-10

**Files:**
- Modify: `site/src/pages/tour.astro`

- [ ] **Step 1: Update hero copy**

Find the hero `<h1>` (line ~14):

OLD:
```astro
  From a finding to a closed investigation, in seven screens.
```

NEW:
```astro
  From a finding to a closed investigation, in ten screens.
```

- [ ] **Step 2: Replace screen 6's caption to flow into 7**

Find screen 06's caption (line ~135). Update so the closing line invites the reader forward instead of feeling final:

OLD:
```astro
        caption="The case file shape: tabs across the top, identity + linked entities on the right."
```

NEW:
```astro
        caption="The case file shape: tabs across the top, identity + linked entities on the right. The next four screens walk through the tabs that do the actual work."
```

- [ ] **Step 3: Insert screens 7-10**

After the closing `</section>` for screen 6 (line ~137) and before the `{/* ── close ──...}` comment, insert:

```astro

    {/* ── 7 ─────────────────────────────────────────────────────── */}
    <section>
      <div class="flex items-baseline gap-3 mb-3">
        <span class="text-[28px] font-bold text-brand-700 leading-none">07</span>
        <h2 class="text-[22px] font-semibold text-ink leading-tight">The timeline</h2>
      </div>
      <p class="text-[14px] text-ink-dim leading-relaxed mb-5">
        The Timeline tab draws every event in the case against one shared time axis.
        Findings dots, run bars, IOC markers, daimon ticks — same axis, different
        lanes. The filter bar at the top scopes by severity, run status, host,
        and agent; saved searches stash a useful slice for next time. An
        outlier-resistant <em>autoFit</em> keeps the active cluster visible
        rather than zooming out to nothing.
      </p>
      <OperatorView
        src="tour-investigation-timeline"
        alt="Investigation timeline — three lanes (findings, runs, IOCs) with filter chips and a saved-search bar above."
        url="okesu / Investigations / #31 / Timeline"
        caption="Timeline filtered to severity ≥ HIGH and run-status = approved."
      />
    </section>

    {/* ── 8 ─────────────────────────────────────────────────────── */}
    <section>
      <div class="flex items-baseline gap-3 mb-3">
        <span class="text-[28px] font-bold text-brand-700 leading-none">08</span>
        <h2 class="text-[22px] font-semibold text-ink leading-tight">The graph</h2>
      </div>
      <p class="text-[14px] text-ink-dim leading-relaxed mb-5">
        The Graph tab shows the case's relationships as a bipartite layout — hosts
        on one side, IOCs on the other, edges for each observed relationship.
        Server-side aggregation rolls up host/IOC/daimon/run counts so the
        frontend doesn't walk the entity graph itself; the response shape is
        the same whether the case is owned by this CP or proxied from a child.
      </p>
      <OperatorView
        src="tour-investigation-graph"
        alt="Investigation graph view — hosts on the left, IOCs on the right, edges drawn between them."
        url="okesu / Investigations / #31 / Graph"
        caption="Graph view — relationships as edges between hosts and IOCs."
      />
    </section>

    {/* ── 9 ─────────────────────────────────────────────────────── */}
    <section>
      <div class="flex items-baseline gap-3 mb-3">
        <span class="text-[28px] font-bold text-brand-700 leading-none">09</span>
        <h2 class="text-[22px] font-semibold text-ink leading-tight">The war room</h2>
      </div>
      <p class="text-[14px] text-ink-dim leading-relaxed mb-5">
        The Notes tab has two modes: a quick comment composer for one-off
        observations, and a <strong>war room</strong> draft buffer for live
        collaborative thinking. Open the war room from the case detail page
        and everyone connected sees each other's cursors, types into the same
        textarea, reads each other's contributions in real time. Yjs CRDT
        under the hood — operations are eventually consistent, brief network
        blips don't lose anyone's input. When the team's done, one operator
        hits Send and the draft becomes one immutable note row.
      </p>
      <OperatorView
        src="tour-investigation-war-room"
        alt="War room panel mid-edit — two presence chips visible, partial draft text in the textarea, two coloured cursors."
        url="okesu / Investigations / #31 / Notes"
        caption="Two operators editing the same buffer. Send produces one immutable note row."
      />
    </section>

    {/* ── 10 ────────────────────────────────────────────────────── */}
    <section>
      <div class="flex items-baseline gap-3 mb-3">
        <span class="text-[28px] font-bold text-brand-700 leading-none">10</span>
        <h2 class="text-[22px] font-semibold text-ink leading-tight">The handoff</h2>
      </div>
      <p class="text-[14px] text-ink-dim leading-relaxed mb-5">
        Stakeholders rarely log into Okesu. The PDF export gives them
        everything they need without one: an executive summary on page one,
        a chronological narrative through the investigation, every linked
        finding/IOC/run/audit event in formatted reference tables. The
        endpoint is federated — a parent CP can serve a report for a child's
        case by proxying the request through the federation token.
      </p>
      <OperatorView
        src="tour-investigation-pdf"
        alt="Page one of an investigation PDF report — executive summary plus key counts."
        url="okesu / Investigations / #31 / Export"
        caption="Page one of the rendered PDF. One click, stakeholder-ready."
      />
    </section>
```

- [ ] **Step 4: Update the closing copy**

Find the closing section that begins `<h3 class="text-[18px] font-semibold text-ink mb-3">That was the loop.</h3>`. Update its first paragraph:

OLD:
```astro
        Daimon emits a finding. CP routes it. Orchestration triages it. Investigation files it.
        Daimons keep ticking; the same loop runs the next time something interesting happens.
```

NEW:
```astro
        Daimon emits a finding. CP routes it. Orchestration triages it. Investigation files it.
        Operators thinking together close it; the PDF goes to whoever needs it. Daimons keep
        ticking; the same loop runs the next time something interesting happens.
```

- [ ] **Step 5: Verify build + view**

```bash
npm run dev
```
Open `/tour`. Verify:
- Hero says "in ten screens"
- 10 numbered sections render in order; section 10's caption is the rendered-PDF line
- Closing paragraph reads as the new copy
- All 10 `<OperatorView>` blocks render (the four new ones show broken-image placeholders for now)

Stop dev server.

- [ ] **Step 6: Commit**

```bash
git add site/src/pages/tour.astro
git commit -m "$(cat <<'EOF'
feat(site): tour — extend to 10 screens (timeline, graph, war-room, PDF)

The tour now closes on the investigation workspace flow: timeline →
graph → war room → PDF. Hero updated to ten screens; closing copy
reflects the operator-collaboration loop.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: Demo data seed script

**Files:**
- Create: `site/scripts/seed-investigation-demo.mjs`

- [ ] **Step 1: Create the script**

```javascript
#!/usr/bin/env node
// Drive the demo CP into a known state for screenshot capture:
//   1. Run edr-critical-response end-to-end
//   2. Confirm the resulting case
//   3. Add an operator note
// The capture scripts read CASE_ID from this script's stdout (last
// line) so they can land directly on the right case detail.
//
// Usage:
//   cd site
//   OKESU_URL=https://localhost:7443 \
//   OKESU_EMAIL=admin@local \
//   OKESU_PASSWORD=okesu-demo \
//     node scripts/seed-investigation-demo.mjs
//
// On success the last line of stdout is `CASE_ID=<id>`.

const URL      = process.env.OKESU_URL      ?? 'https://localhost:7443';
const EMAIL    = process.env.OKESU_EMAIL    ?? 'admin@local';
const PASSWORD = process.env.OKESU_PASSWORD ?? 'okesu-demo';

let cookieJar = '';

async function api(path, opts = {}) {
  const headers = {
    'Content-Type': 'application/json',
    ...(opts.headers ?? {}),
    ...(cookieJar ? { Cookie: cookieJar } : {}),
  };
  const res = await fetch(`${URL}${path}`, {
    ...opts,
    headers,
    // Allow the demo CP's self-signed cert.
    // Node 18+: Setting this env var is the cleanest way:
    //   NODE_TLS_REJECT_UNAUTHORIZED=0 (set externally before running).
  });
  // Capture session cookie on login.
  const setCookie = res.headers.get('set-cookie');
  if (setCookie) cookieJar = setCookie.split(';')[0];
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(`${opts.method ?? 'GET'} ${path} → ${res.status}: ${body.slice(0, 200)}`);
  }
  return res;
}

async function login() {
  console.log('> login');
  const res = await api('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  await res.json().catch(() => null);
}

async function dispatchOrchestration() {
  console.log('> dispatch edr-critical-response');
  // The CP's manual-run endpoint is /api/orchestrations/{name}/run with
  // optional inputs. Empty inputs lets the orchestration's defaults apply.
  const res = await api('/api/orchestrations/edr-critical-response/run', {
    method: 'POST',
    body: JSON.stringify({ inputs: {} }),
  });
  const body = await res.json();
  return body.run_id ?? body.id;
}

async function waitForRunCompletion(runId, timeoutMs = 90000) {
  console.log(`> wait for run ${runId} to complete (or hit approval gate)`);
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const res = await api(`/api/orchestrations/runs/${runId}`);
    const run = await res.json();
    const status = run.status ?? '';
    if (status === 'completed' || status === 'failed' || status === 'approval_required') {
      console.log(`  status=${status}`);
      return run;
    }
    await new Promise(r => setTimeout(r, 2000));
  }
  throw new Error(`run ${runId} did not settle within ${timeoutMs}ms`);
}

async function findOrCreateCase(runId) {
  console.log('> find or create investigation for run');
  // First, see whether the run has already attached to a case.
  const res = await api(`/api/orchestrations/runs/${runId}`);
  const run = await res.json();
  if (run.investigation_id) return run.investigation_id;

  // Otherwise create one from the run's findings.
  const create = await api('/api/investigations', {
    method: 'POST',
    body: JSON.stringify({
      title: 'Demo: critical EDR callback investigation',
      severity: 'high',
      run_ids: [runId],
    }),
  });
  const body = await create.json();
  return body.id;
}

async function addNote(caseId, body) {
  console.log(`> add note to case ${caseId}`);
  await api(`/api/investigations/${caseId}/notes`, {
    method: 'POST',
    body: JSON.stringify({ body }),
  });
}

async function main() {
  if (process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0') {
    console.warn('[warn] expecting NODE_TLS_REJECT_UNAUTHORIZED=0 for self-signed CP');
  }

  await login();
  const runId = await dispatchOrchestration();
  await waitForRunCompletion(runId);
  const caseId = await findOrCreateCase(runId);
  await addNote(caseId, 'Triage in progress — EDR triage step landed, pulling host snapshots.');
  await addNote(caseId, 'Confirmed callback to 8.8.8.8 — pivoting to fleet hunt.');

  // Final line of stdout is the marker the capture scripts read.
  console.log(`CASE_ID=${caseId}`);
}

main().catch(err => {
  console.error('FATAL:', err.message);
  process.exit(1);
});
```

- [ ] **Step 2: Sanity check (do not actually run unless the demo CP is up)**

```bash
node --check site/scripts/seed-investigation-demo.mjs
```
Expected: silent (syntax OK).

- [ ] **Step 3: Commit**

```bash
git add site/scripts/seed-investigation-demo.mjs
git commit -m "$(cat <<'EOF'
feat(site): seed-investigation-demo script

Drive the local demo CP into a known case state for screenshot
capture: dispatch edr-critical-response, wait for it to settle, find
or create the resulting investigation, attach two notes. Emits
CASE_ID=<id> on the final stdout line.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: Extend `capture-screenshots.mjs` with investigation captures

**Files:**
- Modify: `site/scripts/capture-screenshots.mjs`

- [ ] **Step 1: Add a CASE_ID env reader**

Open `site/scripts/capture-screenshots.mjs`. After the `URL`/`EMAIL`/`PASSWORD` const declarations, add:

```javascript
const CASE_ID = process.env.OKESU_CASE_ID; // produced by seed-investigation-demo.mjs
```

- [ ] **Step 2: Add a captureInvestigationSurfaces helper**

Find the section near the other `async function captureX(page)` declarations. Add a new helper:

```javascript
async function captureInvestigationSurfaces(page) {
  if (!CASE_ID) {
    console.log('  (OKESU_CASE_ID not set — skipping investigation surfaces)');
    return;
  }
  const base = `${URL}/investigations/${CASE_ID}`;

  // Overview
  console.log('> investigation overview');
  await page.goto(base, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: join(OUT_DIR, 'investigation-overview.png'), fullPage: false });
  console.log('  ✓ investigation-overview.png');

  // Timeline tab
  console.log('> investigation timeline');
  await page.goto(`${base}?tab=timeline`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2500);
  await page.screenshot({ path: join(OUT_DIR, 'investigation-timeline.png'), fullPage: false });
  console.log('  ✓ investigation-timeline.png');

  // Graph tab
  console.log('> investigation graph');
  await page.goto(`${base}?tab=graph`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2500); // let react-flow settle
  await page.screenshot({ path: join(OUT_DIR, 'investigation-graph.png'), fullPage: false });
  console.log('  ✓ investigation-graph.png');

  // War-room thumb (single-context — the multi-presence shot lives in capture-tour.mjs).
  console.log('> investigation war-room thumb');
  await page.goto(`${base}?tab=notes`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  // Fire-and-forget click; if the war-room button isn't there, we just
  // capture the notes panel as-is and skip the dedicated thumb.
  try {
    const btn = page.locator('button').filter({ hasText: /war.?room/i }).first();
    if (await btn.count() > 0) {
      await btn.click();
      await page.waitForTimeout(1500);
    }
  } catch (err) { /* skip */ }
  await page.screenshot({ path: join(OUT_DIR, 'investigation-war-room-thumb.png'), fullPage: false });
  console.log('  ✓ investigation-war-room-thumb.png');

  // PDF page-1 — best-effort. If the endpoint serves an inline preview
  // we capture it; otherwise we log + skip and the page falls back to a
  // placeholder.
  console.log('> investigation pdf page-1');
  try {
    const res = await page.goto(`${URL}/api/investigations/${CASE_ID}/report.pdf`, { waitUntil: 'networkidle' });
    const ctype = (res?.headers() ?? {})['content-type'] ?? '';
    if (ctype.includes('application/pdf')) {
      // Browsers may render PDFs inline; if so the screenshot captures it.
      await page.waitForTimeout(2000);
      await page.screenshot({ path: join(OUT_DIR, 'investigation-pdf-page1.png'), fullPage: false });
      console.log('  ✓ investigation-pdf-page1.png');
    } else {
      console.log('  (PDF endpoint did not return application/pdf — skipping)');
    }
  } catch (err) {
    console.log(`  (PDF capture failed: ${err.message} — skipping)`);
  }
}
```

- [ ] **Step 3: Wire the helper into main()**

Inside `main()`, after the existing capture calls but before `await browser.close();`, add:

```javascript
  await captureInvestigationSurfaces(page);
```

- [ ] **Step 4: Sanity check**

```bash
node --check site/scripts/capture-screenshots.mjs
```
Expected: silent.

- [ ] **Step 5: Commit**

```bash
git add site/scripts/capture-screenshots.mjs
git commit -m "$(cat <<'EOF'
feat(site): capture-screenshots — investigation surfaces

Add captureInvestigationSurfaces(): overview, timeline, graph,
war-room thumb, PDF page-1. Reads OKESU_CASE_ID produced by the
seed-investigation-demo script. PDF capture is best-effort; logs and
skips on failure rather than crashing the run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 18: Extend `capture-tour.mjs` with screens 7-10 + 2-context war-room

**Files:**
- Modify: `site/scripts/capture-tour.mjs`

- [ ] **Step 1: Add a CASE_ID env reader**

Open `site/scripts/capture-tour.mjs`. After the existing `RUN_ID`/`FANOUT_RUN_ID` declarations, add:

```javascript
const CASE_ID = process.env.OKESU_CASE_ID;
```

- [ ] **Step 2: Add capture functions for screens 7, 8, 10 (single-context)**

After the existing capture flow but before `await browser.close();`, add:

```javascript
  if (!CASE_ID) {
    console.log('  (OKESU_CASE_ID not set — skipping tour investigation tail)');
  } else {
    const base = `${URL}/investigations/${CASE_ID}`;

    // ── 7. Timeline.
    console.log('> tour: investigation timeline');
    await page.goto(`${base}?tab=timeline`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(2500);
    await shoot(page, 'investigation-timeline');

    // ── 8. Graph.
    console.log('> tour: investigation graph');
    await page.goto(`${base}?tab=graph`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(2500);
    await shoot(page, 'investigation-graph');

    // ── 10. PDF page-1 (handled before war-room because war-room
    //       opens a second context that we want to tear down last).
    console.log('> tour: investigation pdf');
    try {
      const res = await page.goto(`${URL}/api/investigations/${CASE_ID}/report.pdf`, { waitUntil: 'networkidle' });
      const ctype = (res?.headers() ?? {})['content-type'] ?? '';
      if (ctype.includes('application/pdf')) {
        await page.waitForTimeout(2000);
        await shoot(page, 'investigation-pdf');
      } else {
        console.log('  (PDF endpoint did not return application/pdf — skipping)');
      }
    } catch (err) {
      console.log(`  (PDF capture failed: ${err.message} — skipping)`);
    }
  }
```

- [ ] **Step 3: Add screen 9 — war room with 2 contexts**

Right before `await browser.close();`, add:

```javascript
  if (CASE_ID) {
    console.log('> tour: investigation war-room (two contexts)');
    let altContext;
    try {
      // Open a second browser context as a different operator.
      altContext = await browser.newContext({
        ignoreHTTPSErrors: true,
        viewport: { width: 1440, height: 900 },
        deviceScaleFactor: 2,
      });
      const altPage = await altContext.newPage();

      // Login on the alt context (uses the same admin user — distinct
      // session yields a distinct presence chip via session id).
      await altPage.goto(`${URL}/login`, { waitUntil: 'networkidle' });
      await altPage.fill('input[type="email"]', EMAIL);
      await altPage.fill('input[type="password"]', PASSWORD);
      await altPage.click('button[type="submit"]');
      await altPage.waitForFunction(() => !location.pathname.startsWith('/login'), { timeout: 10000 });

      // Both contexts navigate to the war room.
      const url = `${URL}/investigations/${CASE_ID}?tab=notes`;
      await page.goto(url, { waitUntil: 'networkidle' });
      await altPage.goto(url, { waitUntil: 'networkidle' });
      await page.waitForTimeout(1500);

      // Open the war room on each (button text may be "Start war room"
      // or similar — match anything containing "war").
      for (const p of [page, altPage]) {
        try {
          const btn = p.locator('button').filter({ hasText: /war/i }).first();
          if (await btn.count() > 0) await btn.click();
        } catch (e) { /* skip */ }
      }
      await page.waitForTimeout(1500);

      // Type into both — produces real cursor positions + presence.
      const textareaA = page.locator('textarea').first();
      const textareaB = altPage.locator('textarea').first();
      if (await textareaA.count() > 0) {
        await textareaA.fill('callback to 8.8.8.8 from web-prod-01 — netflow confirms');
      }
      if (await textareaB.count() > 0) {
        await textareaB.fill('EDR shows pid 4421 spawned by sshd — pulling memory snapshot now');
      }
      await page.waitForTimeout(1200);

      await shoot(page, 'investigation-war-room');
    } catch (err) {
      console.log(`  (war-room two-context capture failed: ${err.message} — skipping)`);
    } finally {
      if (altContext) await altContext.close();
    }
  }
```

- [ ] **Step 4: Sanity check**

```bash
node --check site/scripts/capture-tour.mjs
```
Expected: silent.

- [ ] **Step 5: Commit**

```bash
git add site/scripts/capture-tour.mjs
git commit -m "$(cat <<'EOF'
feat(site): capture-tour — tail four screens (timeline, graph, war-room, PDF)

Adds capture for tour screens 7, 8, 10 (timeline, graph, PDF) under
single-context, and screen 9 (war room) with a second Playwright
context so presence chips reflect a real second operator. Both
PDF and war-room captures are best-effort: log and skip on failure.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 19: Run captures + final acceptance

**Files:**
- Updates `site/public/screenshots/` (10 new images + WebP optimisations)

- [ ] **Step 1: Bring the demo CP up**

Verify the local demo CP is reachable:
```bash
curl -sk https://localhost:7443/ | head -5
```
Expected: HTML response. If it fails, start the CP via `make redeploy-local` or whatever the project's preferred local-bring-up step is.

- [ ] **Step 2: Run the seed script**

```bash
cd site
NODE_TLS_REJECT_UNAUTHORIZED=0 \
OKESU_URL=https://localhost:7443 \
OKESU_EMAIL=admin@local \
OKESU_PASSWORD=okesu-demo \
  node scripts/seed-investigation-demo.mjs | tee /tmp/seed.out
```
Expected: final stdout line is `CASE_ID=<id>`. Capture that id:
```bash
export CASE_ID=$(tail -1 /tmp/seed.out | sed -n 's/^CASE_ID=//p')
echo "CASE_ID=$CASE_ID"
```

- [ ] **Step 3: Run the capture scripts**

```bash
NODE_TLS_REJECT_UNAUTHORIZED=0 \
OKESU_URL=https://localhost:7443 \
OKESU_EMAIL=admin@local \
OKESU_PASSWORD=okesu-demo \
OKESU_CASE_ID=$CASE_ID \
  node scripts/capture-screenshots.mjs

NODE_TLS_REJECT_UNAUTHORIZED=0 \
OKESU_URL=https://localhost:7443 \
OKESU_EMAIL=admin@local \
OKESU_PASSWORD=okesu-demo \
OKESU_CASE_ID=$CASE_ID \
  node scripts/capture-tour.mjs
```
Expected: each script prints `✓` per captured asset; PNG files appear in `site/public/screenshots/`. Skipped captures (e.g. PDF if endpoint doesn't support inline preview) print `( ... — skipping)`.

- [ ] **Step 4: Optimise to WebP**

```bash
node scripts/optimize-screenshots.mjs
```
Expected: each new PNG gets a sibling `.webp`. Each WebP is < 200 KB.

If any newly-captured PNG is unusually large (> 500 KB pre-optimisation), check `site/scripts/optimize-screenshots.mjs` for quality knobs and re-run.

- [ ] **Step 5: Manual acceptance check**

```bash
npm run dev
```

Visit each page in turn and verify:

1. **`/`** — 4-up concept cards align on desktop, stack to 2-up at sm, 1-up on mobile. FeatureGrid has 7 entries with the rewritten Investigations + new war-room rows.
2. **`/concepts/investigations`** — all 4 inline animated demos animate. All 4 OperatorView blocks show real screenshots (or placeholder + TODO if the capture failed).
3. **`/concepts/agents`** — In-platform section has the new LLM-keys + RBAC paragraph.
4. **`/concepts/daimons`** — daimon roster lists log-investigator + log-sigma-hunter; the Sigma-pipeline paragraph follows.
5. **`/concepts/orchestrations`** — Selectors & labels section appears, SelectorFanoutDemo animates, TOC includes it.
6. **`/daimons`** (long-form) — log-sigma-hunter section renders.
7. **`/recipes`** — t2-medium-noise-triage entry appears with its YAML `<details>` block.
8. **`/tour`** — 10 numbered screens render in order; screen 9 shows two presence chips in the war-room screenshot.
9. **`prefers-reduced-motion`** — toggle on in OS settings, visit each new demo. All animations sit at their static end state; nothing is broken.

Stop dev server.

- [ ] **Step 6: Build + Lighthouse**

```bash
npm run build
```
Expected: build succeeds. No broken-link warnings.

If a Lighthouse run is part of the project's CI (or if you have it installed locally), run against the build output and verify no regression vs the current 100/100/100/100 baseline.

- [ ] **Step 7: Commit screenshot assets**

```bash
git add site/public/screenshots/
git commit -m "$(cat <<'EOF'
feat(site): screenshots — investigations + tour tail

Captured against the local demo CP via seed-investigation-demo +
capture-screenshots + capture-tour. Includes overview, timeline,
graph, war-room (single + two-context), PDF page-1 where the
endpoint allows inline preview.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 8: Open the PR**

```bash
git push -u origin <branch>
gh pr create --title "Site refresh: investigations workspace + tour extension" --body "$(cat <<'EOF'
## Summary
- New `/concepts/investigations` page (timeline, graph, war room, PDF)
- 5 new CSS-only animated demos (TimelineLane, GraphFan, WarRoomCursors, PDFReport, SelectorFanout)
- Tour grows from 6 → 10 screens covering the case-workspace flow
- Home page concept-cards go 3-up → 4-up; FeatureGrid gains a Real-time war room entry
- Daimon deep-dive gains a log-sigma-hunter section; orchestrations gains a Selectors & labels section
- New recipe: t2-medium-noise-triage
- New `seed-investigation-demo.mjs` reproduces the demo state for screenshot capture
- All animations gated under `prefers-reduced-motion: no-preference`

## Test plan
- [ ] `npm run dev` — every page renders without console errors
- [ ] `/concepts/investigations` — all four inline demos animate
- [ ] Tour screen 9 shows two real presence chips in the war-room shot
- [ ] Reduced-motion toggle pauses every new demo
- [ ] `npm run build` — passes, no broken links
- [ ] Lighthouse — no regression vs baseline
EOF
)"
```

---

## Self-Review

**Spec coverage:** Walking the spec section-by-section.

| Spec section | Covered by |
|---|---|
| New page `concepts/investigations.mdx` | Task 7 |
| Edits — agents.mdx (LLM keys + RBAC) | Task 8 |
| Edits — daimons.mdx (log-sigma-hunter, IOC feeds) | Task 8 |
| Edits — orchestrations.mdx (Selectors & labels + SelectorFanoutDemo) | Tasks 6 + 9 |
| Edits — daimons.astro (Sigma roster) | Task 10 |
| Edits — recipes.astro + new YAML | Task 11 |
| Edits — index.astro 4-up + Investigations card | Task 14 |
| Edits — FeatureGrid.astro rewrite + new row | Task 13 |
| Edits — ConceptCard.astro 4-up sizing | Task 12 |
| Edits — Nav.astro | Existing `/concepts` prefix matching covers `/concepts/investigations`; deferred unless visual review needs a direct nav entry. Noted in Task 7 file map. |
| Tour extension to 10 screens | Task 15 |
| Screenshot strategy + 6 investigation captures | Tasks 16 + 17 + 19 |
| 4 tail tour captures + 2-context war room | Tasks 18 + 19 |
| Animation keyframes (3 new) | Task 1 |
| 5 CSS-only diagrams | Tasks 2-6 |
| Edge case: PDF endpoint can't serve inline | Tasks 17 + 18 (best-effort with skip) |
| Edge case: war-room two-context fails | Task 18 (try/catch with fallback) |
| Edge case: log-sigma-hunter spec not present | Task 11 (skip second YAML if absent) |
| Edge case: MDX layout regression | Task 7 (existing layout pattern reused) |

**Placeholder scan:** Searched for "TBD", "TODO", "implement later", "fill in details", "appropriate error handling", "validation", "edge cases", "Similar to". The plan uses TODO as a noun (placeholder/SVG fallback for missing screenshots, mentioned in the design spec) but never as an instruction to the engineer.

**Type consistency:** Component names, IDs, file paths, env vars (`OKESU_CASE_ID`, `OKESU_URL`), and class names referenced across tasks are consistent. The `CASE_ID` shell variable in Task 19 matches the `OKESU_CASE_ID` env var that capture scripts read in Tasks 17/18, and the `CASE_ID=...` stdout line emitted by the seed script in Task 16. The animation class names (`okesu-lane-slide`, `okesu-cursor-blink`, `okesu-chip-resolve`) defined in Task 1 are consumed by Tasks 2 (lane-slide), 4 (cursor-blink), and 6 (chip-resolve).

---

**Plan complete and saved to `docs/superpowers/plans/2026-05-04-site-feature-refresh.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
