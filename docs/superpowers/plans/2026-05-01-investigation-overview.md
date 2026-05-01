# Investigation Overview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the text-shaped Investigation Overview tab with a layered visual: status header (5-cell row), horizontal Gantt timeline (curated default + lane toggles), and structure summary cards. All client-side derivation from the existing `InvestigationDetail` bundle plus the existing `api.investigations.audit` endpoint when the audit lane is toggled on.

**Architecture:** Three new presentational components in `web/src/components/investigations/` — `CaseStatusBar`, `CaseTimeline` (hand-rolled SVG, no new deps), `CaseStructure` — composed by a refactored `OverviewPanel` in `InvestigationDetail.tsx`. Click affordances reuse the SmartPayload `entity:open` event bus + `EntityDrawerHost` already mounted at App root. The Identity sidebar's content folds into the new status header; `LabelEditor` moves below the three sections alongside `SuggestedFindingsCard`.

**Tech Stack:** React 18 + TypeScript + Tailwind, Vitest + @testing-library/react + jsdom (already configured by the SmartPayload PR), lucide-react icons. No new server-side endpoints.

---

## File structure

### New

- `web/src/components/investigations/CaseStatusBar.tsx` — top 5-cell row.
- `web/src/components/investigations/CaseStatusBar.test.tsx`
- `web/src/components/investigations/CaseTimeline.tsx` — Gantt band + toggles + zoom.
- `web/src/components/investigations/CaseTimeline.test.tsx`
- `web/src/components/investigations/CaseStructure.tsx` — 4 summary cards.
- `web/src/components/investigations/CaseStructure.test.tsx`
- `web/src/components/investigations/timeline/types.ts` — shared `TimelineEvent` discriminated union, lane key enum.
- `web/src/components/investigations/timeline/buildEvents.ts` — pure derivation `bundle → TimelineEvent[]`.
- `web/src/components/investigations/timeline/buildEvents.test.ts`
- `web/src/components/investigations/timeline/scale.ts` — pure `(t, tMin, tMax, width) → x` + zoom helpers.
- `web/src/components/investigations/timeline/scale.test.ts`

### Modified

- `web/src/pages/InvestigationDetail.tsx` — `OverviewPanel` becomes a thin compositor; Identity sidebar content folds into `CaseStatusBar`; `LabelEditor` block moves below the three new sections; `SuggestedFindingsCard` placement unchanged (below the structure cards).

---

## Task Group A — `CaseStructure` (smallest piece, ship first for momentum)

### Task A1: aggregator helpers

**Files:**
- Create: `web/src/components/investigations/CaseStructure.tsx`
- Create: `web/src/components/investigations/CaseStructure.test.tsx`

- [ ] **Step A1.1: Write the failing test for aggregators**

```tsx
// web/src/components/investigations/CaseStructure.test.tsx
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { CaseStructure } from './CaseStructure';
import type { InvestigationDetail } from '../../api';

afterEach(cleanup);

function makeBundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-05-01T00:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [],
    runs: [],
    iocs: [],
    daimons: [],
    orchestrations: [],
    notes: [],
    war_room: false,
    ...over,
  };
}

describe('CaseStructure', () => {
  it('renders four cards with zero counts on an empty bundle', () => {
    render(<CaseStructure bundle={makeBundle()} />);
    expect(screen.getByText(/Hosts/i)).toBeTruthy();
    expect(screen.getByText(/IOCs/i)).toBeTruthy();
    expect(screen.getByText(/Daimons/i)).toBeTruthy();
    expect(screen.getByText(/Runs/i)).toBeTruthy();
    expect(screen.getAllByText(/no .* linked/i).length).toBe(4);
  });

  it('counts distinct hosts and surfaces the heavy hitter', () => {
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: 1, Agent: { String: 'a', Valid: true }, Host: { String: 'h1', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 2, Agent: { String: 'a', Valid: true }, Host: { String: 'h1', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 3, Ts: 3, Agent: { String: 'a', Valid: true }, Host: { String: 'h2', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    render(<CaseStructure bundle={bundle} />);
    expect(screen.getByText(/Hosts \(2\)/)).toBeTruthy();
    // Most-hit host is h1 with 2 findings
    expect(screen.getByText(/h1/)).toBeTruthy();
  });

  it('counts IOCs from bundle.iocs by ObservationCount', () => {
    const bundle = makeBundle({
      iocs: [
        { ID: 1, Kind: 'sha256', Value: 'a'.repeat(64), Severity: { String: 'HIGH', Valid: true }, ObservationCount: 5, HostCount: 2, FirstSeen: '', LastSeen: '' },
        { ID: 2, Kind: 'ip', Value: '1.2.3.4', Severity: { String: 'LOW', Valid: true }, ObservationCount: 23, HostCount: 4, FirstSeen: '', LastSeen: '' },
      ],
    });
    render(<CaseStructure bundle={bundle} />);
    expect(screen.getByText(/IOCs \(2\)/)).toBeTruthy();
    // Top-observed is the ip with 23 obs
    expect(screen.getByText(/1\.2\.3\.4/)).toBeTruthy();
  });

  it('counts daimons by FindingCount with last-seen tie break', () => {
    const bundle = makeBundle({
      daimons: [
        { Agent: 'edr-agent', FindingCount: 9, LastSeenTs: 1000 },
        { Agent: 'compliance', FindingCount: 9, LastSeenTs: 2000 },
        { Agent: 'sre', FindingCount: 1, LastSeenTs: 500 },
      ],
    });
    render(<CaseStructure bundle={bundle} />);
    expect(screen.getByText(/Daimons \(3\)/)).toBeTruthy();
    // Top emitter ties at 9; later last-seen wins (compliance)
    expect(screen.getByText(/compliance/)).toBeTruthy();
  });

  it('counts orchestrations and per-orchestration run-status breakdown', () => {
    const bundle = makeBundle({
      runs: [
        { ID: 1, OrchestrationID: 100, OrchestrationName: { String: 'triage', Valid: true }, Status: 'completed', TriggerKind: 'auto', StartedAt: '2026-05-01T00:00:00Z', EndedAt: { String: '2026-05-01T00:01:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
        { ID: 2, OrchestrationID: 100, OrchestrationName: { String: 'triage', Valid: true }, Status: 'completed', TriggerKind: 'auto', StartedAt: '2026-05-01T00:01:00Z', EndedAt: { String: '2026-05-01T00:02:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
        { ID: 3, OrchestrationID: 100, OrchestrationName: { String: 'triage', Valid: true }, Status: 'failed',    TriggerKind: 'auto', StartedAt: '2026-05-01T00:02:00Z', EndedAt: { String: '2026-05-01T00:03:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: 'oops', Valid: true }, LinkedAt: '' },
      ],
      orchestrations: [
        { OrchestrationID: { Int64: 100, Valid: true }, OrchestrationName: 'triage', RunCount: 3, LastStartedAt: '2026-05-01T00:02:00Z' },
      ],
    });
    render(<CaseStructure bundle={bundle} />);
    expect(screen.getByText(/Runs \(3 .* 1 orch/)).toBeTruthy();
    expect(screen.getByText(/triage/)).toBeTruthy();
    expect(screen.getByText(/2 ✓/)).toBeTruthy();
    expect(screen.getByText(/1 ✗/)).toBeTruthy();
  });
});
```

- [ ] **Step A1.2: Run test, expect FAIL**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview/web
npm test -- --run src/components/investigations/CaseStructure.test.tsx
```

Expected: FAIL — module doesn't exist.

- [ ] **Step A1.3: Implement `CaseStructure.tsx`**

```tsx
// CaseStructure: four-card grid summarising case shape across hosts /
// IOCs / daimons / runs+orchestrations. Pure presentation over the
// existing InvestigationDetail bundle. Click-throughs use react-router
// for tab navigation; row clicks fire entity:open for drawer-bearing
// kinds (IOC, run) — same bus the SmartPayload chips use.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Bot, Hash, Server, Workflow } from 'lucide-react';
import type { InvestigationDetail } from '../../api';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
}

export function CaseStructure({ bundle, cpInstanceID }: Props) {
  const hosts = useMemo(() => aggregateHosts(bundle), [bundle]);
  const iocs = useMemo(() => topIOCs(bundle), [bundle]);
  const daimons = useMemo(() => topDaimons(bundle), [bundle]);
  const orchs = useMemo(() => topOrchestrations(bundle), [bundle]);

  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  const invID = bundle.investigation.ID;

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
      <Card
        icon={<Server size={14} className="text-emerald-600" />}
        title={`Hosts (${hosts.distinct})`}
        empty={hosts.distinct === 0 ? 'no hosts linked yet' : null}
        topLine={hosts.top ? `Most-hit: ${hosts.top.host} — ${hosts.top.count} finding${hosts.top.count === 1 ? '' : 's'}` : null}
        rows={hosts.list.slice(1, 4).map((h) => ({ key: h.host, label: h.host, suffix: `${h.count}` }))}
        tabHref={`/investigations/${invID}?tab=findings${cpQS}`}
      />
      <Card
        icon={<Hash size={14} className="text-purple-600" />}
        title={`IOCs (${iocs.length})`}
        empty={iocs.length === 0 ? 'no IOCs linked yet' : null}
        topLine={iocs[0] ? `Most-observed: ${iocs[0].Kind}:${last4(iocs[0].Value)} — ${iocs[0].ObservationCount} obs across ${iocs[0].HostCount} host${iocs[0].HostCount === 1 ? '' : 's'}` : null}
        rows={iocs.slice(1, 4).map((i) => ({
          key: `${i.Kind}:${i.Value}`,
          label: `${i.Kind}:${last4(i.Value)}`,
          suffix: `${i.ObservationCount} obs`,
          onClick: () => window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'ioc', identityKey: `${i.Kind}:${i.Value}`, cpInstanceID } })),
        }))}
        tabHref={`/investigations/${invID}?tab=iocs${cpQS}`}
      />
      <Card
        icon={<Bot size={14} className="text-indigo-600" />}
        title={`Daimons (${daimons.length})`}
        empty={daimons.length === 0 ? 'no daimons linked yet' : null}
        topLine={daimons[0] ? `Top emitter: ${daimons[0].Agent} — ${daimons[0].FindingCount} finding${daimons[0].FindingCount === 1 ? '' : 's'}, last seen ${relTime(daimons[0].LastSeenTs)}` : null}
        rows={daimons.slice(1, 4).map((d) => ({ key: d.Agent, label: d.Agent, suffix: `${d.FindingCount}` }))}
        tabHref={`/investigations/${invID}?tab=daimons${cpQS}`}
      />
      <Card
        icon={<Workflow size={14} className="text-cyan-600" />}
        title={`Runs (${orchs.runCount} / ${orchs.orchCount} orch${orchs.orchCount === 1 ? '' : 's'})`}
        empty={orchs.runCount === 0 ? 'no runs linked yet' : null}
        topLine={orchs.top ? `Most-run: ${orchs.top.name} — ${orchs.top.runs} run${orchs.top.runs === 1 ? '' : 's'} (${orchs.top.completed} ✓ ${orchs.top.failed} ✗)` : null}
        rows={orchs.list.slice(1, 4).map((o) => ({
          key: o.name,
          label: o.name,
          suffix: `${o.runs} run${o.runs === 1 ? '' : 's'}`,
        }))}
        tabHref={`/investigations/${invID}?tab=runs${cpQS}`}
      />
    </div>
  );
}

interface CardRow {
  key: string;
  label: string;
  suffix: string;
  onClick?: () => void;
}

function Card({
  icon, title, empty, topLine, rows, tabHref,
}: {
  icon: React.ReactNode;
  title: string;
  empty: string | null;
  topLine: string | null;
  rows: CardRow[];
  tabHref: string;
}) {
  return (
    <div className={`border border-border rounded-md bg-white p-3 text-sm ${empty ? 'opacity-60' : ''}`}>
      <div className="flex items-center gap-1.5 mb-1.5">
        {icon}
        <Link to={tabHref} className="font-medium text-ink hover:text-brand-700 truncate">{title}</Link>
      </div>
      {empty ? (
        <div className="text-ink-mute text-xs italic">{empty}</div>
      ) : (
        <>
          {topLine && <div className="text-xs text-ink mb-1.5 truncate">{topLine}</div>}
          {rows.length > 0 && (
            <ul className="text-[11px] text-ink-dim space-y-0.5">
              {rows.map((r) => (
                <li key={r.key} className="flex items-center justify-between gap-2">
                  <button
                    type="button"
                    onClick={r.onClick}
                    className={`truncate text-left ${r.onClick ? 'hover:text-brand-700 cursor-pointer' : 'cursor-default'}`}
                    disabled={!r.onClick}
                  >
                    {r.label}
                  </button>
                  <span className="font-mono text-ink-mute shrink-0">{r.suffix}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}

// ─── aggregators (pure, exported for testability) ─────────────

function aggregateHosts(b: InvestigationDetail) {
  const counts = new Map<string, number>();
  for (const f of b.findings) {
    if (!f.Host.Valid || !f.Host.String) continue;
    counts.set(f.Host.String, (counts.get(f.Host.String) ?? 0) + 1);
  }
  const list = Array.from(counts, ([host, count]) => ({ host, count }))
    .sort((a, b) => b.count - a.count);
  return { distinct: list.length, top: list[0] ?? null, list };
}

function topIOCs(b: InvestigationDetail) {
  return [...b.iocs].sort((a, b) => b.ObservationCount - a.ObservationCount);
}

function topDaimons(b: InvestigationDetail) {
  // FindingCount desc; tie-break by latest LastSeenTs.
  return [...b.daimons].sort((a, b) => {
    if (a.FindingCount !== b.FindingCount) return b.FindingCount - a.FindingCount;
    return b.LastSeenTs - a.LastSeenTs;
  });
}

function topOrchestrations(b: InvestigationDetail) {
  // Group runs by OrchestrationID, joining with bundle.orchestrations
  // for the friendly name. Some runs may reference an orchestration
  // not in the orchestrations list (e.g. deleted) — fall back to
  // OrchestrationName from the run row.
  const byID = new Map<number, { name: string; runs: number; completed: number; failed: number; cancelled: number; running: number }>();
  for (const r of b.runs) {
    const id = r.OrchestrationID;
    const name = r.OrchestrationName.Valid ? r.OrchestrationName.String : `orchestration #${id}`;
    let row = byID.get(id);
    if (!row) {
      row = { name, runs: 0, completed: 0, failed: 0, cancelled: 0, running: 0 };
      byID.set(id, row);
    }
    row.runs += 1;
    if (r.Status === 'completed') row.completed += 1;
    else if (r.Status === 'failed') row.failed += 1;
    else if (r.Status === 'cancelled') row.cancelled += 1;
    else row.running += 1;
  }
  const list = Array.from(byID.values()).sort((a, b) => b.runs - a.runs);
  return {
    runCount: b.runs.length,
    orchCount: byID.size,
    top: list[0] ?? null,
    list,
  };
}

// ─── tiny helpers ───────────────────────────────────────────

function last4(s: string): string {
  return s.length > 4 ? s.slice(-4) : s;
}

function relTime(unixSec: number): string {
  if (!unixSec) return 'never';
  const ageMs = Date.now() - unixSec * 1000;
  const m = Math.floor(ageMs / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
```

- [ ] **Step A1.4: Run tests, expect PASS**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview/web
npm test -- --run src/components/investigations/CaseStructure.test.tsx
node_modules/.bin/tsc -b
```

Expected: 5 PASS, tsc clean.

- [ ] **Step A1.5: Commit**

```bash
git add web/src/components/investigations/CaseStructure.tsx \
        web/src/components/investigations/CaseStructure.test.tsx
git commit -m "web(investigations): CaseStructure summary cards (hosts/IOCs/daimons/runs)"
```

---

## Task Group B — `CaseStatusBar`

### Task B1: 5-cell row component

**Files:**
- Create: `web/src/components/investigations/CaseStatusBar.tsx`
- Create: `web/src/components/investigations/CaseStatusBar.test.tsx`

- [ ] **Step B1.1: Write the failing test**

```tsx
// web/src/components/investigations/CaseStatusBar.test.tsx
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseStatusBar } from './CaseStatusBar';
import type { InvestigationDetail } from '../../api';

afterEach(cleanup);

function makeBundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 7, Title: 't', Status: 'active', Resolution: '', Summary: 'working theory',
      CreatedBy: 'me', CreatedAt: '2026-04-28T00:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

describe('CaseStatusBar', () => {
  it('renders status, severity histogram, freshness, counts, summary on empty case', () => {
    render(
      <MemoryRouter>
        <CaseStatusBar bundle={makeBundle()} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/active/i)).toBeTruthy();
    expect(screen.getByText(/no findings linked yet/i)).toBeTruthy();
    expect(screen.getByText(/working theory/)).toBeTruthy();
  });

  it('severity histogram counts CRITICAL+HIGH+LOW correctly', () => {
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: 1, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'CRITICAL', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 2, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 3, Ts: 3, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    render(<MemoryRouter><CaseStatusBar bundle={bundle} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} /></MemoryRouter>);
    // Each severity segment carries an aria-label like "CRITICAL: 1"
    expect(screen.getByLabelText('CRITICAL: 1')).toBeTruthy();
    expect(screen.getByLabelText('HIGH: 1')).toBeTruthy();
    expect(screen.getByLabelText('LOW: 1')).toBeTruthy();
  });

  it('renders war-room badge when war_room is true', () => {
    const bundle = makeBundle({ war_room: true });
    render(<MemoryRouter><CaseStatusBar bundle={bundle} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} /></MemoryRouter>);
    expect(screen.getByText(/war.room/i)).toBeTruthy();
  });

  it('freshness reports the most recent of finding/note/run timestamps', () => {
    vi.setSystemTime(new Date('2026-05-01T01:00:00Z'));
    const bundle = makeBundle({
      findings: [
        { ID: 1, Ts: Date.parse('2026-04-30T22:00:00Z'), Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
      notes: [
        { ID: 1, InvestigationID: 7, Author: 'me', Body: '...', CreatedAt: '2026-05-01T00:30:00Z' },
      ],
    });
    render(<MemoryRouter><CaseStatusBar bundle={bundle} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={() => {}} /></MemoryRouter>);
    // Most recent is the note at 00:30:00Z, which is 30m before "now"
    expect(screen.getByText(/30m ago/i)).toBeTruthy();
    vi.useRealTimers();
  });

  it('Edit button on Summary toggles editing prop', () => {
    const onEditToggle = vi.fn();
    render(<MemoryRouter><CaseStatusBar bundle={makeBundle()} editing={false} draftSummary="" setDraftSummary={() => {}} onEditToggle={onEditToggle} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /edit/i }));
    expect(onEditToggle).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step B1.2: Run test, expect FAIL**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview/web
npm test -- --run src/components/investigations/CaseStatusBar.test.tsx
```

Expected: FAIL — module doesn't exist.

- [ ] **Step B1.3: Implement `CaseStatusBar.tsx`**

```tsx
// CaseStatusBar: 5-cell single row at the top of the Investigation
// Overview tab. Status pill + severity histogram + freshness +
// counts + truncated Summary with Edit toggle. Pure presentation
// over the InvestigationDetail bundle; the Summary edit state is
// owned by InvestigationDetailPage and threaded in via props.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, Pencil } from 'lucide-react';
import type { InvestigationDetail, Severity } from '../../api';
import { ALL_SEVERITIES } from '../../api';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
  editing: boolean;
  draftSummary: string;
  setDraftSummary: (s: string) => void;
  onEditToggle: () => void;
}

const SEV_BG: Record<Severity, string> = {
  CRITICAL: 'bg-red-500',
  HIGH:     'bg-orange-500',
  MEDIUM:   'bg-amber-400',
  LOW:      'bg-blue-400',
  INFO:     'bg-slate-300',
};

const SEV_TEXT: Record<Severity, string> = {
  CRITICAL: 'text-red-700',
  HIGH:     'text-orange-700',
  MEDIUM:   'text-amber-700',
  LOW:      'text-blue-700',
  INFO:     'text-slate-700',
};

export function CaseStatusBar({ bundle, cpInstanceID, editing, draftSummary, setDraftSummary, onEditToggle }: Props) {
  const inv = bundle.investigation;
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  const invID = inv.ID;

  const sevCounts = useMemo(() => {
    const counts: Record<Severity, number> = { CRITICAL: 0, HIGH: 0, MEDIUM: 0, LOW: 0, INFO: 0 };
    for (const f of bundle.findings) {
      const s = (f.Severity.Valid ? f.Severity.String : 'INFO') as Severity;
      if (s in counts) counts[s] += 1;
    }
    return counts;
  }, [bundle.findings]);

  const sevTotal = bundle.findings.length;

  const freshness = useMemo(() => {
    let latest = 0;
    for (const f of bundle.findings) latest = Math.max(latest, f.Ts);
    for (const n of bundle.notes) latest = Math.max(latest, Date.parse(n.CreatedAt));
    for (const r of bundle.runs) {
      latest = Math.max(latest, Date.parse(r.StartedAt));
      if (r.EndedAt.Valid) latest = Math.max(latest, Date.parse(r.EndedAt.String));
    }
    return latest;
  }, [bundle]);

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
      {/* Status */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Status</div>
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className={`inline-flex items-center text-[11px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 ${statusTone(inv.Status)}`}>
            {inv.Status}
          </span>
          {bundle.war_room && (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-semibold px-1.5 py-0.5 rounded bg-red-600 text-white">
              <AlertTriangle size={10} /> War-room
            </span>
          )}
        </div>
        {inv.CreatedBy && <div className="text-[11px] text-ink-mute truncate">by <code className="font-mono">{inv.CreatedBy}</code></div>}
      </div>

      {/* Severity histogram */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Severity</div>
        {sevTotal === 0 ? (
          <div className="text-ink-mute italic">no findings linked yet</div>
        ) : (
          <>
            <div className="flex h-4 rounded overflow-hidden" role="img" aria-label={`Severity histogram across ${sevTotal} findings`}>
              {ALL_SEVERITIES.map((s) => sevCounts[s] > 0 && (
                <Link
                  key={s}
                  to={`/investigations/${invID}?tab=findings&severity=${s}${cpQS}`}
                  className={SEV_BG[s]}
                  style={{ flexBasis: `${(sevCounts[s] / sevTotal) * 100}%` }}
                  aria-label={`${s}: ${sevCounts[s]}`}
                  title={`${s}: ${sevCounts[s]}`}
                />
              ))}
            </div>
            <div className="flex flex-wrap gap-x-2 gap-y-0.5 text-[10px]">
              {ALL_SEVERITIES.map((s) => sevCounts[s] > 0 && (
                <span key={s} className={SEV_TEXT[s]}>{s} {sevCounts[s]}</span>
              ))}
            </div>
          </>
        )}
      </div>

      {/* Freshness */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Freshness</div>
        <div className="text-ink">Last activity {relTime(freshness)}</div>
        <div className="text-ink-mute">Created {relTime(Date.parse(inv.CreatedAt))}</div>
      </div>

      {/* Counts */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1">
        <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Linked</div>
        <CountLink href={`/investigations/${invID}?tab=findings${cpQS}`} label="findings" n={bundle.findings.length} />
        <CountLink href={`/investigations/${invID}?tab=runs${cpQS}`}     label="runs"     n={bundle.runs.length} />
        <CountLink href={`/investigations/${invID}?tab=iocs${cpQS}`}     label="iocs"     n={bundle.iocs.length} />
        <CountLink href={`/investigations/${invID}?tab=daimons${cpQS}`}  label="daimons"  n={bundle.daimons.length} />
        <CountLink href={`/investigations/${invID}?tab=notes${cpQS}`}    label="notes"    n={bundle.notes.length} />
      </div>

      {/* Summary */}
      <div className="border border-border rounded-md bg-white p-3 text-xs space-y-1.5 lg:col-span-1">
        <div className="flex items-center justify-between gap-2">
          <div className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">Summary</div>
          {!editing && (
            <button
              type="button"
              onClick={onEditToggle}
              className="text-[11px] text-brand-700 hover:text-brand-800 inline-flex items-center gap-0.5"
              aria-label="Edit summary"
            >
              <Pencil size={10} /> Edit
            </button>
          )}
        </div>
        {editing ? (
          <textarea
            value={draftSummary}
            onChange={(e) => setDraftSummary(e.target.value)}
            rows={3}
            className="w-full px-2 py-1 rounded border border-border bg-white text-xs"
            placeholder="Hypothesis, scope, working theory…"
          />
        ) : inv.Summary ? (
          <div className="text-ink line-clamp-3 whitespace-pre-wrap">{inv.Summary}</div>
        ) : (
          <div className="text-ink-mute italic">No summary yet.</div>
        )}
      </div>
    </div>
  );
}

function CountLink({ href, label, n }: { href: string; label: string; n: number }) {
  return (
    <div className="flex items-center justify-between gap-1.5">
      <Link to={href} className="text-ink-mute hover:text-brand-700">{label}</Link>
      <span className="font-mono text-ink">{n}</span>
    </div>
  );
}

function statusTone(s: 'active' | 'closed' | 'archived'): string {
  if (s === 'active') return 'text-blue-700 bg-blue-50 ring-blue-200';
  if (s === 'closed') return 'text-emerald-700 bg-emerald-50 ring-emerald-200';
  return 'text-slate-700 bg-slate-100 ring-slate-200';
}

function relTime(ts: number): string {
  if (!ts || Number.isNaN(ts)) return 'never';
  const ageMs = Date.now() - ts;
  const m = Math.floor(ageMs / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
```

- [ ] **Step B1.4: Run + commit**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview/web
npm test -- --run src/components/investigations/CaseStatusBar.test.tsx
node_modules/.bin/tsc -b
```

Expected: 5 PASS, tsc clean.

```bash
git add web/src/components/investigations/CaseStatusBar.tsx \
        web/src/components/investigations/CaseStatusBar.test.tsx
git commit -m "web(investigations): CaseStatusBar — status / severity / freshness / counts / summary"
```

---

## Task Group C — Timeline scaffold (`buildEvents` + `scale` pure helpers)

### Task C1: Shared types

**Files:**
- Create: `web/src/components/investigations/timeline/types.ts`

- [ ] **Step C1.1: Define the discriminated union**

```ts
// web/src/components/investigations/timeline/types.ts
//
// TimelineEvent — discriminated union covering every kind of dot or
// bar that can land on the case timeline. Lane key drives both the
// y-axis row and the toggle state in localStorage.

export type TimelineLane =
  | 'lifecycle'
  | 'findings'
  | 'runs'
  | 'notes'
  | 'iocs'
  | 'daimons'
  | 'audit';

export interface TimelineLifecycleEvent {
  kind: 'lifecycle';
  ts: number;             // unix ms
  marker: 'created' | 'closed';
  title: string;          // for tooltip
}

export interface TimelineFindingEvent {
  kind: 'finding';
  ts: number;
  id: number;
  severity: 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';
  title: string;
  agent: string;
  host: string;
  cpInstanceID?: string;
}

export interface TimelineRunEvent {
  kind: 'run';
  startTs: number;
  endTs: number;          // === now when still running
  running: boolean;
  id: number;
  status: 'completed' | 'failed' | 'cancelled' | 'running' | string;
  orchestrationName: string;
}

export interface TimelineNoteEvent {
  kind: 'note';
  ts: number;
  id: number;
  author: string;
  body: string;
}

export interface TimelineIOCEvent {
  kind: 'ioc';
  startTs: number;
  endTs: number;
  id: number;
  iocKind: string;
  value: string;
}

export interface TimelineDaimonEvent {
  kind: 'daimon';
  ts: number;
  agent: string;
  // Daimon "ticks" are derived from finding Ts grouped by agent. The
  // referenced finding id is stashed so the click handler can route
  // to the finding drawer (a daimon doesn't have a drawer).
  findingID: number;
}

export interface TimelineAuditEvent {
  kind: 'audit';
  ts: number;
  // The audit endpoint already excludes 'created' / 'closed' (those
  // are lifecycle markers) — this lane only renders the others.
  auditKind: 'note' | 'finding_linked' | 'run_linked';
  by: string;
  title: string;
}

export type TimelineEvent =
  | TimelineLifecycleEvent
  | TimelineFindingEvent
  | TimelineRunEvent
  | TimelineNoteEvent
  | TimelineIOCEvent
  | TimelineDaimonEvent
  | TimelineAuditEvent;

export const DEFAULT_LANES_ON: ReadonlyArray<TimelineLane> = ['lifecycle', 'findings', 'runs', 'notes'];
export const ALL_LANES: ReadonlyArray<TimelineLane> = ['lifecycle', 'findings', 'runs', 'notes', 'iocs', 'daimons', 'audit'];
```

- [ ] **Step C1.2: Commit (no test needed — types only)**

```bash
git add web/src/components/investigations/timeline/types.ts
git commit -m "web(timeline): TimelineEvent discriminated union + lane enum"
```

### Task C2: `buildEvents` derivation

**Files:**
- Create: `web/src/components/investigations/timeline/buildEvents.ts`
- Create: `web/src/components/investigations/timeline/buildEvents.test.ts`

- [ ] **Step C2.1: Write the failing test**

```ts
// web/src/components/investigations/timeline/buildEvents.test.ts
import { describe, it, expect } from 'vitest';
import { buildEvents } from './buildEvents';
import type { InvestigationDetail, InvestigationAuditEvent } from '../../../api';

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-05-01T00:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

describe('buildEvents', () => {
  it('emits a lifecycle "created" event from inv.CreatedAt', () => {
    const evs = buildEvents(bundle(), []);
    const created = evs.find((e) => e.kind === 'lifecycle' && e.marker === 'created');
    expect(created).toBeDefined();
    expect(created?.ts).toBe(Date.parse('2026-05-01T00:00:00Z'));
  });

  it('emits a lifecycle "closed" event when ClosedAt is non-zero', () => {
    const b = bundle({
      investigation: { ...bundle().investigation, Status: 'closed', ClosedAt: '2026-05-02T00:00:00Z' },
    });
    const evs = buildEvents(b, []);
    const closed = evs.find((e) => e.kind === 'lifecycle' && e.marker === 'closed');
    expect(closed).toBeDefined();
  });

  it('omits "closed" lifecycle for active cases (zero-time ClosedAt)', () => {
    const evs = buildEvents(bundle(), []);
    const closed = evs.find((e) => e.kind === 'lifecycle' && e.marker === 'closed');
    expect(closed).toBeUndefined();
  });

  it('maps each finding to a TimelineFindingEvent', () => {
    const b = bundle({
      findings: [
        { ID: 42, Ts: 1234567890000, Agent: { String: 'edr', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 'x', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const evs = buildEvents(b, []);
    const f = evs.find((e) => e.kind === 'finding') as Extract<typeof evs[0], { kind: 'finding' }>;
    expect(f.id).toBe(42);
    expect(f.severity).toBe('HIGH');
    expect(f.ts).toBe(1234567890000);
  });

  it('treats an in-progress run (EndedAt invalid) as running with endTs=now', () => {
    const now = Date.now();
    const b = bundle({
      runs: [
        { ID: 1, OrchestrationID: 100, OrchestrationName: { String: 'tri', Valid: true }, Status: 'running', TriggerKind: 'auto', StartedAt: '2026-05-01T00:00:00Z', EndedAt: { String: '', Valid: false }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
      ],
    });
    const evs = buildEvents(b, []);
    const r = evs.find((e) => e.kind === 'run') as Extract<typeof evs[0], { kind: 'run' }>;
    expect(r.running).toBe(true);
    expect(r.endTs).toBeGreaterThanOrEqual(now);
  });

  it('produces one daimon event per finding emitted by that agent', () => {
    const b = bundle({
      findings: [
        { ID: 1, Ts: 100, Agent: { String: 'edr-agent', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 200, Agent: { String: 'edr-agent', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const evs = buildEvents(b, []);
    const ds = evs.filter((e) => e.kind === 'daimon');
    expect(ds.length).toBe(2);
  });

  it('routes audit events into the audit lane, dropping created/closed (those are lifecycle)', () => {
    const audit: InvestigationAuditEvent[] = [
      { ts: '2026-05-01T00:01:00Z', kind: 'created', by: 'me', title: 'opened' },
      { ts: '2026-05-01T00:05:00Z', kind: 'finding_linked', by: 'me', title: 'linked F#1' },
      { ts: '2026-05-02T00:00:00Z', kind: 'closed', by: 'me', title: 'closed' },
    ];
    const evs = buildEvents(bundle(), audit);
    const auditEvs = evs.filter((e) => e.kind === 'audit');
    expect(auditEvs.length).toBe(1);
    expect((auditEvs[0] as Extract<typeof evs[0], { kind: 'audit' }>).auditKind).toBe('finding_linked');
  });

  it('sorts events by their primary timestamp ascending', () => {
    const b = bundle({
      findings: [
        { ID: 1, Ts: 200, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
        { ID: 2, Ts: 100, Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'LOW', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const evs = buildEvents(b, []).filter((e) => e.kind === 'finding');
    expect((evs[0] as Extract<typeof evs[0], { kind: 'finding' }>).id).toBe(2);
    expect((evs[1] as Extract<typeof evs[0], { kind: 'finding' }>).id).toBe(1);
  });
});
```

- [ ] **Step C2.2: Run, expect FAIL**

```bash
npm test -- --run src/components/investigations/timeline/buildEvents.test.ts
```

Expected: FAIL — module doesn't exist.

- [ ] **Step C2.3: Implement `buildEvents.ts`**

```ts
// web/src/components/investigations/timeline/buildEvents.ts
//
// Pure derivation of the timeline event list from the
// InvestigationDetail bundle + the optional audit-events array
// (audit lane is fetched lazily — only when the operator toggles
// the audit lane on, so the bundle path doesn't pay for it
// up-front).
import type { InvestigationDetail, InvestigationAuditEvent } from '../../../api';
import type { TimelineEvent } from './types';

const ZERO_TIME = '0001-01-01T00:00:00Z';

export function buildEvents(
  bundle: InvestigationDetail,
  audit: InvestigationAuditEvent[],
): TimelineEvent[] {
  const out: TimelineEvent[] = [];

  // Lifecycle: created always; closed when ClosedAt is non-zero.
  const inv = bundle.investigation;
  out.push({
    kind: 'lifecycle',
    ts: Date.parse(inv.CreatedAt),
    marker: 'created',
    title: 'Investigation created',
  });
  if (inv.ClosedAt && inv.ClosedAt !== ZERO_TIME) {
    out.push({
      kind: 'lifecycle',
      ts: Date.parse(inv.ClosedAt),
      marker: 'closed',
      title: 'Investigation closed',
    });
  }

  // Findings → finding events + daimon ticks.
  for (const f of bundle.findings) {
    const sev = (f.Severity.Valid ? f.Severity.String : 'INFO') as TimelineEvent extends { kind: 'finding' } ? never : never;
    out.push({
      kind: 'finding',
      ts: f.Ts,
      id: f.ID,
      severity: (f.Severity.Valid ? f.Severity.String : 'INFO') as 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO',
      title: f.Title.Valid ? f.Title.String : '(no title)',
      agent: f.Agent.Valid ? f.Agent.String : '',
      host: f.Host.Valid ? f.Host.String : '',
    });
    if (f.Agent.Valid && f.Agent.String) {
      out.push({
        kind: 'daimon',
        ts: f.Ts,
        agent: f.Agent.String,
        findingID: f.ID,
      });
    }
    void sev;
  }

  // Runs → bars (use now as endTs for in-flight ones).
  const now = Date.now();
  for (const r of bundle.runs) {
    const startTs = Date.parse(r.StartedAt);
    const endTs = r.EndedAt.Valid ? Date.parse(r.EndedAt.String) : now;
    out.push({
      kind: 'run',
      startTs,
      endTs,
      running: !r.EndedAt.Valid,
      id: r.ID,
      status: r.Status,
      orchestrationName: r.OrchestrationName.Valid ? r.OrchestrationName.String : `orchestration #${r.OrchestrationID}`,
    });
  }

  // Notes → icons.
  for (const n of bundle.notes) {
    out.push({
      kind: 'note',
      ts: Date.parse(n.CreatedAt),
      id: n.ID,
      author: n.Author,
      body: n.Body,
    });
  }

  // IOCs → bars (FirstSeen → LastSeen).
  for (const i of bundle.iocs) {
    out.push({
      kind: 'ioc',
      startTs: Date.parse(i.FirstSeen),
      endTs: Date.parse(i.LastSeen),
      id: i.ID,
      iocKind: i.Kind,
      value: i.Value,
    });
  }

  // Audit (excluding created/closed which are lifecycle).
  for (const a of audit) {
    if (a.kind === 'created' || a.kind === 'closed') continue;
    out.push({
      kind: 'audit',
      ts: Date.parse(a.ts),
      auditKind: a.kind,
      by: a.by,
      title: a.title,
    });
  }

  // Stable sort by primary timestamp ascending.
  out.sort((a, b) => primaryTs(a) - primaryTs(b));
  return out;
}

function primaryTs(e: TimelineEvent): number {
  switch (e.kind) {
    case 'run':
    case 'ioc':
      return e.startTs;
    default:
      return e.ts;
  }
}
```

- [ ] **Step C2.4: Run, expect PASS**

```bash
npm test -- --run src/components/investigations/timeline/buildEvents.test.ts
```

Expected: 8 PASS.

- [ ] **Step C2.5: Commit**

```bash
git add web/src/components/investigations/timeline/buildEvents.ts \
        web/src/components/investigations/timeline/buildEvents.test.ts
git commit -m "web(timeline): pure buildEvents derivation"
```

### Task C3: `scale` pure helpers (time → x, range presets, war-room auto-zoom)

**Files:**
- Create: `web/src/components/investigations/timeline/scale.ts`
- Create: `web/src/components/investigations/timeline/scale.test.ts`

- [ ] **Step C3.1: Write the failing test**

```ts
// web/src/components/investigations/timeline/scale.test.ts
import { describe, it, expect } from 'vitest';
import { tToX, autoFitRange, presetRange } from './scale';

describe('scale', () => {
  it('tToX maps tMin to 0 and tMax to width', () => {
    expect(tToX(100, 100, 200, 1000)).toBe(0);
    expect(tToX(200, 100, 200, 1000)).toBe(1000);
    expect(tToX(150, 100, 200, 1000)).toBe(500);
  });

  it('tToX clamps below tMin and above tMax', () => {
    expect(tToX(50, 100, 200, 1000)).toBe(0);
    expect(tToX(250, 100, 200, 1000)).toBe(1000);
  });

  it('autoFitRange returns [tMin, max(now, tMax)] for a non-empty event set', () => {
    const now = 1_000_000_000;
    const r = autoFitRange([100, 200, 500_000_000], now);
    expect(r.tMin).toBe(100);
    expect(r.tMax).toBe(now);
  });

  it('autoFitRange falls back to [now-1h, now] for empty events', () => {
    const now = 1_000_000_000;
    const r = autoFitRange([], now);
    expect(r.tMax).toBe(now);
    expect(r.tMin).toBe(now - 3_600_000);
  });

  it('autoFitRange war-room mode caps to last 1h regardless of older events', () => {
    const now = 1_000_000_000;
    const r = autoFitRange([100, 200, 500_000_000], now, { warRoom: true });
    expect(r.tMax).toBe(now);
    expect(r.tMin).toBe(now - 3_600_000);
  });

  it('presetRange "1h" returns [now - 1h, now]', () => {
    const now = 1_000_000_000;
    const r = presetRange('1h', now);
    expect(r.tMax).toBe(now);
    expect(r.tMin).toBe(now - 3_600_000);
  });

  it('presetRange "24h" / "7d" produce expected widths', () => {
    const now = 1_000_000_000;
    expect(presetRange('24h', now).tMin).toBe(now - 86_400_000);
    expect(presetRange('7d', now).tMin).toBe(now - 7 * 86_400_000);
  });
});
```

- [ ] **Step C3.2: Run, expect FAIL**

```bash
npm test -- --run src/components/investigations/timeline/scale.test.ts
```

- [ ] **Step C3.3: Implement `scale.ts`**

```ts
// web/src/components/investigations/timeline/scale.ts
//
// Pure time → x mapping + auto-fit / preset range helpers. Kept
// separate from the React component so the math is unit-testable
// in isolation.

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;

export interface Range {
  tMin: number;
  tMax: number;
}

export function tToX(t: number, tMin: number, tMax: number, width: number): number {
  if (tMax <= tMin) return 0;
  const clamped = Math.min(Math.max(t, tMin), tMax);
  return ((clamped - tMin) / (tMax - tMin)) * width;
}

export function autoFitRange(timestamps: number[], now: number, opts?: { warRoom?: boolean }): Range {
  if (opts?.warRoom) {
    return { tMin: now - HOUR_MS, tMax: now };
  }
  if (timestamps.length === 0) {
    return { tMin: now - HOUR_MS, tMax: now };
  }
  let tMin = timestamps[0];
  let tMax = timestamps[0];
  for (const t of timestamps) {
    if (t < tMin) tMin = t;
    if (t > tMax) tMax = t;
  }
  return { tMin, tMax: Math.max(tMax, now) };
}

export type Preset = '1h' | '24h' | '7d';

export function presetRange(p: Preset, now: number): Range {
  if (p === '1h')  return { tMin: now - HOUR_MS, tMax: now };
  if (p === '24h') return { tMin: now - DAY_MS,  tMax: now };
  return { tMin: now - 7 * DAY_MS, tMax: now };
}
```

- [ ] **Step C3.4: Run, expect PASS + commit**

```bash
npm test -- --run src/components/investigations/timeline/scale.test.ts
```

```bash
git add web/src/components/investigations/timeline/scale.ts \
        web/src/components/investigations/timeline/scale.test.ts
git commit -m "web(timeline): pure scale + range helpers"
```

---

## Task Group D — `CaseTimeline` component

### Task D1: Skeleton with lane bands + axis (no events yet)

**Files:**
- Create: `web/src/components/investigations/CaseTimeline.tsx`
- Create: `web/src/components/investigations/CaseTimeline.test.tsx`

- [ ] **Step D1.1: Write the failing test**

```tsx
// web/src/components/investigations/CaseTimeline.test.tsx
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CaseTimeline } from './CaseTimeline';
import type { InvestigationDetail } from '../../api';

afterEach(() => {
  cleanup();
  localStorage.clear();
});

function bundle(over: Partial<InvestigationDetail> = {}): InvestigationDetail {
  return {
    investigation: {
      ID: 1, Title: 't', Status: 'active', Resolution: '', Summary: '',
      CreatedBy: 'me', CreatedAt: '2026-04-30T10:00:00Z',
      ClosedAt: '0001-01-01T00:00:00Z', UpdatedAt: '2026-05-01T00:00:00Z',
    },
    findings: [], runs: [], iocs: [], daimons: [], orchestrations: [], notes: [],
    war_room: false,
    ...over,
  };
}

describe('CaseTimeline (D1 skeleton)', () => {
  it('renders four default lanes (lifecycle/findings/runs/notes) on an empty bundle', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    expect(screen.getByText(/lifecycle/i)).toBeTruthy();
    expect(screen.getByText(/findings/i)).toBeTruthy();
    expect(screen.getByText(/runs/i)).toBeTruthy();
    expect(screen.getByText(/notes/i)).toBeTruthy();
  });

  it('renders empty-state hint when no events exist beyond the lifecycle "created" marker', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    expect(screen.getByText(/no signals yet/i)).toBeTruthy();
  });

  it('toggle bar starts with iocs/daimons/audit off (aria-pressed=false)', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    expect(screen.getByRole('button', { name: /^iocs$/i })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: /^daimons$/i })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: /^audit$/i })).toHaveAttribute('aria-pressed', 'false');
  });

  it('clicking iocs toggle persists to localStorage', () => {
    render(<MemoryRouter><CaseTimeline bundle={bundle()} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /^iocs$/i }));
    const stored = JSON.parse(localStorage.getItem('investigation:overview:lanes') ?? '[]');
    expect(stored).toContain('iocs');
  });
});
```

- [ ] **Step D1.2: Run, expect FAIL**

```bash
npm test -- --run src/components/investigations/CaseTimeline.test.tsx
```

- [ ] **Step D1.3: Implement skeleton (lane bands + toggle bar + empty state; no events yet)**

```tsx
// web/src/components/investigations/CaseTimeline.tsx
//
// Horizontal Gantt-style timeline of case events. Hand-rolled SVG;
// no extra deps. Default-curated lanes (lifecycle/findings/runs/
// notes) + opt-in lanes (iocs/daimons/audit). Audit-lane fetch is
// lazy: triggered the first time the operator toggles audit on,
// using the existing api.investigations.audit endpoint.
//
// Refresh is driven by the parent's existing 30s/5s war-room poll —
// re-render happens on every bundle replacement.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { InvestigationDetail, InvestigationAuditEvent } from '../../api';
import { api } from '../../api';
import { buildEvents } from './timeline/buildEvents';
import { ALL_LANES, DEFAULT_LANES_ON, type TimelineLane } from './timeline/types';
import { autoFitRange, presetRange, tToX, type Preset, type Range } from './timeline/scale';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
}

const STORAGE_KEY = 'investigation:overview:lanes';
const LANE_HEIGHT = 36;
const LANE_LABEL_W = 80;
const HEIGHT = 320;

export function CaseTimeline({ bundle, cpInstanceID }: Props) {
  const [activeLanes, setActiveLanes] = useState<TimelineLane[]>(loadLanes);
  const [audit, setAudit] = useState<InvestigationAuditEvent[]>([]);
  const [range, setRange] = useState<Range | null>(null);
  const auditFetchedRef = useRef(false);

  // Lazy-fetch audit lane the first time it's enabled.
  useEffect(() => {
    if (!activeLanes.includes('audit') || auditFetchedRef.current) return;
    auditFetchedRef.current = true;
    api.investigations.audit(bundle.investigation.ID, cpInstanceID)
      .then(setAudit)
      .catch(() => { auditFetchedRef.current = false; });
  }, [activeLanes, bundle.investigation.ID, cpInstanceID]);

  const events = useMemo(() => buildEvents(bundle, audit), [bundle, audit]);

  const visibleLanes = useMemo(() => ALL_LANES.filter((l) => activeLanes.includes(l)), [activeLanes]);

  // Auto-fit on first render and when bundle freshness advances.
  useEffect(() => {
    const stamps: number[] = [];
    for (const e of events) {
      if (e.kind === 'run' || e.kind === 'ioc') {
        stamps.push(e.startTs, e.endTs);
      } else {
        stamps.push(e.ts);
      }
    }
    setRange(autoFitRange(stamps, Date.now(), { warRoom: bundle.war_room }));
  }, [events, bundle.war_room]);

  const width = 800; // svg viewBox; the SVG itself is responsive via preserveAspectRatio.
  const drawableWidth = width - LANE_LABEL_W;

  function toggleLane(l: TimelineLane) {
    setActiveLanes((prev) => {
      const next = prev.includes(l) ? prev.filter((x) => x !== l) : [...prev, l];
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)); } catch { /* ignore */ }
      return next;
    });
  }

  function applyPreset(p: Preset) {
    setRange(presetRange(p, Date.now()));
  }

  function applyFit() {
    const stamps: number[] = [];
    for (const e of events) {
      if (e.kind === 'run' || e.kind === 'ioc') {
        stamps.push(e.startTs, e.endTs);
      } else {
        stamps.push(e.ts);
      }
    }
    setRange(autoFitRange(stamps, Date.now(), { warRoom: bundle.war_room }));
  }

  const onlyLifecycle = events.length === 1 && events[0].kind === 'lifecycle';

  return (
    <div className="border border-border rounded-md bg-white p-3 space-y-2">
      {/* Toggle bar + zoom controls */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-1.5 flex-wrap">
          {ALL_LANES.map((l) => (
            <button
              key={l}
              type="button"
              onClick={() => toggleLane(l)}
              aria-pressed={activeLanes.includes(l)}
              className={`text-[11px] px-2 py-0.5 rounded-md border ${activeLanes.includes(l) ? 'border-brand-300 bg-brand-50 text-brand-800' : 'border-border bg-white text-ink-mute'}`}
            >
              {l}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1 text-[11px]">
          <button type="button" onClick={applyFit} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">Fit</button>
          <button type="button" onClick={() => applyPreset('1h')} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">1h</button>
          <button type="button" onClick={() => applyPreset('24h')} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">24h</button>
          <button type="button" onClick={() => applyPreset('7d')} className="px-2 py-0.5 rounded border border-border hover:bg-slate-50">7d</button>
        </div>
      </div>

      {/* SVG band */}
      {onlyLifecycle ? (
        <div className="h-[280px] flex items-center justify-center text-sm text-ink-mute italic">
          No signals yet — link findings to populate the timeline.
        </div>
      ) : (
        <svg viewBox={`0 0 ${width} ${HEIGHT}`} className="w-full" style={{ height: HEIGHT }} role="img" aria-label="Case timeline">
          {/* Lane labels */}
          {visibleLanes.map((l, i) => (
            <g key={l}>
              <text x={6} y={i * LANE_HEIGHT + LANE_HEIGHT / 2 + 4} className="text-[11px] fill-ink-mute uppercase tracking-wide" fontSize="11">
                {l}
              </text>
              <line x1={LANE_LABEL_W} x2={width} y1={(i + 1) * LANE_HEIGHT} y2={(i + 1) * LANE_HEIGHT} stroke="#e2e8f0" strokeDasharray="2 4" />
            </g>
          ))}
          {/* Events go here in Task D2 */}
          {range && events.length > 0 && (
            <EventsLayer events={events} visibleLanes={visibleLanes} range={range} laneLabelWidth={LANE_LABEL_W} drawableWidth={drawableWidth} cpInstanceID={cpInstanceID} />
          )}
        </svg>
      )}
    </div>
  );
}

// Stub for Task D2 — replaced with a real implementation there.
function EventsLayer(_props: {
  events: ReturnType<typeof buildEvents>;
  visibleLanes: TimelineLane[];
  range: Range;
  laneLabelWidth: number;
  drawableWidth: number;
  cpInstanceID?: string;
}) {
  return null;
}

// silence unused-tToX warning; D2 wires it in.
void tToX;

function loadLanes(): TimelineLane[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [...DEFAULT_LANES_ON];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...DEFAULT_LANES_ON];
    return parsed.filter((x): x is TimelineLane => ALL_LANES.includes(x as TimelineLane));
  } catch {
    return [...DEFAULT_LANES_ON];
  }
}
```

- [ ] **Step D1.4: Run, expect PASS + commit**

```bash
npm test -- --run src/components/investigations/CaseTimeline.test.tsx
node_modules/.bin/tsc -b
```

Expected: 4 PASS.

```bash
git add web/src/components/investigations/CaseTimeline.tsx \
        web/src/components/investigations/CaseTimeline.test.tsx
git commit -m "web(timeline): CaseTimeline skeleton — lanes, toggles, range presets"
```

### Task D2: Render finding dots, run bars, note icons, lifecycle markers

**Files:**
- Modify: `web/src/components/investigations/CaseTimeline.tsx` — replace the `EventsLayer` stub with a real renderer.
- Modify: `web/src/components/investigations/CaseTimeline.test.tsx` — add tests.

- [ ] **Step D2.1: Add tests**

Append to `CaseTimeline.test.tsx`:

```tsx
describe('CaseTimeline (D2 events)', () => {
  it('renders one finding dot per finding event with an aria-label including severity', async () => {
    const b = bundle({
      findings: [
        { ID: 1, Ts: Date.parse('2026-04-30T11:00:00Z'), Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'CRITICAL', Valid: true }, Title: { String: 'crit', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    expect(screen.getByLabelText(/Finding #1.*CRITICAL/i)).toBeTruthy();
  });

  it('renders a run bar spanning startTs..endTs with status tone', async () => {
    const b = bundle({
      runs: [
        { ID: 5, OrchestrationID: 100, OrchestrationName: { String: 'tri', Valid: true }, Status: 'completed', TriggerKind: 'auto', StartedAt: '2026-04-30T11:00:00Z', EndedAt: { String: '2026-04-30T11:30:00Z', Valid: true }, CurrentStepID: { String: '', Valid: false }, Error: { String: '', Valid: false }, LinkedAt: '' },
      ],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    expect(screen.getByLabelText(/Run #5.*completed/i)).toBeTruthy();
  });

  it('clicking a finding dot fires entity:open event for the drawer', async () => {
    const b = bundle({
      findings: [
        { ID: 7, Ts: Date.parse('2026-04-30T11:00:00Z'), Agent: { String: 'a', Valid: true }, Host: { String: 'h', Valid: true }, Severity: { String: 'HIGH', Valid: true }, Title: { String: 't', Valid: true }, Status: { String: 'open', Valid: true }, Tags: { String: '', Valid: false }, Subtype: { String: '', Valid: false }, LinkedAt: '', LinkMethod: { String: '', Valid: false }, LinkedBy: { String: '', Valid: false } },
      ],
    });
    const events: { kind: string; identityKey: string }[] = [];
    const handler = (e: Event) => {
      const ce = e as CustomEvent<{ kind: string; identityKey: string }>;
      events.push(ce.detail);
    };
    window.addEventListener('entity:open', handler);
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByLabelText(/Finding #7/));
    window.removeEventListener('entity:open', handler);
    expect(events).toEqual([{ kind: 'finding', identityKey: '7' }]);
  });
});
```

- [ ] **Step D2.2: Implement `EventsLayer`**

Replace the stub `EventsLayer` in `CaseTimeline.tsx` with:

```tsx
import { tToX } from './timeline/scale';
import type { TimelineEvent } from './timeline/types';

const SEV_FILL: Record<string, string> = {
  CRITICAL: '#dc2626',
  HIGH:     '#ea580c',
  MEDIUM:   '#f59e0b',
  LOW:      '#3b82f6',
  INFO:     '#94a3b8',
};

const RUN_FILL: Record<string, string> = {
  completed: '#10b981',
  failed:    '#ef4444',
  cancelled: '#94a3b8',
  running:   '#3b82f6',
};

function EventsLayer({ events, visibleLanes, range, laneLabelWidth, drawableWidth, cpInstanceID }: {
  events: TimelineEvent[];
  visibleLanes: TimelineLane[];
  range: Range;
  laneLabelWidth: number;
  drawableWidth: number;
  cpInstanceID?: string;
}) {
  const laneIndex = (l: TimelineLane) => visibleLanes.indexOf(l);
  const x = (t: number) => laneLabelWidth + tToX(t, range.tMin, range.tMax, drawableWidth);
  const yMid = (l: TimelineLane) => laneIndex(l) * LANE_HEIGHT + LANE_HEIGHT / 2;

  function fire(kind: string, identityKey: string) {
    window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind, identityKey, cpInstanceID } }));
  }

  return (
    <g>
      {events.map((e, i) => {
        switch (e.kind) {
          case 'lifecycle':
            if (laneIndex('lifecycle') < 0) return null;
            return (
              <text
                key={i}
                x={x(e.ts)}
                y={yMid('lifecycle') + 4}
                fontSize="14"
                textAnchor="middle"
                role="img"
                aria-label={e.title}
              >
                {e.marker === 'created' ? '⊕' : '⊗'}
              </text>
            );
          case 'finding':
            if (laneIndex('findings') < 0) return null;
            return (
              <circle
                key={i}
                cx={x(e.ts)}
                cy={yMid('findings')}
                r={5}
                fill={SEV_FILL[e.severity] ?? SEV_FILL.INFO}
                role="button"
                aria-label={`Finding #${e.id}: ${e.severity} ${e.title} on ${e.host}`}
                tabIndex={0}
                style={{ cursor: 'pointer' }}
                onClick={() => fire('finding', String(e.id))}
                onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('finding', String(e.id)); }}
              >
                <title>{`Finding #${e.id} ${e.severity} — ${e.title} (${e.agent} on ${e.host})`}</title>
              </circle>
            );
          case 'run': {
            if (laneIndex('runs') < 0) return null;
            const x0 = x(e.startTs);
            const x1 = x(e.endTs);
            const w = Math.max(2, x1 - x0);
            return (
              <rect
                key={i}
                x={x0}
                y={yMid('runs') - 6}
                width={w}
                height={12}
                rx={2}
                fill={RUN_FILL[e.status] ?? RUN_FILL.cancelled}
                role="button"
                aria-label={`Run #${e.id}: ${e.status} (${e.orchestrationName})`}
                tabIndex={0}
                style={{ cursor: 'pointer', opacity: e.running ? 0.85 : 1 }}
                onClick={() => fire('run', String(e.id))}
                onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('run', String(e.id)); }}
              >
                <title>{`Run #${e.id} ${e.status} — ${e.orchestrationName}${e.running ? ' (running)' : ''}`}</title>
              </rect>
            );
          }
          case 'note':
            if (laneIndex('notes') < 0) return null;
            return (
              <text
                key={i}
                x={x(e.ts)}
                y={yMid('notes') + 4}
                fontSize="12"
                textAnchor="middle"
                role="img"
                aria-label={`Note by ${e.author}`}
              >
                ✎<title>{`${e.author}: ${e.body.slice(0, 80)}`}</title>
              </text>
            );
          case 'ioc': {
            if (laneIndex('iocs') < 0) return null;
            const x0 = x(e.startTs);
            const x1 = x(e.endTs);
            return (
              <rect
                key={i}
                x={x0}
                y={yMid('iocs') - 2}
                width={Math.max(2, x1 - x0)}
                height={4}
                fill="#a855f7"
                role="button"
                aria-label={`IOC ${e.iocKind}:${e.value.slice(-4)}`}
                tabIndex={0}
                style={{ cursor: 'pointer' }}
                onClick={() => fire('ioc', `${e.iocKind}:${e.value}`)}
                onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') fire('ioc', `${e.iocKind}:${e.value}`); }}
              >
                <title>{`IOC ${e.iocKind}:${e.value}`}</title>
              </rect>
            );
          }
          case 'daimon':
            if (laneIndex('daimons') < 0) return null;
            return (
              <rect
                key={i}
                x={x(e.ts) - 2}
                y={yMid('daimons') - 2}
                width={4}
                height={4}
                fill="#6366f1"
                role="button"
                aria-label={`Daimon ${e.agent}`}
                tabIndex={0}
                style={{ cursor: 'pointer' }}
                onClick={() => fire('finding', String(e.findingID))}
              >
                <title>{`${e.agent} fired finding #${e.findingID}`}</title>
              </rect>
            );
          case 'audit':
            if (laneIndex('audit') < 0) return null;
            return (
              <text
                key={i}
                x={x(e.ts)}
                y={yMid('audit') + 4}
                fontSize="11"
                textAnchor="middle"
                fill="#64748b"
                aria-label={`Audit ${e.auditKind} by ${e.by}`}
              >
                ↗<title>{`${e.auditKind} by ${e.by}: ${e.title}`}</title>
              </text>
            );
        }
      })}
    </g>
  );
}
```

(Make sure to remove the `void tToX;` line in the previous task since the import is now used.)

- [ ] **Step D2.3: Run + commit**

```bash
npm test -- --run src/components/investigations/CaseTimeline.test.tsx
node_modules/.bin/tsc -b
```

Expected: 7 PASS.

```bash
git add web/src/components/investigations/CaseTimeline.tsx \
        web/src/components/investigations/CaseTimeline.test.tsx
git commit -m "web(timeline): render finding dots, run bars, note icons, IOC bars, daimon ticks, audit, lifecycle"
```

---

## Task Group E — Compositor: rewrite `OverviewPanel`

### Task E1: Wire the three new components into the Overview tab

**Files:**
- Modify: `web/src/pages/InvestigationDetail.tsx`

- [ ] **Step E1.1: Add `setEditing` to OverviewPanel props and the call site**

Find the `OverviewPanel` definition (around line 419) and add to its props:

```tsx
function OverviewPanel({
  inv, invID, cpInstanceID, editing, setEditing, draftSummary, setDraftSummary, bundle, onChange,
}: {
  inv: Investigation;
  invID: number;
  cpInstanceID?: string;
  editing: boolean;
  setEditing: (v: boolean) => void;
  draftSummary: string;
  setDraftSummary: (s: string) => void;
  bundle: InvestigationDetail;
  onChange: () => void;
}) {
```

Find the OverviewPanel call site (around line 324; the `editing={editing}` prop) and add `setEditing={setEditing}` next to it.

- [ ] **Step E1.2: Replace `OverviewPanel` body**

Replace the `return (...)` JSX in `OverviewPanel` (lines ~440–501) with:

```tsx
  return (
    <section className="space-y-4">
      <CaseStatusBar
        bundle={bundle}
        cpInstanceID={cpInstanceID}
        editing={editing}
        draftSummary={draftSummary}
        setDraftSummary={setDraftSummary}
        onEditToggle={() => setEditing(true)}
      />
      <CaseTimeline bundle={bundle} cpInstanceID={cpInstanceID} />
      <CaseStructure bundle={bundle} cpInstanceID={cpInstanceID} />

      {showSuggestions && (
        <SuggestedFindingsCard
          invID={invID}
          cpInstanceID={cpInstanceID}
          onChange={onChange}
          readOnly={!isActive}
        />
      )}

      <div id="labels-card" className="border border-border rounded-md bg-white p-4">
        <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-2">Labels</div>
        <LabelEditor kind="investigation" idOrKey={bundle.investigation.ID} />
      </div>
    </section>
  );
```

(The Identity sidebar's content collapsed into the new `CaseStatusBar`'s Status + Counts + Freshness cells; ID/Created/Updated/Closed live in those cells now. Keep `KV` / `fmtDate` / `isZeroTime` helpers — they're used by other panels in the file.)

- [ ] **Step E1.3: Add the imports**

At the top of `InvestigationDetail.tsx` (next to the other component imports):

```tsx
import { CaseStatusBar } from '../components/investigations/CaseStatusBar';
import { CaseTimeline } from '../components/investigations/CaseTimeline';
import { CaseStructure } from '../components/investigations/CaseStructure';
```

- [ ] **Step E1.4: Sweep the test suite + type-check**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview/web
node_modules/.bin/tsc -b
npm test -- --run
```

Both must pass. The existing investigation-page rendering test (if any) may need a small adjustment if it asserted on the old Identity sidebar text — keep its expectations on tab badges + status pill, both of which the new layout still surfaces.

- [ ] **Step E1.5: Commit**

```bash
git add web/src/pages/InvestigationDetail.tsx
git commit -m "web(investigations): replace text-shaped Overview tab with status / timeline / structure layering"
```

---

## Task Group Z — docs + sweep + PR body

### Task Z1: Architecture doc append

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

```markdown
## Investigation Overview — layered status / timeline / structure

The Investigation Overview tab (`/investigations/{id}?tab=overview`)
renders three stacked sections:

1. **Status header** (`web/src/components/investigations/CaseStatusBar.tsx`)
   — five-cell row with status pill + war-room badge,
   severity histogram, freshness, linked-entity counts, and the
   editable Summary.

2. **Timeline** (`web/src/components/investigations/CaseTimeline.tsx`)
   — horizontal Gantt band, hand-rolled SVG. Default-curated lanes
   (lifecycle / findings / runs / notes); opt-in lanes (iocs /
   daimons / audit). Audit-lane fetch is lazy via the existing
   `api.investigations.audit` endpoint. Lane state persists in
   `localStorage` keyed by `investigation:overview:lanes`. War-room
   cases auto-zoom to the last 1 hour.

3. **Structure** (`web/src/components/investigations/CaseStructure.tsx`)
   — four summary cards (hosts / IOCs / daimons / runs ×
   orchestrations) with a top-hitter line and a 3-row mini-list per
   card.

All three sections are pure derivations of the existing
`InvestigationDetail` bundle (loaded via
`api.investigations.get`); no new server-side endpoints. Click
affordances reuse the SmartPayload `entity:open` event bus +
`EntityDrawerHost` mounted at the App root.
```

```bash
git add docs/architecture.md
git commit -m "docs(architecture): investigation Overview layered layout"
```

### Task Z2: Test sweep

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview/web
node_modules/.bin/tsc -b
npm test -- --run 2>&1 | tail -15
```

All must pass.

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview
mkdir -p controlplane/ui/dist && touch controlplane/ui/dist/.gitkeep
go test ./... -count=1 -timeout 180s 2>&1 | grep -E "^(FAIL|ok|---)" | head -25
rm -rf controlplane/ui/dist
```

All Go packages must pass (no Go code changed in this PR; this is a smoke check).

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-05-01-investigation-overview-pr-body.md`

```markdown
## Summary

Replaces the text-shaped Investigation Overview tab with a layered
visual that answers three operator questions at a glance: case
status (where am I?), chronology (what happened?), and structure
(what's involved?). Pure client-side derivation of the existing
`InvestigationDetail` bundle plus the existing
`api.investigations.audit` endpoint when the audit lane is toggled
on. No new server-side endpoints.

- New `CaseStatusBar` (`web/src/components/investigations/CaseStatusBar.tsx`)
  — 5-cell row: status pill + war-room badge, severity histogram,
  freshness, linked-entity counts, editable Summary.
- New `CaseTimeline` (`web/src/components/investigations/CaseTimeline.tsx`)
  — horizontal Gantt band, hand-rolled SVG. Default-curated lanes
  (lifecycle / findings / runs / notes); opt-in lanes (iocs /
  daimons / audit). Audit-lane fetch is lazy. Lane state persists
  per-browser via `localStorage`. War-room cases auto-zoom to last
  1 hour.
- New `CaseStructure`
  (`web/src/components/investigations/CaseStructure.tsx`) — four
  summary cards (hosts / IOCs / daimons / runs × orchestrations)
  with top-hitter line + 3-row mini-list per card.
- `OverviewPanel` in `InvestigationDetail.tsx` becomes a thin
  compositor of the three new components; Identity sidebar's
  content folds into the new status header; `LabelEditor` block
  moves below the three sections alongside `SuggestedFindingsCard`.

Click affordances reuse the SmartPayload `entity:open` event bus +
`EntityDrawerHost` we shipped earlier — finding dots / run bars /
IOC bars all open the matching detail drawer in-place.

## Test plan

Unit tests landed in this PR (component-level, vitest +
@testing-library/react):
- `CaseStatusBar.test.tsx` — status pill + war-room badge,
  severity histogram, freshness derivation, edit toggle.
- `CaseStructure.test.tsx` — four-card aggregations
  (hosts/IOCs/daimons/orchestrations) + heavy-hitter selection +
  empty-state rendering.
- `CaseTimeline.test.tsx` — default lane state, toggle persistence
  to localStorage, finding-dot / run-bar rendering, click →
  `entity:open` dispatch.
- `timeline/buildEvents.test.ts` — lifecycle markers, audit lane
  filtering, sorted output.
- `timeline/scale.test.ts` — `tToX` clamping, `autoFitRange`
  fallback, war-room override, presets.

Manual lab smoke (post-merge):
- [ ] Open an active investigation with ≥ 5 findings, ≥ 1 run,
      ≥ 1 note. Confirm the three sections render correctly.
- [ ] Toggle on `iocs`, `daimons`, `audit` lanes; verify rendering
      and that the audit lane fetches lazily (network tab).
- [ ] Click a finding dot → FindingDrawer opens.
- [ ] Click a run bar → run drawer / detail page opens.
- [ ] Resize narrow → status row collapses to 2-column / 1-column
      grid.
- [ ] Tag a linked finding `war-bridge` → war-room badge appears
      and the timeline auto-zooms to "last 1 hour" on next reload.

## Files

- New components: `web/src/components/investigations/{CaseStatusBar,CaseTimeline,CaseStructure}.tsx`
  + tests.
- New helpers: `web/src/components/investigations/timeline/{types.ts,buildEvents.ts,scale.ts}` + tests.
- Modified: `web/src/pages/InvestigationDetail.tsx` — `OverviewPanel`
  becomes a thin compositor.
- Docs: `docs/architecture.md` "Investigation Overview" section.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-05-01-investigation-overview-design.md`
- Plan: `docs/superpowers/plans/2026-05-01-investigation-overview.md`

## Open follow-ups

Tracked in the
`investigation_overview_followups.md` memory:
1. Bipartite relationship graph as a `Graph view` tab.
2. Server-side aggregation endpoints for case-overview at scale.
3. Note streaming / collaborative editing.
4. Search / filter expressions on the timeline.
5. Pinned annotations / range-select / case "phases".
6. PDF / print export of the overview.
```

```bash
git add docs/superpowers/plans/2026-05-01-investigation-overview-pr-body.md
git commit -m "docs: investigation Overview PR body"
```

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.
