# okesu.to v2 — animated concept site

**Status:** approved
**Date:** 2026-04-30

## Goal

Upgrade `okesu.to` from a static brochure to a site that *shows* what
Okesu does. Replace v1's four hand-drawn SVG diagrams with CSS-only
animated components that mirror the platform's run-canvas idioms
(pulsing rings on running steps, dot-strip fill-ins, growing
histograms, animated edges, amber approval gates). Restore the
Japanese etymology to the brand surface. Restructure each concept
page around multiple inline animated demos so visitors learn by
seeing the platform's mechanics in motion, not just by reading.

The v1 site went live at https://okesu.to as a clean but quiet
brochure. v2 ports the platform's signature visual energy to the
public face — same purple, same chrome, same animation primitives
— so a visitor's first impression of the site is the same impression
the operator has of the platform itself.

## Non-goals

- Real platform screenshots — still needs a populated demo CP.
- JS-driven interactivity (click-to-run demos) — pure CSS only in v2.
- Per-feature micro-animations on the home page's feature grid.
- Site search, analytics, blog, changelog — same v1 deferrals.
- A11y audit beyond Astro defaults plus the explicit additions in
  this spec.
- Migrating away from the existing v1 page structure (still 6 routes:
  `/`, `/concepts/{daimons,agents,orchestrations}`, `/install`,
  `/docs`). v2 adds `/about-the-name`.

## Decisions

| # | Decision |
|---|---|
| 1 | Animation ambition: B from question 1 — port the platform's run-canvas energy to marketing. Pulsing rings, dot strips, histograms, amber approval pulses. |
| 2 | Animation tech: pure CSS keyframes only. Zero JS bundle additions. Reduced motion gates every animation. |
| 3 | Concept page richness: B from question 3 — multiple inline animated demos per concept page, ~3–5 per page. |
| 4 | Etymology surfaced: home hero sub-tagline, footer brand line, dedicated `/about-the-name` page. The name *Okesu* derives from オーケストラ (*ōkesutora*), the Japanese loanword for orchestra; `okesu.to` reads as "the orchestra." |
| 5 | Performance budget: zero JS, ≤40 KB HTML home page, Lighthouse Performance ≥ 85 mobile. |
| 6 | Existing v1 SVGs retained but moved to `site/src/components/diagrams/old/` for posterity. Removed from all imports. v2 components have new names that signal they're animated demos (`*Demo.astro`). |

## Etymology

The site's brand surface gets three new touchpoints:

**Home hero sub-tagline.** Directly under the existing headline,
one new line in `text-ink-mute italic`:

> *Okesu — from オーケストラ (ōkesutora), the Japanese for "orchestra." We conduct the players; you write the score.*

**Footer brand line.** The existing `Okesu — orchestrate your defense.`
becomes:

> Okesu (オーケストラ) — orchestrate your defense.

The katakana `オーケストラ` renders in `font-mono text-ink-dim` so it
reads as a typographic accent, not a translation.

**`/about-the-name` page.** ~150-word standalone page linked from
the footer brand line. Content (~final draft):

> # About the name
>
> Okesu (オーケストラ) is the Japanese word for orchestra — itself a
> loanword from European languages, written in katakana to mark its
> foreign origin. The name made the round trip: a European concept,
> rendered phonetically into Japanese, then chosen as the name of a
> tool built around that same metaphor.
>
> The platform conducts a fleet of agents the way a conductor leads
> an orchestra. Daimons are the players on each host. Agents are the
> parts they perform. Orchestrations are the score they follow. The
> control plane stands at the front, shaping the timing.
>
> The domain `okesu.to` reads as the abbreviated katakana for "the
> orchestra" — the conductor's "the" that introduces a performance
> about to begin.

## Animated diagram inventory

### Top-level demo components (replace v1's diagrams)

| Component | Page | Loop | What it shows |
|---|---|---|---|
| `OrchestrationLoopDemo.astro` | `/` (home anchor) | 12 s | Full 4-step DAG: scan → fan-out edr-triage on 6 hosts → summarize → approval-gated contain. The signature demo. |
| `DispatchFlowDemo.astro` | `/concepts/daimons` (anchor) | 8 s | CP → daimon dispatch arrow + agent loop + finding card emitted back. |
| `AgentJSONLDemo.astro` | `/concepts/agents` (anchor) | 10 s | Black terminal panel typing JSONL events line-by-line; finding + orchestration_result lines highlight. |
| `OrchestrationDAGDemo.astro` | `/concepts/orchestrations` (anchor) | 10 s | Same as the home loop but tighter and annotated with field names per step. |

### Inline demo components (used inside concept page MDX bodies)

| Component | Pages | Loop | What it shows |
|---|---|---|---|
| `HeartbeatDemo.astro` | Daimons / Lifecycle | 4 s | Pulsing heartbeat circle + "last seen: 0.4s ago" counter. |
| `BinaryUpgradeDemo.astro` | Daimons / Auto-deploy | 6 s | Binary file rsyncs CP→host, daimon swaps in place. |
| `AgentSpecDemo.astro` | Agents / Authoring | 8 s | YAML card "compiles" into a running agent panel. |
| `ToolsGridDemo.astro` | Agents / Tools | 6 s | Grid of 5 tool tiles pulse brand-purple in sequence. |
| `FanoutStripDemo.astro` | Orchestrations / Fan-out | 6 s | Standalone fan-out card: 8-dot strip fills in left-to-right + histogram grows. |
| `ApprovalGateDemo.astro` | Orchestrations / Gates | 5 s | Single step pulses amber → click-pulse on Approve → green. |
| `BindingsDemo.astro` | Orchestrations / Branching | 7 s | Two adjacent steps; `result.pid: 12345` flies into next step's `{{step1.result.pid}}` placeholder. |
| `TriggersDemo.astro` | Orchestrations / Triggers | 6 s | Three trigger sources (finding, manual, cron) funnel into one orchestration run. |

### Static SVG components retained

Two small static SVGs survive into v2 unchanged because animation
adds no value:

- Daimons / Where it runs — three host icons each containing a
  daimon box ("one per host" visualisation).
- Agents / Built-in catalog grid — uses real `web/src/components/docs/`
  copy as a static list.

### Animation primitives (shared CSS)

A new `site/src/styles/animations.css` defines the keyframes shared
across components so the same pulse/grow/fly-in idioms aren't
copy-pasted into every demo:

```css
@media (prefers-reduced-motion: no-preference) {
  @keyframes okesu-pulse-ring {
    0%, 100% { box-shadow: 0 0 0 0 rgba(59, 130, 246, 0); }
    50%      { box-shadow: 0 0 0 4px rgba(59, 130, 246, 0.4); }
  }
  @keyframes okesu-amber-pulse {
    0%, 100% { box-shadow: 0 0 0 0 rgba(251, 191, 36, 0); }
    50%      { box-shadow: 0 0 0 4px rgba(251, 191, 36, 0.5); }
  }
  @keyframes okesu-stripe-fill {
    from { width: 0; }
    to   { width: 100%; }
  }
  @keyframes okesu-fade-in {
    from { opacity: 0; }
    to   { opacity: 1; }
  }
  @keyframes okesu-fade-out {
    from { opacity: 1; }
    to   { opacity: 0; }
  }
  @keyframes okesu-edge-dash {
    from { stroke-dashoffset: 100; }
    to   { stroke-dashoffset: 0; }
  }
  @keyframes okesu-dot-pulse {
    0%, 100% { opacity: 1; }
    50%      { opacity: 0.4; }
  }
  @keyframes okesu-typewriter {
    from { width: 0; }
    to   { width: 100%; }
  }
}
```

Each demo component composes these keyframes with its own
`animation-delay` values to sequence its loop. With reduced motion
on, the keyframes don't exist and the demo renders its final-frame
static state.

## Concept page restructuring

### Daimons (5 → 7 sections)

| Section | Demo | Source |
|---|---|---|
| What a daimon is | `DispatchFlowDemo` (anchor) | existing v1 prose, expanded |
| Where it runs | static stack-of-hosts SVG | existing v1 prose |
| Lifecycle | `HeartbeatDemo` | existing v1 prose, broken into 5 lifecycle stages with the heartbeat as visual hook |
| The dispatch contract | YAML code card with arrow into daimon (CSS pulse on the arrow) | existing v1 prose |
| **Tunnel vs jobs mode** *(new)* | static side-by-side SVG | lifted from `docs/architecture.md` |
| **Auto-deploy & upgrades** *(new)* | `BinaryUpgradeDemo` | lifted from `INSTALL.md` Phase 7 section |
| Where to next | — | existing v1 nav block |

### Agents (5 → 6 sections)

| Section | Demo | Source |
|---|---|---|
| What an agent is | `AgentJSONLDemo` (anchor) | existing v1 prose, expanded |
| The JSONL contract | small static schema diagram | existing v1 prose |
| Authoring an agent | `AgentSpecDemo` | existing v1 YAML example, animated |
| **Tools an agent can call** *(new)* | `ToolsGridDemo` | lifted from `web/src/components/docs/DocsAgents.tsx` tools section |
| Built-in agents | static catalog list | existing v1 prose |
| Where to next | — | existing v1 nav block |

### Orchestrations (6 → 7 sections)

| Section | Demo | Source |
|---|---|---|
| What an orchestration is | `OrchestrationDAGDemo` (anchor) | existing v1 prose |
| The YAML spec | YAML code card with sync-highlighted step boxes | existing v1 prose |
| Fan-out across hosts | `FanoutStripDemo` | existing v1 prose, expanded with the per-host run-canvas reference |
| Approval gates | `ApprovalGateDemo` | existing v1 prose |
| Branching on results | `BindingsDemo` | existing v1 prose |
| **Triggers** *(new)* | `TriggersDemo` | lifted from `docs/orchestrations.md` triggers section |
| Where to next | — | existing v1 nav block |

Each rewritten page lands at ~1000 words (was ~600 in v1) and ~3–5
inline demo components per page.

## Architecture

### File map

```
site/src/components/diagrams/
  old/                              (new dir — v1 SVGs preserved here)
    SystemMap.astro                 (moved from ../)
    DaimonOnHost.astro              (moved)
    AgentLifecycle.astro            (moved)
    OrchestrationFlow.astro         (moved)
  OrchestrationLoopDemo.astro       (new — home anchor)
  DispatchFlowDemo.astro            (new — daimons anchor)
  AgentJSONLDemo.astro              (new — agents anchor)
  OrchestrationDAGDemo.astro        (new — orchestrations anchor)
  HeartbeatDemo.astro               (new — inline)
  BinaryUpgradeDemo.astro           (new — inline)
  AgentSpecDemo.astro               (new — inline)
  ToolsGridDemo.astro               (new — inline)
  FanoutStripDemo.astro             (new — inline)
  ApprovalGateDemo.astro            (new — inline)
  BindingsDemo.astro                (new — inline)
  TriggersDemo.astro                (new — inline)

site/src/styles/
  animations.css                    (new — shared @keyframes)
  global.css                        (modified — imports animations.css)

site/src/pages/
  index.astro                       (modified — uses OrchestrationLoopDemo, etymology sub-tagline)
  about-the-name.astro              (new)
  concepts/
    daimons.mdx                     (rewritten — 7 sections, 3 inline demos)
    agents.mdx                      (rewritten — 6 sections, 4 inline demos)
    orchestrations.mdx              (rewritten — 7 sections, 5 inline demos)

site/src/components/
  Footer.astro                      (modified — etymology katakana line)
  Hero.astro                        (modified — etymology sub-tagline)
```

### Component shape (template)

Each `*Demo.astro` follows this skeleton:

```astro
---
// One-line description of the demo's purpose.
// All animations gated on prefers-reduced-motion: no-preference.
// Reduced-motion final state described in the static layout below.
---

<svg viewBox="..." class="w-full h-auto" role="img"
     aria-label="Animated diagram: …">
  {/* SVG geometry — final-frame state */}
</svg>

<style>
  @media (prefers-reduced-motion: no-preference) {
    /* Demo-specific @keyframes — composed from animations.css primitives
       where possible, or local where the demo needs something unique */
    .demo-step-1 { animation: okesu-pulse-ring 2s ease-in-out 0s infinite; }
    .demo-step-2 { animation: okesu-pulse-ring 2s ease-in-out 3s infinite; }
    /* etc — animation-delay sequences the loop */
  }
</style>
```

### Build & deploy

No changes from v1. Same workflow at `.github/workflows/deploy-site.yml`.
Same path filter `site/**`. Same Pages config. v2 lands by merging
the PR; the deploy workflow runs automatically.

## Performance & accessibility

### Budget

| Metric | v1 | v2 target | Hard cap |
|---|---|---|---|
| JS on home | 0 KB | 0 KB | 0 KB |
| CSS bundle (all pages) | ~12 KB | 25–35 KB | 60 KB |
| HTML weight, home | 11 KB | 25–40 KB | 100 KB |
| Lighthouse Performance (mobile) | ~95 | ≥ 85 | — |
| Lighthouse Accessibility | ~90 | ≥ 90 | — |
| Lighthouse Best Practices | ~95 | ≥ 90 | — |

### Reduced motion

Every animated component wraps its `@keyframes` declarations and any
`animation-*` rules in `@media (prefers-reduced-motion: no-preference)`.
With reduced motion on, the SVG renders its static last-frame state
— all steps green, fan-out 8/8, approval already approved. Same
information, no movement.

### Mobile

- Below Tailwind's `md` breakpoint (< 768 px), the home
  `OrchestrationLoopDemo` stacks its 4 step cards vertically.
  Animation timing is unchanged; layout flips via `md:` utility
  classes.
- Inline demo components scale to container via `viewBox` (already
  set in v1). No mobile-specific timing changes.
- Concept-page TOC hides below `md` (existing v1 behaviour).

### Accessibility

- Each animated `<svg>` carries a descriptive `aria-label`.
- Status changes (pulsing → settled) pair with non-motion cues —
  colored stripe, status label text, badge. Motion is never the
  sole signal.
- Decorative buttons inside demos (the Approve button in
  `ApprovalGateDemo`) are real `<button type="button" aria-disabled="true">`.

## Rollout

1. Merge the v2 PR to `main`.
2. `.github/workflows/deploy-site.yml` runs automatically (path
   filter `site/**` matches).
3. Build → upload → deploy. ~2 minutes.
4. Live at `https://okesu.to`.

No DNS changes. No Pages config changes. No new GitHub workflow
permissions needed.

## Testing

The site has no test runner (Phase 1 acknowledged this). Manual
verification:

1. **Local dev** — `cd site && npm run dev`. Each animated demo
   runs its loop continuously. No console warnings or errors.
2. **Build** — `cd site && npm run build` produces `dist/` clean.
   `dist/index.html` weight under 40 KB.
3. **Reduced motion** — toggle OS reduced-motion. All demos render
   their final-frame static state. No animation triggers.
4. **Mobile** — narrow browser to 360 px. Home demo stacks vertically
   and continues to animate. Concept pages remain readable; TOC
   collapses; inline demos scale.
5. **Lighthouse** — informal mobile audit on the home page; spot
   any regression below the v2 targets. Fix before merge if Performance
   drops below 85.
6. **Visual regression spot-check** — open every page in a desktop
   browser; confirm each demo renders its initial frame and begins
   its loop within 1 second of page load.

## Edge cases

- **Animation pause when tab is hidden** — modern browsers throttle
  CSS animations on hidden tabs automatically. No special handling
  needed.
- **Slow device** — CSS animations gracefully degrade in frame rate;
  the loop continues but at lower fps. Acceptable.
- **Print stylesheet** — out of scope for v2; the site isn't
  expected to print.
- **Deep-link to `/about-the-name`** — works exactly like any other
  Astro page; no special routing.
- **Adding a new demo later** — composition pattern means a new
  `*Demo.astro` drops in next to the existing nine without touching
  any shared infrastructure.

## Deferred / explicit non-goals

1. Click-to-run interactive demos (would require JS).
2. Real platform screenshots.
3. Per-feature micro-animations on the feature grid.
4. Site search (Pagefind / Algolia).
5. Analytics (Plausible / GA).
6. Blog / changelog / case studies.
7. A11y audit beyond this spec's explicit additions.
