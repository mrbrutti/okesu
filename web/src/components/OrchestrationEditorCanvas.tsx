// Drag-and-drop orchestration authoring canvas.
//
// Workflow:
//   1. Operator drags an agent from the left palette onto the canvas
//      → creates a step node with auto-generated id.
//   2. Drags from one node's right handle to another's left handle
//      → creates an edge defining execution order.
//   3. Clicks a node → right inspector opens with editable fields
//      (id, node target, prompt, timeout, approval gate).
//   4. Save → canvas state is topologically sorted by x-position and
//      serialized to YAML.
//
// Built on @xyflow/react for the connection drawing + port snap +
// pan/zoom. Hand-rolling those alone would be ~1000 lines of fragile
// state machines.
//
// Two-way sync: opening an existing orchestration parses its YAML
// once into nodes/edges; from then on the canvas is the canonical
// state. The YAML view is regenerated on demand from the canvas.

import {
  ChangeEvent,
  DragEvent as ReactDragEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  addEdge,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import yaml from 'js-yaml';
import { Cpu, Network, Pause, Server, Trash2, Workflow, X } from 'lucide-react';
import { api, type AgentLibraryItem, type NodeItem } from '../api';
import { cn } from '../lib/cn';

import '@xyflow/react/dist/style.css';

// ── Step-node data shape ────────────────────────────────────────────

interface StepNodeData {
  agent: string;
  agentDescription?: string;
  /** Selected node hostnames. Empty = local-only step. One = single
   *  dispatch (`node:` in YAML). Two or more = fan-out (`nodes:` in
   *  YAML), executed in parallel by the engine. */
  nodes: string[];
  prompt: string;
  timeout?: string;
  approval?: 'required' | '';
  /** Per-step dispatch hint: '' = auto (tunnel if connected else
   *  jobs), 'tunnel' = require tunnel (auto-start if jobs runtime
   *  available), 'jobs' = require pull queue. */
  dispatch?: '' | 'tunnel' | 'jobs';
  // Run-time status (only set when the canvas is rendering a live
  // run rather than an editor draft; the editor passes 'draft').
  status?: 'draft' | 'pending' | 'running' | 'completed' | 'failed' | 'waiting_approval' | 'skipped';
  [key: string]: unknown;
}

type StepNode = Node<StepNodeData>;

// ── Custom node renderer ────────────────────────────────────────────

const NODE_W = 240;

function StepNodeView({ data, selected }: NodeProps<StepNode>) {
  const status = data.status ?? 'draft';
  const tone = stepNodeTone(status);
  return (
    <div
      className={cn(
        'bg-panel border rounded-lg shadow-card overflow-hidden text-left',
        selected ? 'ring-2 ring-brand-300 border-brand-200' : 'border-border',
        status === 'waiting_approval' && 'animate-pulse ring-2 ring-amber-300',
        status === 'running' && 'ring-2 ring-blue-300',
      )}
      style={{ width: NODE_W }}
    >
      <Handle type="target" position={Position.Left} className="!bg-brand-500 !w-2 !h-2 !border-0" />
      <div className={cn('h-1', tone.stripe)} />
      {/* Fan-out header strip — only when 2+ nodes are configured.
          Mirrors the run-canvas fanout card's identity treatment so
          operators see the same shape in editor + run viewer. */}
      {data.nodes && data.nodes.length > 1 && (
        <div className="bg-brand-50 text-brand-700 px-3 py-1 text-[10px] uppercase tracking-wide font-semibold flex items-center gap-1.5 border-b border-brand-100">
          <Network size={10} />
          <span>Fan-out · {data.nodes.length} hosts</span>
        </div>
      )}
      <div className="p-3">
        <div className="flex items-center gap-1.5 mb-1.5 flex-wrap">
          <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded font-medium">
            <Cpu size={9} />
            {data.agent || 'select agent'}
          </span>
          {data.approval === 'required' && (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-amber-700 bg-amber-50 ring-1 ring-amber-200 px-1.5 py-0.5 rounded font-medium">
              <Pause size={9} />
              gated
            </span>
          )}
          {status !== 'draft' && (
            <span className={cn('text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded', tone.badge)}>
              {status.replace('_', ' ')}
            </span>
          )}
        </div>
        {data.nodes && data.nodes.length === 1 && (
          <div className="text-[11px] text-ink-dim flex items-center gap-1 mb-1 min-w-0">
            <Server size={10} className="shrink-0" />
            <span className="truncate font-mono">{data.nodes[0]}</span>
          </div>
        )}
        {data.nodes && data.nodes.length > 1 && (
          <div className="mb-1.5 space-y-0.5" title={data.nodes.join(', ')}>
            {data.nodes.slice(0, 3).map((host) => (
              <div key={host} className="text-[11px] text-ink-dim font-mono flex items-center gap-1.5 min-w-0">
                <span className="w-1 h-1 rounded-full bg-slate-300 shrink-0" />
                <span className="truncate">{host}</span>
              </div>
            ))}
            {data.nodes.length > 3 && (
              <div className="text-[10px] text-ink-mute italic pl-2.5">
                +{data.nodes.length - 3} more
              </div>
            )}
          </div>
        )}
        <div className="text-xs text-ink line-clamp-2 leading-snug">
          {data.prompt || <span className="text-ink-mute italic">no prompt</span>}
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="!bg-brand-500 !w-2 !h-2 !border-0" />
    </div>
  );
}

const nodeTypes = { step: StepNodeView };

function stepNodeTone(status: NonNullable<StepNodeData['status']>) {
  switch (status) {
    case 'completed':         return { stripe: 'bg-green-500',  badge: 'bg-green-50 text-green-700' };
    case 'failed':            return { stripe: 'bg-red-500',    badge: 'bg-red-50 text-red-700' };
    case 'running':           return { stripe: 'bg-blue-500',   badge: 'bg-blue-50 text-blue-700' };
    case 'waiting_approval':  return { stripe: 'bg-amber-400',  badge: 'bg-amber-50 text-amber-700' };
    case 'skipped':           return { stripe: 'bg-slate-300',  badge: 'bg-slate-100 text-slate-600' };
    case 'pending':           return { stripe: 'bg-slate-300',  badge: 'bg-slate-100 text-slate-600' };
    default:                  return { stripe: 'bg-brand-400',  badge: 'bg-slate-100 text-slate-600' };
  }
}

// ── YAML <-> canvas conversion ──────────────────────────────────────

interface SpecMeta {
  name: string;
  description: string;
  // We don't wire trigger/inputs into the canvas yet — operators
  // edit those in the YAML view if they need them. v1.5 will add
  // form fields for these without changing the canvas itself.
  rawHeader?: string;
}

interface ParsedSpec {
  meta: SpecMeta;
  nodes: StepNode[];
  edges: Edge[];
  // Fields the parser saw but doesn't expose on the canvas — we
  // round-trip them as-is on save so toggling between Visual and
  // YAML modes doesn't drop trigger/inputs/defaults.
  passthrough?: Record<string, unknown>;
}

const X_SPACING = 280;

export function parseSpecYAML(content: string): ParsedSpec | null {
  // Pull the frontmatter block. The body is operator notes — we keep
  // it in `meta.rawHeader` so save can round-trip it.
  const match = content.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
  if (!match) return null;
  const fm = match[1];
  const body = match[2];

  let parsed: Record<string, unknown> = {};
  try {
    parsed = yaml.load(fm) as Record<string, unknown>;
    if (parsed === null || typeof parsed !== 'object') {
      return null;
    }
  } catch {
    return null;
  }

  const stepsArr = (parsed['steps'] as Array<Record<string, unknown>>) ?? [];
  const nodes: StepNode[] = stepsArr.map((s, i) => {
    // Normalise both `node:` and `nodes:` into a single array — the
    // canvas only knows the multi-form, the serializer flips back to
    // singular when only one host is selected.
    const nodeList: string[] = [];
    if (typeof s.node === 'string' && s.node) nodeList.push(s.node);
    if (Array.isArray(s.nodes)) {
      for (const n of s.nodes) {
        if (typeof n === 'string' && n) nodeList.push(n);
      }
    }
    return {
      id: String(s.id ?? `step-${i + 1}`),
      type: 'step',
      position: { x: i * X_SPACING + 40, y: 80 },
      data: {
        agent: String(s.agent ?? ''),
        nodes: nodeList,
        prompt: typeof s.prompt === 'string' ? s.prompt : '',
        timeout: typeof s.timeout === 'string' ? s.timeout : undefined,
        approval: s.approval === 'required' ? 'required' : '',
        dispatch:
          s.dispatch === 'tunnel' ? 'tunnel' :
          s.dispatch === 'jobs' ? 'jobs' :
          '',
      },
    };
  });
  const edges: Edge[] = stepsArr.slice(0, -1).map((s, i) => ({
    id: `${s.id}-${stepsArr[i + 1].id}`,
    source: String(s.id ?? `step-${i + 1}`),
    target: String(stepsArr[i + 1].id ?? `step-${i + 2}`),
    type: 'smoothstep',
    markerEnd: { type: MarkerType.ArrowClosed, color: '#94a3b8' },
    style: { stroke: '#94a3b8', strokeWidth: 2 },
  }));

  // Passthrough = everything except `name`, `description`, `steps`.
  const passthrough: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(parsed)) {
    if (k !== 'name' && k !== 'description' && k !== 'steps') {
      passthrough[k] = v;
    }
  }

  return {
    meta: {
      name: String(parsed['name'] ?? ''),
      description: String(parsed['description'] ?? ''),
      rawHeader: body.trim(),
    },
    nodes,
    edges,
    passthrough,
  };
}

export function serializeToYAML(meta: SpecMeta, nodes: StepNode[], edges: Edge[], passthrough?: Record<string, unknown>): string {
  // Topological sort by edge order, falling back to x-position so the
  // YAML reads naturally (left-to-right visually = top-to-bottom
  // textually). Detached nodes get appended after the connected chain.
  const sorted = topoSort(nodes, edges);

  const stepsObj = sorted.map((n) => {
    const s: Record<string, unknown> = {
      id: n.id,
      agent: n.data.agent,
    };
    // Serialize nodes back to singular `node:` when there's exactly
    // one — keeps the YAML tidy for the common single-host case. Two
    // or more become `nodes: [...]` to trigger engine-side fan-out.
    if (n.data.nodes && n.data.nodes.length === 1) {
      s['node'] = n.data.nodes[0];
    } else if (n.data.nodes && n.data.nodes.length > 1) {
      s['nodes'] = n.data.nodes;
    }
    if (n.data.timeout)           s['timeout']  = n.data.timeout;
    if (n.data.approval === 'required') s['approval'] = 'required';
    if (n.data.dispatch === 'tunnel' || n.data.dispatch === 'jobs') {
      s['dispatch'] = n.data.dispatch;
    }
    s['prompt'] = n.data.prompt || '';
    return s;
  });

  const obj: Record<string, unknown> = {
    name: meta.name || 'untitled-orchestration',
    description: meta.description || '',
    ...(passthrough ?? {}),
    steps: stepsObj,
  };

  const fm = yaml.dump(obj, {
    lineWidth: 100,
    noRefs: true,
    sortKeys: false,
  });
  const body = meta.rawHeader ? `\n${meta.rawHeader}\n` : '\n';
  return `---\n${fm}---\n${body}`;
}

function topoSort(nodes: StepNode[], edges: Edge[]): StepNode[] {
  const incoming = new Map<string, number>(nodes.map((n) => [n.id, 0]));
  for (const e of edges) {
    incoming.set(e.target, (incoming.get(e.target) ?? 0) + 1);
  }
  // Kahn's algorithm with x-position as tiebreaker so the YAML order
  // matches the operator's visual left-to-right reading.
  const ready: StepNode[] = nodes
    .filter((n) => (incoming.get(n.id) ?? 0) === 0)
    .sort((a, b) => a.position.x - b.position.x);
  const out: StepNode[] = [];
  while (ready.length) {
    const n = ready.shift()!;
    out.push(n);
    for (const e of edges.filter((e) => e.source === n.id)) {
      const cur = (incoming.get(e.target) ?? 0) - 1;
      incoming.set(e.target, cur);
      if (cur === 0) {
        const next = nodes.find((x) => x.id === e.target);
        if (next) ready.push(next);
      }
    }
    ready.sort((a, b) => a.position.x - b.position.x);
  }
  // Append cycle / unreachable nodes at the end so we never silently
  // drop them — saving an invalid graph is the user's call, not the
  // serializer's.
  if (out.length < nodes.length) {
    const seen = new Set(out.map((n) => n.id));
    out.push(...nodes.filter((n) => !seen.has(n.id)));
  }
  return out;
}

// ── Editor component ────────────────────────────────────────────────

interface EditorProps {
  /** Initial YAML to load into the canvas. Empty string for new. */
  initialYAML: string;
  onSave: (yaml: string) => void | Promise<void>;
  onCancel: () => void;
  busy?: boolean;
  saveError?: string | null;
}

export default function OrchestrationEditorCanvas(props: EditorProps) {
  return (
    <ReactFlowProvider>
      <EditorInner {...props} />
    </ReactFlowProvider>
  );
}

function EditorInner({ initialYAML, onSave, onCancel, busy, saveError }: EditorProps) {
  const initial = useMemo(() => {
    if (initialYAML) {
      const parsed = parseSpecYAML(initialYAML);
      if (parsed) return parsed;
    }
    return {
      meta: { name: '', description: '', rawHeader: '' },
      nodes: [] as StepNode[],
      edges: [] as Edge[],
      passthrough: {} as Record<string, unknown>,
    };
  }, [initialYAML]);

  const [meta, setMeta] = useState<SpecMeta>(initial.meta);
  const [nodes, setNodes, onNodesChange] = useNodesState<StepNode>(initial.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>(initial.edges);
  const passthroughRef = useRef(initial.passthrough);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [agents, setAgents] = useState<AgentLibraryItem[]>([]);
  const [availableNodes, setAvailableNodes] = useState<NodeItem[]>([]);
  const [mode, setMode] = useState<'visual' | 'yaml'>('visual');
  const [yamlBuffer, setYamlBuffer] = useState<string>(initialYAML);
  const reactFlow = useReactFlow();
  const wrapperRef = useRef<HTMLDivElement | null>(null);

  // Load agent library for the palette + node fleet for the
  // inspector's multi-select picker. Errors are non-fatal — the
  // canvas still works without these (operator types in YAML mode).
  useEffect(() => {
    api.agentLibrary().then(setAgents).catch(() => setAgents([]));
    api.nodes().then(setAvailableNodes).catch(() => setAvailableNodes([]));
  }, []);

  // Switching to YAML mode regenerates the buffer from canvas state
  // so power users see a current snapshot, not stale.
  useEffect(() => {
    if (mode === 'yaml') {
      setYamlBuffer(serializeToYAML(meta, nodes, edges, passthroughRef.current));
    }
    // We deliberately don't sync YAML edits back to the canvas while
    // typing — that would require re-parsing on every keystroke and
    // fight with the operator. Instead, the YAML view has an
    // "Apply to canvas" button.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  const onConnect = useCallback(
    (c: Connection) => setEdges((es) => addEdge({
      ...c,
      type: 'smoothstep',
      markerEnd: { type: MarkerType.ArrowClosed, color: '#94a3b8' },
      style: { stroke: '#94a3b8', strokeWidth: 2 },
    }, es)),
    [setEdges],
  );

  function newStepID(): string {
    const existing = new Set(nodes.map((n) => n.id));
    let i = nodes.length + 1;
    while (existing.has(`step-${i}`)) i += 1;
    return `step-${i}`;
  }

  // Drag agent from palette → drop onto canvas → spawn a step node.
  // We use the standard HTML5 dataTransfer the agent card sets in
  // onDragStart, so no global state is needed.
  const onDragOver = useCallback((e: ReactDragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  }, []);

  const onDrop = useCallback(
    (e: ReactDragEvent<HTMLDivElement>) => {
      e.preventDefault();
      const agentName = e.dataTransfer.getData('text/agent-name');
      if (!agentName || !wrapperRef.current) return;
      const bounds = wrapperRef.current.getBoundingClientRect();
      const position = reactFlow.screenToFlowPosition({
        x: e.clientX - bounds.left,
        y: e.clientY - bounds.top,
      });
      const id = newStepID();
      const node: StepNode = {
        id,
        type: 'step',
        position,
        data: {
          agent: agentName,
          nodes: [],
          prompt: '',
          status: 'draft',
        },
      };
      setNodes((nds) => nds.concat(node));
      setSelectedID(id);
    },
    // newStepID closes over `nodes` which is stable enough — react-flow
    // calls this synchronously on drop, no stale-closure window.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [reactFlow, setNodes],
  );

  function updateNode(id: string, patch: Partial<StepNodeData>) {
    setNodes((nds) =>
      nds.map((n) => (n.id === id ? { ...n, data: { ...n.data, ...patch } } : n)),
    );
  }
  function renameNode(oldID: string, newID: string) {
    if (!newID || newID === oldID) return;
    if (nodes.some((n) => n.id === newID)) return; // duplicate
    setNodes((nds) => nds.map((n) => (n.id === oldID ? { ...n, id: newID } : n)));
    setEdges((es) =>
      es.map((e) => ({
        ...e,
        source: e.source === oldID ? newID : e.source,
        target: e.target === oldID ? newID : e.target,
      })),
    );
    setSelectedID(newID);
  }
  function deleteNode(id: string) {
    setNodes((nds) => nds.filter((n) => n.id !== id));
    setEdges((es) => es.filter((e) => e.source !== id && e.target !== id));
    setSelectedID(null);
  }

  async function handleSave() {
    let toSend: string;
    if (mode === 'yaml') {
      toSend = yamlBuffer;
    } else {
      toSend = serializeToYAML(meta, nodes, edges, passthroughRef.current);
    }
    await onSave(toSend);
  }

  function applyYamlToCanvas() {
    const parsed = parseSpecYAML(yamlBuffer);
    if (!parsed) return;
    setMeta(parsed.meta);
    setNodes(parsed.nodes);
    setEdges(parsed.edges);
    passthroughRef.current = parsed.passthrough ?? {};
    setMode('visual');
  }

  const selected = selectedID ? nodes.find((n) => n.id === selectedID) ?? null : null;

  return (
    <div className="flex flex-col h-full bg-bg">
      {/* Header — gradient backdrop + icon badge match the rest of
          the platform (Dashboard, Findings, Daimons headers all use
          this idiom). Inline-edited name + description sit on top of
          a discreet brand-50/60 wash so the chrome reads as part of
          the page, not as a floating overlay. */}
      <header className="px-5 py-3 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <div className="flex items-center gap-3">
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm shrink-0">
            <Workflow size={14} />
          </span>
          <div className="min-w-0 flex-1">
            <input
              type="text"
              placeholder="orchestration-name"
              value={meta.name}
              onChange={(e) => setMeta({ ...meta, name: e.target.value })}
              className="block w-full text-base font-semibold text-ink bg-transparent focus:outline-none focus:ring-2 focus:ring-brand-500/30 rounded px-1.5 py-0.5 -ml-1.5"
            />
            <input
              type="text"
              placeholder="One-line description shown in the library"
              value={meta.description}
              onChange={(e) => setMeta({ ...meta, description: e.target.value })}
              className="block w-full text-xs text-ink-dim bg-transparent focus:outline-none focus:ring-2 focus:ring-brand-500/30 rounded px-1.5 py-0.5 -ml-1.5"
            />
          </div>
          <div className="flex items-center rounded-md ring-1 ring-border bg-panel overflow-hidden shrink-0">
            <button
              onClick={() => setMode('visual')}
              className={cn(
                'px-3 py-1 text-xs font-medium',
                mode === 'visual' ? 'bg-brand-500 text-white shadow-sm' : 'text-ink-dim hover:text-ink',
              )}
            >
              Visual
            </button>
            <button
              onClick={() => setMode('yaml')}
              className={cn(
                'px-3 py-1 text-xs font-medium',
                mode === 'yaml' ? 'bg-brand-500 text-white shadow-sm' : 'text-ink-dim hover:text-ink',
              )}
            >
              YAML
            </button>
          </div>
          <button
            onClick={onCancel}
            className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-100 shrink-0"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={busy}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium shadow-sm shrink-0"
          >
            Save
          </button>
        </div>
        {saveError && (
          <div className="mt-2 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md whitespace-pre-wrap">
            {saveError}
          </div>
        )}
      </header>

      {mode === 'visual' ? (
        <div className="flex-1 flex overflow-hidden">
          {/* Agent palette */}
          <AgentPalette agents={agents} />

          {/* Canvas — explicit min-height and a stable width (flex-1)
              ensures react-flow can measure the container on first
              render. Without this, the canvas can flash blank on
              first client-side navigation until a resize fires. */}
          <div ref={wrapperRef} className="flex-1 relative min-h-[420px]" onDrop={onDrop} onDragOver={onDragOver}>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              onNodeClick={(_, n) => setSelectedID(n.id)}
              onPaneClick={() => setSelectedID(null)}
              nodeTypes={nodeTypes}
              fitView={nodes.length > 0}
              fitViewOptions={{ padding: 0.2 }}
              defaultEdgeOptions={{
                type: 'smoothstep',
                markerEnd: { type: MarkerType.ArrowClosed, color: '#94a3b8' },
                style: { stroke: '#94a3b8', strokeWidth: 2 },
              }}
              proOptions={{ hideAttribution: true }}
            >
              <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="#e9d5ff" />
              <Controls className="!bg-panel !border-border" />
              <MiniMap pannable zoomable className="!bg-panel !border-border" />
            </ReactFlow>

            {nodes.length === 0 && (
              <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                <div className="text-center text-ink-mute max-w-sm">
                  <p className="text-sm font-medium mb-1">Drag an agent from the left to start.</p>
                  <p className="text-xs">
                    Each step is one agent run. Connect steps by dragging from a node's right handle to another's left handle.
                  </p>
                </div>
              </div>
            )}
          </div>

          {/* Inspector */}
          {selected && (
            <Inspector
              node={selected}
              agents={agents}
              availableNodes={availableNodes}
              onChange={(p) => updateNode(selected.id, p)}
              onRename={(name) => renameNode(selected.id, name)}
              onDelete={() => deleteNode(selected.id)}
              onClose={() => setSelectedID(null)}
            />
          )}
        </div>
      ) : (
        <YAMLPane
          yaml={yamlBuffer}
          onChange={setYamlBuffer}
          onApply={applyYamlToCanvas}
        />
      )}
    </div>
  );
}

// ── Agent palette ───────────────────────────────────────────────────

function AgentPalette({ agents }: { agents: AgentLibraryItem[] }) {
  return (
    <aside className="w-56 shrink-0 border-r border-border bg-panel flex flex-col overflow-hidden">
      <div className="px-3 py-2.5 border-b border-border bg-gradient-to-b from-brand-50/40 to-transparent">
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-ink-mute font-medium">
          <span className="w-1 h-3 rounded-full bg-gradient-to-b from-brand-400 to-brand-600" />
          Agent library
        </div>
        <div className="text-[11px] text-ink-dim mt-0.5">Drag onto canvas →</div>
      </div>
      <div className="flex-1 overflow-auto p-2 space-y-1.5">
        {agents.length === 0 && (
          <p className="text-[11px] text-ink-mute text-center py-4">No agents in library.</p>
        )}
        {agents.map((a) => (
          <div
            key={a.name}
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData('text/agent-name', a.name);
              e.dataTransfer.effectAllowed = 'move';
            }}
            className="bg-panel border border-border rounded-md p-2 cursor-grab active:cursor-grabbing hover:border-brand-200 hover:shadow-card hover:bg-brand-50/20 transition-all"
            title={a.description || a.name}
          >
            <div className="flex items-center gap-1.5 mb-0.5">
              <Cpu size={10} className="text-brand-500/80 shrink-0" />
              <div className="text-xs font-medium text-ink truncate">{a.name}</div>
            </div>
            {a.description && (
              <div className="text-[10px] text-ink-mute line-clamp-2">{a.description}</div>
            )}
          </div>
        ))}
      </div>
    </aside>
  );
}

// ── Inspector ───────────────────────────────────────────────────────

interface InspectorProps {
  node: StepNode;
  agents: AgentLibraryItem[];
  availableNodes: NodeItem[];
  onChange: (patch: Partial<StepNodeData>) => void;
  onRename: (newID: string) => void;
  onDelete: () => void;
  onClose: () => void;
}

function Inspector({ node, agents, availableNodes, onChange, onRename, onDelete, onClose }: InspectorProps) {
  const [idDraft, setIdDraft] = useState(node.id);
  useEffect(() => setIdDraft(node.id), [node.id]);

  return (
    <aside className="w-80 shrink-0 border-l border-border bg-panel flex flex-col overflow-hidden">
      <div className="px-3 py-2.5 border-b border-border bg-gradient-to-b from-brand-50/40 to-transparent flex items-center justify-between">
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-ink-mute font-medium">
          <span className="w-1 h-3 rounded-full bg-gradient-to-b from-brand-400 to-brand-600" />
          Step inspector
        </div>
        <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded" title="Close inspector">
          <X size={14} />
        </button>
      </div>
      <div className="flex-1 overflow-auto p-3 space-y-3 text-xs">
        <Field label="Step id">
          <input
            type="text"
            value={idDraft}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setIdDraft(e.target.value)}
            onBlur={() => onRename(idDraft.trim())}
            className="w-full px-2 py-1.5 border border-border rounded-md font-mono focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Agent">
          <select
            value={node.data.agent}
            onChange={(e) => onChange({ agent: e.target.value })}
            className="w-full px-2 py-1.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          >
            <option value="">— select agent —</option>
            {agents.map((a) => (
              <option key={a.name} value={a.name}>{a.name}</option>
            ))}
          </select>
        </Field>
        <NodeMultiSelect
          selected={node.data.nodes ?? []}
          available={availableNodes}
          onChange={(nodes) => onChange({ nodes })}
        />
        <Field label="Prompt" hint="Templates allowed: {{trigger.x}}, {{step.findings | first.path}}">
          <textarea
            value={node.data.prompt}
            onChange={(e) => onChange({ prompt: e.target.value })}
            rows={8}
            className="w-full px-2 py-1.5 border border-border rounded-md font-mono text-[11px] focus:outline-none focus:ring-2 focus:ring-brand-500/30 resize-y"
          />
        </Field>
        <Field label="Timeout" hint="Optional, e.g. 5m, 30s">
          <input
            type="text"
            value={node.data.timeout ?? ''}
            onChange={(e) => onChange({ timeout: e.target.value })}
            placeholder="5m"
            className="w-full px-2 py-1.5 border border-border rounded-md font-mono focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </Field>
        <Field label="Dispatch runtime" hint="Auto = tunnel if connected, else jobs queue. Tunnel auto-starts on demand if needed.">
          <select
            value={node.data.dispatch ?? ''}
            onChange={(e) => onChange({ dispatch: (e.target.value || '') as StepNodeData['dispatch'] })}
            className="w-full px-2 py-1.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          >
            <option value="">Auto</option>
            <option value="tunnel">Tunnel — fail if not connected</option>
            <option value="jobs">Pull (jobs queue)</option>
          </select>
        </Field>
        <Field label="Approval gate">
          <label className="inline-flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={node.data.approval === 'required'}
              onChange={(e) => onChange({ approval: e.target.checked ? 'required' : '' })}
            />
            <span className="text-[11px]">Pause for operator approval</span>
          </label>
        </Field>
      </div>
      <div className="px-3 py-2.5 border-t border-border">
        <button
          onClick={onDelete}
          className="w-full inline-flex items-center justify-center gap-1.5 text-xs text-red-700 hover:text-red-800 hover:bg-red-50 px-2 py-1.5 rounded-md"
        >
          <Trash2 size={11} />
          Delete step
        </button>
      </div>
    </aside>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
      {hint && <p className="text-[10px] text-ink-mute mt-0.5">{hint}</p>}
    </div>
  );
}

// NodeMultiSelect — searchable picker over the connected fleet.
// Selecting one node serializes to `node:` (single-host); selecting
// many serializes to `nodes:` (parallel fan-out). Operators can also
// type a templated value (e.g. `{{trigger.host}}`) since runtime
// resolution happens engine-side.
function NodeMultiSelect({
  selected,
  available,
  onChange,
}: {
  selected: string[];
  available: NodeItem[];
  onChange: (next: string[]) => void;
}) {
  const [search, setSearch] = useState('');
  const [open, setOpen] = useState(false);
  const [templateInput, setTemplateInput] = useState('');

  const matches = useMemo(() => {
    if (!available.length) return [];
    const q = search.trim().toLowerCase();
    const candidates = available.filter((n) => {
      if (n.status !== 'ready') return false;
      if (selected.includes(n.name)) return false;
      if (!q) return true;
      return n.name.toLowerCase().includes(q) ||
             n.hostname?.toLowerCase().includes(q) ||
             n.daemon_hostname?.toLowerCase().includes(q);
    });
    return candidates.slice(0, 50);
  }, [available, search, selected]);

  function add(name: string) {
    if (name && !selected.includes(name)) {
      onChange([...selected, name]);
    }
    setSearch('');
  }
  function remove(name: string) {
    onChange(selected.filter((n) => n !== name));
  }
  function addTemplate() {
    const v = templateInput.trim();
    if (v) add(v);
    setTemplateInput('');
  }

  return (
    <Field
      label={selected.length > 1 ? `Nodes (${selected.length}, fan-out)` : 'Node target'}
      hint={selected.length > 1
        ? 'Step runs on every selected node in parallel. Findings + outputs are merged.'
        : 'Pick one node, or pick multiple to fan-out the step. Templates allowed.'}
    >
      {/* Selected pills */}
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1 mb-1.5">
          {selected.map((n) => (
            <span
              key={n}
              className="inline-flex items-center gap-1 bg-brand-50 text-brand-700 ring-1 ring-brand-200 rounded px-1.5 py-0.5 text-[11px] font-mono"
            >
              {n}
              <button
                onClick={() => remove(n)}
                className="text-brand-700 hover:text-brand-900 -mr-0.5"
                title="Remove"
                type="button"
              >
                <X size={10} />
              </button>
            </span>
          ))}
        </div>
      )}

      {/* Search-and-pick from connected fleet */}
      <div className="relative">
        <input
          type="text"
          value={search}
          onChange={(e) => { setSearch(e.target.value); setOpen(true); }}
          onFocus={() => setOpen(true)}
          onBlur={() => setTimeout(() => setOpen(false), 150)}
          placeholder={available.length > 0 ? 'Search the fleet…' : 'No connected nodes (use the template field below)'}
          className="w-full px-2 py-1.5 border border-border rounded-md font-mono text-[11px] focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
        {open && matches.length > 0 && (
          <div className="absolute z-10 mt-1 w-full max-h-48 overflow-auto bg-panel border border-border rounded-md shadow-card">
            {matches.map((n) => (
              <button
                key={n.id}
                type="button"
                onClick={() => add(n.name)}
                className="w-full text-left px-2 py-1.5 hover:bg-brand-50 text-[11px] flex items-center justify-between gap-2"
              >
                <span className="font-mono truncate">{n.name}</span>
                {n.daemon_hostname && (
                  <span className="text-ink-mute truncate">{n.daemon_hostname}</span>
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Templated entry — for `{{trigger.host}}` patterns the
          dropdown can't enumerate. */}
      <div className="mt-1.5 flex items-center gap-1">
        <input
          type="text"
          value={templateInput}
          onChange={(e) => setTemplateInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTemplate(); } }}
          placeholder="Or paste a template — {{trigger.host}}"
          className="flex-1 px-2 py-1 border border-dashed border-border rounded-md font-mono text-[11px] focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
        <button
          type="button"
          onClick={addTemplate}
          className="text-[11px] px-2 py-1 bg-brand-50 text-brand-700 ring-1 ring-brand-200 rounded-md hover:bg-brand-100"
        >
          Add
        </button>
      </div>
    </Field>
  );
}

// ── YAML pane ───────────────────────────────────────────────────────

function YAMLPane({
  yaml,
  onChange,
  onApply,
}: {
  yaml: string;
  onChange: (v: string) => void;
  onApply: () => void;
}) {
  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <div className="px-3 py-2 border-b border-border bg-panel flex items-center justify-between">
        <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium">YAML</div>
        <button
          onClick={onApply}
          className="text-xs px-2 py-1 bg-brand-50 text-brand-700 hover:bg-brand-100 ring-1 ring-brand-200 rounded-md"
        >
          Apply to canvas
        </button>
      </div>
      <textarea
        value={yaml}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        className="flex-1 w-full bg-panel font-mono text-xs px-3 py-2 focus:outline-none border-0 resize-none"
      />
    </div>
  );
}
