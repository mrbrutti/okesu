# okesu.to — public website design

**Status:** approved
**Date:** 2026-04-30

## Goal

Ship a public marketing + concept-introduction site at `okesu.to`,
hosted on GitHub Pages, that explains what Okesu is and introduces
the three core concepts (Daimons, Agents, Orchestrations) to
visitors. The site uses the platform's existing brand tokens so it
feels like a continuation of the product, not a separate marketing
artefact. Phase 1 ships the site infrastructure, home page, three
concept pages, an install quick-start, and a docs landing — copy is
engineering-honest first-pass; real product screenshots and polished
marketing copy are deferred to follow-up PRs.

The domain `okesu.to` reflects the project name itself — *Okesu*
evokes the orchestra metaphor that the platform's whole mental
model rests on: a control plane is the conductor, daimons are the
players on each host, agents are the parts they perform, and
orchestrations are the score they follow.

## Non-goals

- Real platform screenshots in v1 (need a populated demo CP and a
  design pass on each shot — deferred).
- Polished marketing copy (engineer-honest is the v1 voice).
- Custom illustrations / hero artwork beyond hand-rolled SVG diagrams.
- A full per-page mirror of every `docs/*.md` styled in the site
  theme (v1 ships an index that links to GitHub-rendered markdown).
- Blog / changelog / case studies.
- Site search (Pagefind / Algolia) — add when content depth demands.
- Analytics — not in v1.
- Accessibility audit beyond Astro's defaults.

## Decisions

| # | Decision |
|---|---|
| 1 | Phase 1 cut: site shell + home + 3 concept pages + install + docs landing + GH Pages + DNS guide. Polish, screenshots, full doc mirror, and copy refinement deferred to follow-up PRs. |
| 2 | Repo placement: same monorepo at `site/`. Co-locating with the product means doc edits can ripple into the public site in one PR; one-time bloat cost is small. |
| 3 | Static-site generator: **Astro** with the official Tailwind + MDX integrations. Component-driven (matches React mental model), markdown-with-components for concept long-form, zero-JS by default, pure static output for GH Pages. |
| 4 | Image strategy: hand-rolled SVG diagrams (one home-page system map + one per concept page). Hero uses brand-50 → white gradient. No third-party illustration sources in v1. |
| 5 | Page list: `/`, `/concepts/{daimons,agents,orchestrations}`, `/docs`, `/install`. Top nav: Concepts dropdown · Docs · Install · GitHub. Footer with license + brand metaphor line. |
| 6 | DNS shape: apex (`okesu.to`) + `www` CNAME redirect → apex. GitHub Pages handles the redirect automatically when the apex is set as the canonical custom domain. |
| 7 | Copy tone: engineer-honest, second-person, declarative. Mirrors the in-app docs voice (`web/src/components/docs/*.tsx`) and the README's framing. |
| 8 | Deploy: GitHub Pages with the modern "build from Actions" source mode. Workflow lives at `.github/workflows/deploy-site.yml` with a `site/**` path filter so product-only PRs don't redeploy the site. |
| 9 | Namecheap DNS records have already been configured externally by the operator. The spec documents the expected records as a verification reference, not a how-to guide. |

## Architecture

### Layers touched

```
site/                                         (new — entire Astro project)
  astro.config.mjs
  tailwind.config.ts
  package.json
  package-lock.json
  public/
    CNAME                                     (literal: okesu.to)
    favicon.svg
    og-image.svg                              (1200×630 social share)
  src/
    layouts/
      Base.astro                              (head, nav, footer)
      Concept.astro                           (Base + sticky TOC)
    components/
      Hero.astro
      Nav.astro
      Footer.astro
      ConceptCard.astro
      FeatureGrid.astro
      diagrams/
        SystemMap.astro                       (home — 4-piece flow)
        DaimonOnHost.astro
        AgentLifecycle.astro
        OrchestrationFlow.astro               (DAG with one fan-out + one gate)
    pages/
      index.astro                             (home)
      install.astro
      docs.astro
      concepts/
        daimons.mdx
        agents.mdx
        orchestrations.mdx
      404.astro
    styles/
      global.css                              (Tailwind base + body font)

.github/workflows/deploy-site.yml             (new — Pages deploy on site/** push)

docs/superpowers/specs/2026-04-30-okesu-to-website-design.md  (this doc)
```

### Build & deploy flow

```
push to main with site/** changes
  └─> GitHub Actions: deploy-site.yml
       ├─ checkout, setup-node@20, npm ci in site/
       ├─ npm run build → site/dist/
       ├─ upload-pages-artifact (site/dist)
       └─ deploy-pages → publishes to https://okesu.to (custom domain)
```

The workflow uses the `concurrency: pages` group so back-to-back
pushes don't race. The `pages: write` and `id-token: write`
permissions are the modern Pages-deploy contract.

### Domain binding

`site/public/CNAME` contains the literal text `okesu.to`. GitHub
Pages reads this on each deploy and binds the custom domain. The
GitHub Pages settings UI shows `okesu.to` as the canonical custom
domain; `www.okesu.to` 301-redirects to apex.

## Design system

The site mirrors `web/tailwind.config.ts`'s tokens 1:1:

```ts
colors: {
  bg:     '#fafafa',
  panel:  '#ffffff',
  border: '#e5e7eb',
  ink:    { DEFAULT: '#0f172a', dim: '#64748b', mute: '#94a3b8' },
  brand:  { 50: '#f5f3ff', 100: '#ede9fe', 500: '#7c3aed', 600: '#6d28d9', 700: '#5b21b6' },
}
fontFamily: {
  sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'system-ui', 'sans-serif'],
  mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
}
boxShadow: {
  card: '0 1px 2px rgba(15, 23, 42, 0.04), 0 0 0 1px rgba(15, 23, 42, 0.06)',
}
```

The same `border-border`, `bg-panel`, `text-ink-mute`, etc. utility
classes work in both codebases — a developer fluent in one is
fluent in the other.

### Logo + brand

- **Logo:** the same `bg-brand-500` rounded square with a centered
  white dot used in the platform sidebar's brand block. Rendered as
  an inline SVG component, not a bitmap.
- **Wordmark:** "Okesu" in `font-semibold text-ink`, no italic, no
  shadow.
- **Sub-tag** (only when explaining the name, e.g. footer):
  "Okesu — *orchestrate your defense*." (mirrors the orchestra
  metaphor without claiming a specific Japanese translation; the
  exact kanji/etymology framing can be added in a follow-up if/when
  finalised by the project lead).

### Hero (home)

- Background: `bg-gradient-to-b from-brand-50 via-white to-white`.
- Headline: ~52px, tight tracking, the phrase "like an orchestra"
  rendered with a `from-brand-600 to-brand-700` text gradient.
- Sub: ~17px, `text-ink-dim`, max-width ~620px.
- Primary CTA `Install Okesu →` → `/install`. Secondary `View on GitHub →`.
- Below the fold: the `SystemMap` diagram showing CP → Daimons →
  Agents → Orchestrations as four iconified nodes joined by arrows.

### Concept cards (home, below hero)

Three-column grid:

```
[ ▦ Daimons     ] [ ✦ Agents          ] [ ⊞ Orchestrations ]
  one-line tag      one-line tag         one-line tag
  "Learn more →"    "Learn more →"        "Learn more →"
```

Cards: `bg-white border border-border rounded-lg shadow-card hover:border-brand-100 hover:shadow-md`,
icon in brand-50 tile.

### Concept page template (`Concept.astro`)

- Sticky top nav (same as home).
- Above 768 px: 220-px left sidebar with the page's section TOC
  (`text-ink-dim`, `text-brand-700` + left-border on the active
  section), 720-px content column.
- Header band: 44-px brand-50 icon tile + `text-brand-700` h1 +
  `text-ink-dim` one-line subtitle.
- Anchor diagram immediately after the header (concept-specific SVG).
- Body: h2-section copy, code blocks (`bg-ink text-slate-300`
  rounded-md font-mono), inline `<span class="term">` highlights
  in `text-brand-700 font-semibold`.
- Footer block: "Where to next" — links to the other two concepts
  + the install guide.
- Below 768 px: TOC collapses into a top-of-page jump menu; content
  column gets full width.

### Code-sample treatment

```
.code-block { background: var(--ink); color: #cbd5e1; padding: 14px 18px;
              border-radius: 8px; font-family: ui-monospace; font-size: 12px; }
.code-block .key { color: #c4b5fd; }   /* brand-300 */
.code-block .str { color: #a7f3d0; }   /* emerald-200 */
.code-block .cmt { color: var(--ink-mute); }
```

Used on the concept pages and the install page. Astro's syntax
highlighter (Shiki) is overkill for the small number of YAML/shell
samples we ship in v1; we hand-roll the highlighting via these
classes. (A follow-up can swap in Shiki when the doc mirror lands.)

## Pages

### `/` — Home

Sections, top to bottom:

1. **Hero** (`from-brand-50` gradient) — headline + sub + two CTAs.
2. **System map diagram** (`SystemMap.astro`) — CP → Daimons →
   Agents → Orchestrations, four iconified nodes with arrows.
3. **Concept cards** — three-column, each linking to its concept page.
4. **Features list** — two-column grid, each row is a brand-700
   checkmark + h4 + one-line description. Items in v1:
   - Federated control plane
   - Multi-provider agents (Claude / Codex / your own)
   - Investigations as a workspace
   - Approval gates + action allowlists
   - Per-host fan-out (one of the project's signature shapes)
   - Single-binary install
5. **Footer** — license, GitHub link, brand metaphor line.

### `/concepts/daimons`

Sections:
- What a daimon is (one paragraph).
- Where it runs (single binary on each host; ports; tunnel/jobs
  modes).
- Lifecycle (install → register → heartbeat → execute runs →
  upgrade-in-place).
- The dispatch contract (what the daimon receives, what it returns).
- "Where to next" → Agents · Install.

Anchor diagram: `DaimonOnHost.astro` — host box with the daimon
process inside, an arrow showing a dispatched run coming in from
the CP and a JSONL stream going back out.

Source material: `web/src/components/docs/DocsDaimons.tsx`,
`docs/daemon.design.md`, `docs/README.daemon.md`.

### `/concepts/agents`

Sections:
- What an agent is (prompt-driven worker, multiple providers).
- The JSONL contract (what an agent emits, how findings are
  structured, the `orchestration_result` category).
- Authoring an agent (markdown spec format, prompt templating,
  `nodes: []` etc).
- Built-in agents (a non-exhaustive list).
- "Where to next" → Daimons · Orchestrations.

Anchor diagram: `AgentLifecycle.astro` — prompt → run → JSONL →
finding flow, with a finding card on the right showing the shape.

Source material: `web/src/components/docs/DocsAgents.tsx`,
`agents/*.md` (the agent spec catalog).

### `/concepts/orchestrations`

Sections:
- What an orchestration is.
- The YAML spec (canonical example).
- Fan-out across hosts (`nodes: [...]` — references the per-host
  card on the run-detail page).
- Approval gates (`approval: required`).
- Branching on results (binding `{{stepN.result.field}}`).
- "Where to next" → Daimons · Agents · Install.

Anchor diagram: `OrchestrationFlow.astro` — a 4-step DAG with one
fan-out node (purple-tinted) and one approval-gated node (red-tinted).

Source material: `web/src/components/docs/DocsOrchestrations.tsx`,
`docs/orchestrations.md`, the spec format `orchestrator/spec.go`.

### `/docs`

Single landing page. Three columns mirroring the in-app docs tabs:

- **Orchestrations** — short intro + link to `/concepts/orchestrations`
  + link to `docs/orchestrations.md` on GitHub for full reference.
- **Daimons** — same shape.
- **Agents** — same shape.

Below: a "Reference" section listing the markdown files in
`docs/*.md` with one-line summaries, each link pointing at the
GitHub-rendered file.

This page is intentionally thin in v1 — its job is to be a stable
URL operators can bookmark while the styled doc mirror is built in
follow-up PRs.

### `/install`

Sections:
- Install the binary (single shell command — `curl ... | sh` or
  Homebrew when available; v1 ships the curl flow).
- Bootstrap a CP (`okesu cp init` + `okesu cp serve`).
- Deploy a daimon to a host (the `okesu node add` + auto-deploy
  flow).
- Write your first orchestration (3-step YAML example, run it via
  the dashboard).
- Production checklist (database choice, federation, ports).

Source material: `INSTALL.md`, `docs/architecture.md` install
sections.

### `404`

Astro default 404 with the brand chrome (top nav, footer, hero
gradient, "Page not found — back to home" message).

## DNS — verification reference

Namecheap records are already configured externally. For the
record, the expected configuration is:

| Type    | Host  | Value                | Purpose |
| ------- | ----- | -------------------- | --- |
| A Record | @     | 185.199.108.153      | GitHub Pages apex |
| A Record | @     | 185.199.109.153      | GitHub Pages apex |
| A Record | @     | 185.199.110.153      | GitHub Pages apex |
| A Record | @     | 185.199.111.153      | GitHub Pages apex |
| CNAME    | www   | mrbrutti.github.io.  | Redirect www → apex |

Verification commands (run after DNS propagates and the site is
deployed):

```bash
dig +short okesu.to                  # expect 4 IPs in the 185.199.108–111.153 block
dig +short www.okesu.to              # expect mrbrutti.github.io.
curl -I https://okesu.to             # expect 200 once HTTPS provisions
```

The HTTPS certificate is auto-provisioned by GitHub via Let's
Encrypt once the apex resolves to GitHub's IPs. Provisioning
typically takes 10–30 minutes after the first successful DNS
lookup.

## Rollout

1. Land this spec + the implementation plan in main.
2. Implementation PR adds `site/` and `.github/workflows/deploy-site.yml`.
3. After PR merges:
   - Operator: GitHub repo Settings → Pages → Source: GitHub Actions, Custom domain: `okesu.to`.
   - The deploy workflow re-runs and publishes.
   - Operator: tick "Enforce HTTPS" once the cert provisions (~10–30 min).
4. Verify with `dig` + `curl`.
5. Iterate on copy / add screenshots in follow-up PRs.

## Testing

The site has no test suite. Verification is manual:

1. **Local dev** — `cd site && npm run dev` serves the site at
   `localhost:4321`. All pages render, all internal links resolve,
   no console errors.
2. **Build** — `cd site && npm run build` produces `site/dist/`
   without errors. Spot-check `dist/index.html`, `dist/concepts/daimons/index.html`, `dist/CNAME`.
3. **Lighthouse** (informal) — run Chrome's Lighthouse on the
   built `dist/index.html`; flag anything below 90 in
   accessibility / best-practices for follow-up.
4. **Mobile** — narrow the browser to 360 px wide. Hero stacks
   gracefully, concept cards become a vertical list, TOC
   collapses on concept pages.
5. **Custom domain** — after deploy, `dig okesu.to` resolves and
   `https://okesu.to` returns the home page with a valid cert.

## Edge cases

- **Workflow runs on a `site/`-less PR** — the `paths:` filter
  prevents this; the workflow simply doesn't run.
- **Operator pushes a broken Astro change** — the build job fails
  in CI; the deploy job is gated on build success, so the live
  site stays on the last good deploy.
- **CNAME file gets deleted** — GitHub Pages drops the custom
  domain on the next deploy and the site reverts to
  `mrbrutti.github.io/okesu`. Acceptance: the file lives in
  `site/public/CNAME` (which Astro copies to `dist/`); deleting it
  requires a deliberate edit, and PR review will catch it.
- **Brand token drift** — the platform's `web/tailwind.config.ts`
  evolves; `site/tailwind.config.ts` doesn't track it
  automatically. v1 accepts this — drift will be small and
  reviewed manually if/when it diverges. Future could share a
  config via npm workspace, but YAGNI for now.

## Deferred / explicit non-goals (recap)

1. Real product screenshots — needs a populated demo CP.
2. Polished marketing copy — first cut is engineer-honest; a writer
   pass is a follow-up.
3. Per-page styled docs mirroring `docs/*.md` — v1 indexes them.
4. Blog / changelog / case studies.
5. Site search.
6. Analytics.
7. A11y audit beyond Astro defaults.
8. Shared Tailwind config between `web/` and `site/` (token drift
   accepted in v1).
