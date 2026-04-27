# Dashboard UI/UX Review

_Read-only review against the established Okesu Control Plane design language. No code edited._

## 1. Summary

The Dashboard has the right shape (4 stat tiles → 2 charts → 2 lists) and reads as familiar at a glance, but it diverges from the rest of the app in three load-bearing ways: (a) the chart **severity colors are wrong** — CRITICAL is drawn red and HIGH orange, swapping the app's purple-CRITICAL / red-HIGH convention encoded in `tailwind.config.ts` and `styles.css`; (b) the chart `PALETTE[0]` (`#6366f1` indigo) is **not** the brand color (the brand is `#7c3aed` purple) so the "brand-aligned" comment is false and the events chart's primary series collides with no app token; (c) the Dashboard rolls its own `Card`, `StatTile`, and `SectionHeader`-like header instead of consuming `SectionHeader`/`ListCard` and the `SummaryCard` shape that already exists on Findings — three card primitives where one belongs. Fix those three and the page snaps into the system.

## 2. Findings

### must-fix

1. **Chart severity colors contradict the design system.**
   `web/src/components/dashboard/DashboardCharts.tsx:38-44` — `CRITICAL: '#dc2626'` (red) and `HIGH: '#ea580c'` (orange) but `tailwind.config.ts:25-31` defines `sev.critical = '#9333ea'` (purple) and `sev.high = '#dc2626'` (red). When a user toggles `groupBy=severity` the chart's CRITICAL line is red while every CRITICAL badge on Findings is purple. Replace with `{ CRITICAL: '#9333ea', HIGH: '#dc2626', MEDIUM: '#ea580c', LOW: '#ca8a04', INFO: '#64748b' }` — exactly the `sev.*` tokens.

2. **`PALETTE[0]` is not the brand color.**
   `web/src/components/dashboard/DashboardCharts.tsx:24-25` — comment says "brand indigo" but `brand-500` is `#7c3aed` (violet/purple), not `#6366f1`. The `EventsPerHourChart` therefore opens with an indigo most-frequent series that exists nowhere else in the app. Use `#7c3aed` as PALETTE[0]. While there, drop the second slot's `#ef4444` (it collides with sev.high in the multi-line chart whenever `groupBy=agent|host` shows red next to a real severity legend elsewhere).

3. **"CRIT" badge on `recent_critical` rows is red, but every other CRITICAL pill in the app is purple.**
   `web/src/pages/Dashboard.tsx:195-197` — uses `text-red-700 bg-red-50 ring-red-200`. Every other CRITICAL surface (Findings list, drawer, ChipPanel critical dot via `bg-sev-critical`) is purple. Replace with `severity-critical` from `styles.css:18` (or inline `text-sev-critical bg-purple-50 ring-purple-200`) and the label "CRITICAL" not "CRIT" — the rest of the app spells it out.

### nice-to-have

4. **Dashboard `Card` headers don't match `SectionHeader`.**
   `web/src/pages/Dashboard.tsx:290-310` renders `<h3 class="text-sm font-semibold">` with no colored dot, while every other page (`Daimons.tsx:181`, `Nodes.tsx:135`, `Findings.tsx:362`) uses `<SectionHeader tone label count />` with a 2x2 dot. Result: same visual area, two different vocabularies. Either reuse `SectionHeader` for the in-card titles ("Recent CRITICAL findings" → tone="critical", count=`recent_critical.length`) or accept the divergence and document it. Recommended: reuse it.

5. **Stat tile dots are only color, no semantic icon, and use raw 500-shade greens/yellows/reds.**
   `web/src/pages/Dashboard.tsx:265-269,280` — `bg-green-500 / yellow-500 / red-500`. The colors are right (they match `SectionHeader` good/warn/bad), but those are also the same shades used for `dot-pulse` heartbeats elsewhere — i.e. they imply "live state" rather than "summary accent." Either move the dot to the corner of the tile as a status pip with a tooltip ("12 unhealthy"), or replace with a thin colored top-border (`border-t-2 border-sev-high`) that doesn't compete with the heartbeat language.

6. **`Picker` is a third toggle-group dialect.**
   `web/src/pages/Dashboard.tsx:312-329` — pill ring with brand-50/brand-700 active state. `Findings.tsx:190-211` uses an inset slate-100 group with white shadowed active. `Daimons.tsx:27-46` uses bottom-border tabs. Three styles for the same affordance. Promote a `<SegmentedControl>` (Findings's pattern is the most polished) and use it everywhere; the Dashboard's range/groupBy switchers should use it.

7. **"Stale fleet" yellow def-version badge tone clashes with severity badges.**
   `web/src/pages/Dashboard.tsx:225-227` — uses `text-yellow-800 bg-yellow-50 ring-yellow-200`. On Daimons (`Daimons.tsx:212-217`) the same metadata renders in `text-brand-700 bg-brand-50 ring-brand-200` (brand) for canonical and the row gets bucketed under "Stale" via `SectionHeader`. Either drop the yellow ring (rely on the row being in a "stale" list) or align with the Daimons treatment for visual continuity.

8. **`groupBy=agent|host` palette starts at indigo for the first agent — same hue family as the upcoming brand fix.**
   `web/src/components/dashboard/DashboardCharts.tsx:46-52` — `colorFor` cycles through PALETTE for non-severity grouping. With brand becoming PALETTE[0], the most-active agent's line will draw in the brand color, which the eye reads as "selected/primary." Skip index 0 for non-severity series or shift the PALETTE for `colorFor`'s callsite by one.

9. **"Other" series at `PALETTE[8]` (slate `#94a3b8`) is too close to invisible against the chart's `#e2e8f0` grid lines.**
   `web/src/components/dashboard/DashboardCharts.tsx:33,84` — slate-400 over slate-200 grid blends. Use `sev.info` (`#64748b`, slate-500) — already a token, more contrast, intentionally muted.

10. **Tooltip uses raw hex, no design tokens; a theme refresh would miss it.**
    `web/src/components/dashboard/DashboardCharts.tsx:146-153` — `background: '#fff'`, `border: '1px solid #e2e8f0'`. These should be `var(--color-panel)` / `var(--color-border)` (or computed from Tailwind config). Add a `tooltipStyle` helper that pulls from the tokens, or accept that recharts is hex-only and at least move the constants to a single `chartTokens.ts`.

11. **Empty-state green checkmark is too cheerful for "no critical findings".**
    `web/src/pages/Dashboard.tsx:349-356` — `text-green-500` icon, 18px, centered. Operators-on-call read green = "all clear", which is fine, but Findings's empty state (`Findings.tsx:285-289`) uses a 32px `Inbox` icon at 50% opacity — calmer and not celebratory. Match Findings: 32px Inbox/CheckCircle2 at `opacity-50`.

12. **Mobile/responsive: charts get cramped below `lg`.**
    `web/src/pages/Dashboard.tsx:151` — `lg:grid-cols-2` for charts means each chart drops to full width at <1024px. At 320–768px the X-axis labels with `interval={3}` show, but the Legend wraps unpredictably (recharts default). Add `<Legend wrapperStyle={{ fontSize: 11, paddingTop: 4 }} verticalAlign="bottom" />` and on the events chart consider `interval="preserveStartEnd"` instead of `3` once narrow.

13. **`max-w-7xl` on the wrapper differs from other pages.**
    `web/src/pages/Dashboard.tsx:95` — `max-w-7xl` (1280px). `Findings.tsx`, `Daimons.tsx`, `Nodes.tsx` use `h-full flex flex-col` with full-width `px-6` headers. On 4K monitors the Dashboard ends mid-screen with empty grey. Drop `max-w-7xl`.

14. **Header pattern is a one-off.**
    `web/src/pages/Dashboard.tsx:96-102` uses `<header>` with no border, padding `p-6`. Daimons/Nodes/Findings use `<header className="px-6 py-4 border-b border-border bg-panel">`. The Dashboard's softer header looks deliberate but breaks the muscle memory of "page title sits in a panel-bg strip with a bottom border." Match the others for consistency.

### nit

15. **Loader2 spinner color in `StatTile` is `text-ink-mute`; in `ChartSkeleton` and `ListSkeleton` it's also ink-mute but the wrapper text is `text-ink-mute` so the icon disappears against the same color.**
    `web/src/pages/Dashboard.tsx:331-347` — fine, but the "loading…" text could be `text-ink-dim` for the wrapper while the spinner stays mute. Tiny tweak.

16. **`relTime` and Daimons's `formatAge` and Findings's `fmtAge` are three near-identical functions.**
    `web/src/pages/Dashboard.tsx:358-364`, `Daimons.tsx:327-332`, `Findings.tsx:1143-1150`. Already triplicated — promoting now is cheap.

17. **`AlertTriangle` import-and-discard.**
    `web/src/pages/Dashboard.tsx:16, 368` — `void AlertTriangle` to keep the import. Just remove it; `git blame` will tell you when you need it back.

18. **`<Picker>` button has no `type="button"`.**
    `web/src/pages/Dashboard.tsx:316` — inside a card that's not a form, but if Dashboard is ever embedded in one, these would submit. Cheap defensive fix.

19. **`hover:bg-slate-50/60` on `recent_critical` rows vs `hover:bg-slate-50/40` on stat tiles.**
    Same hover token used at two opacities, three lines apart. Pick one (the rest of the app uses `/60`).

## 3. Patterns to extract

- **`<StatTile>` → `web/src/components/StatTile.tsx`.** Used by Dashboard (4×) and conceptually by `Findings.tsx:668-684 SummaryCard` (7×). Unify into one primitive that accepts `{ label, value, icon?, accent, to?, sub?, loading? }`. `SummaryCard` becomes a special case (no icon, no link).
- **`<Card title subtitle right>` → `web/src/components/Card.tsx`.** The same shape exists inline on `Display.tsx:124-131` and `Dashboard.tsx:290-310`. One component, two callsites; the Findings `TrendPanel`/`ChipPanel` (`Findings.tsx:405-440, 475-507`) are the same shell. Promote and consume.
- **`<SegmentedControl value options onChange>` → `web/src/components/SegmentedControl.tsx`.** Findings has the canonical version (`Findings.tsx:190-211, 213-228`); Dashboard's `Picker` is a worse copy. Adopt Findings's inset-shadow style as the primitive.
- **`<ChartSkeleton>` and `<ListSkeleton>` → `web/src/components/Skeleton.tsx`.** Currently inline in Dashboard. Generalize to `<Skeleton variant="chart" | "list" height={…} />`.
- **`relTime` / `formatAge` / `fmtAge` → `web/src/lib/time.ts`.** Triplicated; pick the one Findings has and re-export from a single module.
- **`sevBar` / `sevDotBg` (Findings) → `web/src/lib/severity.ts`.** Already used on Findings; the Dashboard's "CRIT" badge logic should consume this, not roll its own.

## 4. Design tokens to add to `tailwind.config.ts`

- **Chart palette as a token group.** Today `PALETTE` is hand-rolled inline. Add under `theme.extend.colors.chart`:
  ```
  chart: {
    1: '#7c3aed',  // brand
    2: '#0891b2',  // cyan-600 — replaces second-slot red
    3: '#10b981',  // emerald-500
    4: '#f59e0b',  // amber-500
    5: '#ec4899',  // pink-500
    6: '#84cc16',  // lime-500
    7: '#0ea5e9',  // sky-500
    other: '#64748b', // slate-500 — was #94a3b8
  }
  ```
  Rationale: lets the chart `colorFor` reference `theme('colors.chart.1')` etc. via a small `chartTokens.ts` and survives a brand recolor.
- **`shadow.tooltip`** — `0 2px 6px rgba(0,0,0,0.06)` is currently inline at `DashboardCharts.tsx:152`. Promote to a named shadow `boxShadow.tooltip` so the recharts tooltip and any future popover share it.
- **`colors.bg-hover`** — `slate-50/60` used as the canonical row-hover background. Right now every page hand-writes `hover:bg-slate-50/60`. Add `'bg-hover': 'rgb(248 250 252 / 0.6)'` so it's `hover:bg-bg-hover` and changing the hover tint is one line.
- **`colors.sev.critical` ring/bg pair** — `styles.css:18` hard-codes `bg-purple-50` and `ring-purple-200`. If purple ever shifts, the `sev.critical` token moves but the badge backgrounds don't. Add `sev.critical-bg: '#faf5ff'` (purple-50) and `sev.critical-ring: '#e9d5ff'` (purple-200), and same for `high/medium/low/info`. `severity-badge` rules in `styles.css` then become token-driven.

---

_Total findings: 19. Highest leverage: fix #1 (severity color swap), #2 (PALETTE[0] mismatch), and #3 (CRIT badge color) — those three alone will make the Dashboard read as "part of the same app" in one PR._
