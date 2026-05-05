# Investigation Overview polish — graph redesign + timeline-empty bug fix

## Goal

Three loosely-related improvements to the Investigation Overview, shipped
in a single PR:

1. Fix the empty-timeline bug by making the bundle handler stop
   silently swallowing query errors.
2. Redesign the relationship graph (`CaseGraph`) with custom per-kind
   node renderers matching the orchestration canvas style.
3. Add MiniMap and the same tinted-dot-grid backdrop the orchestration
   canvas uses.

Operators currently see an empty timeline canvas with no error
indication when the bundle's `findings` query fails (root cause:
`GetInvestigationHandler` discards errors via `findings, _ :=`). They
also see plain ReactFlow rectangles for the graph view, which feels
visually disconnected from the polished orchestration canvas.

## Architecture

A single PR with three independent changes. The bug fix is a backend
change in `controlplane/api/investigations.go` that surfaces errors via
a new `bundle_warnings` field on the response, plus a small frontend
amber strip rendering them. The graph redesign replaces ReactFlow's
default node component with four custom components (one per kind:
finding, host, daimon, IOC), styled edges via a pure stroke-weight
helper, and the same backdrop the orchestration canvas already uses.

The bug fix and graph redesign do not depend on each other; they are
combined into one PR for narrative coherence ("polish the investigation
overview surface") rather than technical coupling.

## File structure

| File | Status | Responsibility |
|---|---|---|
| `controlplane/api/investigations.go` | MODIFY | Surface errors from the five enriched list calls; build `bundle_warnings` slice; include it in the JSON response when non-empty |
| `controlplane/api/investigations_test.go` | MODIFY | Test that a forced error in a list method yields a `bundle_warnings` entry while the response still ships; happy-path test asserts the field is omitted |
| `web/src/api.ts` | MODIFY | Add `bundle_warnings?: string[]` to `InvestigationDetail` |
| `web/src/pages/InvestigationDetail.tsx` | MODIFY | Render an amber strip above the tabs when `bundle_warnings.length > 0` |
| `web/src/components/investigations/graph/nodes/tones.ts` | NEW | Severity → Tailwind class set helper (`severityTone`) |
| `web/src/components/investigations/graph/nodes/FindingNode.tsx` | NEW | 240×86 finding card with severity rail + title + agent/host chips |
| `web/src/components/investigations/graph/nodes/FindingNode.test.tsx` | NEW | Render assertions for the finding card |
| `web/src/components/investigations/graph/nodes/HostNode.tsx` | NEW | 160×44 host badge with server icon |
| `web/src/components/investigations/graph/nodes/HostNode.test.tsx` | NEW | Render assertions |
| `web/src/components/investigations/graph/nodes/DaimonNode.tsx` | NEW | 180×44 daimon badge with bot icon + finding count |
| `web/src/components/investigations/graph/nodes/DaimonNode.test.tsx` | NEW | Render assertions |
| `web/src/components/investigations/graph/nodes/IOCNode.tsx` | NEW | 200×56 IOC card with kind icon + truncated value |
| `web/src/components/investigations/graph/nodes/IOCNode.test.tsx` | NEW | Render assertions + `shortValue` truncation |
| `web/src/components/investigations/graph/nodes/index.ts` | NEW | `nodeTypes` map registering the four components for ReactFlow |
| `web/src/components/investigations/graph/edges.ts` | NEW | Pure helpers: `styleForEdgeWeight` + `weightByTarget` |
| `web/src/components/investigations/graph/edges.test.ts` | NEW | Unit tests for the edge helpers |
| `web/src/components/investigations/graph/layout.ts` | MODIFY | Bump column gap to fit the wider 240-px finding card + 200-px IOC card |
| `web/src/components/investigations/CaseGraph.tsx` | MODIFY | Wire `nodeTypes`, edge styling, MiniMap conditional, tinted backdrop |
| `web/src/components/investigations/CaseGraph.test.tsx` | MODIFY | Update existing render assertions for the new components |
| `docs/architecture.md` | MODIFY | Update the existing graph view section |

## Issue 1: Bundle handler error surfacing

### Server-side change

In `controlplane/api/investigations.go`, replace the silent-error pattern
in `GetInvestigationHandler` (lines 190-243):

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
            if err == nil { return }
            log.Printf("investigation bundle %d: %s: %v", id, name, err)
            warnings = append(warnings, fmt.Sprintf("%s: %s", name, err.Error()))
        }

        findings, fErr := store.ListFindingsForInvestigationEnriched(id)
        recordWarn("findings", fErr)
        runs,    rErr := store.ListRunsForInvestigationEnriched(id)
        recordWarn("runs", rErr)
        iocs,    iErr := store.ListIOCsForInvestigation(id)
        recordWarn("iocs", iErr)
        daimons, dErr := store.ListDaimonsForInvestigation(id)
        recordWarn("daimons", dErr)
        orchs,   oErr := store.ListOrchestrationsForInvestigation(id)
        recordWarn("orchestrations", oErr)
        notes,   nErr := store.ListInvestigationNotes(id)
        recordWarn("notes", nErr)
        warRoom, _ := store.IsInvestigationWarRoom(id)

        if findings == nil { findings = []db.InvestigationFindingItem{} }
        if runs == nil     { runs = []db.InvestigationRunItem{} }
        if iocs == nil     { iocs = []db.InvestigationIOCItem{} }
        if daimons == nil  { daimons = []db.InvestigationDaimonItem{} }
        if orchs == nil    { orchs = []db.InvestigationOrchestrationItem{} }
        if notes == nil    { notes = []db.InvestigationNote{} }

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

`fmt` and `log` should already be in the imports (the existing
`investigations.go` uses both); add them if not.

`IsInvestigationWarRoom` keeps the silent-error pattern because it's a
non-load-bearing UI bit (boolean flag) that's already on the noise
floor.

### Frontend change

In `web/src/api.ts`, add the field to `InvestigationDetail`:

```ts
export interface InvestigationDetail {
  // ...existing fields...
  bundle_warnings?: string[];   // server-emitted partial-failure list
}
```

In `web/src/pages/InvestigationDetail.tsx`, find where the bundle is
rendered above the tab switcher and add:

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

### Test

Add to `controlplane/api/investigations_test.go`:

```go
func TestGetInvestigation_SurfacesListErrors(t *testing.T) {
    store := newSeededTestStore(t)
    invID, _ := store.CreateInvestigation(&db.InvestigationInsert{Title: "case"})

    // Force an error: rename a column the enriched query references.
    if _, err := store.Exec(`ALTER TABLE investigation_findings RENAME COLUMN link_method TO link_method_old`); err != nil {
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
    // Other arrays are still empty slices, not missing.
    if _, ok := resp["findings"].([]any); !ok {
        t.Errorf("findings should be [] not nil")
    }
}
```

If `RENAME COLUMN` doesn't work in the SQLite version used by tests,
fall back to `DROP TABLE investigation_findings` after creating the
investigation — each test gets a fresh temp store so cleanup is
automatic.

Add a complementary happy-path assertion to one existing test:

```go
if _, ok := resp["bundle_warnings"]; ok {
    t.Errorf("bundle_warnings should be omitted on success; got %v", resp["bundle_warnings"])
}
```

## Issues 2+3: Graph node redesign + canvas polish

### Severity tone helper (`tones.ts`)

```ts
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
    case 'CRITICAL': return { bg: 'bg-purple-50',  ring: 'ring-purple-500',   badge: 'severity-badge severity-critical', iconBg: 'bg-purple-100',  iconFg: 'text-purple-700' };
    case 'HIGH':     return { bg: 'bg-red-50',     ring: 'ring-red-500',      badge: 'severity-badge severity-high',     iconBg: 'bg-red-100',     iconFg: 'text-red-700' };
    case 'MEDIUM':   return { bg: 'bg-orange-50',  ring: 'ring-orange-500',   badge: 'severity-badge severity-medium',   iconBg: 'bg-orange-100',  iconFg: 'text-orange-700' };
    case 'LOW':      return { bg: 'bg-yellow-50',  ring: 'ring-yellow-500',   badge: 'severity-badge severity-low',      iconBg: 'bg-yellow-100',  iconFg: 'text-yellow-700' };
    default:         return { bg: 'bg-slate-50',   ring: 'ring-slate-400',    badge: 'severity-badge severity-info',     iconBg: 'bg-slate-100',   iconFg: 'text-slate-700' };
  }
}
```

### Finding card (`FindingNode.tsx`)

```tsx
import { Handle, Position, type NodeProps } from '@xyflow/react';
import { cn } from '../../../../lib/cn';
import { severityTone } from './tones';

export interface FindingNodeData {
  id:       string;
  numericID: number;
  title:    string;
  severity: string;
  host:     string;
  agent:    string;
  [key: string]: unknown;
}

export const FINDING_NODE_W = 240;
export const FINDING_NODE_H = 86;

export function FindingNode({ data, selected }: NodeProps<{ data: FindingNodeData }>) {
  const tone = severityTone(data.severity);
  return (
    <div
      className={cn(
        'rounded-lg border bg-panel shadow-card overflow-hidden text-left ring-1 transition',
        tone.ring,
        selected ? 'ring-2 ring-offset-1' : '',
      )}
      style={{ width: FINDING_NODE_W, height: FINDING_NODE_H }}
    >
      <Handle type="target" position={Position.Left}  style={{ visibility: 'hidden' }} />
      <Handle type="source" position={Position.Right} style={{ visibility: 'hidden' }} />
      <div className="flex h-full">
        <div className={cn('w-1 h-full', tone.ring.replace('ring-', 'bg-'))} />
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

### Host node (`HostNode.tsx`)

```tsx
import { Server } from 'lucide-react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

export const HOST_NODE_W = 160;
export const HOST_NODE_H = 44;

export function HostNode({ data }: NodeProps<{ data: { label: string; finding_count?: number } }>) {
  return (
    <div className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
         style={{ width: HOST_NODE_W, height: HOST_NODE_H }}>
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

### Daimon node (`DaimonNode.tsx`)

```tsx
import { Bot } from 'lucide-react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

export const DAIMON_NODE_W = 180;
export const DAIMON_NODE_H = 44;

export function DaimonNode({ data }: NodeProps<{ data: { label: string; finding_count?: number } }>) {
  const fc = data.finding_count ?? 0;
  return (
    <div className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
         style={{ width: DAIMON_NODE_W, height: DAIMON_NODE_H }}>
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

### IOC node (`IOCNode.tsx`)

```tsx
import { Hash, Globe, FileText, Fingerprint } from 'lucide-react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

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

function shortValue(v: string): string {
  if (v.length <= 18) return v;
  return v.slice(0, 6) + '…' + v.slice(-6);
}

export function IOCNode({ data }: NodeProps<{ data: { ioc_kind: string; ioc_value: string; obs_count?: number; host_count?: number } }>) {
  const Icon = iocIcon(data.ioc_kind);
  const obs = data.obs_count ?? 0;
  const hosts = data.host_count ?? 0;
  return (
    <div className="bg-panel border border-border rounded-md shadow-sm flex items-center gap-2 px-2.5"
         style={{ width: IOC_NODE_W, height: IOC_NODE_H }}>
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

export { shortValue };
```

`shortValue` is exported so the unit test can exercise it without
rendering the full component.

### `nodeTypes` registration (`graph/nodes/index.ts`)

```ts
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
```

### Edge helpers (`graph/edges.ts`)

```ts
export interface EdgeStyle {
  stroke:      string;
  strokeWidth: number;
  opacity:     number;
}

/**
 * styleForEdgeWeight — maps a "this entity is touched by N findings"
 * count to a stroke style. Bands: 1, 2-3, 4-9, 10+ findings.
 */
export function styleForEdgeWeight(weight: number): EdgeStyle {
  if (weight >= 10) return { stroke: '#475569', strokeWidth: 2.5, opacity: 0.9 };
  if (weight >= 4)  return { stroke: '#64748b', strokeWidth: 2,   opacity: 0.85 };
  if (weight >= 2)  return { stroke: '#94a3b8', strokeWidth: 1.5, opacity: 0.8 };
  return                  { stroke: '#cbd5e1', strokeWidth: 1,   opacity: 0.75 };
}

/**
 * weightByTarget — given the graph's edges, returns Map<targetID, count>
 * for use in styling. The same target node may have many incoming
 * edges; we use the count to choose a stroke band.
 */
export function weightByTarget(edges: ReadonlyArray<{ target: string }>): Map<string, number> {
  const m = new Map<string, number>();
  for (const e of edges) m.set(e.target, (m.get(e.target) ?? 0) + 1);
  return m;
}
```

### Layout adjustment (`graph/layout.ts`)

The existing `computePositions` arranges findings in a left column and
entities in a right column. With wider finding cards (240) and wider
IOC nodes (200), the column gap needs to expand. Two changes (the
implementer should locate the relevant constants in the existing
`graph/layout.ts` and bump them):

- **Left column (findings) starts at `x = 0`.** Each finding card
  occupies 240px; column extent is `[0, 240]`.
- **Right column (entities) starts at `x = 380`** (was likely ~200).
  Gap = 140px gives smoothstep edges room to curve cleanly.
- **Vertical pitch** unchanged.

### `CaseGraph.tsx` wiring

Replace the existing node assembly (currently around lines 58-77) with:

```tsx
const { rfNodes, rfEdges } = useMemo(() => {
  if (!data) return { rfNodes: [] as Node[], rfEdges: [] as Edge[] };
  const positions = computePositions(data.nodes, data.edges);
  const weights = weightByTarget(data.edges);

  const rfNodes: Node[] = data.nodes.map((n) => {
    const pos = positions.get(n.id) ?? { x: 0, y: 0 };
    return {
      id: n.id,
      type: n.kind,
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

In the `<ReactFlow>` mount, add `nodeTypes`, the conditional MiniMap,
and the backdrop:

```tsx
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
    {data && data.nodes.filter((n) => n.kind === 'finding').length >= 10 && (
      <MiniMap pannable zoomable nodeStrokeWidth={2} />
    )}
  </ReactFlow>
</div>
```

Add `MiniMap` to the imports. Use the existing `data.total_findings`
banner already in place.

## Testing

### Server-side

`controlplane/api/investigations_test.go`: `TestGetInvestigation_SurfacesListErrors`
+ a happy-path complement asserting `bundle_warnings` is omitted.

### Client-side

- `FindingNode.test.tsx`: title, severity badge class, agent + host
  chips render; long titles get clamped; `CRITICAL` → `severity-critical`,
  `INFO`/empty → slate fallback.
- `HostNode.test.tsx`: label visible; "host" subtext.
- `DaimonNode.test.tsx`: label, finding-count subtext, singular/plural
  agreement.
- `IOCNode.test.tsx`: kind label uppercase, truncated value, obs/host
  counts. Direct unit test on `shortValue`: 64-char hash → 13-char
  `prefix…suffix`, ≤18-char value passes through unchanged.
- `edges.test.ts`: `styleForEdgeWeight(1|2|4|10)` returns the four
  bands; `weightByTarget` counts correctly.
- `CaseGraph.test.tsx`: existing node-render assertions updated for the
  new components (mock returns `{ nodes: [{kind: 'finding', ...}] }`,
  assert finding title is in DOM); MiniMap renders for ≥10 findings,
  doesn't render for <10.

### Manual lab smoke

- Open an investigation with mixed-severity findings + multiple
  hosts/daimons/IOCs:
  - Finding cards show severity-tinted rings.
  - Host nodes: green server icon + hostname.
  - Daimon nodes: indigo bot icon + finding count.
  - IOC nodes: kind icon (Globe / Fingerprint / FileText / Hash).
  - Edges thicken when an entity is touched by many findings.
  - Background has tinted gradient + dot pattern.
- Open an investigation with ≥10 findings: `<MiniMap>` appears.
- Open an investigation with <10 findings: no MiniMap.
- Click a finding card → drawer opens (existing behavior).
- Click a host node → navigates to `/findings?host=<name>` (existing).
- Trigger the bug-fix path: rename `investigation_findings.link_method`
  via `sqlite3` CLI on a dev instance, reload the page, confirm the
  amber warning strip appears at the top with `findings: …` in the list.

## Edge cases

- **Graph with single finding and no entities.** ReactFlow's `fitView`
  zooms to the lone card. MiniMap suppressed.
- **Graph at the 100-finding cap.** Banner already shows "X of Y
  findings shown"; MiniMap is the navigation aid.
- **Finding with null severity.** `severityTone('')` returns slate
  fallback. INFO badge renders.
- **IOC with one-character value.** `shortValue` returns original.
- **Bundle returns warnings AND data.** Amber strip renders above
  tabs; tabs render normally with whatever partial data made it
  through.
- **Bundle returns warnings but no data.** Operator sees an empty
  timeline AND the warning strip — strip explains why.

## Out of scope

- **Real fix for the underlying schema mismatch.** This PR makes the
  failure observable; remediation belongs in a separate PR (e.g., a
  migration-status check at startup, or a self-heal that runs missing
  migrations).
- **Drag-to-pan / drag-to-rearrange the graph.** Static layout
  (`nodesDraggable={false}`) — exploration view, not an editor.
- **Edge labels.** Redundant given the entity node already shows the
  count.
- **Multi-select on the graph.** Multi-select belongs to the Findings
  tab.
- **Animated edges.** Static rendering is intentional — animation
  distracts from comprehension.
