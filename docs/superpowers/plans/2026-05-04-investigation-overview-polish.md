# Investigation Overview Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three loosely-coupled improvements to the Investigation Overview shipped as one PR — surface the silent error in `GetInvestigationHandler` that's causing the empty-timeline bug, redesign `CaseGraph` with custom per-kind node renderers matching the orchestration canvas style, and add MiniMap + backdrop polish.

**Architecture:** Backend gains a `bundle_warnings` field surfaced when any of the five enriched list calls errors. Frontend renders the warnings in an amber strip above the tabs. The graph view replaces ReactFlow's default node renderer with four custom components (finding card 240×86, host/daimon badges 160-180×44, IOC card 200×56) registered via a `nodeTypes` map. Edges scale stroke width via a pure `styleForEdgeWeight` helper based on per-target finding counts. The canvas gets the same gradient + radial-dot backdrop the orchestration canvas uses; MiniMap renders for cases with ≥10 findings.

**Tech Stack:** Go (chi) backend; React 18 + TypeScript + `@xyflow/react` frontend (already in deps). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-05-04-investigation-overview-polish-design.md`

**Branch:** `feat/investigation-overview-polish` (current worktree)

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/api/investigations.go` | MODIFY | `GetInvestigationHandler` builds `bundle_warnings` from list-call errors instead of discarding them |
| `controlplane/api/investigations_test.go` | MODIFY | Test that a forced error yields `bundle_warnings`; happy-path asserts the field is omitted |
| `web/src/api.ts` | MODIFY | Add `bundle_warnings?: string[]` to `InvestigationDetail` |
| `web/src/pages/InvestigationDetail.tsx` | MODIFY | Render an amber warning strip above the tabs when `bundle_warnings.length > 0` |
| `web/src/components/investigations/graph/nodes/tones.ts` | NEW | `severityTone(sev)` returns Tailwind class set per severity |
| `web/src/components/investigations/graph/edges.ts` | NEW | `styleForEdgeWeight(n)` + `weightByTarget(edges)` |
| `web/src/components/investigations/graph/edges.test.ts` | NEW | Unit tests for both helpers |
| `web/src/components/investigations/graph/nodes/FindingNode.tsx` | NEW | 240×86 finding card with severity rail + title + agent/host chips |
| `web/src/components/investigations/graph/nodes/FindingNode.test.tsx` | NEW | Render assertions |
| `web/src/components/investigations/graph/nodes/HostNode.tsx` | NEW | 160×44 host badge |
| `web/src/components/investigations/graph/nodes/HostNode.test.tsx` | NEW | Render assertions |
| `web/src/components/investigations/graph/nodes/DaimonNode.tsx` | NEW | 180×44 daimon badge |
| `web/src/components/investigations/graph/nodes/DaimonNode.test.tsx` | NEW | Render assertions |
| `web/src/components/investigations/graph/nodes/IOCNode.tsx` | NEW | 200×56 IOC card with kind icon + truncated value; exports `shortValue` |
| `web/src/components/investigations/graph/nodes/IOCNode.test.tsx` | NEW | Render assertions + direct `shortValue` test |
| `web/src/components/investigations/graph/nodes/index.ts` | NEW | `nodeTypes` map for ReactFlow |
| `web/src/components/investigations/graph/layout.ts` | MODIFY | Bump `RIGHT_X` from 520 → 560 and use kind-aware vertical pitch (findings 100, others 60) |
| `web/src/components/investigations/CaseGraph.tsx` | MODIFY | Wire `nodeTypes`, edge styling, MiniMap conditional, tinted backdrop |
| `web/src/components/investigations/CaseGraph.test.tsx` | MODIFY | Update assertions for the custom node renderers |
| `docs/architecture.md` | MODIFY | Update the existing graph view section |

---

## Task 1: Bundle handler error surfacing

The fix that makes the empty-timeline bug observable instead of silent.

**Files:**
- Modify: `controlplane/api/investigations.go`
- Modify: `controlplane/api/investigations_test.go`
- Modify: `web/src/api.ts`
- Modify: `web/src/pages/InvestigationDetail.tsx`

- [ ] **Step 1: Write the failing server-side test**

Append to `controlplane/api/investigations_test.go`:

```go
func TestGetInvestigation_SurfacesListErrors(t *testing.T) {
	store := newSeededTestStore(t)
	invID, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Force an error: drop the table the enriched query references.
	// SQLite's RENAME COLUMN works on 3.25+; DROP TABLE is bulletproof.
	if _, err := store.Exec(`DROP TABLE investigation_findings`); err != nil {
		t.Fatalf("schema break: %v", err)
	}

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}", GetInvestigationHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(invID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	warnings, ok := resp["bundle_warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected bundle_warnings entry; got %v", resp["bundle_warnings"])
	}
	first, _ := warnings[0].(string)
	if !strings.HasPrefix(first, "findings:") {
		t.Errorf("warning[0] = %q, want prefix \"findings:\"", first)
	}
	// Other arrays should still be empty slices, not missing.
	if _, ok := resp["findings"].([]any); !ok {
		t.Errorf("findings should be [] not nil")
	}
}

func TestGetInvestigation_NoWarningsOnSuccess(t *testing.T) {
	store := newSeededTestStore(t)
	invID, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}", GetInvestigationHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(invID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["bundle_warnings"]; ok {
		t.Errorf("bundle_warnings should be omitted on success; got %v", resp["bundle_warnings"])
	}
}
```

NOTE: ensure `strings` and `strconv` are in the test imports. They likely are; if not, add them.

- [ ] **Step 2: Run tests to verify the surface-error test fails**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview-polish
go test ./controlplane/api/ -run "TestGetInvestigation_SurfacesListErrors|TestGetInvestigation_NoWarningsOnSuccess" -v
```

Expected: `TestGetInvestigation_SurfacesListErrors` FAILS — `bundle_warnings` not in response. `TestGetInvestigation_NoWarningsOnSuccess` PASSES (the field doesn't exist yet, so it's correctly omitted — but verify it stays passing after the impl change).

- [ ] **Step 3: Modify `GetInvestigationHandler` to surface errors**

In `controlplane/api/investigations.go`, find `GetInvestigationHandler` (around line 190). Replace its body with:

```go
func GetInvestigationHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inv, err := store.GetInvestigation(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var warnings []string
		recordWarn := func(name string, err error) {
			if err == nil {
				return
			}
			log.Printf("investigation bundle %d: %s: %v", id, name, err)
			warnings = append(warnings, fmt.Sprintf("%s: %s", name, err.Error()))
		}

		findings, fErr := store.ListFindingsForInvestigationEnriched(id)
		recordWarn("findings", fErr)
		runs, rErr := store.ListRunsForInvestigationEnriched(id)
		recordWarn("runs", rErr)
		iocs, iErr := store.ListIOCsForInvestigation(id)
		recordWarn("iocs", iErr)
		daimons, dErr := store.ListDaimonsForInvestigation(id)
		recordWarn("daimons", dErr)
		orchs, oErr := store.ListOrchestrationsForInvestigation(id)
		recordWarn("orchestrations", oErr)
		notes, nErr := store.ListInvestigationNotes(id)
		recordWarn("notes", nErr)
		warRoom, _ := store.IsInvestigationWarRoom(id)

		// Defensive nil → empty so JSON consumers see [], not null.
		if findings == nil {
			findings = []db.InvestigationFindingItem{}
		}
		if runs == nil {
			runs = []db.InvestigationRunItem{}
		}
		if iocs == nil {
			iocs = []db.InvestigationIOCItem{}
		}
		if daimons == nil {
			daimons = []db.InvestigationDaimonItem{}
		}
		if orchs == nil {
			orchs = []db.InvestigationOrchestrationItem{}
		}
		if notes == nil {
			notes = []db.InvestigationNote{}
		}

		resp := map[string]any{
			"investigation":  inv,
			"findings":       findings,
			"runs":           runs,
			"iocs":           iocs,
			"daimons":        daimons,
			"orchestrations": orchs,
			"notes":          notes,
			"war_room":       warRoom,
		}
		if len(warnings) > 0 {
			resp["bundle_warnings"] = warnings
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
```

`fmt` and `log` should already be in the imports — `grep -n "\"fmt\"\|\"log\"" controlplane/api/investigations.go` to verify. If either is missing, add it.

- [ ] **Step 4: Run server-side tests to verify they pass**

```bash
go test ./controlplane/api/ -run "TestGetInvestigation_SurfacesListErrors|TestGetInvestigation_NoWarningsOnSuccess" -v
```

Expected: PASS — both tests green.

- [ ] **Step 5: Add the type field to `InvestigationDetail` in `web/src/api.ts`**

Find the `InvestigationDetail` interface (around line 740). Add the optional field:

```ts
export interface InvestigationDetail {
  // ...existing fields...
  bundle_warnings?: string[];   // server-emitted partial-failure list
}
```

- [ ] **Step 6: Add the warning strip to `web/src/pages/InvestigationDetail.tsx`**

Find the bundle render block. The existing tab switcher is around line 220-340. Just below `const inv = bundle.investigation;` (around line 210), add the strip JSX so it renders before the tabs:

```tsx
{bundle.bundle_warnings && bundle.bundle_warnings.length > 0 && (
  <div className="mx-6 mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800">
    <div className="font-medium mb-1">
      Some bundle data couldn't be loaded — case may render incomplete.
    </div>
    <ul className="list-disc pl-5 space-y-0.5">
      {bundle.bundle_warnings.map((w, i) => (
        <li key={i} className="font-mono">{w}</li>
      ))}
    </ul>
  </div>
)}
```

The exact insertion point: the JSX returned by `InvestigationDetail`. Place the strip just before the existing header / breadcrumb / tab block. The implementer should grep for `<TabButton` to find the tab cluster and put the strip immediately above its parent container.

- [ ] **Step 7: Run typecheck to verify the type addition**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS — no type errors.

- [ ] **Step 8: Run the full Go suite + the existing investigations vitest tests**

```bash
go test ./controlplane/api/...
cd web && npx vitest run src/pages/
```

Expected: PASS for both. The new test in step 1 + the others stay green.

- [ ] **Step 9: Commit**

```bash
git add controlplane/api/investigations.go controlplane/api/investigations_test.go web/src/api.ts web/src/pages/InvestigationDetail.tsx
git commit -m "fix(investigations): surface enriched-list errors as bundle_warnings"
```

---

## Task 2: Pure helpers — `tones.ts` + `edges.ts`

Two stateless helpers used by the node components and the graph wiring.

**Files:**
- Create: `web/src/components/investigations/graph/nodes/tones.ts`
- Create: `web/src/components/investigations/graph/edges.ts`
- Create: `web/src/components/investigations/graph/edges.test.ts`

- [ ] **Step 1: Write the failing tests for `edges.ts`**

Create `web/src/components/investigations/graph/edges.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { styleForEdgeWeight, weightByTarget } from './edges';

describe('styleForEdgeWeight', () => {
  it('returns the lightest band for weight 1', () => {
    const s = styleForEdgeWeight(1);
    expect(s.strokeWidth).toBe(1);
    expect(s.stroke).toBe('#cbd5e1');
  });

  it('returns 1.5px for weights 2-3', () => {
    expect(styleForEdgeWeight(2).strokeWidth).toBe(1.5);
    expect(styleForEdgeWeight(3).strokeWidth).toBe(1.5);
  });

  it('returns 2px for weights 4-9', () => {
    expect(styleForEdgeWeight(4).strokeWidth).toBe(2);
    expect(styleForEdgeWeight(9).strokeWidth).toBe(2);
  });

  it('returns 2.5px for weight 10+', () => {
    expect(styleForEdgeWeight(10).strokeWidth).toBe(2.5);
    expect(styleForEdgeWeight(99).strokeWidth).toBe(2.5);
  });
});

describe('weightByTarget', () => {
  it('counts edges per target id', () => {
    const m = weightByTarget([
      { target: 'a' },
      { target: 'a' },
      { target: 'b' },
    ]);
    expect(m.get('a')).toBe(2);
    expect(m.get('b')).toBe(1);
    expect(m.size).toBe(2);
  });

  it('returns empty map for empty input', () => {
    expect(weightByTarget([]).size).toBe(0);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd web && npx vitest run src/components/investigations/graph/edges.test.ts
```

Expected: FAIL — `styleForEdgeWeight` undefined.

- [ ] **Step 3: Implement `edges.ts`**

Create `web/src/components/investigations/graph/edges.ts`:

```ts
// Pure helpers that map "this entity is touched by N findings" weights
// to ReactFlow edge styling. Bands chosen so single-finding entities
// (the common case) stay visually quiet while heavily-shared entities
// (an IOC across 10 hosts) read clearly.

export interface EdgeStyle {
  stroke:      string;
  strokeWidth: number;
  opacity:     number;
}

/**
 * styleForEdgeWeight maps a weight (count of edges sharing the target)
 * to a stroke style. Bands: 1, 2-3, 4-9, 10+.
 */
export function styleForEdgeWeight(weight: number): EdgeStyle {
  if (weight >= 10) return { stroke: '#475569', strokeWidth: 2.5, opacity: 0.9 };
  if (weight >= 4)  return { stroke: '#64748b', strokeWidth: 2,   opacity: 0.85 };
  if (weight >= 2)  return { stroke: '#94a3b8', strokeWidth: 1.5, opacity: 0.8 };
  return                  { stroke: '#cbd5e1', strokeWidth: 1,   opacity: 0.75 };
}

/**
 * weightByTarget — given the graph's edges, returns a Map<targetID, count>.
 * Each target's incoming-edge count is used to choose a stroke band.
 */
export function weightByTarget(edges: ReadonlyArray<{ target: string }>): Map<string, number> {
  const m = new Map<string, number>();
  for (const e of edges) m.set(e.target, (m.get(e.target) ?? 0) + 1);
  return m;
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd web && npx vitest run src/components/investigations/graph/edges.test.ts
```

Expected: PASS — all 6 tests green.

- [ ] **Step 5: Implement `tones.ts`**

Create `web/src/components/investigations/graph/nodes/tones.ts`:

```ts
// Severity → Tailwind class set for the finding card. Mirrors the
// canonical severity-* tokens (SeverityMenu.tsx, CommandPalette.tsx)
// so a CRITICAL chip on a Finding row and the CRITICAL ring on a
// graph node are visually identical.

export type Severity = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';

export interface NodeTone {
  bg:      string;
  ring:    string;
  badge:   string;
  iconBg:  string;
  iconFg:  string;
}

export function severityTone(sev: string): NodeTone {
  switch (sev?.toUpperCase()) {
    case 'CRITICAL': return { bg: 'bg-purple-50',  ring: 'ring-purple-500', badge: 'severity-badge severity-critical', iconBg: 'bg-purple-100',  iconFg: 'text-purple-700' };
    case 'HIGH':     return { bg: 'bg-red-50',     ring: 'ring-red-500',    badge: 'severity-badge severity-high',     iconBg: 'bg-red-100',     iconFg: 'text-red-700' };
    case 'MEDIUM':   return { bg: 'bg-orange-50',  ring: 'ring-orange-500', badge: 'severity-badge severity-medium',   iconBg: 'bg-orange-100',  iconFg: 'text-orange-700' };
    case 'LOW':      return { bg: 'bg-yellow-50',  ring: 'ring-yellow-500', badge: 'severity-badge severity-low',      iconBg: 'bg-yellow-100',  iconFg: 'text-yellow-700' };
    default:         return { bg: 'bg-slate-50',   ring: 'ring-slate-400',  badge: 'severity-badge severity-info',     iconBg: 'bg-slate-100',   iconFg: 'text-slate-700' };
  }
}
```

`tones.ts` doesn't get its own test file — it's exercised indirectly by `FindingNode.test.tsx` (Task 3) which renders a node and asserts the `severity-critical` badge class. The function is short enough that direct unit tests would just restate the switch-case bodies.

- [ ] **Step 6: Verify typecheck passes**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/investigations/graph/nodes/tones.ts web/src/components/investigations/graph/edges.ts web/src/components/investigations/graph/edges.test.ts
git commit -m "feat(graph): pure tones + edges helpers for case graph"
```

---

## Task 3: `FindingNode` — the centerpiece card

The 240×86 finding card is the visual focal point; the other three node types are simpler badges built on the same pattern.

**Files:**
- Create: `web/src/components/investigations/graph/nodes/FindingNode.tsx`
- Create: `web/src/components/investigations/graph/nodes/FindingNode.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/investigations/graph/nodes/FindingNode.test.tsx`:

```tsx
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { FindingNode } from './FindingNode';

afterEach(cleanup);

const baseProps = {
  id: 'f:42',
  type: 'finding',
  position: { x: 0, y: 0 },
  data: {
    id: 'f:42',
    numericID: 42,
    title: 'Suspicious cron job spawning tcp connections',
    severity: 'CRITICAL',
    host: 'edr-fedora-3',
    agent: 'edr-agent',
  },
  selected: false,
  zIndex: 0,
  isConnectable: false,
  xPos: 0,
  yPos: 0,
  dragging: false,
} as any;

function withProvider(ui: React.ReactNode) {
  return <ReactFlowProvider>{ui}</ReactFlowProvider>;
}

describe('FindingNode', () => {
  it('renders the title, severity badge, agent, and host', () => {
    render(withProvider(<FindingNode {...baseProps} />));
    expect(screen.getByText(/Suspicious cron job/)).toBeTruthy();
    expect(screen.getByText('CRITICAL')).toBeTruthy();
    expect(screen.getByText('edr-agent')).toBeTruthy();
    expect(screen.getByText('edr-fedora-3')).toBeTruthy();
    expect(screen.getByText(/^#42$/)).toBeTruthy();
  });

  it('uses severity-critical class for CRITICAL', () => {
    render(withProvider(<FindingNode {...baseProps} />));
    const badge = screen.getByText('CRITICAL');
    expect(badge.className).toContain('severity-critical');
  });

  it('falls back to severity-info for missing severity', () => {
    const props = { ...baseProps, data: { ...baseProps.data, severity: '' } };
    render(withProvider(<FindingNode {...props} />));
    const badge = screen.getByText('INFO');
    expect(badge.className).toContain('severity-info');
  });

  it('shows "(no title)" when title is empty', () => {
    const props = { ...baseProps, data: { ...baseProps.data, title: '' } };
    render(withProvider(<FindingNode {...props} />));
    expect(screen.getByText('(no title)')).toBeTruthy();
  });

  it('omits agent chip when agent is empty', () => {
    const props = { ...baseProps, data: { ...baseProps.data, agent: '' } };
    render(withProvider(<FindingNode {...props} />));
    expect(screen.queryByText('edr-agent')).toBeNull();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd web && npx vitest run src/components/investigations/graph/nodes/FindingNode.test.tsx
```

Expected: FAIL — `FindingNode` undefined.

- [ ] **Step 3: Implement `FindingNode.tsx`**

Create `web/src/components/investigations/graph/nodes/FindingNode.tsx`:

```tsx
// Finding node — 240×86 card matching OrchestrationCanvas's step
// card shape so the relationship view feels like the same product
// surface. Severity rail on the left (4px), then a tinted body with
// the severity badge, finding id, title (clamped to 2 lines), and
// agent/host chips on the bottom row.

import { Handle, Position, type NodeProps } from '@xyflow/react';
import { cn } from '../../../../lib/cn';
import { severityTone } from './tones';

export interface FindingNodeData {
  id:        string;
  numericID: number;
  title:     string;
  severity:  string;
  host:      string;
  agent:     string;
  [key: string]: unknown;
}

export const FINDING_NODE_W = 240;
export const FINDING_NODE_H = 86;

export function FindingNode({ data, selected }: NodeProps<{ data: FindingNodeData }>) {
  const tone = severityTone(data.severity);
  const railBg = tone.ring.replace('ring-', 'bg-'); // ring-purple-500 → bg-purple-500
  return (
    <div
      className={cn(
        'rounded-lg border border-border bg-panel shadow-card overflow-hidden text-left ring-1 transition',
        tone.ring,
        selected ? 'ring-2 ring-offset-1' : '',
      )}
      style={{ width: FINDING_NODE_W, height: FINDING_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="flex h-full">
        <div className={cn('w-1 h-full', railBg)} />
        <div className={cn('flex-1 px-2.5 py-1.5', tone.bg)}>
          <div className="flex items-center gap-1 mb-1">
            <span className={cn(tone.badge, 'text-[9px]')}>{(data.severity || 'INFO').toUpperCase()}</span>
            <span className="font-mono text-[10px] text-ink-mute">#{data.numericID}</span>
          </div>
          <div className="text-[12px] font-medium leading-tight line-clamp-2 text-ink">
            {data.title || '(no title)'}
          </div>
          <div className="mt-1 flex items-center gap-1 text-[10px] text-ink-mute">
            {data.agent && <span className="px-1 rounded bg-slate-100 truncate max-w-[100px]">{data.agent}</span>}
            {data.host && (
              <>
                <span>·</span>
                <span className="truncate max-w-[110px]">{data.host}</span>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
```

NOTE: `cn` is the existing class-merge helper at `web/src/lib/cn.ts`. The relative import path (`../../../../lib/cn`) reaches up four directories from `graph/nodes/`. Verify with `ls web/src/lib/cn.ts`.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/components/investigations/graph/nodes/FindingNode.test.tsx
```

Expected: PASS — all 5 tests green.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/investigations/graph/nodes/FindingNode.tsx web/src/components/investigations/graph/nodes/FindingNode.test.tsx
git commit -m "feat(graph): FindingNode 240x86 card with severity rail"
```

---

## Task 4: `HostNode`, `DaimonNode`, `IOCNode` — secondary badges

Three sibling components, structurally similar. Built together since they share the same pattern (icon tile + label + subtext).

**Files:**
- Create: `web/src/components/investigations/graph/nodes/HostNode.tsx`
- Create: `web/src/components/investigations/graph/nodes/HostNode.test.tsx`
- Create: `web/src/components/investigations/graph/nodes/DaimonNode.tsx`
- Create: `web/src/components/investigations/graph/nodes/DaimonNode.test.tsx`
- Create: `web/src/components/investigations/graph/nodes/IOCNode.tsx`
- Create: `web/src/components/investigations/graph/nodes/IOCNode.test.tsx`

- [ ] **Step 1: Write tests for HostNode**

Create `web/src/components/investigations/graph/nodes/HostNode.test.tsx`:

```tsx
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { HostNode } from './HostNode';

afterEach(cleanup);

const props = {
  id: 'h:edr-fedora-3',
  type: 'host',
  position: { x: 0, y: 0 },
  data: { label: 'edr-fedora-3' },
  selected: false,
  zIndex: 0,
  isConnectable: false,
  xPos: 0, yPos: 0, dragging: false,
} as any;

describe('HostNode', () => {
  it('renders hostname label', () => {
    render(<ReactFlowProvider><HostNode {...props} /></ReactFlowProvider>);
    expect(screen.getByText('edr-fedora-3')).toBeTruthy();
  });

  it('renders the "host" subtext', () => {
    render(<ReactFlowProvider><HostNode {...props} /></ReactFlowProvider>);
    expect(screen.getByText('host')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Implement `HostNode.tsx`**

Create `web/src/components/investigations/graph/nodes/HostNode.tsx`:

```tsx
// Host node — compact 160×44 badge with green server icon. Hosts
// have no drawer; CaseGraph routes their click to /findings?host=<n>.

import { Server } from 'lucide-react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

export interface HostNodeData {
  label:          string;
  finding_count?: number;
  [key: string]:  unknown;
}

export const HOST_NODE_W = 160;
export const HOST_NODE_H = 44;

export function HostNode({ data }: NodeProps<{ data: HostNodeData }>) {
  return (
    <div
      className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
      style={{ width: HOST_NODE_W, height: HOST_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="w-7 h-7 rounded bg-emerald-50 text-emerald-600 flex items-center justify-center shrink-0">
        <Server size={14} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[12px] font-medium truncate text-ink">{data.label}</div>
        <div className="text-[10px] text-ink-mute">host</div>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Write tests for DaimonNode**

Create `web/src/components/investigations/graph/nodes/DaimonNode.test.tsx`:

```tsx
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { DaimonNode } from './DaimonNode';

afterEach(cleanup);

function mkProps(label: string, finding_count: number) {
  return {
    id: `d:${label}`,
    type: 'daimon',
    position: { x: 0, y: 0 },
    data: { label, finding_count },
    selected: false,
    zIndex: 0,
    isConnectable: false,
    xPos: 0, yPos: 0, dragging: false,
  } as any;
}

describe('DaimonNode', () => {
  it('renders the agent label and count', () => {
    render(<ReactFlowProvider><DaimonNode {...mkProps('edr-agent', 7)} /></ReactFlowProvider>);
    expect(screen.getByText('edr-agent')).toBeTruthy();
    expect(screen.getByText(/daimon · 7 findings/)).toBeTruthy();
  });

  it('uses singular "finding" for count of 1', () => {
    render(<ReactFlowProvider><DaimonNode {...mkProps('sre-health', 1)} /></ReactFlowProvider>);
    expect(screen.getByText(/daimon · 1 finding$/)).toBeTruthy();
  });

  it('treats undefined count as 0', () => {
    const p = mkProps('edr-agent', 0);
    p.data.finding_count = undefined;
    render(<ReactFlowProvider><DaimonNode {...p} /></ReactFlowProvider>);
    expect(screen.getByText(/daimon · 0 findings/)).toBeTruthy();
  });
});
```

- [ ] **Step 4: Implement `DaimonNode.tsx`**

Create `web/src/components/investigations/graph/nodes/DaimonNode.tsx`:

```tsx
// Daimon node — 180×44 badge with indigo bot icon and a finding-count
// subtext. "Daimon" is the platform's term for an emitting agent.

import { Bot } from 'lucide-react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

export interface DaimonNodeData {
  label:          string;
  finding_count?: number;
  [key: string]:  unknown;
}

export const DAIMON_NODE_W = 180;
export const DAIMON_NODE_H = 44;

export function DaimonNode({ data }: NodeProps<{ data: DaimonNodeData }>) {
  const fc = data.finding_count ?? 0;
  return (
    <div
      className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
      style={{ width: DAIMON_NODE_W, height: DAIMON_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="w-7 h-7 rounded bg-indigo-50 text-indigo-600 flex items-center justify-center shrink-0">
        <Bot size={14} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[12px] font-medium truncate text-ink">{data.label}</div>
        <div className="text-[10px] text-ink-mute">daimon · {fc} finding{fc === 1 ? '' : 's'}</div>
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Write tests for IOCNode**

Create `web/src/components/investigations/graph/nodes/IOCNode.test.tsx`:

```tsx
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { IOCNode, shortValue } from './IOCNode';

afterEach(cleanup);

function mkProps(kind: string, value: string, obs = 5, hosts = 2) {
  return {
    id: `i:${kind}:${value}`,
    type: 'ioc',
    position: { x: 0, y: 0 },
    data: { ioc_kind: kind, ioc_value: value, obs_count: obs, host_count: hosts },
    selected: false,
    zIndex: 0,
    isConnectable: false,
    xPos: 0, yPos: 0, dragging: false,
  } as any;
}

describe('IOCNode', () => {
  it('renders kind in uppercase + truncated value + counts', () => {
    render(<ReactFlowProvider><IOCNode {...mkProps('sha256', 'a'.repeat(64))} /></ReactFlowProvider>);
    expect(screen.getByText(/^sha256$/i)).toBeTruthy();
    expect(screen.getByText(/5 obs/)).toBeTruthy();
    expect(screen.getByText(/2 hosts/)).toBeTruthy();
  });

  it('singular host', () => {
    render(<ReactFlowProvider><IOCNode {...mkProps('ip', '10.0.0.1', 3, 1)} /></ReactFlowProvider>);
    expect(screen.getByText(/1 host$/)).toBeTruthy();
  });
});

describe('shortValue', () => {
  it('passes through values ≤18 chars', () => {
    expect(shortValue('10.0.0.1')).toBe('10.0.0.1');
    expect(shortValue('a'.repeat(18))).toBe('a'.repeat(18));
  });

  it('elides long values to prefix…suffix (13 chars)', () => {
    const v = 'aaaaaa1234567890bbbbbb';
    const out = shortValue(v);
    expect(out.length).toBe(13);
    expect(out.startsWith('aaaaaa')).toBe(true);
    expect(out.endsWith('bbbbbb')).toBe(true);
    expect(out.includes('…')).toBe(true);
  });

  it('elides 64-char hash predictably', () => {
    const hash = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
    const out = shortValue(hash);
    expect(out).toBe('012345…abcdef');
  });
});
```

- [ ] **Step 6: Implement `IOCNode.tsx`**

Create `web/src/components/investigations/graph/nodes/IOCNode.tsx`:

```tsx
// IOC node — 200×56 card with a kind icon (Globe / Fingerprint /
// FileText / Hash) and a middle-truncated value. Long hashes elide
// to "prefix…suffix" so a sha256 fits in the card width.

import { Hash, Globe, FileText, Fingerprint } from 'lucide-react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

export interface IOCNodeData {
  ioc_kind:    string;
  ioc_value:   string;
  obs_count?:  number;
  host_count?: number;
  [key: string]: unknown;
}

export const IOC_NODE_W = 200;
export const IOC_NODE_H = 56;

function iocIcon(kind: string) {
  switch (kind) {
    case 'ip':     case 'domain':                  return Globe;
    case 'sha256': case 'sha1': case 'md5':        return Fingerprint;
    case 'path':   case 'file':                    return FileText;
    default:                                       return Hash;
  }
}

export function shortValue(v: string): string {
  if (v.length <= 18) return v;
  return v.slice(0, 6) + '…' + v.slice(-6);
}

export function IOCNode({ data }: NodeProps<{ data: IOCNodeData }>) {
  const Icon = iocIcon(data.ioc_kind);
  const obs = data.obs_count ?? 0;
  const hosts = data.host_count ?? 0;
  return (
    <div
      className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
      style={{ width: IOC_NODE_W, height: IOC_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="w-8 h-8 rounded bg-purple-50 text-purple-600 flex items-center justify-center shrink-0">
        <Icon size={15} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[11px] text-ink-mute uppercase tracking-wide">{data.ioc_kind}</div>
        <div className="text-[12px] font-mono font-medium truncate text-ink">{shortValue(data.ioc_value)}</div>
        <div className="text-[10px] text-ink-mute">{obs} obs · {hosts} host{hosts === 1 ? '' : 's'}</div>
      </div>
    </div>
  );
}
```

- [ ] **Step 7: Run all node tests**

```bash
cd web && npx vitest run src/components/investigations/graph/nodes/
```

Expected: PASS — every test in the directory.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/investigations/graph/nodes/HostNode.tsx web/src/components/investigations/graph/nodes/HostNode.test.tsx web/src/components/investigations/graph/nodes/DaimonNode.tsx web/src/components/investigations/graph/nodes/DaimonNode.test.tsx web/src/components/investigations/graph/nodes/IOCNode.tsx web/src/components/investigations/graph/nodes/IOCNode.test.tsx
git commit -m "feat(graph): HostNode, DaimonNode, IOCNode badges"
```

---

## Task 5: `nodeTypes` registration + layout adjustments

**Files:**
- Create: `web/src/components/investigations/graph/nodes/index.ts`
- Modify: `web/src/components/investigations/graph/layout.ts`

- [ ] **Step 1: Create the `nodeTypes` index**

Create `web/src/components/investigations/graph/nodes/index.ts`:

```ts
// nodeTypes — the map ReactFlow uses to render each GraphNode.kind
// with the right component. Registered in CaseGraph.tsx.

import { FindingNode } from './FindingNode';
import { HostNode }    from './HostNode';
import { DaimonNode }  from './DaimonNode';
import { IOCNode }     from './IOCNode';

export const nodeTypes = {
  finding: FindingNode,
  host:    HostNode,
  daimon:  DaimonNode,
  ioc:     IOCNode,
};

export { FindingNode, HostNode, DaimonNode, IOCNode };
```

- [ ] **Step 2: Modify `layout.ts` for kind-aware vertical pitch + wider right column**

Edit `web/src/components/investigations/graph/layout.ts`. The new finding card (86 tall) is taller than the old default (~36 plus padding); the daimon/host badges are 44 tall, IOC is 56 tall. Update the constants and use kind-aware pitch:

Replace lines 19-23:

```ts
export const LEFT_X = 80;
export const RIGHT_X = 560;        // was 520; widened to fit 240-px finding cards in the left column
export const FINDING_PITCH = 100;  // 86-tall card + 14 gap
export const ENTITY_PITCH  = 60;   // 56-tall IOC card + 4 gap (host/daimon at 44 also fit)
export const KIND_GAP = 24;
```

The constants `NODE_HEIGHT` and `VERTICAL_GAP` are removed and replaced.

Update the `Place findings in the left column` block (around lines 62-67):

```ts
// Place findings in the left column.
let y = 40;
for (const f of findings) {
  out.set(f.id, { x: LEFT_X, y });
  y += FINDING_PITCH;
}
```

Update the `Place hosts, daimons, IOCs in the right column` block (around lines 70-84):

```ts
// Place hosts, daimons, IOCs in the right column with kind gaps.
y = 40;
for (const h of hosts) {
  out.set(h.id, { x: RIGHT_X, y });
  y += ENTITY_PITCH;
}
if (hosts.length > 0 && (daimons.length > 0 || iocs.length > 0)) y += KIND_GAP;
for (const d of daimons) {
  out.set(d.id, { x: RIGHT_X, y });
  y += ENTITY_PITCH;
}
if (daimons.length > 0 && iocs.length > 0) y += KIND_GAP;
for (const i of iocs) {
  out.set(i.id, { x: RIGHT_X, y });
  y += ENTITY_PITCH;
}
```

- [ ] **Step 3: Run the existing layout tests + typecheck**

The existing `layout.test.ts` (from PR #121) tests `computePositions` outputs. Verify nothing broke:

```bash
cd web && npx vitest run src/components/investigations/graph/layout.test.ts
cd web && npx tsc --noEmit
```

Expected: PASS for the typecheck. The layout tests may have hardcoded `y = 56 + 12 = 68` style assertions that need updating — if so, update them to match the new pitch values (`FINDING_PITCH = 100`, `ENTITY_PITCH = 60`). Read the failures and fix the y-coordinate assertions.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/investigations/graph/nodes/index.ts web/src/components/investigations/graph/layout.ts web/src/components/investigations/graph/layout.test.ts
git commit -m "feat(graph): nodeTypes map + kind-aware layout pitch"
```

---

## Task 6: `CaseGraph.tsx` wiring

The integration step. Replaces the default node rendering with the custom map; styles edges by weight; adds the MiniMap conditional and the tinted backdrop.

**Files:**
- Modify: `web/src/components/investigations/CaseGraph.tsx`
- Modify: `web/src/components/investigations/CaseGraph.test.tsx`

- [ ] **Step 1: Add the failing integration test**

Append to `web/src/components/investigations/CaseGraph.test.tsx`:

```tsx
describe('CaseGraph custom node renderers', () => {
  it('renders FindingNode body for finding nodes', async () => {
    const apiMod = await import('../../api');
    vi.spyOn(apiMod.api.investigations, 'graph').mockResolvedValueOnce({
      nodes: [
        { id: 'f:1', kind: 'finding', label: 'Suspicious cron', severity: 'CRITICAL', host: 'h1', agent: 'edr' },
      ],
      edges: [],
      total_findings: 1,
      limit_applied: 20,
    } as any);

    render(<MemoryRouter><CaseGraph investigationID={1} bundleFindingsCount={1} /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText(/Suspicious cron/)).toBeTruthy());
    // Severity badge from FindingNode
    expect(screen.getByText('CRITICAL')).toBeTruthy();
    expect(screen.getByText(/^#1$/)).toBeTruthy();
  });

  it('renders MiniMap when there are >= 10 finding nodes', async () => {
    const apiMod = await import('../../api');
    const mkFinding = (n: number) => ({
      id: `f:${n}`,
      kind: 'finding' as const,
      label: `Finding ${n}`,
      severity: 'HIGH',
      host: `h${n}`,
      agent: 'edr',
    });
    vi.spyOn(apiMod.api.investigations, 'graph').mockResolvedValueOnce({
      nodes: Array.from({ length: 10 }, (_, i) => mkFinding(i + 1)),
      edges: [],
      total_findings: 10,
      limit_applied: 20,
    } as any);

    const { container } = render(<MemoryRouter><CaseGraph investigationID={1} bundleFindingsCount={10} /></MemoryRouter>);
    await waitFor(() => expect(container.querySelector('.react-flow__minimap')).toBeTruthy());
  });

  it('does not render MiniMap when there are < 10 finding nodes', async () => {
    const apiMod = await import('../../api');
    vi.spyOn(apiMod.api.investigations, 'graph').mockResolvedValueOnce({
      nodes: [
        { id: 'f:1', kind: 'finding', label: 'one', severity: 'LOW', host: 'h1', agent: 'edr' },
      ],
      edges: [],
      total_findings: 1,
      limit_applied: 20,
    } as any);

    const { container } = render(<MemoryRouter><CaseGraph investigationID={1} bundleFindingsCount={1} /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('one')).toBeTruthy());
    expect(container.querySelector('.react-flow__minimap')).toBeNull();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd web && npx vitest run src/components/investigations/CaseGraph.test.tsx -t "custom node renderers"
```

Expected: FAIL — the default ReactFlow node still renders, no severity badge.

- [ ] **Step 3: Modify `CaseGraph.tsx` to wire `nodeTypes`, edge styles, MiniMap, backdrop**

Edit `web/src/components/investigations/CaseGraph.tsx`. Update the imports first:

```tsx
import { ReactFlow, Background, BackgroundVariant, Controls, MiniMap, type Edge, type Node } from '@xyflow/react';
import { nodeTypes } from './graph/nodes';
import { styleForEdgeWeight, weightByTarget } from './graph/edges';
```

Replace the `useMemo` block (currently around lines 55-77) that builds `rfNodes`/`rfEdges`:

```tsx
const { rfNodes, rfEdges } = useMemo(() => {
  if (!data) return { rfNodes: [] as Node[], rfEdges: [] as Edge[] };
  const positions = computePositions(data.nodes, data.edges);
  const weights = weightByTarget(data.edges);

  const rfNodes: Node[] = data.nodes.map((n) => {
    const pos = positions.get(n.id) ?? { x: 0, y: 0 };
    return {
      id: n.id,
      type: n.kind,                  // dispatches to nodeTypes map
      position: pos,
      data: nodeDataFor(n),
      draggable: false,
      selectable: true,
    };
  });

  const rfEdges: Edge[] = data.edges.map((e) => {
    const style = styleForEdgeWeight(weights.get(e.target) ?? 1);
    return {
      id: e.id,
      source: e.source,
      target: e.target,
      type: 'smoothstep',
      style: { stroke: style.stroke, strokeWidth: style.strokeWidth, opacity: style.opacity },
      animated: false,
    };
  });
  return { rfNodes, rfEdges };
}, [data]);

function nodeDataFor(n: GraphNode) {
  switch (n.kind) {
    case 'finding': return {
      id: n.id,
      numericID: Number(n.id.slice(2)),
      title: n.label,
      severity: n.severity ?? 'INFO',
      host: n.host ?? '',
      agent: n.agent ?? '',
    };
    case 'host':   return { label: n.label, finding_count: n.finding_count };
    case 'daimon': return { label: n.label, finding_count: n.finding_count };
    case 'ioc':    return {
      ioc_kind: n.ioc_kind ?? '',
      ioc_value: n.ioc_value ?? n.label,
      obs_count: n.obs_count,
      host_count: n.host_count,
    };
    default: return { label: n.label };
  }
}
```

Replace the `<ReactFlow>` block (currently around lines 137-149) with:

```tsx
{data && (
  <div
    style={{
      height: 600,
      background: 'linear-gradient(to bottom, #faf8ff, #ffffff), radial-gradient(circle at 8px 8px, #e9d5ff 1px, transparent 1px) 0 0 / 16px 16px',
    }}
  >
    <ReactFlow
      nodes={rfNodes}
      edges={rfEdges}
      nodeTypes={nodeTypes}
      onNodeClick={handleNodeClick}
      nodesDraggable={false}
      nodesConnectable={false}
      elementsSelectable
      fitView
      fitViewOptions={{ padding: 0.15 }}
      proOptions={{ hideAttribution: true }}
    >
      <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
      <Controls showInteractive={false} />
      {data.nodes.filter((n) => n.kind === 'finding').length >= 10 && (
        <MiniMap pannable zoomable nodeStrokeWidth={2} />
      )}
    </ReactFlow>
  </div>
)}
```

The wrapping `<div style={{ height: 600 }}>` from the existing code is replaced by the new `<div>` with the backdrop styling. The `data && data.total_findings > data.limit_applied` banner stays where it is (above this block).

- [ ] **Step 4: Update / remove the old `renderNodeLabel` helper**

The existing `renderNodeLabel` function at the bottom of `CaseGraph.tsx` (around lines 156-168) is no longer used — the custom node components handle their own rendering. Delete `renderNodeLabel` and `truncate` (which is its only caller).

- [ ] **Step 5: Run the targeted tests**

```bash
cd web && npx vitest run src/components/investigations/CaseGraph.test.tsx -t "custom node renderers"
```

Expected: PASS — 3 new tests green.

- [ ] **Step 6: Run the full investigations vitest suite**

```bash
cd web && npx vitest run src/components/investigations/
```

Expected: PASS — all existing tests still green.

- [ ] **Step 7: Run typecheck**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS — no type errors.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/investigations/CaseGraph.tsx web/src/components/investigations/CaseGraph.test.tsx
git commit -m "feat(graph): wire nodeTypes + edge styling + MiniMap + backdrop"
```

---

## Task 7: Architecture docs + sweep + PR

- [ ] **Step 1: Update `docs/architecture.md`**

Find the section "Investigation Overview — bipartite graph view" (added by PR #121, around the `## Investigation Overview` cluster). Replace its body with:

```markdown
## Investigation Overview — bipartite graph view

The Graph view tab renders findings ↔ shared entities (hosts /
daimons / IOCs) as a bipartite ReactFlow canvas with custom per-kind
node renderers. Findings render as 240×86 cards with a severity rail
matching the orchestration step card; hosts/daimons/IOCs render as
compact badges with kind-specific icons (Server / Bot / Globe /
Fingerprint / FileText / Hash). Edges scale stroke width by the
number of findings touching each entity (1 / 2-3 / 4-9 / 10+ bands).

The graph is capped at 20 findings by default (configurable via
`?limit=N`); a banner above the canvas surfaces the cap. For dense
graphs (≥10 findings) a `<MiniMap>` provides navigation.

The canvas backdrop is a tinted-purple gradient + radial dot grid
matching `OrchestrationCanvas` so the Graph view reads as the same
product surface as the orchestration view. Click a finding card →
opens the entity drawer (existing `entity:open` event bus). Click a
host badge → navigates to `/findings?host=<name>`.

Layout (`web/src/components/investigations/graph/layout.ts`) is pure:
findings in the left column at `x=80`, entities in the right column
at `x=560`, with kind-aware vertical pitch (100px for findings,
60px for entity badges) and a 24px gap between entity sections.

The bundle endpoint `GET /api/investigations/{id}` surfaces partial
failures via a `bundle_warnings` field — when one of the enriched
list calls (`ListFindingsForInvestigationEnriched`,
`ListIOCsForInvestigation`, etc.) errors, the response still ships
with empty arrays and the warning is logged + included in the JSON
so the operator sees an amber strip above the tabs explaining what
couldn't be loaded.
```

- [ ] **Step 2: Run the full Go test suite**

```bash
cd /Users/matt/Code/Oracle/Okesu/.claude/worktrees/feat-investigation-overview-polish
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run the full vitest suite**

```bash
cd web && npx vitest run
```

Expected: PASS.

- [ ] **Step 4: Run typecheck**

```bash
cd web && npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 5: Commit docs**

```bash
git add docs/architecture.md
git commit -m "docs(architecture): update graph view section for the redesign"
```

- [ ] **Step 6: Push branch**

```bash
git push -u origin feat/investigation-overview-polish
```

If SSH agent refuses (intermittent in this project), report and STOP — the user pushes manually.

- [ ] **Step 7: Open PR**

```bash
gh pr create --title "fix+feat: investigation overview polish (graph redesign + bundle warnings)" --base main --body "$(cat <<'EOF'
## Summary
Three loosely-related improvements to the Investigation Overview, bundled into one PR:

- **Bundle handler error surfacing.** `GetInvestigationHandler` was discarding errors from the five enriched list calls (`findings, _ := ...`); a query failure (e.g. column mismatch from a missed migration) silently returned `findings: []` while the structure endpoint still worked. The handler now records errors as a `bundle_warnings: string[]` field on the response, logs them, and the frontend renders an amber strip above the tabs listing which list method failed. **This is the fix for the empty-timeline-with-data-cards-populated bug.**

- **Graph node redesign.** `CaseGraph` replaces ReactFlow's default node component with four custom renderers — a 240×86 finding card with a severity rail (purple/red/orange/yellow/slate) matching the orchestration step card, plus host/daimon/IOC badges with kind-specific icons (Server/Bot/Globe/Fingerprint/FileText/Hash).

- **Edge weighting + canvas polish.** Edges scale stroke width by the number of findings touching each entity (1/2-3/4-9/10+ bands). The canvas gets the same tinted gradient + radial-dot backdrop the orchestration canvas uses. A `<MiniMap>` appears for dense graphs (≥10 findings).

Spec: `docs/superpowers/specs/2026-05-04-investigation-overview-polish-design.md`
Plan: `docs/superpowers/plans/2026-05-04-investigation-overview-polish.md`

## Test plan
- [ ] `go test ./...` — full Go suite green
- [ ] `cd web && npx vitest run` — full vitest suite green
- [ ] `cd web && npx tsc --noEmit` — typecheck clean
- [ ] Manual: open an investigation with mixed-severity findings + multiple hosts/daimons/IOCs → confirm the new card renderers, severity-tinted rails, kind-specific icons, and gradient backdrop
- [ ] Manual: open an investigation with ≥10 findings → confirm the MiniMap appears
- [ ] Manual: trigger the bug-fix path on a dev instance (rename `investigation_findings.link_method` via `sqlite3` CLI) → reload the case → confirm the amber warning strip appears at the top with `findings: …` listed

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

If `gh pr create` requires `--base main`, the flag is already in the command above.

---

## Self-review notes

**1. Spec coverage.** Every spec section maps to a task:
- "Issue 1: Bundle handler error surfacing" → Task 1.
- `tones.ts` and `edges.ts` helpers → Task 2.
- `FindingNode` → Task 3.
- `HostNode`, `DaimonNode`, `IOCNode` → Task 4.
- `nodeTypes` registration + layout adjustments → Task 5.
- `CaseGraph.tsx` wiring (nodeTypes, edges, MiniMap, backdrop) → Task 6.
- Architecture docs + sweep + PR → Task 7.

**2. Placeholder scan.** No "TBD" / "TODO" / "add appropriate" / "similar to" lines. Each task step has a concrete code block. The two NOTE callouts (Task 1 step 6 about insertion point; Task 5 step 3 about layout test fixups) point at specific things to verify in existing code — not placeholders.

**3. Type consistency.**
- `FindingNodeData` defined in Task 3, consumed in Task 6's `nodeDataFor`.
- `HostNodeData` / `DaimonNodeData` / `IOCNodeData` in Task 4, consumed in Task 6's `nodeDataFor`.
- `NodeTone` / `severityTone` in Task 2, consumed in Task 3's `FindingNode`.
- `EdgeStyle` / `styleForEdgeWeight` / `weightByTarget` in Task 2, consumed in Task 6's edge styling.
- `nodeTypes` in Task 5 (`graph/nodes/index.ts`), imported in Task 6.
- `bundle_warnings: string[]` in Task 1 (api.ts), rendered in Task 1's InvestigationDetail.tsx, asserted in Task 1's test.
- All node `_W`/`_H` constants exported from each file, available for any future caller (we don't currently use them outside the components themselves).
