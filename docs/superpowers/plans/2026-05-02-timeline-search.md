# Investigation Timeline Search & Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add structured filter controls (severity / host / agent / run-status) above the investigation timeline, persisting selections as named saved searches under the existing `saved_searches` infrastructure with `scope='investigation_timeline'`.

**Architecture:** Pure frontend feature. No new server endpoints, no new DB schema. A pure `applyTimelineFilter(events, filter)` predicate runs client-side over the events the timeline already builds. A `<TimelineFilterBar>` renders the chips and typeahead; the existing `<SavedSearchesBar>` handles save/list/default — exactly as the Findings page does. Defaults auto-apply on case mount.

**Tech Stack:** React 18 + TypeScript + Vitest + @testing-library/react + jsdom. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-02-timeline-search-design.md`

**Branch:** `feat/timeline-search` (current worktree)

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `web/src/components/investigations/timeline/filter.ts` | NEW | Pure `TimelineFilterConfig` + `applyTimelineFilter` predicate |
| `web/src/components/investigations/timeline/filter.test.ts` | NEW | Unit tests for the predicate |
| `web/src/components/investigations/TimelineFilterBar.tsx` | NEW | Severity chips, host typeahead, agent typeahead, run-status chips, Clear |
| `web/src/components/investigations/TimelineFilterBar.test.tsx` | NEW | Component tests for the filter bar |
| `web/src/components/investigations/CaseTimeline.tsx` | MODIFY | Mount `<SavedSearchesBar>` + `<TimelineFilterBar>`, apply filter, zero-matches banner, default-apply on mount |
| `web/src/components/investigations/CaseTimeline.test.tsx` | MODIFY | Add tests for filter integration + banner + default-apply |
| `web/src/api.ts` | MODIFY | Export `TimelineFilterConfig` type next to existing `FindingsFilterConfig` |
| `docs/architecture.md` | MODIFY | New section under "Investigation Overview" describing the filter |

**Saved-search dropdown note:** the spec's UI layout sketch shows an inline saved-search dropdown, but Q3 said "mirror Findings page exactly." Findings uses the existing `<SavedSearchesBar>` (a row of named pills with kebab menu — list / apply / save / rename / set-default / delete). We reuse it here verbatim with `scope='investigation_timeline'`. `<TimelineFilterBar>` therefore owns only the filter inputs (chips + typeahead + Clear) — no saved-search UI inside it.

---

## Task 1: Pure filter module

The filter logic is the load-bearing piece. Build it in isolation first, with no React dependencies, so the predicate semantics are locked down before any UI code can drift.

**Files:**
- Create: `web/src/components/investigations/timeline/filter.ts`
- Create: `web/src/components/investigations/timeline/filter.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/investigations/timeline/filter.test.ts`:

```typescript
import { describe, it, expect } from 'vitest';
import { applyTimelineFilter, type TimelineFilterConfig } from './filter';
import type { TimelineEvent } from './types';

function finding(over: Partial<Extract<TimelineEvent, { kind: 'finding' }>> = {}): TimelineEvent {
  return {
    kind: 'finding',
    ts: 1_000,
    id: 1,
    severity: 'HIGH',
    title: 't',
    agent: 'edr-agent',
    host: 'edr-fedora-3',
    ...over,
  };
}

function run(over: Partial<Extract<TimelineEvent, { kind: 'run' }>> = {}): TimelineEvent {
  return {
    kind: 'run',
    startTs: 1_000,
    endTs: 2_000,
    running: false,
    id: 1,
    status: 'completed',
    orchestrationName: 'orch',
    ...over,
  };
}

function note(over: Partial<Extract<TimelineEvent, { kind: 'note' }>> = {}): TimelineEvent {
  return { kind: 'note', ts: 1_500, id: 1, author: 'me', body: 'b', ...over };
}

function lifecycle(): TimelineEvent {
  return { kind: 'lifecycle', ts: 0, marker: 'created', title: 'created' };
}

function daimon(over: Partial<Extract<TimelineEvent, { kind: 'daimon' }>> = {}): TimelineEvent {
  return { kind: 'daimon', ts: 1_000, agent: 'edr-agent', findingID: 1, ...over };
}

describe('applyTimelineFilter', () => {
  it('returns all events for an empty filter', () => {
    const events: TimelineEvent[] = [lifecycle(), finding(), run(), note()];
    expect(applyTimelineFilter(events, {})).toEqual(events);
  });

  it('returns empty array on empty input', () => {
    expect(applyTimelineFilter([], { severities: ['HIGH'] })).toEqual([]);
  });

  it('severity filter keeps matching findings, passes through other kinds', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, severity: 'CRITICAL' }),
      finding({ id: 2, severity: 'LOW' }),
      note(),
      lifecycle(),
      run(),
    ];
    const filter: TimelineFilterConfig = { severities: ['CRITICAL'] };
    const out = applyTimelineFilter(events, filter);
    expect(out.map((e) => e.kind + ':' + (e.kind === 'finding' ? e.id : ''))).toEqual([
      'finding:1', 'note:', 'lifecycle:', 'run:',
    ]);
  });

  it('severity OR-s within the dimension', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, severity: 'CRITICAL' }),
      finding({ id: 2, severity: 'HIGH' }),
      finding({ id: 3, severity: 'MEDIUM' }),
    ];
    const out = applyTimelineFilter(events, { severities: ['CRITICAL', 'HIGH'] });
    expect(out.map((e) => (e.kind === 'finding' ? e.id : 0))).toEqual([1, 2]);
  });

  it('host filter applies to findings only (others pass through)', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, host: 'edr-fedora-3' }),
      finding({ id: 2, host: 'web-1' }),
      note(),
      run(),
    ];
    const out = applyTimelineFilter(events, { host: 'edr-fedora-3' });
    const findings = out.filter((e) => e.kind === 'finding');
    expect(findings.map((f) => (f.kind === 'finding' ? f.id : 0))).toEqual([1]);
    expect(out.some((e) => e.kind === 'note')).toBe(true);
    expect(out.some((e) => e.kind === 'run')).toBe(true);
  });

  it('agent filter applies to findings AND daimons', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, agent: 'edr-agent' }),
      finding({ id: 2, agent: 'web-agent' }),
      daimon({ findingID: 1, agent: 'edr-agent' }),
      daimon({ findingID: 2, agent: 'web-agent' }),
      note(),
    ];
    const out = applyTimelineFilter(events, { agent: 'edr-agent' });
    const findings = out.filter((e) => e.kind === 'finding');
    const daimons = out.filter((e) => e.kind === 'daimon');
    expect(findings.map((f) => (f.kind === 'finding' ? f.id : 0))).toEqual([1]);
    expect(daimons.map((d) => (d.kind === 'daimon' ? d.findingID : 0))).toEqual([1]);
    expect(out.some((e) => e.kind === 'note')).toBe(true);
  });

  it('run-status filter applies to runs only', () => {
    const events: TimelineEvent[] = [
      run({ id: 1, status: 'completed' }),
      run({ id: 2, status: 'failed' }),
      run({ id: 3, status: 'cancelled' }),
      note(),
      finding(),
    ];
    const out = applyTimelineFilter(events, { run_statuses: ['failed', 'completed'] });
    const runs = out.filter((e) => e.kind === 'run');
    expect(runs.map((r) => (r.kind === 'run' ? r.id : 0)).sort()).toEqual([1, 2]);
    expect(out.some((e) => e.kind === 'finding')).toBe(true);
    expect(out.some((e) => e.kind === 'note')).toBe(true);
  });

  it('AND-s across dimensions (severity + host)', () => {
    const events: TimelineEvent[] = [
      finding({ id: 1, severity: 'HIGH', host: 'edr-fedora-3' }),
      finding({ id: 2, severity: 'HIGH', host: 'web-1' }),
      finding({ id: 3, severity: 'LOW', host: 'edr-fedora-3' }),
    ];
    const out = applyTimelineFilter(events, { severities: ['HIGH'], host: 'edr-fedora-3' });
    const findings = out.filter((e) => e.kind === 'finding');
    expect(findings.map((f) => (f.kind === 'finding' ? f.id : 0))).toEqual([1]);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd web && npx vitest run src/components/investigations/timeline/filter.test.ts
```

Expected: FAIL — `Cannot find module './filter'` or similar.

- [ ] **Step 3: Implement `filter.ts`**

Create `web/src/components/investigations/timeline/filter.ts`:

```typescript
// Pure filter predicate for the investigation timeline. Empty /
// missing fields = no filter on that dimension. Pass-through
// semantic: a dimension only narrows the kinds it applies to;
// other kinds pass through unchanged so the operator's lane
// toggles remain the only knob that hides whole kinds.

import type { TimelineEvent } from './types';

export type Severity = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';
export type RunStatus = 'completed' | 'failed' | 'cancelled' | 'running';

export interface TimelineFilterConfig {
  severities?: Severity[];
  host?: string;
  agent?: string;
  run_statuses?: RunStatus[];
}

export function applyTimelineFilter(
  events: TimelineEvent[],
  filter: TimelineFilterConfig,
): TimelineEvent[] {
  const sevSet = filter.severities && filter.severities.length > 0 ? new Set(filter.severities) : null;
  const runSet = filter.run_statuses && filter.run_statuses.length > 0 ? new Set(filter.run_statuses) : null;
  const host = filter.host && filter.host.trim() !== '' ? filter.host : null;
  const agent = filter.agent && filter.agent.trim() !== '' ? filter.agent : null;

  if (sevSet === null && runSet === null && host === null && agent === null) {
    return events;
  }

  return events.filter((e) => {
    if (sevSet && e.kind === 'finding' && !sevSet.has(e.severity)) return false;
    if (host && e.kind === 'finding' && e.host !== host) return false;
    if (agent && e.kind === 'finding' && e.agent !== agent) return false;
    if (agent && e.kind === 'daimon' && e.agent !== agent) return false;
    if (runSet && e.kind === 'run' && !runSet.has(e.status as RunStatus)) return false;
    return true;
  });
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd web && npx vitest run src/components/investigations/timeline/filter.test.ts
```

Expected: PASS — all 8 tests green.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/investigations/timeline/filter.ts web/src/components/investigations/timeline/filter.test.ts
git commit -m "feat(timeline): pure applyTimelineFilter predicate + tests"
```

---

## Task 2: Re-export TimelineFilterConfig from api.ts

The Findings page exports `FindingsFilterConfig` from `web/src/api.ts` next to `SavedSearch`. We do the same for `TimelineFilterConfig` so the saved-search payload type lives next to the table type — keeps a single import surface for consumers.

**Files:**
- Modify: `web/src/api.ts:604` (add export after `FindingsFilterConfig`)

- [ ] **Step 1: Locate the existing FindingsFilterConfig**

Read `web/src/api.ts` lines 580–625 to confirm the position. The new export sits immediately after the `FindingsFilterConfig` interface ends.

- [ ] **Step 2: Add the re-export**

Insert after the `FindingsFilterConfig` interface in `web/src/api.ts`:

```typescript
/** Investigation-timeline-scoped saved-search payload. Persisted under
 *  scope='investigation_timeline'. Empty fields = no filter on that
 *  dimension. Mirrored from
 *  web/src/components/investigations/timeline/filter.ts so other
 *  callers (e.g. saved-search default-apply effect) can reference
 *  the type without reaching into a component subdirectory. */
export type { TimelineFilterConfig } from './components/investigations/timeline/filter';
```

- [ ] **Step 3: Verify typecheck still passes**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS — no type errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/api.ts
git commit -m "feat(api): re-export TimelineFilterConfig type"
```

---

## Task 3: TimelineFilterBar component

The filter UI: severity chips, host typeahead, agent typeahead, run-status chips, Clear. **No** saved-search controls — those live in the existing `<SavedSearchesBar>` mounted by the parent. Component is dumb: receives `filter` + `onChange`, derives typeahead options from `bundle`.

**Files:**
- Create: `web/src/components/investigations/TimelineFilterBar.tsx`
- Create: `web/src/components/investigations/TimelineFilterBar.test.tsx`

- [ ] **Step 1: Write the first failing test**

Create `web/src/components/investigations/TimelineFilterBar.test.tsx`:

```typescript
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { TimelineFilterBar } from './TimelineFilterBar';
import type { InvestigationDetail } from '../../api';

afterEach(cleanup);

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

describe('TimelineFilterBar', () => {
  it('renders five severity chips, four run-status chips, host & agent inputs, and Clear', () => {
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={() => {}} />);
    expect(screen.getByRole('button', { name: /^CRITICAL$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^HIGH$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^MEDIUM$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^LOW$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^INFO$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^completed$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^failed$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^cancelled$/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /^running$/ })).toBeTruthy();
    expect(screen.getByLabelText(/host/i)).toBeTruthy();
    expect(screen.getByLabelText(/agent/i)).toBeTruthy();
    expect(screen.getByRole('button', { name: /clear/i })).toBeTruthy();
  });
});
```

- [ ] **Step 2: Verify test fails**

```bash
cd web && npx vitest run src/components/investigations/TimelineFilterBar.test.tsx
```

Expected: FAIL — `Cannot find module './TimelineFilterBar'`.

- [ ] **Step 3: Implement minimal TimelineFilterBar shell**

Create `web/src/components/investigations/TimelineFilterBar.tsx`:

```tsx
// TimelineFilterBar — filter inputs above the case timeline.
// Severity (5 chips, multi-select) + host typeahead + agent typeahead
// + run-status (4 chips, multi-select) + Clear. The component is
// "dumb": state lives in the parent CaseTimeline, which threads
// `filter` in and `onChange` out. Saved-search list / save / default
// pinning is handled by the existing <SavedSearchesBar> mounted as
// a sibling — same pattern as the Findings page.

import { useMemo } from 'react';
import type { InvestigationDetail } from '../../api';
import type { Severity, RunStatus, TimelineFilterConfig } from './timeline/filter';

const SEVERITIES: Severity[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];
const RUN_STATUSES: RunStatus[] = ['completed', 'failed', 'cancelled', 'running'];

interface Props {
  bundle: InvestigationDetail;
  filter: TimelineFilterConfig;
  onChange: (next: TimelineFilterConfig) => void;
}

export function TimelineFilterBar({ bundle, filter, onChange }: Props) {
  const hosts = useMemo(() => distinctHosts(bundle), [bundle]);
  const agents = useMemo(() => distinctAgents(bundle), [bundle]);

  function toggleSev(s: Severity) {
    const cur = filter.severities ?? [];
    const next = cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s];
    onChange({ ...filter, severities: next.length ? next : undefined });
  }

  function toggleRun(r: RunStatus) {
    const cur = filter.run_statuses ?? [];
    const next = cur.includes(r) ? cur.filter((x) => x !== r) : [...cur, r];
    onChange({ ...filter, run_statuses: next.length ? next : undefined });
  }

  function setHost(v: string) {
    onChange({ ...filter, host: v.trim() === '' ? undefined : v });
  }

  function setAgent(v: string) {
    onChange({ ...filter, agent: v.trim() === '' ? undefined : v });
  }

  function clear() {
    onChange({});
  }

  const sevSelected = (s: Severity) => (filter.severities ?? []).includes(s);
  const runSelected = (r: RunStatus) => (filter.run_statuses ?? []).includes(r);

  return (
    <div className="flex items-center flex-wrap gap-3 text-[11px]">
      <div className="flex items-center gap-1">
        <span className="text-ink-mute">Severity:</span>
        {SEVERITIES.map((s) => (
          <button
            key={s}
            type="button"
            onClick={() => toggleSev(s)}
            aria-pressed={sevSelected(s)}
            className={`px-2 py-0.5 rounded-md border ${sevSelected(s) ? sevTone(s) : 'border-border bg-white text-ink-mute'}`}
          >
            {s}
          </button>
        ))}
      </div>
      <label className="flex items-center gap-1">
        <span className="text-ink-mute">Host:</span>
        <input
          list="timeline-host-options"
          value={filter.host ?? ''}
          onChange={(e) => setHost(e.target.value)}
          placeholder="any"
          className="px-2 py-0.5 rounded border border-border outline-none focus:ring-1 focus:ring-brand-300"
          style={{ minWidth: 140 }}
        />
        <datalist id="timeline-host-options">
          {hosts.map((h) => <option key={h} value={h} />)}
        </datalist>
      </label>
      <label className="flex items-center gap-1">
        <span className="text-ink-mute">Agent:</span>
        <input
          list="timeline-agent-options"
          value={filter.agent ?? ''}
          onChange={(e) => setAgent(e.target.value)}
          placeholder="any"
          className="px-2 py-0.5 rounded border border-border outline-none focus:ring-1 focus:ring-brand-300"
          style={{ minWidth: 140 }}
        />
        <datalist id="timeline-agent-options">
          {agents.map((a) => <option key={a} value={a} />)}
        </datalist>
      </label>
      <div className="flex items-center gap-1">
        <span className="text-ink-mute">Runs:</span>
        {RUN_STATUSES.map((r) => (
          <button
            key={r}
            type="button"
            onClick={() => toggleRun(r)}
            aria-pressed={runSelected(r)}
            className={`px-2 py-0.5 rounded-md border ${runSelected(r) ? runTone(r) : 'border-border bg-white text-ink-mute'}`}
          >
            {r}
          </button>
        ))}
      </div>
      <button
        type="button"
        onClick={clear}
        className="px-2 py-0.5 rounded-md border border-border text-ink-mute hover:bg-slate-50"
      >
        Clear
      </button>
    </div>
  );
}

function distinctHosts(bundle: InvestigationDetail): string[] {
  const set = new Set<string>();
  for (const f of bundle.findings) {
    if (f.Host.Valid && f.Host.String) set.add(f.Host.String);
  }
  return [...set].sort();
}

function distinctAgents(bundle: InvestigationDetail): string[] {
  const set = new Set<string>();
  for (const f of bundle.findings) {
    if (f.Agent.Valid && f.Agent.String) set.add(f.Agent.String);
  }
  for (const d of bundle.daimons ?? []) {
    if (d.Agent) set.add(d.Agent);
  }
  return [...set].sort();
}

function sevTone(s: Severity): string {
  if (s === 'CRITICAL') return 'border-red-300 bg-red-50 text-red-700';
  if (s === 'HIGH') return 'border-orange-300 bg-orange-50 text-orange-700';
  if (s === 'MEDIUM') return 'border-amber-300 bg-amber-50 text-amber-700';
  if (s === 'LOW') return 'border-blue-300 bg-blue-50 text-blue-700';
  return 'border-slate-300 bg-slate-50 text-slate-700';
}

function runTone(r: RunStatus): string {
  if (r === 'completed') return 'border-emerald-300 bg-emerald-50 text-emerald-700';
  if (r === 'failed') return 'border-red-300 bg-red-50 text-red-700';
  if (r === 'cancelled') return 'border-slate-300 bg-slate-50 text-slate-700';
  return 'border-blue-300 bg-blue-50 text-blue-700';
}
```

- [ ] **Step 4: Run the rendering test**

```bash
cd web && npx vitest run src/components/investigations/TimelineFilterBar.test.tsx
```

Expected: PASS — rendering test green.

- [ ] **Step 5: Add the chip-toggle test**

Append to `web/src/components/investigations/TimelineFilterBar.test.tsx`:

```typescript
  it('clicking a severity chip calls onChange with that severity added', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(onChange).toHaveBeenCalledWith({ severities: ['CRITICAL'] });
  });

  it('clicking an already-selected severity chip removes it', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{ severities: ['CRITICAL', 'HIGH'] }} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(onChange).toHaveBeenCalledWith({ severities: ['HIGH'] });
  });

  it('removing the last severity drops the field instead of leaving an empty array', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{ severities: ['LOW'] }} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^LOW$/ }));
    expect(onChange).toHaveBeenCalledWith({ severities: undefined });
  });

  it('typing in the host input calls onChange with that host', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText(/host/i), { target: { value: 'edr-fedora-3' } });
    expect(onChange).toHaveBeenCalledWith({ host: 'edr-fedora-3' });
  });

  it('clearing the host input drops the field', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{ host: 'edr-fedora-3' }} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText(/host/i), { target: { value: '' } });
    expect(onChange).toHaveBeenCalledWith({ host: undefined });
  });

  it('clicking a run-status chip toggles run_statuses', () => {
    const onChange = vi.fn();
    render(<TimelineFilterBar bundle={bundle()} filter={{}} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /^failed$/ }));
    expect(onChange).toHaveBeenCalledWith({ run_statuses: ['failed'] });
  });

  it('Clear button calls onChange({})', () => {
    const onChange = vi.fn();
    render(
      <TimelineFilterBar
        bundle={bundle()}
        filter={{ severities: ['HIGH'], host: 'h', agent: 'a', run_statuses: ['failed'] }}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /clear/i }));
    expect(onChange).toHaveBeenCalledWith({});
  });
```

- [ ] **Step 6: Run all TimelineFilterBar tests**

```bash
cd web && npx vitest run src/components/investigations/TimelineFilterBar.test.tsx
```

Expected: PASS — 8 tests green.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/investigations/TimelineFilterBar.tsx web/src/components/investigations/TimelineFilterBar.test.tsx
git commit -m "feat(timeline): TimelineFilterBar with severity/run-status chips + host/agent typeahead"
```

---

## Task 4: Wire filter + saved-searches into CaseTimeline

Mount `<TimelineFilterBar>` and the existing `<SavedSearchesBar>` above the SVG. Apply `applyTimelineFilter(events, filter)` to derive `filteredEvents`. Pre-apply the user's default `scope='investigation_timeline'` saved search on mount. Render the zero-matches banner when the filter narrows everything away.

**Files:**
- Modify: `web/src/components/investigations/CaseTimeline.tsx`
- Modify: `web/src/components/investigations/CaseTimeline.test.tsx`

- [ ] **Step 1: Add the filter-narrows integration test**

Append to `web/src/components/investigations/CaseTimeline.test.tsx`:

```typescript
describe('CaseTimeline filter integration', () => {
  function makeFinding(id: number, severity: string, host: string) {
    return {
      ID: id,
      Severity: { Valid: true, String: severity },
      Title: { Valid: true, String: `f${id}` },
      Agent: { Valid: true, String: 'edr-agent' },
      Host: { Valid: true, String: host },
      Ts: 1700000000000 + id * 1000,
    } as unknown as InvestigationDetail['findings'][number];
  }

  it('clicking a severity chip narrows the rendered events to the matching ones', async () => {
    const b = bundle({
      findings: [makeFinding(1, 'CRITICAL', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(1);
    expect(screen.getByRole('button', { name: /Finding #1/ })).toBeTruthy();
  });

  it('renders zero-matches banner when filter excludes every event', () => {
    const b = bundle({
      findings: [makeFinding(1, 'LOW', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(screen.getByText(/0 of \d+ events match/i)).toBeTruthy();
  });

  it('Clear restores all events', () => {
    const b = bundle({
      findings: [makeFinding(1, 'CRITICAL', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /^CRITICAL$/ }));
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: /clear/i }));
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(2);
  });
});
```

The existing `vi.mock('../../api', ...)` mock at module scope in `CaseTimeline.test.tsx` already mocks `api.investigations.audit`. Extend that mock to also stub `api.savedSearches.list`. Edit the existing module-scope mock to:

```typescript
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        audit: vi.fn().mockResolvedValue([]),
      },
      savedSearches: {
        ...actual.api.savedSearches,
        list: vi.fn().mockResolvedValue([]),
        create: vi.fn(),
        update: vi.fn(),
        delete: vi.fn(),
      },
    },
  };
});
```

- [ ] **Step 2: Verify the new tests fail**

```bash
cd web && npx vitest run src/components/investigations/CaseTimeline.test.tsx -t "filter integration"
```

Expected: FAIL — no `CRITICAL` button, no banner.

- [ ] **Step 3: Modify CaseTimeline.tsx — imports + state**

Edit `web/src/components/investigations/CaseTimeline.tsx`. Add to the import block at the top:

```typescript
import type { SavedSearch } from '../../api';
import { TimelineFilterBar } from './TimelineFilterBar';
import { SavedSearchesBar } from '../SavedSearchesBar';
import { applyTimelineFilter, type TimelineFilterConfig } from './timeline/filter';
```

Inside `CaseTimeline`, alongside the existing `useState` calls, add:

```typescript
  const [filter, setFilter] = useState<TimelineFilterConfig>({});
  const [savedSearches, setSavedSearches] = useState<SavedSearch[]>([]);
  const defaultAppliedRef = useRef(false);

  const refreshSavedSearches = () => {
    api.savedSearches.list('investigation_timeline')
      .then(setSavedSearches)
      .catch(() => { /* silent — filtering still works without saved searches */ });
  };
  useEffect(() => { refreshSavedSearches(); }, []);

  // Apply default saved search once after the list lands. Skip if a
  // filter is already set (operator already changed something) so we
  // don't clobber explicit intent.
  useEffect(() => {
    if (defaultAppliedRef.current) return;
    if (savedSearches.length === 0) return;
    const def = savedSearches.find((s) => s.is_default);
    defaultAppliedRef.current = true;
    if (!def) return;
    const hasFilter = Object.keys(filter).length > 0;
    if (hasFilter) return;
    try {
      const parsed = JSON.parse(def.config_json) as TimelineFilterConfig;
      setFilter(parsed);
    } catch {
      console.warn('investigation_timeline saved search has malformed config_json', def.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedSearches]);
```

Replace the existing `events` derivation:

```typescript
  const events = useMemo(() => buildEvents(bundle, audit), [bundle, audit]);
```

with:

```typescript
  const events = useMemo(() => buildEvents(bundle, audit), [bundle, audit]);
  const filteredEvents = useMemo(() => applyTimelineFilter(events, filter), [events, filter]);
```

Update the `autoFitRange` effect on the `events` array to keep using `events` (so the visible range never collapses to "nothing matches"):

```typescript
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
```

(no change to the `events`-based stamps — leaving here for clarity).

Update the active-saved-search detection (memoized):

```typescript
  const activeSavedID = useMemo(() => {
    const target = JSON.stringify(filter);
    for (const s of savedSearches) {
      try {
        if (JSON.stringify(JSON.parse(s.config_json)) === target) return s.id;
      } catch { /* skip malformed */ }
    }
    return null;
  }, [savedSearches, filter]);
```

- [ ] **Step 4: Modify CaseTimeline.tsx — render block**

Update the JSX returned by `CaseTimeline`. Find the existing toggle-bar block:

```tsx
      {/* Toggle bar + zoom controls */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        ...
      </div>
```

Insert two siblings *after* it (still inside the outer `<div className="border border-border rounded-md ...">`):

```tsx
      {(savedSearches.length > 0 || Object.keys(filter).length > 0) && (
        <div className="flex items-center gap-2 flex-wrap pt-1 border-t border-border">
          <SavedSearchesBar
            scope="investigation_timeline"
            searches={savedSearches}
            currentConfig={filter}
            activeID={activeSavedID}
            onApply={(s) => {
              try { setFilter(JSON.parse(s.config_json) as TimelineFilterConfig); }
              catch { /* malformed — ignore */ }
            }}
            onSearchesChange={refreshSavedSearches}
          />
        </div>
      )}

      <TimelineFilterBar bundle={bundle} filter={filter} onChange={setFilter} />

      {filteredEvents.length === 0 && events.length > 0 && (
        <div className="px-3 py-1.5 rounded-md text-xs text-amber-800 bg-amber-50 border border-amber-200">
          0 of {events.length} events match — adjust filters or click Clear.
        </div>
      )}
```

Replace the `<EventsLayer events={events} ...>` call inside the `<svg>` with `events={filteredEvents}`:

```tsx
              <EventsLayer
                events={filteredEvents}
                visibleLanes={visibleLanes}
                range={range}
                laneLabelWidth={LANE_LABEL_W}
                drawableWidth={drawableWidth}
                cpInstanceID={cpInstanceID}
              />
```

Also update the `range && events.length > 0 &&` guard to `range && filteredEvents.length > 0 &&` so the events layer is skipped when nothing matches (lane bands + axis still render, banner sits above):

```tsx
          {range && filteredEvents.length > 0 && (
            <g transform={`translate(0, ${TOP_PADDING})`}>
              <EventsLayer
                events={filteredEvents}
                ...
```

The `onlyLifecycle` early-return uses `events` (the unfiltered array), so the empty-state placeholder still appears for blank cases.

- [ ] **Step 5: Run the integration tests**

```bash
cd web && npx vitest run src/components/investigations/CaseTimeline.test.tsx -t "filter integration"
```

Expected: PASS — 3 tests green.

- [ ] **Step 6: Add the default-applies-on-mount test**

Append to `web/src/components/investigations/CaseTimeline.test.tsx`:

```typescript
import { api } from '../../api';

describe('CaseTimeline default saved search', () => {
  function makeFinding(id: number, severity: string, host: string) {
    return {
      ID: id,
      Severity: { Valid: true, String: severity },
      Title: { Valid: true, String: `f${id}` },
      Agent: { Valid: true, String: 'edr-agent' },
      Host: { Valid: true, String: host },
      Ts: 1700000000000 + id * 1000,
    } as unknown as InvestigationDetail['findings'][number];
  }

  it('auto-applies the default saved search on mount', async () => {
    (api.savedSearches.list as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce([
      {
        id: 5, user_id: 1, name: 'crit-only', scope: 'investigation_timeline',
        config_json: JSON.stringify({ severities: ['CRITICAL'] }),
        is_default: true,
        created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z',
      },
    ]);
    const b = bundle({
      findings: [makeFinding(1, 'CRITICAL', 'h1'), makeFinding(2, 'LOW', 'h1')],
    });
    render(<MemoryRouter><CaseTimeline bundle={b} /></MemoryRouter>);
    // Wait one tick so the savedSearches.list promise resolves.
    await new Promise((r) => setTimeout(r, 0));
    // Only the CRITICAL finding should still be on the timeline.
    expect(screen.getAllByRole('button', { name: /Finding #/ })).toHaveLength(1);
    expect(screen.getByRole('button', { name: /Finding #1/ })).toBeTruthy();
  });
});
```

- [ ] **Step 7: Run the new test**

```bash
cd web && npx vitest run src/components/investigations/CaseTimeline.test.tsx -t "default saved search"
```

Expected: PASS.

- [ ] **Step 8: Run full CaseTimeline test file**

```bash
cd web && npx vitest run src/components/investigations/CaseTimeline.test.tsx
```

Expected: PASS — every test in the file (existing + new).

- [ ] **Step 9: Commit**

```bash
git add web/src/components/investigations/CaseTimeline.tsx web/src/components/investigations/CaseTimeline.test.tsx
git commit -m "feat(timeline): mount filter bar + saved searches in CaseTimeline"
```

---

## Task 5: Architecture docs + final sweep + PR

- [ ] **Step 1: Update `docs/architecture.md`**

Find the section "Investigation Overview — timeline collision avoidance + portal'd tooltip" (around line 2326). Insert a new section *after* it:

```markdown
## Investigation Overview — timeline filter & saved searches

The case timeline ships a structured filter bar above the SVG canvas:
severity chips (CRITICAL/HIGH/MEDIUM/LOW/INFO), host typeahead, agent
typeahead, and run-status chips (completed/failed/cancelled/running).
Selections feed `applyTimelineFilter(events, filter)`
(`web/src/components/investigations/timeline/filter.ts`) — a pure
predicate that keeps matching findings/runs/daimons and passes
non-applicable kinds (notes/audit/lifecycle/IOCs) through unchanged.
Lane toggles remain the only knob that hides whole kinds.

Saved filter sets reuse the existing `saved_searches` table
(migration 045) under `scope='investigation_timeline'`. The shared
`<SavedSearchesBar>` mounts above the filter inputs — same component
the Findings page uses — so save/list/rename/default-pin/delete come
free. Default-flagged saved searches auto-apply on case mount via
`api.savedSearches.list('investigation_timeline')`.

When a filter selection narrows the result to zero events on a
non-empty case, the canvas renders lane bands + axis with a small
amber banner above ("0 of N events match — adjust filters or
click Clear"). Federation: filtering is purely client-side over
events the federation aggregator already returned, and saved
searches are user-scoped (not CP-scoped) so an operator's
"my CRIT-only view" works on every case across federated CPs.
```

- [ ] **Step 2: Run the full vitest suite**

```bash
cd web && npx vitest run
```

Expected: PASS — every existing test still green plus the new ones.

- [ ] **Step 3: Run the typechecker**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS — no type errors.

- [ ] **Step 4: Run the linter**

```bash
cd web && npm run lint
```

Expected: PASS — no new violations.

- [ ] **Step 5: Commit docs**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): timeline filter & saved searches section"
```

- [ ] **Step 6: Push branch**

```bash
git push -u origin feat/timeline-search
```

- [ ] **Step 7: Open PR**

```bash
gh pr create --title "feat(timeline): structured filter + saved searches" --body "$(cat <<'EOF'
## Summary
- Adds a structured filter bar above the case timeline (severity / host / agent / run-status), driven by a pure `applyTimelineFilter` predicate.
- Persists filter sets via the existing `saved_searches` infrastructure under `scope='investigation_timeline'`; the shared `<SavedSearchesBar>` ships save / list / default / rename / delete out of the box.
- Default-flagged saved searches auto-apply on case mount; a zero-matches banner appears when a filter narrows the case to nothing.
- Pure frontend feature — no new endpoints, no migrations, no federation changes.

Closes follow-up #4 from `investigation_overview_followups.md`.

Spec: `docs/superpowers/specs/2026-05-02-timeline-search-design.md`
Plan: `docs/superpowers/plans/2026-05-02-timeline-search.md`

## Test plan
- [ ] `cd web && npx vitest run` — full vitest suite green
- [ ] `cd web && npx tsc --noEmit` — typecheck clean
- [ ] `cd web && npm run lint` — lint clean
- [ ] Manual: open an active case with mixed-severity findings ≥ 2 hosts ≥ 2 agents ≥ 2 run statuses; toggle each filter dimension and verify events update live
- [ ] Manual: save a filter as "my CRIT-only view"; close the case; reopen; verify the saved search appears as a pill
- [ ] Manual: mark a saved search as default via the pill kebab menu; reload the case; verify it auto-applies
- [ ] Manual: delete the active saved search; verify the filter UI keeps the values but the pill list updates
- [ ] Manual on a federated case (`cp_source` set): saved searches still appear and filter applies correctly

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review notes

**1. Spec coverage:** Every spec section has a task.
- "Filter shape" → Task 1 (`TimelineFilterConfig` shape baked into `filter.ts`); Task 2 (re-export).
- "Filter semantics" → Task 1 (predicate + 8 unit tests covering pass-through, OR within, AND across).
- "Filter UI" → Task 3 (chips + typeahead + Clear, dumb component).
- "Saved-search lifecycle" → Task 4 (parent owns state, `<SavedSearchesBar>` reused, default-apply effect, `is_default` honored).
- "CaseTimeline integration" → Task 4 (state, filtered events, banner, range still derived from full event list).
- "Edge cases" — Task 4's banner test covers zero-match; saved-search-references-missing-host is intrinsically covered (the predicate just returns []); deleted-while-active is covered by `<SavedSearchesBar>`'s existing behavior; malformed `config_json` is caught silently in the default-apply effect with a `console.warn`.
- "Out of scope" — explicitly preserved (no free-text input, no time-range filter, no localStorage persistence beyond saved searches).

**2. Placeholder scan:** No "TBD"/"TODO"/"add appropriate handling" lines. Every code step contains a complete code block.

**3. Type consistency:**
- `TimelineFilterConfig` defined in Task 1 (`filter.ts`), re-exported in Task 2 (`api.ts`), consumed in Task 3 (`TimelineFilterBar.tsx`) and Task 4 (`CaseTimeline.tsx`) — same name, same shape.
- `Severity` and `RunStatus` exported from `filter.ts` and used in `TimelineFilterBar.tsx` — single source of truth.
- `applyTimelineFilter` signature consistent: `(events, filter) => filtered events`.
- Component props (`bundle`, `filter`, `onChange`) match the spec verbatim.
- `<SavedSearchesBar>`'s actual props (verified against `web/src/components/SavedSearchesBar.tsx`): `scope`, `searches`, `currentConfig`, `activeID`, `onApply`, `onSearchesChange` — Task 4's mount call uses exactly those names.
