# okesu.to feature refresh — May 2026

**Status:** approved
**Date:** 2026-05-04

## Goal

Refresh okesu.to to reflect April–May platform features. The headline
gap is the **investigation workspace**: war-room collaborative drafts
(Yjs CRDT real-time co-editing), timeline search/filter/clusters, the
graph view, and PDF report export. Smaller updates land alongside on
the existing concept pages, the daimon deep-dive, the recipes catalog,
the tour, and the home page. Ships as one PR.

## Non-goals

- Re-recording earlier tour screens with newer UI chrome. Sidebar
  grouping landed, but the existing screenshots remain recognizable.
- A dedicated catalog / federation / IOC-feeds concept page. These
  features surface inline on the daimon and orchestration pages.
- Bucket provisioning, OCI deploy, smart payload, severity rules —
  too operational / admin-flavored for the public site.
- Any platform code change. This is a site-only PR.

## Decisions

| # | Decision |
|---|---|
| 1 | Investigations gets its own concept page (`concepts/investigations.mdx`) — not a deep-dive long-form page. Keeps shape consistent with agents/daimons/orchestrations. |
| 2 | Tour extends from 6 to 10 screens; new tail covers timeline → graph → war-room → PDF as the resolution arc of the existing detection→automation narrative. |
| 3 | Demo data is reproducible: a one-shot `seed-investigation-demo.mjs` script runs `edr-critical-response` end-to-end, then drives the resulting investigation through realistic operator actions. |
| 4 | War-room screenshot uses **two parallel Playwright contexts** so presence chips + cursors are real, not faked. |
| 5 | PDF screenshot is best-effort: if the endpoint serves an inline preview we capture it; otherwise the script logs and skips, the page falls back to an SVG placeholder + TODO. |
| 6 | Five new CSS-only animated demos, all <100 lines, all gated under `prefers-reduced-motion: no-preference`. Borrow the shared keyframe palette in `site/src/styles/animations.css`. |
| 7 | Home page concept-card row goes 3-up → 4-up to add Investigations. `ConceptCard` markup adjusts to handle the wider layout. |
| 8 | `FeatureGrid` grows from 6 → 7 entries: rewrite the existing "Investigations as a workspace" body and add a new "Real-time war room" feature. The 2-col grid handles an odd last row cleanly. Drop nothing. |

## New page: `concepts/investigations.mdx`

Same shape as the existing concept pages (agents, daimons,
orchestrations). Sections in order:

1. **What is an investigation** — case file that groups findings + IOCs
   + runs + notes for a single incident
2. **Timeline** — search/filter chips + cluster lanes; one CSS demo
   (`TimelineLaneDemo.astro`) + screenshot
3. **Graph view** — bipartite host↔IOC layout; one CSS demo
   (`GraphFanDemo.astro`) + screenshot
4. **War room** — Yjs-backed real-time co-edited draft notes; novel
   concept; one CSS demo (`WarRoomCursorsDemo.astro`: two cursors +
   presence chips typing into a shared draft) + screenshot of
   mid-edit state
5. **PDF report** — one-click incident export; small demo
   (`PDFReportDemo.astro`) + thumbnail screenshot
6. **Cross-references** to orchestrations (runs hang off cases) and
   daimons (continuous monitoring → finding → case)

## Edits to existing pages

### `concepts/agents.mdx`
Add one sentence noting that agents now run with **per-fleet LLM API
keys** threaded by the CP via `/etc/okesu/jobs.env`, and that **RBAC
credential bindings** are wired into `agent_run` dispatch. No new
diagrams.

### `concepts/daimons.mdx`
Add the **log-sigma-hunter** daimon to the roster (cross-references
log-investigator). Mention **IOC feeds + Sigma catalog**. Light edit.

### `concepts/orchestrations.mdx`
New "**Selectors & labels**" subsection covering:
- Generic labels are cross-entity (LabelEditor lives on every detail page)
- `nodes_selector` enables declarative selector-based fan-out
  (previously: explicit host list only)
- One small CSS demo (`SelectorFanoutDemo.astro`) showing
  `label:env=prod` resolving to N hosts

### `daimons.astro` (long-form deep-dive)
Add a section near the existing daimon roster covering log-sigma-hunter
and the Sigma-rules pipeline (IOC feeds → Sigma daimon → matches
surface as findings). Update any "current daimon roster" counts.

### `recipes.astro`
Add one new recipe entry pulled from the running CP:
- `t2-medium-noise-triage` (conservative MEDIUM auto-classifier)

If `log-sigma-hunter` orchestration spec exists in the running CP,
add it as a second new entry; otherwise skip.

### `index.astro`
- Concept-card row goes 3-up → 4-up (add Investigations card,
  glyph `◈`)
- `FeatureGrid` body for "Investigations as a workspace" rewritten
  to mention war-room + graph + PDF
- New feature row: **"Real-time war room"** — Yjs CRDT-backed
  collaborative draft notes during an incident; multiple operators
  edit the same buffer, see each other's cursors, and Send produces
  one immutable note row.

### `FeatureGrid.astro`
Rewrite the existing "Investigations as a workspace" body to mention
war-room + graph + PDF. Add a new "Real-time war room" entry. Total
goes from 6 → 7 entries. The 2-col grid handles an odd last row.

### `ConceptCard.astro`
Adjust internal padding/typography so the card reads cleanly at the
narrower 4-up width. No new props.

### `Nav.astro`
Add `Investigations` link under the existing concept entries.

## Tour extension (`tour.astro`)

Six existing screens stay. Four new screens at the tail:

| # | Screen | Notes |
|---|---|---|
| 7 | Investigation timeline | Filter chips visible (severity + run-status). Caption explains lanes. |
| 8 | Graph view | Bipartite layout, hosts left / IOCs right. Caption explains the relationship model. |
| 9 | War room | Two presence chips visible, mid-edit state with partial draft. Caption explains Yjs/CRDT and Send-to-immutable-note flow. |
| 10 | PDF report | Generated report visible, or download dialog + page-1 thumbnail. Caption: "One-click handoff for stakeholders." |

The capture script extends; existing one-context flow stays for
screens 1–8 and 10. Screen 9 uses **two parallel Playwright contexts**.

## Screenshot strategy

`capture-screenshots.mjs` extends to capture investigation surfaces:

| Asset | Notes |
|---|---|
| `investigation-overview.png` | Header, summary cards (hosts/IOCs/daimons/runs), tab row |
| `investigation-timeline.png` | Timeline tab, filter bar visible, chips set to severity:high + status:resolved |
| `investigation-graph.png` | Graph tab, bipartite layout settled |
| `investigation-war-room.png` | Two-context capture, partial draft, two presence chips |
| `investigation-war-room-thumb.png` | Smaller crop — panel header + chip row only — used inline on the concept page |
| `investigation-pdf-page1.png` | First page of rendered PDF (Playwright PDF render → PNG, or HTML preview screenshot) |

### Demo data setup — `seed-investigation-demo.mjs`

One-shot, scripted, reproducible:

1. Run `edr-critical-response` end-to-end against the demo CP fleet
2. From the resulting findings, create one investigation; attach 4
   hosts, 6 IOCs, 3 runs, 5 daimon hits
3. Add 2 simple notes (different authors) for the activity feed
4. Open a war-room session in **two parallel Playwright contexts**;
   type a few sentences across both so the textarea has real content
   and presence chips show real users

Failure handling: if any individual capture fails (e.g. PDF endpoint
doesn't return inline preview), the script logs the failure and
**skips** the screenshot rather than crashing the run. The page falls
back to a simple SVG placeholder + TODO comment for any missing
asset, so the build stays green.

All artefacts run through the existing `optimize-screenshots.mjs` to
produce WebP variants alongside the PNGs.

## New CSS-only diagrams

Five new components in `site/src/components/diagrams/`. Each <100
lines, pure CSS keyframes, gated under
`@media (prefers-reduced-motion: no-preference)`:

| Component | Used on | What it shows |
|---|---|---|
| `TimelineLaneDemo.astro` | investigations | Three horizontal lanes (findings, runs, IOCs); dots/bars slide in left-to-right; a filter chip toggle dims/un-dims one lane |
| `GraphFanDemo.astro` | investigations | 4 host nodes left, 5 IOC nodes right; edges fade in one-by-one; CSS-only hover highlight shows neighborhood |
| `WarRoomCursorsDemo.astro` | investigations | Faux textarea with two animated caret carets + presence chips; one operator types, then the other inserts a sentence; Send → buffer locks |
| `SelectorFanoutDemo.astro` | orchestrations | Selector pill `label:env=prod` resolves into 5 host chips with a brief "matching…" loading state, then a fan-out strip dispatches |
| `PDFReportDemo.astro` | investigations | Faux page outlines stack with a download-icon pulse; tiny inline diagram for the PDF section |

All five borrow shared `@keyframes` from
`site/src/styles/animations.css`. We add 2–3 new keyframes there
(timeline-lane slide; cursor-blink; chip-resolve fade-in). No JS.

## File map

```
NEW
  site/src/pages/concepts/investigations.mdx
  site/src/components/diagrams/TimelineLaneDemo.astro
  site/src/components/diagrams/GraphFanDemo.astro
  site/src/components/diagrams/WarRoomCursorsDemo.astro
  site/src/components/diagrams/SelectorFanoutDemo.astro
  site/src/components/diagrams/PDFReportDemo.astro
  site/src/data/recipes/t2-medium-noise-triage.yaml
  site/scripts/seed-investigation-demo.mjs
  (10 new PNG/WebP pairs under site/public/screenshots/)

MODIFY
  site/src/styles/animations.css            (+2-3 keyframes)
  site/src/pages/concepts/agents.mdx        (LLM keys + RBAC bindings)
  site/src/pages/concepts/daimons.mdx       (log-sigma-hunter, IOC feeds)
  site/src/pages/concepts/orchestrations.mdx (Selectors & labels section)
  site/src/pages/daimons.astro              (sigma roster, IOC pipeline)
  site/src/pages/recipes.astro              (+1-2 new recipes)
  site/src/pages/index.astro                (4-up cards)
  site/src/components/FeatureGrid.astro     (rewrite + new feature)
  site/src/components/ConceptCard.astro     (4-up sizing)
  site/src/components/Nav.astro             (investigations entry)
  site/src/pages/tour.astro                 (+4 tail screens)
  site/scripts/capture-screenshots.mjs      (+6 investigation captures)
  site/scripts/capture-tour.mjs             (+4 screens, 2-context war-room)
```

## Acceptance check

The site has no test runner; verification is manual.

1. `cd site && npm run dev` — all pages render without console errors
2. Investigations concept page: all 4 inline demos animate (or sit
   static when reduced-motion is on)
3. Tour: 10 screens load in order; war-room screen shows 2 real
   presence chips
4. Home page: 4-up concept cards align on desktop, stack cleanly on
   mobile; FeatureGrid rewrite reads cleanly
5. Reduced-motion: OS toggle on → all new keyframes pause; demos
   still legible as static
6. `npm run build` — Astro static build succeeds, no broken links
7. Lighthouse: no regression vs current 100/100/100/100
8. Each new screenshot is <200KB post-WebP

## Edge cases

- **PDF endpoint can't serve inline preview** — capture script logs
  + skips; page renders SVG placeholder + TODO comment.
- **War-room two-context capture fails** (Playwright context can't
  open second) — script falls back to single-context capture; the
  inline thumbnail uses a hand-drawn presence-chip overlay; concept
  page caption notes the rendering is illustrative.
- **`log-sigma-hunter` orchestration spec not present in CP** — recipes
  page adds only `t2-medium-noise-triage`; daimon deep-dive still
  documents the daimon itself.
- **Build-time MDX failure** in the new investigations page (frontmatter
  layout shape regression) — same pattern as the existing concept pages:
  `const fp = Astro.props.frontmatter ?? Astro.props;` in the layout.
