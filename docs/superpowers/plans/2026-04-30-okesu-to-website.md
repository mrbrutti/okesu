# okesu.to public website — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a public marketing + concept-introduction site at `okesu.to`, hosted on GitHub Pages from the monorepo's new `site/` directory, with home + 3 concept pages + install + docs index, brand-aligned with the platform.

**Architecture:** Astro static-site generator with the Tailwind + MDX integrations, brand tokens mirrored 1:1 from `web/tailwind.config.ts`. Hand-rolled SVG diagrams (no third-party illustration sources). GitHub Pages "build from Actions" mode via a workflow that runs only on `site/**` changes. Custom domain bound via `site/public/CNAME`.

**Tech Stack:** Astro 5, Tailwind 3, MDX, TypeScript, lucide icons (via inline SVG), GitHub Actions, GitHub Pages.

**Spec:** `docs/superpowers/specs/2026-04-30-okesu-to-website-design.md`

---

## File structure

**Created (entire `site/` directory + workflow):**

```
site/
  .gitignore
  astro.config.mjs
  package.json
  package-lock.json
  tailwind.config.ts
  tsconfig.json
  public/
    CNAME
    favicon.svg
    og-image.svg
  src/
    env.d.ts
    layouts/
      Base.astro
      Concept.astro
    components/
      Nav.astro
      Footer.astro
      Hero.astro
      ConceptCard.astro
      FeatureGrid.astro
      diagrams/
        SystemMap.astro
        DaimonOnHost.astro
        AgentLifecycle.astro
        OrchestrationFlow.astro
    pages/
      index.astro
      install.astro
      docs.astro
      404.astro
      concepts/
        daimons.mdx
        agents.mdx
        orchestrations.mdx
    styles/
      global.css

.github/workflows/deploy-site.yml
```

15 created files in `site/` plus 1 workflow. No existing files modified.

---

## Task 1: Astro project scaffold

**Files:**
- Create: `site/package.json`, `site/tsconfig.json`, `site/astro.config.mjs`, `site/src/env.d.ts`, `site/.gitignore`

We bypass `npm create astro@latest`'s interactive scaffold (it wants its own directory) and write the files directly. Astro 5.0+ uses ESM config, `output: 'static'` by default.

- [ ] **Step 1: Create `site/.gitignore`**

```gitignore
# Astro build artefacts
dist/
.astro/

# Node
node_modules/

# Local env
.env
.env.local

# Editor
.DS_Store
.vscode/
```

- [ ] **Step 2: Create `site/package.json`**

```json
{
  "name": "okesu-site",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "astro dev",
    "build": "astro build",
    "preview": "astro preview",
    "astro": "astro"
  },
  "dependencies": {
    "@astrojs/check": "^0.9.4",
    "@astrojs/mdx": "^4.2.0",
    "@astrojs/sitemap": "^3.4.0",
    "@astrojs/tailwind": "^5.1.5",
    "astro": "^5.6.0",
    "tailwindcss": "^3.4.17",
    "typescript": "^5.7.3"
  }
}
```

- [ ] **Step 3: Create `site/tsconfig.json`**

```json
{
  "extends": "astro/tsconfigs/strict",
  "compilerOptions": {
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] }
  },
  "include": [".astro/types.d.ts", "**/*"],
  "exclude": ["dist"]
}
```

- [ ] **Step 4: Create `site/src/env.d.ts`**

```ts
/// <reference path="../.astro/types.d.ts" />
```

- [ ] **Step 5: Create `site/astro.config.mjs`**

```js
import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';
import mdx from '@astrojs/mdx';
import sitemap from '@astrojs/sitemap';

// Astro config for okesu.to.
//
// site:    canonical URL — feeds <link rel="canonical">, sitemap, OG tags.
// output:  static — pure HTML/JS, GitHub Pages serves dist/ directly.
// trailingSlash: never — GitHub Pages serves /concepts/daimons.html cleanly
//          without a trailing slash; matches what the CNAME-bound domain
//          resolves to in practice.
export default defineConfig({
  site: 'https://okesu.to',
  output: 'static',
  trailingSlash: 'never',
  integrations: [
    tailwind({ applyBaseStyles: true }),
    mdx(),
    sitemap(),
  ],
});
```

- [ ] **Step 6: Install dependencies + verify Astro builds the empty project**

```bash
cd site
npm install
```

Expected: completes with no peer-dep errors. Creates `package-lock.json`.

```bash
npm run build
```

Expected: Astro reports `0 page(s) built` (no `src/pages/` content yet) — the build still succeeds and `dist/` is created (probably with just the sitemap stub). If Astro errors because `src/pages/` is empty, that's fine — Task 5 lands the first page; we confirm the toolchain runs end-to-end then.

If the empty-pages error blocks the build, stub a tiny `src/pages/index.astro` with `<h1>okesu.to</h1>` for this task; Task 5 will overwrite it.

- [ ] **Step 7: Commit**

```bash
git add site/.gitignore site/package.json site/package-lock.json site/tsconfig.json site/astro.config.mjs site/src/env.d.ts
git commit -m "$(cat <<'EOF'
site(scaffold): Astro 5 project with Tailwind + MDX + sitemap integrations

Bare Astro config wired to canonical https://okesu.to. Brand tokens
and base styles arrive in the next commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Tailwind config + global stylesheet

**Files:**
- Create: `site/tailwind.config.ts`, `site/src/styles/global.css`

Brand tokens mirror `web/tailwind.config.ts` 1:1 so the site reads as a continuation of the platform.

- [ ] **Step 1: Create `site/tailwind.config.ts`**

```ts
import type { Config } from 'tailwindcss';

// Brand tokens are an exact mirror of web/tailwind.config.ts. Keep
// them in sync manually for v1; a future workspace package can host
// the shared config if drift becomes a problem.
export default {
  content: ['./src/**/*.{astro,html,ts,tsx,mdx}'],
  theme: {
    extend: {
      colors: {
        bg: '#fafafa',
        panel: '#ffffff',
        border: '#e5e7eb',
        ink: {
          DEFAULT: '#0f172a',
          dim: '#64748b',
          mute: '#94a3b8',
        },
        brand: {
          50:  '#f5f3ff',
          100: '#ede9fe',
          500: '#7c3aed',
          600: '#6d28d9',
          700: '#5b21b6',
        },
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'system-ui', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      boxShadow: {
        card: '0 1px 2px rgba(15, 23, 42, 0.04), 0 0 0 1px rgba(15, 23, 42, 0.06)',
      },
      maxWidth: {
        'content': '720px',
        'page': '1120px',
      },
    },
  },
  plugins: [],
} satisfies Config;
```

- [ ] **Step 2: Create `site/src/styles/global.css`**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

/* Inter is loaded from Google Fonts in the Base layout. The system
   font stack is the fallback. */

html {
  scroll-behavior: smooth;
}

body {
  background: theme('colors.bg');
  color: theme('colors.ink.DEFAULT');
  font-family: theme('fontFamily.sans');
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

/* Code blocks rendered via the .code-block class throughout the site
   share these styles so MDX content + Astro components read the
   same way. */
.code-block {
  background: theme('colors.ink.DEFAULT');
  color: #cbd5e1;
  border-radius: theme('borderRadius.lg');
  padding: 14px 18px;
  font-family: theme('fontFamily.mono');
  font-size: 12px;
  line-height: 1.65;
  overflow-x: auto;
}
.code-block .key { color: #c4b5fd; }   /* brand-300-ish */
.code-block .str { color: #a7f3d0; }   /* emerald-200 */
.code-block .cmt { color: theme('colors.ink.mute'); }

/* The Concept page's body uses a constrained .prose-like rule that
   doesn't pull in @tailwindcss/typography (we only need a few rules,
   the plugin is overkill). */
.concept-prose h2 {
  font-size: 19px;
  font-weight: 600;
  color: theme('colors.ink.DEFAULT');
  margin: 28px 0 10px;
  scroll-margin-top: 80px;
}
.concept-prose p {
  font-size: 14px;
  line-height: 1.65;
  margin-bottom: 12px;
}
.concept-prose .term {
  font-weight: 600;
  color: theme('colors.brand.700');
}
.concept-prose ul {
  list-style: disc;
  padding-left: 20px;
  margin-bottom: 12px;
  font-size: 14px;
  line-height: 1.65;
}
.concept-prose code {
  background: theme('colors.brand.50');
  color: theme('colors.brand.700');
  padding: 1px 5px;
  border-radius: 4px;
  font-family: theme('fontFamily.mono');
  font-size: 12px;
}
.concept-prose .code-block code {
  background: transparent;
  color: inherit;
  padding: 0;
}
.concept-prose a {
  color: theme('colors.brand.700');
  font-weight: 500;
}
.concept-prose a:hover {
  color: theme('colors.brand.600');
  text-decoration: underline;
}
```

- [ ] **Step 3: Verify Tailwind compiles**

```bash
cd site
npm run build
```

Expected: build succeeds. Astro will warn about no pages but the Tailwind compilation step runs cleanly. If you stubbed `index.astro` in Task 1 step 6, the page still builds.

- [ ] **Step 4: Commit**

```bash
git add site/tailwind.config.ts site/src/styles/global.css
git commit -m "$(cat <<'EOF'
site(theme): Tailwind config + global stylesheet

Brand tokens mirror web/tailwind.config.ts exactly. .code-block and
.concept-prose are the two named components used by MDX content.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Base layout, Nav, Footer

**Files:**
- Create: `site/src/layouts/Base.astro`, `site/src/components/Nav.astro`, `site/src/components/Footer.astro`

The Base layout owns `<head>`, top nav, footer, and the global stylesheet import. Every page wraps its content in Base.

- [ ] **Step 1: Create `site/src/components/Nav.astro`**

```astro
---
// Top navigation. Sticky, white panel with bottom border. The
// brand logo links home; the right-side CTA links to GitHub.
//
// Active-route highlighting uses Astro.url.pathname matching at
// the segment level — not exact, so /concepts/daimons activates
// the "Concepts" link.

const path = Astro.url.pathname;
function isActive(prefix: string): boolean {
  if (prefix === '/') return path === '/';
  return path === prefix || path.startsWith(prefix + '/');
}
---

<header class="sticky top-0 z-40 bg-panel/80 backdrop-blur border-b border-border">
  <nav class="max-w-page mx-auto flex items-center gap-6 px-6 py-3">
    <a href="/" class="flex items-center gap-2 group">
      <span class="w-7 h-7 rounded-md bg-brand-500 flex items-center justify-center transition-transform group-hover:scale-105">
        <span class="w-3 h-3 rounded-full bg-white"></span>
      </span>
      <span class="font-semibold text-ink text-[15px]">Okesu</span>
    </a>
    <div class="hidden md:flex items-center gap-5 text-sm text-ink-dim">
      <a
        href="/concepts/daimons"
        class={`hover:text-ink transition-colors ${isActive('/concepts') ? 'text-brand-700 font-medium' : ''}`}
      >Concepts</a>
      <a
        href="/docs"
        class={`hover:text-ink transition-colors ${isActive('/docs') ? 'text-brand-700 font-medium' : ''}`}
      >Docs</a>
      <a
        href="/install"
        class={`hover:text-ink transition-colors ${isActive('/install') ? 'text-brand-700 font-medium' : ''}`}
      >Install</a>
    </div>
    <a
      href="https://github.com/mrbrutti/okesu"
      class="ml-auto inline-flex items-center gap-1.5 bg-ink hover:bg-ink/90 text-white text-xs font-medium px-3 py-1.5 rounded-md"
      target="_blank"
      rel="noopener"
    >
      <svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>
      GitHub
    </a>
  </nav>
</header>
```

- [ ] **Step 2: Create `site/src/components/Footer.astro`**

```astro
---
const year = new Date().getFullYear();
---

<footer class="border-t border-border bg-panel mt-24">
  <div class="max-w-page mx-auto px-6 py-6 flex flex-col md:flex-row justify-between gap-3 text-[11px] text-ink-mute">
    <div>
      © {year} Okesu · Apache 2.0 · <a class="hover:text-ink-dim" href="https://github.com/mrbrutti/okesu">github.com/mrbrutti/okesu</a>
    </div>
    <div>
      Okesu — <em>orchestrate your defense.</em>
    </div>
  </div>
</footer>
```

- [ ] **Step 3: Create `site/src/layouts/Base.astro`**

```astro
---
import '../styles/global.css';
import Nav from '../components/Nav.astro';
import Footer from '../components/Footer.astro';

interface Props {
  title: string;
  description?: string;
  ogImage?: string;
}

const { title, description, ogImage = '/og-image.svg' } = Astro.props;
const fullTitle = title === 'Okesu' ? title : `${title} — Okesu`;
const url = new URL(Astro.url.pathname, Astro.site).toString();
---

<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="generator" content={Astro.generator} />
    <link rel="icon" href="/favicon.svg" type="image/svg+xml" />

    <title>{fullTitle}</title>
    {description && <meta name="description" content={description} />}

    <link rel="canonical" href={url} />

    {/* Open Graph */}
    <meta property="og:type" content="website" />
    <meta property="og:url" content={url} />
    <meta property="og:title" content={fullTitle} />
    {description && <meta property="og:description" content={description} />}
    <meta property="og:image" content={new URL(ogImage, Astro.site).toString()} />

    {/* Twitter */}
    <meta name="twitter:card" content="summary_large_image" />
    <meta name="twitter:title" content={fullTitle} />
    {description && <meta name="twitter:description" content={description} />}
    <meta name="twitter:image" content={new URL(ogImage, Astro.site).toString()} />

    {/* Inter font */}
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet" />
  </head>
  <body class="min-h-screen flex flex-col">
    <Nav />
    <main class="flex-1">
      <slot />
    </main>
    <Footer />
  </body>
</html>
```

- [ ] **Step 4: Stub a working homepage to verify**

If you stubbed `site/src/pages/index.astro` in Task 1, replace its content with:

```astro
---
import Base from '../layouts/Base.astro';
---
<Base title="Okesu" description="Orchestrate your security team like an orchestra.">
  <div class="max-w-page mx-auto px-6 py-16">
    <h1 class="text-4xl font-bold">Okesu placeholder home</h1>
    <p class="mt-3 text-ink-dim">Real home page lands in Task 5.</p>
  </div>
</Base>
```

If `index.astro` doesn't exist yet, create it with the above content as a stub.

- [ ] **Step 5: Verify the page renders with the chrome**

```bash
cd site
npm run build
```

Expected: build succeeds. `dist/index.html` contains the brand square logo, the nav, the footer, and the placeholder body. Inspect it:

```bash
grep -E 'Okesu|footer|brand-500' dist/index.html | head -10
```

Expected: hits on the brand class and the wordmark.

- [ ] **Step 6: Commit**

```bash
git add site/src/layouts/Base.astro site/src/components/Nav.astro site/src/components/Footer.astro site/src/pages/index.astro
git commit -m "$(cat <<'EOF'
site(layout): Base layout + Nav + Footer chrome

Sticky top nav with active-route highlighting, brand square logo,
GitHub CTA. Footer shows the orchestra metaphor + Apache 2.0 + repo
link. Base layout owns <head> tags incl. OG/Twitter metadata.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: SVG diagrams

**Files:**
- Create: `site/src/components/diagrams/SystemMap.astro`
- Create: `site/src/components/diagrams/DaimonOnHost.astro`
- Create: `site/src/components/diagrams/AgentLifecycle.astro`
- Create: `site/src/components/diagrams/OrchestrationFlow.astro`

Four hand-rolled SVG components. Each is purely visual — no props, no client-side JS. They use the brand palette via inline `fill`/`stroke` attributes (Astro doesn't compile Tailwind classes inside `<svg>` reliably across all SVG presentation attrs, so we use literal hex values referencing the same tokens).

Brand hex shortcuts used in all four:
- ink: `#0f172a`
- ink-mute: `#94a3b8`
- ink-dim: `#64748b`
- border: `#e5e7eb`
- brand-50: `#f5f3ff`
- brand-100: `#ede9fe`
- brand-500: `#7c3aed`
- brand-600: `#6d28d9`
- brand-700: `#5b21b6`

- [ ] **Step 1: Create `site/src/components/diagrams/SystemMap.astro`**

The home-page anchor diagram. Four iconified nodes (CP / Daimons / Agents / Orchestrations) connected by arrows. ~700 px wide, ~180 px tall. Responsive via `viewBox`.

```astro
---
// Home-page system map. Renders the four-piece flow:
//   Control Plane → Daimons → Agents → Orchestrations
// Each node is a brand-50 rounded square with a glyph icon plus a
// label below. Arrows are ink-mute. The diagram is a single static
// SVG — no JS, scales with the container.
---

<svg
  viewBox="0 0 760 200"
  class="w-full h-auto"
  role="img"
  aria-label="Diagram: Control Plane dispatches to Daimons running on hosts, which execute Agents that emit findings into Orchestrations."
>
  <!-- nodes -->
  <g>
    {/* Control Plane */}
    <rect x="20"  y="50" width="120" height="80" rx="10" fill="#f5f3ff" stroke="#ede9fe" />
    <text x="80" y="84" text-anchor="middle" font-size="22" fill="#5b21b6" font-family="ui-monospace">⌬</text>
    <text x="80" y="158" text-anchor="middle" font-size="13" fill="#0f172a" font-weight="600">Control Plane</text>

    {/* Daimons */}
    <rect x="200" y="50" width="120" height="80" rx="10" fill="#f5f3ff" stroke="#ede9fe" />
    <text x="260" y="84" text-anchor="middle" font-size="22" fill="#5b21b6" font-family="ui-monospace">▦</text>
    <text x="260" y="158" text-anchor="middle" font-size="13" fill="#0f172a" font-weight="600">Daimons</text>

    {/* Agents */}
    <rect x="380" y="50" width="120" height="80" rx="10" fill="#f5f3ff" stroke="#ede9fe" />
    <text x="440" y="86" text-anchor="middle" font-size="22" fill="#5b21b6" font-family="ui-monospace">✦</text>
    <text x="440" y="158" text-anchor="middle" font-size="13" fill="#0f172a" font-weight="600">Agents</text>

    {/* Orchestrations */}
    <rect x="560" y="50" width="180" height="80" rx="10" fill="#f5f3ff" stroke="#ede9fe" />
    <text x="650" y="84" text-anchor="middle" font-size="22" fill="#5b21b6" font-family="ui-monospace">⊞</text>
    <text x="650" y="158" text-anchor="middle" font-size="13" fill="#0f172a" font-weight="600">Orchestrations</text>
  </g>

  {/* arrows */}
  <g stroke="#94a3b8" stroke-width="2" fill="none" marker-end="url(#arrow-tip)">
    <line x1="142" y1="90" x2="200" y2="90" />
    <line x1="322" y1="90" x2="380" y2="90" />
    <line x1="502" y1="90" x2="560" y2="90" />
  </g>

  <defs>
    <marker id="arrow-tip" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
    </marker>
  </defs>
</svg>
```

- [ ] **Step 2: Create `site/src/components/diagrams/DaimonOnHost.astro`**

Concept-page anchor for Daimons. A host outline with the daimon process inside, an arrow showing a dispatched run coming in from the CP and a JSONL stream going back out.

```astro
---
// Daimons concept-page anchor. Host box with the daimon process
// inside, dispatch + JSONL arrows in/out.
---

<svg viewBox="0 0 720 240" class="w-full h-auto" role="img" aria-label="Diagram: a daimon runs on a host, receives dispatched runs from the CP, and streams JSONL findings back.">
  {/* CP marker */}
  <rect x="20" y="100" width="100" height="40" rx="6" fill="#f5f3ff" stroke="#ede9fe" />
  <text x="70" y="125" text-anchor="middle" font-size="12" fill="#5b21b6" font-weight="600">Control Plane</text>

  {/* Host box */}
  <rect x="240" y="40" width="320" height="160" rx="10" fill="#fafafa" stroke="#e5e7eb" stroke-dasharray="4 3" />
  <text x="252" y="60" font-size="11" fill="#94a3b8" font-family="ui-monospace">host: web-prod-01</text>

  {/* Daimon process inside the host */}
  <rect x="280" y="80" width="240" height="100" rx="8" fill="#ffffff" stroke="#e5e7eb" />
  <text x="400" y="110" text-anchor="middle" font-size="13" fill="#0f172a" font-weight="600">daimon (okesu)</text>
  <text x="400" y="132" text-anchor="middle" font-size="11" fill="#64748b">long-running on each host</text>
  <text x="400" y="154" text-anchor="middle" font-size="11" fill="#64748b">listens for run dispatches</text>
  <text x="400" y="172" text-anchor="middle" font-size="11" fill="#64748b">emits findings as JSONL</text>

  {/* CP → daimon arrow */}
  <line x1="120" y1="115" x2="280" y2="115" stroke="#94a3b8" stroke-width="2" marker-end="url(#arrow-tip-2)" />
  <text x="200" y="106" text-anchor="middle" font-size="10" fill="#5b21b6">dispatch run</text>

  {/* daimon → CP return */}
  <line x1="280" y1="155" x2="120" y2="155" stroke="#94a3b8" stroke-width="2" marker-end="url(#arrow-tip-2)" />
  <text x="200" y="172" text-anchor="middle" font-size="10" fill="#5b21b6">findings (JSONL)</text>

  {/* finding card on the right */}
  <rect x="600" y="80" width="100" height="80" rx="6" fill="#ffffff" stroke="#e5e7eb" />
  <rect x="600" y="80" width="100" height="3" fill="#7c3aed" />
  <text x="650" y="106" text-anchor="middle" font-size="10" fill="#0f172a" font-weight="600">finding</text>
  <text x="650" y="124" text-anchor="middle" font-size="9" fill="#64748b">severity: high</text>
  <text x="650" y="138" text-anchor="middle" font-size="9" fill="#64748b">title: …</text>
  <line x1="520" y1="130" x2="600" y2="120" stroke="#94a3b8" stroke-width="1" stroke-dasharray="3 3" />

  <defs>
    <marker id="arrow-tip-2" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
    </marker>
  </defs>
</svg>
```

- [ ] **Step 3: Create `site/src/components/diagrams/AgentLifecycle.astro`**

```astro
---
// Agents concept-page anchor. Prompt → run → JSONL → finding.
---

<svg viewBox="0 0 720 200" class="w-full h-auto" role="img" aria-label="Diagram: agent lifecycle — a prompt drives an LLM run, which streams JSONL events that resolve into findings.">
  {/* prompt */}
  <rect x="20" y="60" width="140" height="80" rx="8" fill="#ffffff" stroke="#e5e7eb" />
  <rect x="20" y="60" width="140" height="3" fill="#7c3aed" />
  <text x="90" y="92" text-anchor="middle" font-size="12" fill="#0f172a" font-weight="600">Prompt</text>
  <text x="90" y="112" text-anchor="middle" font-size="10" fill="#64748b">+ tools</text>
  <text x="90" y="126" text-anchor="middle" font-size="10" fill="#64748b">+ inputs</text>

  {/* run */}
  <rect x="220" y="60" width="180" height="80" rx="8" fill="#f5f3ff" stroke="#ede9fe" />
  <text x="310" y="92" text-anchor="middle" font-size="12" fill="#5b21b6" font-weight="600">Run (LLM loop)</text>
  <text x="310" y="112" text-anchor="middle" font-size="10" fill="#64748b">Claude · Codex · custom</text>
  <text x="310" y="126" text-anchor="middle" font-size="10" fill="#64748b">tool calls + reasoning</text>

  {/* JSONL stream */}
  <rect x="460" y="60" width="120" height="80" rx="8" fill="#ffffff" stroke="#e5e7eb" />
  <text x="520" y="92" text-anchor="middle" font-size="12" fill="#0f172a" font-weight="600">JSONL events</text>
  <text x="520" y="112" text-anchor="middle" font-size="10" fill="#64748b">finding</text>
  <text x="520" y="126" text-anchor="middle" font-size="10" fill="#64748b">orchestration_result</text>

  {/* finding */}
  <rect x="630" y="60" width="80" height="80" rx="8" fill="#ffffff" stroke="#e5e7eb" />
  <rect x="630" y="60" width="80" height="3" fill="#22c55e" />
  <text x="670" y="92" text-anchor="middle" font-size="12" fill="#0f172a" font-weight="600">Finding</text>
  <text x="670" y="112" text-anchor="middle" font-size="10" fill="#64748b">in CP</text>

  {/* arrows */}
  <g stroke="#94a3b8" stroke-width="2" fill="none" marker-end="url(#arrow-tip-3)">
    <line x1="160" y1="100" x2="220" y2="100" />
    <line x1="400" y1="100" x2="460" y2="100" />
    <line x1="580" y1="100" x2="630" y2="100" />
  </g>

  <defs>
    <marker id="arrow-tip-3" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
    </marker>
  </defs>
</svg>
```

- [ ] **Step 4: Create `site/src/components/diagrams/OrchestrationFlow.astro`**

A 4-step DAG with one fan-out and one approval-gated step. Mirrors the run-canvas idiom.

```astro
---
// Orchestrations concept-page anchor. 4-step DAG with a fan-out
// step (purple-tinted) and an approval-gated step (red-tinted).
---

<svg viewBox="0 0 760 200" class="w-full h-auto" role="img" aria-label="Diagram: orchestration DAG — scan, fan-out edr-triage on 6 hosts, summarize, approval-gated contain.">
  {/* step 1: scan */}
  <rect x="20" y="70" width="120" height="60" rx="8" fill="#ffffff" stroke="#e5e7eb" />
  <rect x="20" y="70" width="120" height="3" fill="#22c55e" />
  <text x="80" y="98" text-anchor="middle" font-size="12" fill="#0f172a" font-weight="600">scan</text>
  <text x="80" y="116" text-anchor="middle" font-size="10" fill="#64748b">1 host</text>

  {/* step 2: fanout edr-triage */}
  <rect x="200" y="50" width="160" height="100" rx="8" fill="#f5f3ff" stroke="#ede9fe" />
  <rect x="200" y="50" width="160" height="3" fill="#7c3aed" />
  <text x="280" y="78" text-anchor="middle" font-size="12" fill="#5b21b6" font-weight="600">edr-triage</text>
  <text x="280" y="98" text-anchor="middle" font-size="10" fill="#64748b">6 hosts (fan-out)</text>
  <g>
    <circle cx="220" cy="120" r="3" fill="#22c55e" />
    <circle cx="232" cy="120" r="3" fill="#22c55e" />
    <circle cx="244" cy="120" r="3" fill="#22c55e" />
    <circle cx="256" cy="120" r="3" fill="#22c55e" />
    <circle cx="268" cy="120" r="3" fill="#ef4444" />
    <circle cx="280" cy="120" r="3" fill="#3b82f6" />
  </g>

  {/* step 3: summarize */}
  <rect x="420" y="70" width="120" height="60" rx="8" fill="#ffffff" stroke="#e5e7eb" />
  <rect x="420" y="70" width="120" height="3" fill="#94a3b8" />
  <text x="480" y="98" text-anchor="middle" font-size="12" fill="#0f172a" font-weight="600">summarize</text>
  <text x="480" y="116" text-anchor="middle" font-size="10" fill="#64748b">local</text>

  {/* step 4: contain (approval gated) */}
  <rect x="600" y="70" width="140" height="60" rx="8" fill="#fef2f2" stroke="#fecaca" />
  <rect x="600" y="70" width="140" height="3" fill="#ef4444" />
  <text x="670" y="98" text-anchor="middle" font-size="12" fill="#b91c1c" font-weight="600">⏸ contain</text>
  <text x="670" y="116" text-anchor="middle" font-size="10" fill="#b91c1c">approval required</text>

  {/* arrows */}
  <g stroke="#94a3b8" stroke-width="2" fill="none" marker-end="url(#arrow-tip-4)">
    <line x1="140" y1="100" x2="200" y2="100" />
    <line x1="360" y1="100" x2="420" y2="100" />
    <line x1="540" y1="100" x2="600" y2="100" />
  </g>

  <defs>
    <marker id="arrow-tip-4" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
    </marker>
  </defs>
</svg>
```

- [ ] **Step 5: Verify all four diagrams compile**

```bash
cd site
npm run build
```

Expected: build succeeds. The diagrams aren't yet imported by any page, but Astro's TS check still runs over them.

- [ ] **Step 6: Commit**

```bash
git add site/src/components/diagrams/
git commit -m "$(cat <<'EOF'
site(diagrams): four hand-rolled SVG diagrams for hero + concept anchors

SystemMap (home), DaimonOnHost, AgentLifecycle, OrchestrationFlow.
Brand palette via literal hex; static viewBox-scaled, no JS, no
external dependencies.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Home page — hero, system map, concept cards, features

**Files:**
- Create / replace: `site/src/pages/index.astro`
- Create: `site/src/components/Hero.astro`, `site/src/components/ConceptCard.astro`, `site/src/components/FeatureGrid.astro`

- [ ] **Step 1: Create `site/src/components/Hero.astro`**

```astro
---
// Home-page hero. Brand-50 → white gradient background, large
// headline with the orchestra metaphor, two CTAs.
---

<section class="bg-gradient-to-b from-brand-50 via-white to-white">
  <div class="max-w-page mx-auto px-6 pt-16 pb-12 text-center">
    <h1 class="text-4xl md:text-5xl lg:text-[52px] font-bold leading-tight tracking-tight text-ink max-w-4xl mx-auto">
      Orchestrate your security team
      <span class="bg-gradient-to-br from-brand-600 to-brand-700 bg-clip-text text-transparent">
        like an orchestra.
      </span>
    </h1>
    <p class="mt-5 text-base md:text-[17px] text-ink-dim leading-relaxed max-w-2xl mx-auto">
      Okesu coordinates a fleet of AI agents across your hosts. Findings, investigations, and response — all under one control plane.
    </p>
    <div class="mt-7 flex flex-wrap items-center justify-center gap-3">
      <a
        href="/install"
        class="inline-flex items-center gap-1.5 bg-brand-700 hover:bg-brand-600 text-white text-sm font-semibold px-5 py-2.5 rounded-md shadow-sm transition-colors"
      >
        Install Okesu →
      </a>
      <a
        href="https://github.com/mrbrutti/okesu"
        target="_blank"
        rel="noopener"
        class="inline-flex items-center gap-1.5 bg-white hover:bg-slate-50 text-ink text-sm font-medium px-5 py-2.5 rounded-md border border-border transition-colors"
      >
        View on GitHub →
      </a>
    </div>
  </div>
</section>
```

- [ ] **Step 2: Create `site/src/components/ConceptCard.astro`**

```astro
---
interface Props {
  href: string;
  title: string;
  glyph: string;          // ASCII glyph stand-in for the lucide icon
  description: string;
}

const { href, title, glyph, description } = Astro.props;
---

<a
  href={href}
  class="group block bg-panel border border-border rounded-lg shadow-card p-5 hover:border-brand-100 hover:shadow-md transition-all"
>
  <div class="w-9 h-9 rounded-md bg-brand-50 text-brand-700 flex items-center justify-center text-base mb-3 border border-brand-100">
    <span class="font-mono">{glyph}</span>
  </div>
  <h3 class="text-[15px] font-semibold text-ink mb-1.5">{title}</h3>
  <p class="text-xs text-ink-dim leading-relaxed mb-3">{description}</p>
  <span class="text-xs text-brand-700 font-medium group-hover:text-brand-600">
    Learn more →
  </span>
</a>
```

- [ ] **Step 3: Create `site/src/components/FeatureGrid.astro`**

```astro
---
interface Feature {
  title: string;
  body: string;
}

const features: Feature[] = [
  {
    title: 'Federated control plane',
    body: 'Run one CP per environment, federate them into a hierarchy. Findings + runs flow upstream; commands flow down.',
  },
  {
    title: 'Multi-provider agents',
    body: 'Run Claude, Codex, or your own provider. Same dispatch surface, same JSONL contract.',
  },
  {
    title: 'Investigations as a workspace',
    body: 'Group findings into a case file. Notes, timeline, and orchestrations all hang off one investigation entity.',
  },
  {
    title: 'Approval gates + action allowlists',
    body: 'Step-level human approval. Per-class allowlists for what an orchestration can mutate.',
  },
  {
    title: 'Per-host fan-out',
    body: 'Run an agent on N hosts in parallel; live histogram + per-host status in the run viewer.',
  },
  {
    title: 'Single-binary install',
    body: 'okesu and okesu-cp are static Go binaries. No runtime, no agent framework dependencies, just drop in.',
  },
];
---

<section class="max-w-page mx-auto px-6 py-12">
  <div class="grid md:grid-cols-2 gap-x-12 gap-y-7">
    {features.map(f => (
      <div class="flex gap-3">
        <div class="text-brand-700 text-sm shrink-0 mt-0.5">✓</div>
        <div>
          <h4 class="text-[13px] font-semibold text-ink mb-1">{f.title}</h4>
          <p class="text-xs text-ink-dim leading-relaxed">{f.body}</p>
        </div>
      </div>
    ))}
  </div>
</section>
```

- [ ] **Step 4: Replace `site/src/pages/index.astro`**

```astro
---
import Base from '../layouts/Base.astro';
import Hero from '../components/Hero.astro';
import ConceptCard from '../components/ConceptCard.astro';
import FeatureGrid from '../components/FeatureGrid.astro';
import SystemMap from '../components/diagrams/SystemMap.astro';
---

<Base
  title="Okesu"
  description="Orchestrate your security team like an orchestra. Okesu coordinates a fleet of AI agents across your hosts."
>
  <Hero />

  <section class="max-w-page mx-auto px-6 mb-16">
    <div class="bg-panel border border-border rounded-xl shadow-card p-6 md:p-8">
      <SystemMap />
    </div>
  </section>

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

  <FeatureGrid />
</Base>
```

- [ ] **Step 5: Verify the home page builds and renders**

```bash
cd site
npm run build
```

Expected: clean build, `dist/index.html` ~10–20 KB. Run a dev server to eyeball:

```bash
npm run dev
```

Open `http://localhost:4321` (Astro's default port). Acceptance: hero gradient visible, system-map diagram inside the card, three concept cards in a row above the features grid, footer pinned to the bottom. Stop the dev server with Ctrl-C.

- [ ] **Step 6: Commit**

```bash
git add site/src/components/Hero.astro site/src/components/ConceptCard.astro site/src/components/FeatureGrid.astro site/src/pages/index.astro
git commit -m "$(cat <<'EOF'
site(home): hero + system-map diagram + concept cards + features

Hero renders the orchestra metaphor headline with brand-gradient
text, two CTAs, and a brand-50 backdrop. System-map diagram lives
in a card immediately below. Three concept cards link to their
dedicated pages. Six-feature grid rounds out the page.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Concept page layout (Concept.astro)

**Files:**
- Create: `site/src/layouts/Concept.astro`

Shared layout for the three concept MDX pages. Wraps Base, adds a sticky TOC and a constrained content column. Auto-generates the TOC from MDX `<h2>` headings via Astro's content-collection-style approach — but to keep this simple, we accept the TOC items as a frontmatter prop (saves us pulling in `astro:content` for v1).

- [ ] **Step 1: Create `site/src/layouts/Concept.astro`**

```astro
---
import Base from './Base.astro';

interface TOCItem {
  id: string;
  label: string;
}

interface Props {
  title: string;
  subtitle: string;
  glyph: string;
  description: string;     // for <meta description>
  toc: TOCItem[];
  diagram?: string;        // optional component name reference, used in MDX directly
}

const { title, subtitle, glyph, description, toc } = Astro.props;
---

<Base title={title} description={description}>
  <div class="bg-panel border-b border-border">
    <div class="max-w-page mx-auto px-6 py-10">
      <div class="flex items-start gap-4">
        <div class="w-11 h-11 rounded-lg bg-brand-50 text-brand-700 flex items-center justify-center text-2xl border border-brand-100 shrink-0">
          <span class="font-mono">{glyph}</span>
        </div>
        <div>
          <h1 class="text-[28px] md:text-[32px] font-bold text-brand-700 leading-tight">{title}</h1>
          <p class="mt-1.5 text-sm md:text-[15px] text-ink-dim leading-relaxed max-w-2xl">{subtitle}</p>
        </div>
      </div>
    </div>
  </div>

  <div class="max-w-page mx-auto px-6 py-8">
    <div class="grid md:grid-cols-[220px_minmax(0,1fr)] gap-x-10">
      {/* Sticky TOC */}
      <aside class="hidden md:block">
        <div class="sticky top-20">
          <div class="text-[10px] uppercase tracking-wide text-ink-mute font-bold mb-2">On this page</div>
          <ul class="text-xs space-y-1.5">
            {toc.map(item => (
              <li>
                <a href={`#${item.id}`} class="text-ink-dim hover:text-brand-700 transition-colors">{item.label}</a>
              </li>
            ))}
          </ul>
        </div>
      </aside>

      {/* Content */}
      <article class="concept-prose max-w-content">
        <slot />
      </article>
    </div>
  </div>
</Base>
```

- [ ] **Step 2: Verify it compiles (no consumer yet)**

```bash
cd site
npm run build
```

Expected: build succeeds (Concept.astro is type-checked but unused; Astro doesn't emit a page for unused layouts).

- [ ] **Step 3: Commit**

```bash
git add site/src/layouts/Concept.astro
git commit -m "$(cat <<'EOF'
site(layout): Concept page layout with header band + sticky TOC

220-px sticky TOC on md+, full-width content column below. Header
band uses brand-700 h1 + brand-50 icon tile. TOC items come in via
frontmatter prop so MDX pages stay readable.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Daimons concept page

**Files:**
- Create: `site/src/pages/concepts/daimons.mdx`

- [ ] **Step 1: Create the page**

```mdx
---
layout: ../../layouts/Concept.astro
title: Daimons
subtitle: The runtime layer. A long-running daemon on every host that listens for run dispatches and streams findings back.
glyph: ▦
description: Daimons are Okesu's per-host runtime. They execute agents and emit findings to the control plane.
toc:
  - { id: what,        label: "What a daimon is" }
  - { id: where,       label: "Where it runs" }
  - { id: lifecycle,   label: "Lifecycle" }
  - { id: contract,    label: "The dispatch contract" }
  - { id: next,        label: "Where to next" }
---

import DaimonOnHost from '../../components/diagrams/DaimonOnHost.astro';

<div class="bg-bg border border-border rounded-xl p-6 md:p-8 my-8">
  <DaimonOnHost />
</div>

<h2 id="what">What a daimon is</h2>

<p>A <span class="term">daimon</span> is the long-running daemon Okesu installs on each host. It listens for run dispatches from the control plane, executes the agent (Claude / Codex / a custom provider), and streams findings back over the management tunnel.</p>

<p>One daimon per host. It's the piece that runs.</p>

<p>The Go binary is called <code>okesu</code>; the same binary serves as daemon and CLI depending on the subcommand. Out of the box it ships with a small library of built-in agents (<code>edr</code>, <code>instance-integrity</code>, <code>instance-threat</code>, <code>sre-health</code>) so a fresh daimon has something to do on tick zero.</p>

<h2 id="where">Where it runs</h2>

<p>The daimon is a static Go binary. It runs anywhere a recent Linux or macOS host runs — no JVM, no Python, no agent framework. Memory footprint is modest (tens of megabytes idle). It needs:</p>

<ul>
  <li>Outbound HTTPS to the control plane (default port 8443) for dispatch + tunnel.</li>
  <li>A writable directory for state (default <code>/var/lib/okesu</code>).</li>
  <li>Credentials for the LLM provider it'll drive (env vars for Claude / Codex tokens).</li>
</ul>

<p>That's the deployment surface. Drop the binary in, hand it the CP URL and a bootstrap token, it registers and goes to work.</p>

<h2 id="lifecycle">Lifecycle</h2>

<p>A daimon's life:</p>

<ul>
  <li><span class="term">install</span> — drop the binary; create the systemd unit (or your supervisor of choice).</li>
  <li><span class="term">register</span> — first contact with the CP; the CP issues the daimon a long-lived agent token bound to its hostname.</li>
  <li><span class="term">heartbeat</span> — every few seconds the daimon ping-pongs with the CP so dispatches are deliverable.</li>
  <li><span class="term">execute runs</span> — the CP dispatches a run (an agent invocation with prompt, inputs, dispatch mode); the daimon runs the agent and streams JSONL events back.</li>
  <li><span class="term">upgrade in place</span> — the CP can push a new binary; the daimon swaps itself out under systemd and re-registers with the same identity.</li>
</ul>

<h2 id="contract">The dispatch contract</h2>

<p>A run dispatch is a small JSON envelope. It tells the daimon which agent to run, what prompt to render, what inputs to bind, and what dispatch mode to use (<code>tunnel</code> for live tunnels, <code>jobs</code> for offline pull queue).</p>

<div class="code-block">
<span class="cmt">// what the daimon receives</span><br/>
{`{`}<br/>
&nbsp;&nbsp;<span class="key">"step_id"</span>: <span class="str">"scan@web-prod-01"</span>,<br/>
&nbsp;&nbsp;<span class="key">"agent"</span>: <span class="str">"edr-investigator"</span>,<br/>
&nbsp;&nbsp;<span class="key">"prompt"</span>: <span class="str">"check for C2 indicators around process 12345"</span>,<br/>
&nbsp;&nbsp;<span class="key">"inputs"</span>: {`{`} <span class="key">"pid"</span>: <span class="str">12345</span> {`}`},<br/>
&nbsp;&nbsp;<span class="key">"timeout"</span>: <span class="str">"5m"</span><br/>
{`}`}
</div>

<p>What the daimon emits is a sequence of newline-delimited JSON events — most importantly <code>finding</code> events for security signals and a final <code>orchestration_result</code> for the structured payload downstream steps consume.</p>

<h2 id="next">Where to next</h2>

<ul>
  <li><a href="/concepts/agents">Agents</a> — the prompt + tool definitions the daimon executes.</li>
  <li><a href="/concepts/orchestrations">Orchestrations</a> — the YAML specs that dispatch runs to daimons in sequence.</li>
  <li><a href="/install">Install Okesu</a> — drop a daimon onto your first host.</li>
</ul>
```

- [ ] **Step 2: Verify the page builds**

```bash
cd site
npm run build
```

Expected: build succeeds; `dist/concepts/daimons.html` exists. Spot-check:

```bash
grep -c "What a daimon is\|DaimonOnHost\|term" dist/concepts/daimons.html
```

Expected: ≥ 3 (the heading appears, the diagram is inlined as SVG, term spans appear).

- [ ] **Step 3: Commit**

```bash
git add site/src/pages/concepts/daimons.mdx
git commit -m "$(cat <<'EOF'
site(concepts): Daimons page

Long-form intro: what a daimon is, where it runs, lifecycle, the
dispatch contract. Anchor diagram (DaimonOnHost) shows CP →
daimon → finding flow. Source material lifted from
web/src/components/docs/DocsDaimons.tsx + docs/daemon.design.md.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Agents concept page

**Files:**
- Create: `site/src/pages/concepts/agents.mdx`

- [ ] **Step 1: Create the page**

```mdx
---
layout: ../../layouts/Concept.astro
title: Agents
subtitle: The capability layer. Prompt-driven workers that run on a daimon and emit structured findings as JSONL.
glyph: ✦
description: Agents are Okesu's capability units — markdown specs with a prompt, a provider, and an emission contract.
toc:
  - { id: what,         label: "What an agent is" }
  - { id: contract,     label: "The JSONL contract" }
  - { id: authoring,    label: "Authoring an agent" }
  - { id: builtins,     label: "Built-in agents" }
  - { id: next,         label: "Where to next" }
---

import AgentLifecycle from '../../components/diagrams/AgentLifecycle.astro';

<div class="bg-bg border border-border rounded-xl p-6 md:p-8 my-8">
  <AgentLifecycle />
</div>

<h2 id="what">What an agent is</h2>

<p>An <span class="term">agent</span> in Okesu is a markdown file with YAML frontmatter. The frontmatter declares identity, the LLM provider, the tools the agent can call, and the JSONL events it's expected to emit. The body of the file is the system prompt the LLM sees on every run.</p>

<p>That's it. No SDK, no runtime framework. The daimon parses the spec, drives the LLM in a turn-by-turn loop with the declared tools available, and watches for the JSONL events the agent emits. When a <code>finding</code> event lands, it goes to the control plane.</p>

<h2 id="contract">The JSONL contract</h2>

<p>An agent emits a stream of newline-delimited JSON events. The two that matter to the control plane are:</p>

<ul>
  <li><code>finding</code> — a security signal: severity, title, optional category (process/file/network/cert/cloud), evidence, dedup key, attributes.</li>
  <li><code>orchestration_result</code> — a single structured payload at the end of a run, surfaced as <code>{`{{stepN.result}}`}</code> in downstream orchestration steps. Optional but powerful for chained playbooks.</li>
</ul>

<p>Other event types (logs, progress, tool calls) are ignored by the CP but visible in the run's transcript. Agents are also free to emit nothing at all — a "no findings on this host" run is meaningful information.</p>

<h2 id="authoring">Authoring an agent</h2>

<p>A minimal agent looks like this:</p>

<div class="code-block">
<span class="cmt">---</span><br/>
<span class="key">name</span>: edr-investigator<br/>
<span class="key">version</span>: <span class="str">"1"</span><br/>
<span class="key">description</span>: triage suspected C2 callbacks<br/>
<br/>
<span class="key">provider</span>: claude<br/>
<span class="key">model</span>: claude-mythos-preview<br/>
<span class="key">effort</span>: medium<br/>
<span class="key">maxTurns</span>: 30<br/>
<span class="key">timeout</span>: 5m<br/>
<br/>
<span class="key">tools</span>:<br/>
&nbsp;&nbsp;- bash<br/>
&nbsp;&nbsp;- read_file<br/>
&nbsp;&nbsp;- search<br/>
<span class="cmt">---</span><br/>
You investigate suspected command-and-control callbacks on this host.<br/>
Use ps, netstat, lsof, /proc to gather evidence. When you find a high-confidence<br/>
indicator, emit a finding event with the appropriate severity.
</div>

<p>The same agent definition works whether the daimon is local-only or behind a federated CP — the dispatch mode is decided at the orchestration step, not the agent.</p>

<h2 id="builtins">Built-in agents</h2>

<p>Okesu ships with a small library of agents covering common security operations:</p>

<ul>
  <li><code>edr</code> — Linux EDR running on a 5-minute schedule.</li>
  <li><code>instance-integrity</code> — file-integrity monitoring on critical paths.</li>
  <li><code>instance-threat</code> — IMDS abuse, container escapes, cryptomining detection.</li>
  <li><code>sre-health</code> — TLS expiry, deployment frequency, recent incident patterns.</li>
  <li><code>edr-investigator</code> — interactive triage agent invoked by orchestrations.</li>
  <li><code>forensic-collector</code> — volatile-state snapshotting for incident response.</li>
</ul>

<p>The full catalog is in the platform's Agents tab; each spec is a complete worked example you can copy and adapt.</p>

<h2 id="next">Where to next</h2>

<ul>
  <li><a href="/concepts/daimons">Daimons</a> — what executes the agent.</li>
  <li><a href="/concepts/orchestrations">Orchestrations</a> — sequence agents into playbooks.</li>
  <li><a href="/install">Install Okesu</a> — bootstrap a CP and your first daimon.</li>
</ul>
```

- [ ] **Step 2: Verify and commit**

```bash
cd site
npm run build
```

Expected: clean. Then:

```bash
git add site/src/pages/concepts/agents.mdx
git commit -m "$(cat <<'EOF'
site(concepts): Agents page

Markdown-frontmatter spec format, JSONL emission contract,
canonical example. AgentLifecycle diagram anchors the page. Lists
the built-in agent catalog (edr, instance-integrity, etc).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Orchestrations concept page

**Files:**
- Create: `site/src/pages/concepts/orchestrations.mdx`

- [ ] **Step 1: Create the page**

```mdx
---
layout: ../../layouts/Concept.astro
title: Orchestrations
subtitle: The coordination layer. YAML specs that sequence agents into playbooks — with fan-out, gates, and branching.
glyph: ⊞
description: Orchestrations are Okesu's playbook layer — YAML specs that drive agents step-by-step, with fan-out across hosts and human approval gates.
toc:
  - { id: what,        label: "What an orchestration is" }
  - { id: spec,        label: "The YAML spec" }
  - { id: fanout,      label: "Fan-out across hosts" }
  - { id: gates,       label: "Approval gates" }
  - { id: branching,   label: "Branching on results" }
  - { id: next,        label: "Where to next" }
---

import OrchestrationFlow from '../../components/diagrams/OrchestrationFlow.astro';

<div class="bg-bg border border-border rounded-xl p-6 md:p-8 my-8">
  <OrchestrationFlow />
</div>

<h2 id="what">What an orchestration is</h2>

<p>An <span class="term">orchestration</span> is a YAML spec that describes a sequence of agent runs. Each step says <em>which agent</em> to run, <em>which host(s)</em> to run it on, and <em>what prompt</em> to give it. The control plane parses the spec, dispatches each step in turn, persists results, and renders a live DAG on the run-detail page.</p>

<p>Orchestrations are not "if-this-then-that" workflows. They're <em>investigative playbooks</em> — the kind of "here's how a senior analyst would approach this" runbook you'd otherwise carry around as a Confluence page. Okesu makes those runbooks executable.</p>

<h2 id="spec">The YAML spec</h2>

<p>A minimal orchestration looks like this:</p>

<div class="code-block">
<span class="cmt">---</span><br/>
<span class="key">name</span>: c2-callback-triage<br/>
<span class="key">description</span>: triage a suspected C2 callback<br/>
<span class="key">steps</span>:<br/>
&nbsp;&nbsp;- <span class="key">id</span>: scan<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">agent</span>: edr-investigator<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">node</span>: <span class="str">"{`{{trigger.host}}`}"</span><br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">prompt</span>: <span class="str">"check for C2 indicators around process {`{{trigger.pid}}`}"</span><br/>
&nbsp;&nbsp;- <span class="key">id</span>: contain<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">agent</span>: forensic-collector<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">node</span>: <span class="str">"{`{{trigger.host}}`}"</span><br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">prompt</span>: <span class="str">"snapshot volatile state from {`{{scan.result.pid}}`}"</span><br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">approval</span>: required
</div>

<p>The <code>{`{{trigger.X}}`}</code> placeholders bind context from whatever fired the orchestration (a finding, a manual run, a scheduled cron). <code>{`{{stepN.result.field}}`}</code> binds structured data from earlier steps.</p>

<h2 id="fanout">Fan-out across hosts</h2>

<p>Replace <code>node:</code> with <code>nodes:</code> and an array of hostnames. The engine dispatches the step on every host in parallel, aggregates the results, and exposes per-host status on the run-detail canvas.</p>

<div class="code-block">
&nbsp;&nbsp;- <span class="key">id</span>: scan<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">agent</span>: edr-triage<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">nodes</span>: [web-prod-01, web-prod-02, web-prod-03]<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">prompt</span>: <span class="str">"sweep for known IOCs"</span>
</div>

<p>The platform's run viewer renders a per-host status strip — a colored dot per host plus a histogram bar — so you can see which host succeeded, failed, or is still running at a glance. Failed hosts surface inline with their error.</p>

<h2 id="gates">Approval gates</h2>

<p>Add <code>approval: required</code> to any step. The engine pauses the run at that step until an operator approves it from the dashboard. Useful for steps that mutate state — containment actions, tag changes, finding triage — where you want a human in the loop. The CP enforces a per-action-class allowlist so a runaway agent can't push beyond what's been pre-authorised.</p>

<h2 id="branching">Branching on results</h2>

<p>Steps can read structured fields from earlier steps via the binding system. An agent that emits an <code>orchestration_result</code> finding exposes its attributes as <code>{`{{stepN.result.X}}`}</code>; the next step can use them in its prompt or as input parameters.</p>

<p>For runs that fanned out, each host's payload is also addressable: <code>{`{{stepN.byNode["host-1"].result.foo}}`}</code>.</p>

<h2 id="next">Where to next</h2>

<ul>
  <li><a href="/concepts/daimons">Daimons</a> — what runs the dispatched steps.</li>
  <li><a href="/concepts/agents">Agents</a> — the worker units orchestrations sequence.</li>
  <li><a href="/install">Install Okesu</a> — bootstrap a CP and write your first orchestration.</li>
</ul>
```

- [ ] **Step 2: Verify and commit**

```bash
cd site
npm run build
```

Expected: clean. Spot-check:

```bash
grep -c "fanout\|approval: required\|byNode" dist/concepts/orchestrations.html
```

Expected: ≥ 3.

```bash
git add site/src/pages/concepts/orchestrations.mdx
git commit -m "$(cat <<'EOF'
site(concepts): Orchestrations page

YAML spec, fan-out via nodes:, approval gates, result binding.
OrchestrationFlow diagram anchors the page (4-step DAG with one
fan-out and one approval-gated step).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Install page

**Files:**
- Create: `site/src/pages/install.astro`

- [ ] **Step 1: Create the page**

```astro
---
import Base from '../layouts/Base.astro';
---

<Base
  title="Install"
  description="Quick-start: install the Okesu binary, bootstrap a control plane, deploy a daimon, write your first orchestration."
>
  <div class="max-w-content mx-auto px-6 py-12">
    <h1 class="text-3xl font-bold text-ink mb-3">Install Okesu</h1>
    <p class="text-base text-ink-dim leading-relaxed mb-10">
      A single Go binary, a few minutes of setup, and your first orchestration running on a real host.
      This guide gets you from zero to a working CP with one daimon.
    </p>

    <section class="mb-10">
      <h2 id="binary" class="text-xl font-semibold text-ink mb-3">1. Install the binary</h2>
      <p class="text-sm text-ink-dim mb-3 leading-relaxed">
        For now, build from source. Pre-built releases are on the roadmap.
      </p>
      <div class="code-block">
<span class="cmt"># clone + build</span><br/>
git clone https://github.com/mrbrutti/okesu.git<br/>
cd okesu<br/>
go build -o okesu ./cmd/okesu<br/>
go build -o okesu-cp ./cmd/okesu-cp<br/>
<br/>
<span class="cmt"># install both binaries on PATH</span><br/>
sudo install okesu okesu-cp /usr/local/bin/
      </div>
    </section>

    <section class="mb-10">
      <h2 id="cp" class="text-xl font-semibold text-ink mb-3">2. Bootstrap a control plane</h2>
      <p class="text-sm text-ink-dim mb-3 leading-relaxed">
        The CP is optional but you'll want it the moment you operate more than one daimon.
        It serves the dashboard, receives findings, and dispatches orchestration steps.
      </p>
      <div class="code-block">
<span class="cmt"># first-run init creates ./cp.db, generates the CA, prints an admin password</span><br/>
okesu-cp init<br/>
<br/>
<span class="cmt"># start the CP — UI on https://localhost:8443, mTLS mgmt-plane on :8444</span><br/>
okesu-cp serve
      </div>
      <p class="text-sm text-ink-dim mt-3 leading-relaxed">
        Browse to <code>https://localhost:8443</code> — accept the self-signed cert (production deployments use a real cert via the CP config), log in with the admin password printed by <code>init</code>.
      </p>
    </section>

    <section class="mb-10">
      <h2 id="daimon" class="text-xl font-semibold text-ink mb-3">3. Deploy a daimon to a host</h2>
      <p class="text-sm text-ink-dim mb-3 leading-relaxed">
        From the dashboard, go to <strong>Fleet → Nodes → Add node</strong>. Paste the host's SSH connection details; the CP rsyncs the binary, writes the systemd unit, and registers the daimon. The auto-deploy flow handles upgrades in place.
      </p>
      <p class="text-sm text-ink-dim leading-relaxed">
        Want to skip SSH and install manually? Drop the <code>okesu</code> binary on the host, run <code>okesu daemon register --cp https://your-cp:8443 --token &lt;bootstrap&gt;</code>, then start it with your init system of choice.
      </p>
    </section>

    <section class="mb-10">
      <h2 id="first-orch" class="text-xl font-semibold text-ink mb-3">4. Write your first orchestration</h2>
      <p class="text-sm text-ink-dim mb-3 leading-relaxed">
        From the dashboard, go to <strong>Automation → Orchestrations → New</strong>. Paste this:
      </p>
      <div class="code-block">
<span class="cmt">---</span><br/>
<span class="key">name</span>: hello-okesu<br/>
<span class="key">description</span>: smoke test<br/>
<span class="key">steps</span>:<br/>
&nbsp;&nbsp;- <span class="key">id</span>: greet<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">agent</span>: instance-integrity<br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">node</span>: <span class="str">"your-host-name"</span><br/>
&nbsp;&nbsp;&nbsp;&nbsp;<span class="key">prompt</span>: <span class="str">"list the top 5 critical files on this host and confirm their hashes"</span>
      </div>
      <p class="text-sm text-ink-dim mt-3 leading-relaxed">
        Save → click <strong>Run</strong> → watch the run-detail page render the DAG and the agent's findings stream in.
      </p>
    </section>

    <section class="mb-10">
      <h2 id="checklist" class="text-xl font-semibold text-ink mb-3">5. Production checklist</h2>
      <ul class="text-sm text-ink-dim leading-relaxed space-y-2 list-disc pl-5">
        <li>Swap SQLite for Postgres in the CP config when you cross a few thousand findings.</li>
        <li>Federate environments by enabling the federation peer relationship (see the architecture doc on GitHub).</li>
        <li>Open ports <strong>8443</strong> (UI/webhook) and <strong>8444</strong> (mTLS mgmt) only to the networks that need them.</li>
        <li>Configure your LLM provider via env vars (<code>ANTHROPIC_API_KEY</code> etc) — same envvars on the CP for jobs-mode dispatch and on each daimon for tunnel-mode.</li>
      </ul>
    </section>

    <section>
      <h2 id="more" class="text-xl font-semibold text-ink mb-3">More</h2>
      <ul class="text-sm space-y-1.5">
        <li><a class="text-brand-700 font-medium hover:underline" href="https://github.com/mrbrutti/okesu/blob/main/INSTALL.md">Full INSTALL.md on GitHub</a></li>
        <li><a class="text-brand-700 font-medium hover:underline" href="https://github.com/mrbrutti/okesu/blob/main/docs/architecture.md">Architecture reference</a></li>
        <li><a class="text-brand-700 font-medium hover:underline" href="/docs">Docs index</a></li>
      </ul>
    </section>
  </div>
</Base>
```

- [ ] **Step 2: Verify and commit**

```bash
cd site
npm run build
```

Expected: clean.

```bash
git add site/src/pages/install.astro
git commit -m "$(cat <<'EOF'
site(install): quick-start guide

Five-section flow: build the binary, bootstrap CP, deploy a daimon,
first orchestration, production checklist. Links to the full
INSTALL.md and architecture docs on GitHub for depth.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Docs landing page

**Files:**
- Create: `site/src/pages/docs.astro`

- [ ] **Step 1: Create the page**

```astro
---
import Base from '../layouts/Base.astro';

interface DocLink {
  title: string;
  body: string;
  href: string;
  external?: boolean;
}

const concepts: DocLink[] = [
  {
    title: 'Daimons',
    body: 'The runtime layer — long-running daemons on each host that listen for run dispatches and stream findings back.',
    href: '/concepts/daimons',
  },
  {
    title: 'Agents',
    body: 'The capability layer — markdown specs declaring a prompt, a provider, and a JSONL emission contract.',
    href: '/concepts/agents',
  },
  {
    title: 'Orchestrations',
    body: 'The coordination layer — YAML playbooks that sequence agents, fan out across hosts, gate on approval.',
    href: '/concepts/orchestrations',
  },
];

const reference: DocLink[] = [
  {
    title: 'Architecture',
    body: 'High-level view of the platform: CP, daimons, the management tunnel, the federation hierarchy.',
    href: 'https://github.com/mrbrutti/okesu/blob/main/docs/architecture.md',
    external: true,
  },
  {
    title: 'Installation',
    body: 'The full INSTALL.md — every deployment shape, networking, lifecycle, OCI specifics.',
    href: 'https://github.com/mrbrutti/okesu/blob/main/INSTALL.md',
    external: true,
  },
  {
    title: 'Orchestration spec',
    body: 'YAML reference: every field, every modifier, every binding shape.',
    href: 'https://github.com/mrbrutti/okesu/blob/main/docs/orchestrations.md',
    external: true,
  },
  {
    title: 'Daemon design',
    body: 'How the daimon process is structured internally — config, scheduler, runtime, output sinks.',
    href: 'https://github.com/mrbrutti/okesu/blob/main/docs/daemon.design.md',
    external: true,
  },
  {
    title: 'S3 transport',
    body: 'Offline daimon deployment via S3 dead-drop — for hosts that can\'t maintain an outbound tunnel.',
    href: 'https://github.com/mrbrutti/okesu/blob/main/docs/s3-transport.md',
    external: true,
  },
  {
    title: 'Roadmap',
    body: 'What\'s shipped, what\'s in flight, what\'s on the horizon.',
    href: 'https://github.com/mrbrutti/okesu/blob/main/docs/roadmap.md',
    external: true,
  },
];
---

<Base
  title="Docs"
  description="Documentation index — concept pages and reference material for Okesu."
>
  <div class="max-w-page mx-auto px-6 py-12">
    <h1 class="text-3xl font-bold text-ink mb-3">Documentation</h1>
    <p class="text-base text-ink-dim leading-relaxed mb-10 max-w-2xl">
      Concept introductions live here on the site. The reference material for spec formats, deployment shapes, and the platform's internal design lives as markdown in the GitHub repo — those links are below.
    </p>

    <section class="mb-12">
      <h2 class="text-xs uppercase tracking-wide text-ink-mute font-bold mb-3">Concepts</h2>
      <div class="grid md:grid-cols-3 gap-4">
        {concepts.map(c => (
          <a href={c.href} class="block bg-panel border border-border rounded-lg shadow-card p-5 hover:border-brand-100 hover:shadow-md transition-all">
            <h3 class="text-[15px] font-semibold text-ink mb-1.5">{c.title}</h3>
            <p class="text-xs text-ink-dim leading-relaxed">{c.body}</p>
            <span class="text-xs text-brand-700 font-medium mt-3 inline-block">Read →</span>
          </a>
        ))}
      </div>
    </section>

    <section>
      <h2 class="text-xs uppercase tracking-wide text-ink-mute font-bold mb-3">Reference (on GitHub)</h2>
      <div class="grid md:grid-cols-2 gap-3">
        {reference.map(d => (
          <a
            href={d.href}
            target={d.external ? '_blank' : undefined}
            rel={d.external ? 'noopener' : undefined}
            class="block bg-panel border border-border rounded-lg p-4 hover:border-brand-100 transition-colors"
          >
            <h3 class="text-sm font-semibold text-ink mb-1">{d.title}{d.external && <span class="ml-1 text-ink-mute">↗</span>}</h3>
            <p class="text-xs text-ink-dim leading-relaxed">{d.body}</p>
          </a>
        ))}
      </div>
    </section>
  </div>
</Base>
```

- [ ] **Step 2: Verify and commit**

```bash
cd site
npm run build
```

Expected: clean.

```bash
git add site/src/pages/docs.astro
git commit -m "$(cat <<'EOF'
site(docs): documentation landing page

Indexes the three concept pages and links out to the reference
material in the GitHub repo (architecture, INSTALL, orchestrations
spec, daemon design, S3 transport, roadmap).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: 404 page

**Files:**
- Create: `site/src/pages/404.astro`

- [ ] **Step 1: Create the page**

```astro
---
import Base from '../layouts/Base.astro';
---

<Base title="404 — Not Found" description="That page does not exist on okesu.to.">
  <div class="max-w-content mx-auto px-6 py-24 text-center">
    <div class="inline-flex items-center justify-center w-14 h-14 rounded-lg bg-brand-50 text-brand-700 text-2xl border border-brand-100 mb-5">
      <span class="font-mono">?</span>
    </div>
    <h1 class="text-3xl font-bold text-ink mb-3">Page not found</h1>
    <p class="text-sm text-ink-dim leading-relaxed max-w-md mx-auto mb-7">
      That URL doesn't match anything on okesu.to. Maybe head back home, or jump into the docs.
    </p>
    <div class="flex flex-wrap items-center justify-center gap-3">
      <a href="/" class="bg-brand-700 hover:bg-brand-600 text-white text-sm font-semibold px-4 py-2 rounded-md">
        ← Back to home
      </a>
      <a href="/docs" class="bg-white hover:bg-slate-50 text-ink text-sm font-medium px-4 py-2 rounded-md border border-border">
        Browse docs
      </a>
    </div>
  </div>
</Base>
```

- [ ] **Step 2: Verify and commit**

```bash
cd site
npm run build
```

Expected: clean. `dist/404.html` exists.

```bash
git add site/src/pages/404.astro
git commit -m "$(cat <<'EOF'
site(404): branded not-found page

Reuses the brand chrome (top nav, footer) and offers two routes
back: home, or the docs index.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Public assets — CNAME, favicon, OG image

**Files:**
- Create: `site/public/CNAME`, `site/public/favicon.svg`, `site/public/og-image.svg`

- [ ] **Step 1: Create `site/public/CNAME`**

A single line, no trailing newline issues — GitHub Pages reads it strictly.

```
okesu.to
```

(Just the literal text `okesu.to`. No protocol, no path, no trailing newline strictly required but Astro/Git will normalize.)

- [ ] **Step 2: Create `site/public/favicon.svg`**

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <!-- Brand square + white dot, matching the in-app logo -->
  <rect width="64" height="64" rx="14" fill="#7c3aed"/>
  <circle cx="32" cy="32" r="11" fill="#ffffff"/>
</svg>
```

- [ ] **Step 3: Create `site/public/og-image.svg`**

A 1200×630 SVG used as the og:image. SVG works for OG in modern crawlers (Twitter, Slack, LinkedIn all rasterise SVG); if rendering ever becomes a problem, swap to a 1200×630 PNG in a follow-up.

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#f5f3ff"/>
      <stop offset="100%" stop-color="#ffffff"/>
    </linearGradient>
    <linearGradient id="brand-gradient" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%" stop-color="#6d28d9"/>
      <stop offset="100%" stop-color="#5b21b6"/>
    </linearGradient>
  </defs>

  <rect width="1200" height="630" fill="url(#bg)"/>

  <!-- Brand square + dot -->
  <rect x="80" y="80" width="80" height="80" rx="18" fill="#7c3aed"/>
  <circle cx="120" cy="120" r="14" fill="#ffffff"/>
  <text x="180" y="135" font-size="38" font-weight="700" font-family="Inter,system-ui,sans-serif" fill="#0f172a">Okesu</text>

  <!-- Tagline -->
  <text x="80" y="320" font-size="58" font-weight="700" font-family="Inter,system-ui,sans-serif" fill="#0f172a">Orchestrate your security team</text>
  <text x="80" y="395" font-size="58" font-weight="700" font-family="Inter,system-ui,sans-serif" fill="url(#brand-gradient)">like an orchestra.</text>

  <!-- Sub -->
  <text x="80" y="465" font-size="28" font-family="Inter,system-ui,sans-serif" fill="#64748b">Coordinated AI agents across your fleet — findings, investigations, response.</text>

  <!-- Bottom URL -->
  <text x="80" y="560" font-size="22" font-family="ui-monospace,monospace" fill="#94a3b8">okesu.to</text>
</svg>
```

- [ ] **Step 4: Verify everything copies into dist/**

```bash
cd site
npm run build
ls dist/CNAME dist/favicon.svg dist/og-image.svg
```

Expected: all three files exist in `dist/`. The CNAME content matches:

```bash
cat dist/CNAME
```

Expected: `okesu.to`.

- [ ] **Step 5: Commit**

```bash
git add site/public/CNAME site/public/favicon.svg site/public/og-image.svg
git commit -m "$(cat <<'EOF'
site(assets): CNAME, favicon, og-image

CNAME binds GitHub Pages to okesu.to. Favicon is the brand square
+ white dot. og-image is a 1200×630 SVG with the orchestra
metaphor, used as og:image and twitter:image.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: GitHub Pages deploy workflow

**Files:**
- Create: `.github/workflows/deploy-site.yml`

This workflow runs only when `site/**` files change on `main`. Uses GitHub Pages' modern "build from Actions" mode (no `gh-pages` branch).

- [ ] **Step 1: Create the workflow**

```yaml
name: Deploy okesu.to

on:
  push:
    branches: [main]
    paths: ['site/**']
  workflow_dispatch:

permissions:
  contents: read
  pages: write
  id-token: write

# Only one Pages deploy at a time. Subsequent pushes queue rather
# than cancel — we want every change to land.
concurrency:
  group: "pages"
  cancel-in-progress: false

jobs:
  build:
    name: Build site
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: ./site
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Node
        uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'npm'
          cache-dependency-path: site/package-lock.json

      - name: Install dependencies
        run: npm ci

      - name: Build with Astro
        run: npm run build

      - name: Upload artifact
        uses: actions/upload-pages-artifact@v3
        with:
          path: site/dist

  deploy:
    name: Deploy to Pages
    needs: build
    runs-on: ubuntu-latest
    environment:
      name: github-pages
      url: ${{ steps.deployment.outputs.page_url }}
    steps:
      - name: Deploy
        id: deployment
        uses: actions/deploy-pages@v4
```

- [ ] **Step 2: Lint the YAML locally (optional)**

```bash
# If you have actionlint installed:
actionlint .github/workflows/deploy-site.yml
```

If not installed, skip — GitHub will validate on push. The workflow follows GitHub's published "Pages from Actions" recipe and is well-tested.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/deploy-site.yml
git commit -m "$(cat <<'EOF'
ci: GitHub Pages deploy workflow for okesu.to

Triggers on main pushes that touch site/**. Builds with Astro,
uploads dist/ as a Pages artifact, deploys via the modern
build-from-Actions mode. Concurrency group "pages" with no
cancel-in-progress so every change lands.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Manual verification + final smoke

**Files:** None.

- [ ] **Step 1: Full build from a clean state**

```bash
cd site
rm -rf dist .astro
npm run build
```

Expected: clean build, `dist/` regenerated. Inspect:

```bash
ls dist/
# expect: CNAME, favicon.svg, og-image.svg, index.html, 404.html, install/, docs/, concepts/, sitemap-index.xml
```

- [ ] **Step 2: Spot-check critical files**

```bash
# CNAME is exactly 'okesu.to'
cat dist/CNAME
echo "---"
# Home references the brand square
grep -c "bg-brand-500\|brand-700\|orchestra" dist/index.html
# Concept pages exist
ls dist/concepts/
# Each concept has its diagram
grep -c "viewBox" dist/concepts/daimons.html dist/concepts/agents.html dist/concepts/orchestrations.html
```

Acceptance:
- CNAME content: `okesu.to`.
- Home file has multiple brand-class hits and the word "orchestra".
- All three concept HTML files exist.
- Each concept page has a `viewBox` (the inline SVG diagram).

- [ ] **Step 3: Local preview**

```bash
npm run preview &
```

Astro's preview server runs on `localhost:4321` by default. Visit:

- `http://localhost:4321/` — home renders with hero, system map, concept cards, features, footer.
- `http://localhost:4321/concepts/daimons` — header band with brand-700 h1, sticky TOC visible above 768 px viewport, content body, diagram inline.
- `http://localhost:4321/concepts/agents` — same shape with the AgentLifecycle diagram.
- `http://localhost:4321/concepts/orchestrations` — same shape with the OrchestrationFlow diagram including the fan-out indicator.
- `http://localhost:4321/install` — five-step quick-start.
- `http://localhost:4321/docs` — concept cards + reference grid.
- `http://localhost:4321/non-existent` — 404 page renders.

Stop the preview server with `kill %1` (or close the terminal).

- [ ] **Step 4: Mobile sanity check**

Open Chrome DevTools' device toolbar, set width to 360 px. Visit each page above. Acceptance:

- Hero headline scales down (no overflow).
- System map fits the card width (it's an SVG with `viewBox` so it scales).
- Concept cards stack vertically.
- Concept page TOC is hidden (it's `hidden md:block`); page content is full width.
- Footer wraps gracefully.

- [ ] **Step 5: Lighthouse spot-check (informal)**

In Chrome DevTools, run Lighthouse against `http://localhost:4321/`. Acceptance: Accessibility ≥ 90, Best Practices ≥ 90. Performance scores will vary based on the Inter font load — flag anything below 70 for a follow-up.

- [ ] **Step 6: Final commit if needed**

If steps 3–5 surfaced tweaks, fix them and commit. If everything passed cleanly, no commit needed for this step.

---

## Self-review

**1. Spec coverage**

- Decision 1 (Phase 1 cut) — Tasks 1–14 ship the listed Phase 1 surface; deferred items aren't in any task. ✓
- Decision 2 (monorepo `site/`) — All site files land at `site/`. ✓
- Decision 3 (Astro + Tailwind + MDX) — Task 1 wires those integrations. ✓
- Decision 4 (hand-rolled SVG diagrams) — Task 4 ships the four SVG diagram components. ✓
- Decision 5 (page list) — `/`, `/concepts/{daimons,agents,orchestrations}`, `/docs`, `/install`, `/404` all in Tasks 5, 7–12. ✓
- Decision 6 (apex + www DNS) — Task 13 ships the CNAME with `okesu.to`; DNS is already configured externally per the spec. ✓
- Decision 7 (engineer-honest copy) — Concept pages and install lift / adapt content from the in-app docs in that voice. ✓
- Decision 8 (deploy workflow with `site/**` filter) — Task 14. ✓
- Decision 9 (Namecheap already configured externally) — No DNS task in the plan; the spec documents the expected records. ✓

Visual treatment from the spec (hero, concept cards, concept template, code-block colors) — implemented across Tasks 2 (CSS), 3 (chrome), 5 (home), 6 (concept layout), 7–12 (per-page).

Edge cases:
- Workflow runs on a `site/`-less PR — `paths: ['site/**']` filter in Task 14. ✓
- Broken Astro change in CI — build job fails, deploy gated on success. ✓
- CNAME deletion — `site/public/CNAME` in Task 13. ✓
- Brand token drift — no automatic sync; spec accepts manual review.

**2. Placeholder scan**

No "TBD", "TODO", or "fill in details". Every step has the actual code or command. The deferred items are explicitly listed in the spec, not in plan tasks.

One soft spot: the install page's "deploy a daimon" instructions reference `okesu daemon register` which I'm asserting based on the project's existing CLI surface — implementer should verify the exact subcommand against the current CLI before merging. Adding a verification note rather than a task: the install page text can be refined in a follow-up if the CLI's exact command differs.

**3. Type / name consistency**

- Brand tokens (`brand-50`, `brand-700`, `ink-mute`, etc.) used consistently across Tasks 2 (config), 3 (chrome), 4 (diagrams), 5 (home), 6 (concept layout), 7–12 (pages). ✓
- Component imports: Hero, ConceptCard, FeatureGrid, SystemMap, DaimonOnHost, AgentLifecycle, OrchestrationFlow, Concept, Base, Nav, Footer — all defined and consumed at consistent paths (`../components/...`, `../layouts/...`). ✓
- MDX frontmatter shape (`layout`, `title`, `subtitle`, `glyph`, `description`, `toc`) consistent across Tasks 7, 8, 9; the `Concept.astro` Props interface matches. ✓
- `toc[].id` values match the `id=` attributes on the `<h2>` headings in each MDX file. ✓

**4. Scope**

15 tasks, all in one direction (additive — only Task 14 touches `.github/`, Tasks 1–13 + 15 are all under `site/`). Sized for a single subagent-driven session.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-30-okesu-to-website.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
