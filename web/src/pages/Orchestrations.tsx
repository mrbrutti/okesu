// Orchestrations page — chained agent runs.
//
// Two tabs:
//   Library — orchestration specs operators have authored. Click to
//             open the editor; pulse "Run" kicks off a manual run.
//   Runs    — execution history with status pills + step timeline.
//
// Phase A: linear-only. The run-detail panel renders steps as a
// vertical timeline (DAG-shaped layout comes in Phase C). All status
// colors come from the platform's existing tokens — STATUS_STYLES for
// finding-style states (open/acknowledged/etc.) is reused conceptually
// via the same brand-* / sev-* / ink-* palette.

import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import {
  CheckCircle2,
  ChevronRight,
  Clock,
  Filter,
  GitBranch,
  LayoutList,
  LayoutGrid,
  Loader2,
  Pause,
  Play,
  Plus,
  RotateCcw,
  Square,
  Trash2,
  Workflow,
  X,
  XCircle,
} from 'lucide-react';
import {
  api,
  ApiError,
  type FederationPeer,
  type Orchestration,
  type OrchestrationRunView,
  type OrchestrationRunStatus,
  type OrchestrationRunsFilter,
  type OrchestrationStepView,
} from '../api';
import { cn } from '../lib/cn';
import { lazy, Suspense } from 'react';
import { CPSourceChip } from '../components/CPSourceChip';
import { TabBar, TabButton } from '../components/TabBar';
import { SectionHeader, type SectionTone } from '../components/lists/SectionHeader';
import { ListCard } from '../components/lists/ListCard';
import { parseSpecYAMLLite } from '../lib/orchestrationSpec';
import { HarnessOutput } from '../components/HarnessOutput';
import { StructuredView } from '../components/StructuredView';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';
import { useSelection } from '../lib/useSelection';
import { BulkActionBar, BulkActionButton } from '../components/BulkActionBar';

// Lazy-load the canvases — react-flow + js-yaml together add ~120KB
// gzipped. Operators viewing the Library/Runs lists shouldn't pay
// that until they open an editor or a run-detail panel.
const OrchestrationEditorCanvas = lazy(() => import('../components/OrchestrationEditorCanvas'));
const OrchestrationRunCanvas = lazy(() => import('../components/OrchestrationRunCanvas'));

type Tab = 'library' | 'runs';

// New-orchestration template — kept lean so an operator who hits
// Save without editing gets a valid spec rather than dispatch errors.
// The first step is intentionally local-only (no node target) so a
// manual run on a fresh CP doesn't fail with "node not connected"
// before the operator has wired anything to a real host.
const TEMPLATE = `---
name: my-orchestration
description: One-line description of what this chain does.

inputs:
  host:
    type: string
    required: false
    default: ""

steps:
  - id: triage
    agent: investigator
    # Pick a node from the inspector (right pane) — single host or
    # multi-host fan-out. Templates like {{trigger.host}} also work
    # when this orchestration is finding-triggered.
    prompt: |
      Investigate the most recent activity. If a host was supplied:
      {{trigger.host}}.

  - id: respond
    approval: required
    agent: incident-responder
    prompt: |
      Suggest next-step actions based on:
      {{triage.output | tail(50)}}
---
# Notes (optional documentation; ignored by the engine).
`;

// Wrap the page in a top-level Suspense boundary. The Editor and
// RunDetail panes use React.lazy() for the canvases — without this
// outer boundary, a lazy chunk that's still loading on first
// client-side navigation can leave the page blank until the operator
// hard-refreshes (the inner per-pane Suspense isn't always reached on
// the first render frame). The fallback is the same loader the rest
// of the platform uses.
export default function OrchestrationsPage() {
  return (
    <Suspense fallback={<PageLoading />}>
      <OrchestrationsPageInner />
    </Suspense>
  );
}

function PageLoading() {
  return (
    <div className="h-full flex items-center justify-center text-ink-mute text-sm gap-2">
      Loading orchestrations…
    </div>
  );
}

function OrchestrationsPageInner() {
  const [params, setParams] = useSearchParams();
  const tab = (params.get('tab') === 'runs' ? 'runs' : 'library') as Tab;
  const setTab = (t: Tab) => {
    const p = new URLSearchParams(params);
    p.set('tab', t);
    setParams(p, { replace: true });
  };
  const runIDParam = params.get('run');
  const editIDParam = params.get('edit');
  const cpParam = params.get('cp') || undefined;

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
            <Workflow size={14} />
          </span>
          Orchestrations
        </h1>
        <p className="text-xs text-ink-dim mt-0.5 ml-9">
          Chain agents together — each step's output feeds the next. Drag-and-drop board for status changes; gated steps pause for operator approval.
        </p>
      </header>

      <TabBar>
        <TabButton active={tab === 'library'} onClick={() => setTab('library')} icon={GitBranch} label="Library" />
        <TabButton active={tab === 'runs'} onClick={() => setTab('runs')} icon={Clock} label="Runs" />
      </TabBar>

      <div className="flex-1 overflow-hidden">
        {tab === 'library' ? (
          <Library
            editID={editIDParam ? Number(editIDParam) : null}
            editCP={cpParam}
            onCloseEditor={() => {
              const p = new URLSearchParams(params);
              p.delete('edit');
              p.delete('cp');
              setParams(p, { replace: true });
            }}
            onOpenEditor={(id, cp) => {
              const p = new URLSearchParams(params);
              if (id === null) {
                p.delete('edit');
                p.delete('cp');
              } else {
                p.set('edit', String(id));
                if (cp) p.set('cp', cp);
                else p.delete('cp');
              }
              setParams(p, { replace: true });
            }}
          />
        ) : (
          <Runs
            selectedRunID={runIDParam ? Number(runIDParam) : null}
            selectedCP={cpParam}
            onSelectRun={(id, cp) => {
              const p = new URLSearchParams(params);
              if (id === null) {
                p.delete('run');
                p.delete('cp');
              } else {
                p.set('run', String(id));
                if (cp) p.set('cp', cp);
                else p.delete('cp');
              }
              setParams(p, { replace: true });
            }}
          />
        )}
      </div>
    </div>
  );
}

// ── Library tab ─────────────────────────────────────────────────────

function Library({
  editID,
  editCP,
  onOpenEditor,
  onCloseEditor,
}: {
  editID: number | null;
  editCP?: string;
  onOpenEditor: (id: number | null, cp?: string) => void; // null = create new
  onCloseEditor: () => void;
}) {
  const [list, setList] = useState<Orchestration[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  const refresh = () => {
    api.orchestrations()
      .then((rows) => { setList(rows); setError(null); })
      .catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  // editID semantics: null = closed, -1 = new, N>=0 = editing existing
  const editorOpen = editID !== null;
  const isCreate = editID === -1;

  async function runOrchestration(o: Orchestration) {
    try {
      const { run_id } = await api.orchestrationRun(o.id, {}, o.cp_source?.instance_id);
      const cpParam = o.cp_source ? `&cp=${encodeURIComponent(o.cp_source.instance_id)}` : '';
      navigate(`/orchestrations?tab=runs&run=${run_id}${cpParam}`);
    } catch (e) {
      setError(String(e));
    }
  }

  // Group by trigger kind so the operator sees auto-fired
  // orchestrations separately from manual ones — same visual rhythm
  // Daimons / Agents / Findings use, just bucketed differently.
  const groups = useMemo(() => {
    const out: Record<'finding' | 'cron' | 'manual', Orchestration[]> = {
      finding: [], cron: [], manual: [],
    };
    for (const o of list ?? []) {
      const k = o.trigger_kind === 'finding' ? 'finding'
              : o.trigger_kind === 'cron' ? 'cron'
              : 'manual';
      out[k].push(o);
    }
    return out;
  }, [list]);

  return (
    <div className="h-full flex">
      <div className="flex-1 overflow-auto p-6 space-y-5">
        <div className="flex items-center justify-between">
          <p className="text-xs text-ink-dim">
            {list ? `${list.length} orchestration${list.length === 1 ? '' : 's'}` : 'Loading…'}
          </p>
          <button
            onClick={() => onOpenEditor(-1, undefined)}
            className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-xs font-medium px-3 py-1.5 rounded-md shadow-sm"
          >
            <Plus size={12} />
            New orchestration
          </button>
        </div>

        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {list && list.length === 0 && (
          <div className="text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <Workflow size={32} className="mx-auto mb-2 opacity-40" />
            <p className="text-sm font-medium text-ink mb-1">No orchestrations yet.</p>
            <p className="text-xs max-w-md mx-auto">
              Click <strong>New orchestration</strong> to author your first chain.
              See <code className="bg-slate-100 px-1 rounded">docs/orchestrations.md</code> for the YAML language reference.
            </p>
          </div>
        )}

        {list && list.length > 0 && (
          <>
            {(['finding', 'cron', 'manual'] as const).map((kind) => {
              const items = groups[kind];
              if (items.length === 0) return null;
              return (
                <section key={kind}>
                  <SectionHeader
                    tone={LIBRARY_BUCKET_TONE[kind]}
                    label={LIBRARY_BUCKET_LABEL[kind]}
                    count={items.length}
                    hint={LIBRARY_BUCKET_HINT[kind]}
                  />
                  <ListCard>
                    {items.map((o) => (
                      <LibraryRow
                        key={`${o.cp_source?.instance_id ?? 'local'}-${o.id}`}
                        o={o}
                        onRun={() => runOrchestration(o)}
                        onEdit={() => onOpenEditor(o.id, o.cp_source?.instance_id)}
                      />
                    ))}
                  </ListCard>
                </section>
              );
            })}
          </>
        )}
      </div>

      {editorOpen && (
        <Editor
          id={isCreate ? null : editID!}
          cpInstanceID={editCP}
          onClose={() => { onCloseEditor(); refresh(); }}
        />
      )}
    </div>
  );
}

// ── Library bucket tokens + row ─────────────────────────────────────

const LIBRARY_BUCKET_TONE: Record<'finding' | 'cron' | 'manual', SectionTone> = {
  finding: 'progress',  // brand purple — auto-fired on signal
  cron:    'progress',  // brand purple — auto-fired on schedule
  manual:  'muted',     // slate — operator-driven
};

const LIBRARY_BUCKET_LABEL: Record<'finding' | 'cron' | 'manual', string> = {
  finding: 'Auto-trigger · finding',
  cron:    'Auto-trigger · cron',
  manual:  'Manual',
};

const LIBRARY_BUCKET_HINT: Record<'finding' | 'cron' | 'manual', string | undefined> = {
  finding: 'fires on matching findings',
  cron:    'fires on schedule',
  manual:  'operator triggers via Run button',
};

function LibraryRow({
  o,
  onRun,
  onEdit,
}: {
  o: Orchestration;
  onRun: () => void;
  onEdit: () => void;
}) {
  return (
    <div className="px-4 py-3 flex items-center gap-3 hover:bg-slate-50/40 cursor-pointer" onClick={onEdit}>
      <div className="w-1 h-9 rounded-full bg-gradient-to-b from-brand-400 to-brand-600 shrink-0" />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium text-ink truncate">{o.name}</span>
          <CPSourceChip source={o.cp_source} />
          {!o.enabled && (
            <span className="text-[10px] uppercase tracking-wide text-slate-700 bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
              disabled
            </span>
          )}
        </div>
        {o.description && (
          <p className="text-[11px] text-ink-dim mt-0.5 truncate">{o.description}</p>
        )}
        <div className="flex items-center gap-3 mt-1 text-[11px] text-ink-mute">
          {o.trigger_kind === 'finding' && o.trigger_filter && (
            <span className="inline-flex items-center gap-1 truncate max-w-[28rem]" title={o.trigger_filter}>
              <Filter size={10} />
              <code className="font-mono truncate">{o.trigger_filter}</code>
            </span>
          )}
          {o.trigger_kind === 'cron' && o.trigger_cron && (
            <span className="inline-flex items-center gap-1">
              <Clock size={10} />
              <code className="font-mono">{o.trigger_cron}</code>
            </span>
          )}
        </div>
      </div>
      <button
        onClick={(e) => { e.stopPropagation(); onRun(); }}
        className="inline-flex items-center gap-1 text-xs text-brand-700 bg-brand-50 hover:bg-brand-100 ring-1 ring-brand-200 px-2 py-1 rounded-md font-medium"
        title="Run this orchestration now"
      >
        <Play size={11} />
        Run
      </button>
      <ChevronRight size={14} className="text-ink-mute shrink-0" />
    </div>
  );
}

function Editor({
  id,
  cpInstanceID,
  onClose,
}: {
  id: number | null;
  cpInstanceID?: string;
  onClose: () => void;
}) {
  const isCreate = id === null;
  const [content, setContent] = useState<string>(isCreate ? TEMPLATE : '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [meta, setMeta] = useState<Orchestration | null>(null);
  // Phase B: Global-CP create gets a target picker. We fetch peers
  // lazily on dialog open. Loaded with `null` until known; empty
  // array means "this CP isn't a parent" → no picker, single-CP UX.
  const [peers, setPeers] = useState<FederationPeer[] | null>(null);
  const [targetCP, setTargetCP] = useState<string>(''); // "" = local

  useEffect(() => {
    if (isCreate) return;
    api.orchestration(id!, cpInstanceID)
      .then((o) => { setContent(o.spec_yaml); setMeta(o); })
      .catch((e) => setError(String(e)));
  }, [id, isCreate, cpInstanceID]);

  useEffect(() => {
    if (!isCreate) return;
    api.federationPeers()
      .then(setPeers)
      .catch(() => setPeers([]));
  }, [isCreate]);

  async function save(yamlFromCanvas: string) {
    setBusy(true); setError(null);
    try {
      if (isCreate) {
        await api.orchestrationCreate(yamlFromCanvas, targetCP || undefined);
      } else {
        await api.orchestrationUpdate(id!, yamlFromCanvas, cpInstanceID);
      }
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function del() {
    if (isCreate) return;
    if (!confirm('Delete this orchestration? In-flight runs continue but new runs cannot start.')) return;
    setBusy(true); setError(null);
    try {
      await api.orchestrationDelete(id!, cpInstanceID);
      onClose();
    } catch (e) {
      setError(String(e));
      setBusy(false);
    }
  }

  // Full-screen overlay — the visual authoring canvas needs the
  // whole viewport (palette + canvas + inspector). Editor lives over
  // a backdrop so an operator can dismiss with Esc / click-outside.
  return (
    <div className="fixed inset-0 z-40 flex flex-col bg-bg">
      {/* Optional CP picker banner — only on create when peers exist */}
      {isCreate && peers && peers.length > 0 && (
        <div className="px-4 py-2 border-b border-brand-200 bg-gradient-to-r from-brand-50 to-panel flex items-center gap-3">
          <label className="text-[10px] uppercase tracking-wide text-ink-mute font-medium shrink-0">
            Save to
          </label>
          <select
            value={targetCP}
            onChange={(e) => setTargetCP(e.target.value)}
            className="text-xs px-2 py-1 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-panel"
          >
            <option value="">This CP (Global)</option>
            {peers.filter((p) => p.healthy).map((p) => (
              <option key={p.id} value={p.introspect?.instance_id ?? ''}>
                {p.display_name || p.introspect?.display_name || p.url}
                {p.introspect?.region ? ` · ${p.introspect.region}` : ''}
              </option>
            ))}
          </select>
          <span className="text-[11px] text-ink-mute">
            Runs execute on the CP that hosts the orchestration.
          </span>
          {!isCreate && (
            <button
              onClick={del}
              disabled={busy}
              className="ml-auto inline-flex items-center gap-1 text-xs text-red-700 hover:text-red-800 hover:bg-red-50 px-2 py-1 rounded-md"
            >
              <Trash2 size={11} />
              Delete
            </button>
          )}
        </div>
      )}

      {/* Visual authoring canvas — handles its own header (name +
          description + mode toggle + save/cancel) and palette +
          inspector. The CP picker banner above is the only thing
          this wrapper adds.

          Important: don't mount the canvas until `content` is loaded
          on edit. The canvas captures its initial parsed state via
          useNodesState/useEdgesState which only read the seed once;
          mounting with an empty string and *then* updating
          initialYAML produces a permanently empty canvas. */}
      <div className="flex-1 overflow-hidden">
        {!isCreate && content === '' ? (
          <EditorLoading />
        ) : (
          <Suspense fallback={<EditorLoading />}>
            <OrchestrationEditorCanvas
              initialYAML={isCreate ? TEMPLATE : content}
              onSave={(yaml) => { setContent(yaml); return save(yaml); }}
              onCancel={onClose}
              busy={busy}
              saveError={error}
            />
          </Suspense>
        )}
      </div>

      {/* Delete button is moved to the CP banner on edits; on a
          single-CP setup with no banner, expose it as a footer. */}
      {!isCreate && (!peers || peers.length === 0) && (
        <footer className="px-5 py-2.5 border-t border-border flex items-center justify-between">
          <button
            onClick={del}
            disabled={busy}
            className="inline-flex items-center gap-1 text-xs text-red-700 hover:text-red-800 hover:bg-red-50 px-2 py-1 rounded-md"
          >
            <Trash2 size={11} />
            Delete orchestration
          </button>
          <span className="text-[11px] text-ink-mute">{meta?.cp_source?.display_name ?? 'local'}</span>
        </footer>
      )}
    </div>
  );
}

function EditorLoading() {
  return (
    <div className="h-full flex items-center justify-center text-ink-mute text-xs gap-2">
      <Loader2 size={14} className="animate-spin" />
      Loading editor…
    </div>
  );
}

// ── Runs tab ────────────────────────────────────────────────────────

// Status pill identities. "Active" is a virtual tab grouping
// pending+running+approval_required so the operator's default view
// is the smallest bucket they actually need to act on.
type RunsStatusPill = 'all' | 'active' | 'approval' | 'failed' | 'completed' | 'cancelled';

const PILL_LABEL: Record<RunsStatusPill, string> = {
  all:        'All',
  active:     'Active',
  approval:   'Approval',
  failed:     'Failed',
  completed:  'Completed',
  cancelled:  'Cancelled',
};

const PILL_TONE: Record<RunsStatusPill, string> = {
  all:        'text-ink bg-slate-100 ring-slate-200',
  active:     'text-blue-700 bg-blue-50 ring-blue-200',
  approval:   'text-amber-700 bg-amber-50 ring-amber-200',
  failed:     'text-red-700 bg-red-50 ring-red-200',
  completed:  'text-green-700 bg-green-50 ring-green-200',
  cancelled:  'text-ink-dim bg-slate-100 ring-slate-200',
};

const PILLS_ORDER: RunsStatusPill[] = ['active', 'approval', 'failed', 'completed', 'cancelled', 'all'];

// pillToServerStatuses maps a UI pill to the OrchestrationRunStatus
// values it should request server-side. The "active" bucket folds
// pending+running together; "all" omits the filter.
function pillToServerStatuses(pill: RunsStatusPill): OrchestrationRunStatus[] | undefined {
  switch (pill) {
    case 'active':    return ['pending', 'running'];
    case 'approval':  return ['approval_required'];
    case 'failed':    return ['failed'];
    case 'completed': return ['completed'];
    case 'cancelled': return ['cancelled'];
    case 'all':       return undefined;
  }
}

// Sum the counts the server returned into UI-pill totals. Server
// counts are by raw status; the active pill sums pending+running.
function sumPillCount(pill: RunsStatusPill, counts: Partial<Record<OrchestrationRunStatus, number>>): number {
  const get = (s: OrchestrationRunStatus) => counts[s] ?? 0;
  switch (pill) {
    case 'active':    return get('pending') + get('running');
    case 'approval':  return get('approval_required');
    case 'failed':    return get('failed');
    case 'completed': return get('completed');
    case 'cancelled': return get('cancelled');
    case 'all':
      // Sum every recognized status. Don't over-count by also
      // adding "active" — the server returned each row exactly once.
      return Object.values(counts).reduce((a, b) => a + (b ?? 0), 0);
  }
}

const PAGE_SIZE = 50;
const RANGES: { value: string; label: string }[] = [
  { value: '30m',  label: '30m' },
  { value: '1h',   label: '1h' },
  { value: '24h',  label: '24h' },
  { value: '7d',   label: '7d' },
  { value: '',     label: 'all time' },
];

function Runs({
  selectedRunID,
  selectedCP,
  onSelectRun,
}: {
  selectedRunID: number | null;
  selectedCP?: string;
  onSelectRun: (id: number | null, cp?: string) => void;
}) {
  // Run-detail view takes over the whole pane.
  if (selectedRunID !== null) {
    return (
      <RunDetailWithRefresh
        runID={selectedRunID}
        cpInstanceID={selectedCP}
        onClose={() => onSelectRun(null)}
      />
    );
  }
  return <RunsList onSelectRun={onSelectRun} />;
}

// RunDetailWithRefresh is a wrapper that holds the refresh function
// shared between the detail view and any operator action that needs
// to invalidate the list (cancel, approve). Kept here so the Runs
// component above stays purely a router.
function RunDetailWithRefresh({
  runID,
  cpInstanceID,
  onClose,
}: {
  runID: number;
  cpInstanceID?: string;
  onClose: () => void;
}) {
  // The list refresh is controlled inside RunsList; this view just
  // needs to bounce its own internal reload via the existing prop.
  return (
    <RunDetail
      runID={runID}
      cpInstanceID={cpInstanceID}
      onClose={onClose}
      onChanged={() => { /* list re-fetches on tab return */ }}
    />
  );
}

type RunsView = 'list' | 'kanban';

// Server-side statuses worth surfacing as kanban columns. Rendered
// in operator-priority order: things needing eyes first, then the
// stable buckets, then the noise tail.
const KANBAN_COLUMNS: { status: OrchestrationRunStatus; label: string; tone: string }[] = [
  { status: 'approval_required', label: 'Approval',  tone: 'bg-amber-50 ring-amber-200 text-amber-700' },
  { status: 'running',            label: 'Running',   tone: 'bg-blue-50 ring-blue-200 text-blue-700' },
  { status: 'pending',            label: 'Pending',   tone: 'bg-slate-50 ring-slate-200 text-slate-700' },
  { status: 'failed',             label: 'Failed',    tone: 'bg-red-50 ring-red-200 text-red-700' },
  { status: 'completed',          label: 'Completed', tone: 'bg-green-50 ring-green-200 text-green-700' },
  { status: 'cancelled',          label: 'Cancelled', tone: 'bg-slate-100 ring-slate-200 text-slate-600' },
];

function RunsList({
  onSelectRun,
}: {
  onSelectRun: (id: number | null, cp?: string) => void;
}) {
  const [params, setParams] = useSearchParams();

  const pill = (params.get('status') as RunsStatusPill) || 'active';
  const triggerKindCsv = params.get('trigger_kind') || '';
  const sinceParam = params.get('since') ?? '24h';
  const search = params.get('q') || '';
  const orchID = params.get('orch') || '';
  const view: RunsView = (params.get('view') as RunsView) === 'kanban' ? 'kanban' : 'list';
  // Grouping collapses runs with the same orchestration_id + payload
  // dedup key into one card. Especially useful when one finding fires
  // 30 identical autotriages across the fleet.
  const grouped = params.get('group') === 'trigger';

  const [orchestrations, setOrchestrations] = useState<Orchestration[]>([]);
  const [counts, setCounts] = useState<Partial<Record<OrchestrationRunStatus, number>>>({});
  const [rows, setRows] = useState<OrchestrationRunView[] | null>(null);
  const [hasMore, setHasMore] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Selection — keys encode CP scope so the bulk handler can route
  // each id to the right CP without an extra lookup.
  const sel = useSelection<string>();

  // Orchestration list — populates the picker dropdown. Cheap;
  // already fetched on the Library tab too but the cache between
  // tabs is per-component.
  useEffect(() => {
    api.orchestrations().then(setOrchestrations).catch(() => { /* ignore */ });
  }, []);

  const filter: OrchestrationRunsFilter = useMemo(() => ({
    status:           pillToServerStatuses(pill),
    trigger_kind:     triggerKindCsv ? triggerKindCsv.split(',') as ('manual' | 'finding' | 'cron')[] : undefined,
    orchestration_id: orchID ? [Number(orchID)] : undefined,
    since:            sinceParam || undefined,
    q:                search || undefined,
  }), [pill, triggerKindCsv, sinceParam, search, orchID]);

  // Fetch first page + counts whenever any filter changes.
  useEffect(() => {
    setRows(null);
    setHasMore(true);
    api.orchestrationRunsFiltered({ ...filter, limit: PAGE_SIZE, offset: 0 }, { withCounts: true })
      .then((res) => {
        setRows(res.rows);
        setCounts(res.counts_by_status);
        setHasMore(res.rows.length >= PAGE_SIZE);
        setError(null);
      })
      .catch((e) => setError(String(e)));
  }, [filter]);

  // Auto-refresh on a slow tick so the list reflects newly-arrived
  // runs without aggressive polling. Active/Approval/Failed buckets
  // bump every 5s (operator likely watching); All/Completed every 30s.
  useEffect(() => {
    const fastBucket = pill === 'active' || pill === 'approval' || pill === 'failed';
    const interval = fastBucket ? 5_000 : 30_000;
    const t = setInterval(() => {
      api.orchestrationRunsFiltered({ ...filter, limit: PAGE_SIZE, offset: 0 }, { withCounts: true })
        .then((res) => {
          setRows(res.rows);
          setCounts(res.counts_by_status);
          setHasMore(res.rows.length >= PAGE_SIZE);
        })
        .catch(() => { /* ignore — keep stale data on transient errors */ });
    }, interval);
    return () => clearInterval(t);
  }, [filter, pill]);

  // Infinite scroll — the existing useInfiniteScroll hook calls a
  // load function when the sentinel scrolls into view; we issue
  // limit/offset paginated fetches and concat onto rows.
  const scroll = useInfiniteScroll({
    hasMore: !!rows && hasMore,
    loadMore: async () => {
      if (!rows) return;
      const next = await api.orchestrationRunsFiltered(
        { ...filter, limit: PAGE_SIZE, offset: rows.length },
      );
      if (next.rows.length === 0) {
        setHasMore(false);
        return;
      }
      setRows((prev) => [...(prev ?? []), ...next.rows]);
      if (next.rows.length < PAGE_SIZE) setHasMore(false);
    },
  });

  function setParam(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value === '') next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  }

  return (
    <div className="h-full flex flex-col overflow-hidden">
      {/* Status pill row */}
      <div className="px-6 pt-4 pb-2 border-b border-border bg-panel flex items-center gap-2 flex-wrap">
        {PILLS_ORDER.map((p) => {
          const active = p === pill;
          const count = sumPillCount(p, counts);
          return (
            <button
              key={p}
              onClick={() => setParam('status', p === 'active' ? '' : p)}
              className={cn(
                'inline-flex items-center gap-1.5 text-xs font-medium px-2.5 py-1 rounded-md ring-1',
                active ? PILL_TONE[p] + ' ring-2' : 'text-ink-dim bg-white ring-border hover:bg-slate-50',
              )}
            >
              {PILL_LABEL[p]}
              <span className={cn(
                'text-[10px] tabular-nums px-1 rounded',
                active ? 'bg-white/40' : 'bg-slate-100',
              )}>
                {count}
              </span>
            </button>
          );
        })}
      </div>

      {/* Filter row */}
      <div className="px-6 py-2 border-b border-border bg-panel flex items-center gap-2 flex-wrap text-xs">
        <input
          value={search}
          onChange={(e) => setParam('q', e.target.value)}
          placeholder="Search run id, host, finding id…"
          className="px-2 py-1 border border-border rounded-md bg-white w-64"
        />
        <select
          value={orchID}
          onChange={(e) => setParam('orch', e.target.value)}
          className="px-2 py-1 border border-border rounded-md bg-white"
        >
          <option value="">All orchestrations</option>
          {orchestrations.map((o) => (
            <option key={o.id} value={o.id}>{o.name}</option>
          ))}
        </select>
        <select
          value={triggerKindCsv}
          onChange={(e) => setParam('trigger_kind', e.target.value)}
          className="px-2 py-1 border border-border rounded-md bg-white"
        >
          <option value="">Any trigger</option>
          <option value="manual">Manual</option>
          <option value="finding">Finding</option>
          <option value="cron">Cron</option>
        </select>
        <select
          value={sinceParam}
          onChange={(e) => setParam('since', e.target.value)}
          className="px-2 py-1 border border-border rounded-md bg-white"
        >
          {RANGES.map((r) => (
            <option key={r.value} value={r.value}>{r.label}</option>
          ))}
        </select>
        <label className="inline-flex items-center gap-1 text-ink-dim cursor-pointer ml-1">
          <input
            type="checkbox"
            checked={grouped}
            onChange={(e) => setParam('group', e.target.checked ? 'trigger' : '')}
          />
          Group identical triggers
        </label>
        {(search || orchID || triggerKindCsv || pill !== 'active' || sinceParam !== '24h' || grouped) && (
          <button
            onClick={() => {
              const next = new URLSearchParams();
              if (view === 'kanban') next.set('view', 'kanban');
              setParams(next, { replace: true });
            }}
            className="text-ink-dim hover:text-ink underline"
          >
            clear filters
          </button>
        )}
        {/* View toggle pinned right */}
        <div className="ml-auto inline-flex rounded-md ring-1 ring-border overflow-hidden">
          <button
            onClick={() => setParam('view', '')}
            className={cn(
              'inline-flex items-center gap-1 px-2 py-1 text-xs',
              view === 'list' ? 'bg-brand-50 text-brand-700' : 'bg-white text-ink-dim hover:bg-slate-50',
            )}
            title="List view"
          >
            <LayoutList size={12} /> List
          </button>
          <button
            onClick={() => setParam('view', 'kanban')}
            className={cn(
              'inline-flex items-center gap-1 px-2 py-1 text-xs border-l border-border',
              view === 'kanban' ? 'bg-brand-50 text-brand-700' : 'bg-white text-ink-dim hover:bg-slate-50',
            )}
            title="Kanban view (columns by status)"
          >
            <LayoutGrid size={12} /> Kanban
          </button>
        </div>
      </div>

      {/* Body — list OR kanban — share the rows fetched above */}
      <div className="flex-1 overflow-auto">
        {error && (
          <div className="mx-6 mt-4 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}
        {!rows && (
          <div className="px-6 py-6 text-xs text-ink-mute">Loading…</div>
        )}
        {rows && rows.length === 0 && (
          <div className="m-6 text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <Clock size={32} className="mx-auto mb-2 opacity-40" />
            <p className="text-sm font-medium text-ink mb-1">No runs match.</p>
            <p className="text-xs">
              Try widening the time range, switching to the All tab, or clearing filters.
            </p>
          </div>
        )}
        {rows && rows.length > 0 && view === 'list' && (
          <RunsListBody
            rows={grouped ? collapseGroups(rows) : rows.map((r) => ({ kind: 'single', run: r }))}
            sel={sel}
            onSelectRun={onSelectRun}
            scrollSentinelRef={scroll.sentinelRef}
            scrollLoading={scroll.loading}
            hasMore={hasMore}
          />
        )}
        {rows && rows.length > 0 && view === 'kanban' && (
          <RunsKanbanBody
            rows={grouped ? collapseGroups(rows) : rows.map((r) => ({ kind: 'single', run: r }))}
            sel={sel}
            onSelectRun={onSelectRun}
            scrollSentinelRef={scroll.sentinelRef}
            scrollLoading={scroll.loading}
            hasMore={hasMore}
          />
        )}
      </div>

      {/* Bulk action bar — only when something is selected. */}
      <div className="px-6">
        <BulkActionBar count={sel.count} onClear={sel.clear} label="Runs">
          <BulkActionButton
            icon={Square}
            label={busy ? 'Cancelling…' : `Cancel ${sel.count}`}
            tone="bad"
            busy={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await runBulkOp(sel.all, 'cancel');
                sel.clear();
                refreshNow();
              } finally {
                setBusy(false);
              }
            }}
            title="Mark selected runs as cancelled (already-terminal runs are skipped)"
          />
          <BulkActionButton
            icon={RotateCcw}
            label={busy ? 'Retrying…' : `Retry ${sel.count}`}
            tone="neutral"
            busy={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await runBulkOp(sel.all, 'retry');
                sel.clear();
                refreshNow();
              } finally {
                setBusy(false);
              }
            }}
            title="Spawn fresh runs cloning each selected run's trigger payload"
          />
        </BulkActionBar>
      </div>
    </div>
  );

  // Force-refresh after a bulk op so the operator sees the new state
  // immediately rather than waiting for the auto-refresh tick.
  function refreshNow() {
    api.orchestrationRunsFiltered({ ...filter, limit: PAGE_SIZE, offset: 0 }, { withCounts: true })
      .then((res) => {
        setRows(res.rows);
        setCounts(res.counts_by_status);
        setHasMore(res.rows.length >= PAGE_SIZE);
      })
      .catch(() => { /* ignore */ });
  }

  // Run a bulk op against the currently-selected keys. Selection
  // keys are `<cp>:<id>` (cp='' for local) so we partition by CP and
  // issue one POST per partition.
  async function runBulkOp(keys: string[], op: 'cancel' | 'retry') {
    const byCP = new Map<string, number[]>();
    for (const k of keys) {
      const [cp, idStr] = k.split(':');
      const id = Number(idStr);
      if (!Number.isFinite(id) || id <= 0) continue;
      const list = byCP.get(cp) ?? [];
      list.push(id);
      byCP.set(cp, list);
    }
    const calls: Promise<unknown>[] = [];
    for (const [cp, ids] of byCP) {
      const cpArg = cp === '' ? undefined : cp;
      calls.push(
        op === 'cancel'
          ? api.orchestrationRunsBulkCancel(ids, cpArg)
          : api.orchestrationRunsBulkRetry(ids, cpArg),
      );
    }
    await Promise.allSettled(calls);
  }
}

// runRowKey — encodes (cp, id) so the bulk handler can route each
// selected key back to its owning CP without a follow-up lookup.
// cp_source is undefined for local rows; use '' as the CP slot.
function runRowKey(r: OrchestrationRunView): string {
  return `${r.cp_source?.instance_id ?? ''}:${r.id}`;
}

// payloadStr — read a string-ish field from trigger_payload (typed
// `unknown` on the wire) without scattering casts through the JSX.
// Returns the string form when the value is set, otherwise empty.
function payloadStr(payload: Record<string, unknown> | undefined, key: string): string {
  if (!payload) return '';
  const v = payload[key];
  if (v == null) return '';
  if (typeof v === 'string') return v;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  return '';
}

// Group entry — either a single run (no grouping or unique trigger)
// or a group (≥2 runs with identical orchestration_id + dedup_key).
type GroupEntry =
  | { kind: 'single'; run: OrchestrationRunView }
  | { kind: 'group'; key: string; runs: OrchestrationRunView[] };

// collapseGroups buckets runs with the same (orchestration_id,
// trigger_payload.dedup_key OR finding_id OR host) into one card.
// Single-row buckets render as plain rows; multi-row buckets as a
// stacked card with a count.
function collapseGroups(rows: OrchestrationRunView[]): GroupEntry[] {
  const order: string[] = [];
  const buckets = new Map<string, OrchestrationRunView[]>();
  for (const r of rows) {
    const dedup =
      (r.trigger_payload?.dedup_key as string | undefined) ??
      (r.trigger_payload?.finding_id != null ? `f:${r.trigger_payload.finding_id}` : '');
    const host = (r.trigger_payload?.host as string | undefined) ?? '';
    // Scope by orchestration so different orchestrations don't collide.
    const key = `${r.orchestration_id}|${dedup || host || 'each'}|${r.cp_source?.instance_id ?? ''}`;
    // Group only when we have a real dedup signal AND the orchestration
    // is a finding-trigger one (the lab's mass-fired t1-finding-autotriage
    // case). Manual and cron runs stay as singles even with the toggle on.
    const groupable = r.trigger_kind === 'finding' && (dedup !== '' || host !== '');
    const bucketKey = groupable ? key : `single:${r.cp_source?.instance_id ?? ''}:${r.id}`;
    if (!buckets.has(bucketKey)) {
      buckets.set(bucketKey, []);
      order.push(bucketKey);
    }
    buckets.get(bucketKey)!.push(r);
  }
  const out: GroupEntry[] = [];
  for (const k of order) {
    const list = buckets.get(k)!;
    if (list.length === 1) {
      out.push({ kind: 'single', run: list[0] });
    } else {
      out.push({ kind: 'group', key: k, runs: list });
    }
  }
  return out;
}

// RunsListBody — renders a flat list of GroupEntries. Groups
// expand into a header card with a count and stacked sub-rows.
function RunsListBody({
  rows, sel, onSelectRun, scrollSentinelRef, scrollLoading, hasMore,
}: {
  rows: GroupEntry[];
  sel: ReturnType<typeof useSelection<string>>;
  onSelectRun: (id: number | null, cp?: string) => void;
  scrollSentinelRef: (el: HTMLDivElement | null) => void;
  scrollLoading: boolean;
  hasMore: boolean;
}) {
  const totalRuns = rows.reduce((n, e) => n + (e.kind === 'single' ? 1 : e.runs.length), 0);
  return (
    <div className="p-6 pb-24">
      <ListCard>
        {rows.map((entry, i) => entry.kind === 'single' ? (
          <RunRow
            key={runRowKey(entry.run)}
            run={entry.run}
            selected={sel.isSelected(runRowKey(entry.run))}
            onSelect={() => onSelectRun(entry.run.id, entry.run.cp_source?.instance_id)}
            onToggleSelect={(on) => sel.set(runRowKey(entry.run), on)}
          />
        ) : (
          <RunGroupRow
            key={entry.key + ':' + i}
            group={entry}
            sel={sel}
            onSelectRun={onSelectRun}
          />
        ))}
      </ListCard>
      <div ref={scrollSentinelRef} className="px-4 py-3 text-[11px] text-ink-mute text-center">
        {scrollLoading
          ? 'Loading older runs…'
          : hasMore
            ? 'Scroll for more'
            : `— end of ${totalRuns} runs —`}
      </div>
    </div>
  );
}

// RunsKanbanBody — one column per status. Each column holds the
// matching slice of the currently-loaded rows. Infinite scroll lives
// on the page (not per-column); when the user scrolls down inside
// any column, we extend the underlying list.
function RunsKanbanBody({
  rows, sel, onSelectRun, scrollSentinelRef, scrollLoading, hasMore,
}: {
  rows: GroupEntry[];
  sel: ReturnType<typeof useSelection<string>>;
  onSelectRun: (id: number | null, cp?: string) => void;
  scrollSentinelRef: (el: HTMLDivElement | null) => void;
  scrollLoading: boolean;
  hasMore: boolean;
}) {
  const cols = KANBAN_COLUMNS.map((c) => {
    const items = rows.filter((e) => {
      if (e.kind === 'single') return e.run.status === c.status;
      // Group cards live in the column matching the most-advanced status
      // among their runs (so an in-flight group stays visible even when
      // some sub-runs already completed).
      return e.runs.some((r) => r.status === c.status) && !e.runs.some((r) => statusOrder(r.status) > statusOrder(c.status));
    });
    return { ...c, items };
  });
  return (
    <div className="flex gap-3 p-4 pb-24 overflow-x-auto h-full">
      {cols.map((col) => (
        <div
          key={col.status}
          className="w-72 shrink-0 bg-slate-50 border border-border rounded-lg flex flex-col"
        >
          <header className={cn('px-3 py-2 border-b border-border rounded-t-lg ring-1 ring-inset', col.tone)}>
            <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-wide">
              <span>{col.label}</span>
              <span className="tabular-nums text-[11px]">{col.items.length}</span>
            </div>
          </header>
          <div className="p-2 space-y-2 overflow-auto flex-1">
            {col.items.length === 0 && (
              <div className="text-[11px] text-ink-mute italic px-2 py-3">empty</div>
            )}
            {col.items.map((entry, i) => entry.kind === 'single' ? (
              <KanbanCard
                key={runRowKey(entry.run)}
                run={entry.run}
                selected={sel.isSelected(runRowKey(entry.run))}
                onSelect={() => onSelectRun(entry.run.id, entry.run.cp_source?.instance_id)}
                onToggleSelect={(on) => sel.set(runRowKey(entry.run), on)}
              />
            ) : (
              <KanbanGroupCard
                key={entry.key + ':' + i}
                group={entry}
                sel={sel}
                onSelectRun={onSelectRun}
              />
            ))}
          </div>
        </div>
      ))}
      {/* Sentinel sits at the bottom of the kanban viewport; scrolling
          horizontally doesn't trigger more, only vertical (per-column
          scrollers don't bubble). The sentinel is dropped here too so
          a long Failed column triggers more loads. */}
      <div ref={scrollSentinelRef} className="w-px shrink-0">
        {scrollLoading && <span className="text-[10px] text-ink-mute">…</span>}
      </div>
      {!hasMore && (
        <div className="text-[10px] text-ink-mute self-end shrink-0">— end —</div>
      )}
    </div>
  );
}

// statusOrder picks a deterministic ranking so a group card lands
// in the column matching the "most advanced" status among its runs.
function statusOrder(s: OrchestrationRunStatus): number {
  switch (s) {
    case 'pending':           return 0;
    case 'running':           return 1;
    case 'approval_required': return 2;
    case 'failed':            return 3;
    case 'completed':         return 4;
    case 'cancelled':         return 5;
  }
}

// RunGroupRow — list-view rendering of a multi-run group. Click
// expands inline (toggle); click any sub-row to open that run's
// detail pane.
function RunGroupRow({
  group, sel, onSelectRun,
}: {
  group: { kind: 'group'; key: string; runs: OrchestrationRunView[] };
  sel: ReturnType<typeof useSelection<string>>;
  onSelectRun: (id: number | null, cp?: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const head = group.runs[0];
  const allKeys = group.runs.map((r) => runRowKey(r));
  const allSelected = allKeys.every((k) => sel.isSelected(k));
  const someSelected = allKeys.some((k) => sel.isSelected(k));
  return (
    <>
      <div className="px-4 py-2.5 flex items-center gap-3 hover:bg-slate-50/40 cursor-pointer" onClick={() => setOpen((v) => !v)}>
        <input
          type="checkbox"
          ref={(el) => { if (el) el.indeterminate = !allSelected && someSelected; }}
          checked={allSelected}
          onChange={(e) => sel.setMany(allKeys, e.target.checked)}
          onClick={(e) => e.stopPropagation()}
          className="shrink-0"
        />
        <span className="inline-flex items-center text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-brand-700 bg-brand-50 ring-brand-200 shrink-0">
          {group.runs.length}× group
        </span>
        <div className="flex-1 min-w-0">
          <div className="text-sm font-medium text-ink truncate">
            {group.runs.length} runs · same trigger
            {payloadStr(head.trigger_payload, 'host') && (
              <span className="text-ink-dim font-normal"> · {payloadStr(head.trigger_payload, 'host')}</span>
            )}
            {payloadStr(head.trigger_payload, 'finding_id') && (
              <span className="text-ink-dim font-normal"> · finding #{payloadStr(head.trigger_payload, 'finding_id')}</span>
            )}
          </div>
          <div className="text-[11px] text-ink-mute mt-0.5">
            orchestration_id {head.orchestration_id} · trigger {head.trigger_kind}
          </div>
        </div>
        <ChevronRight size={14} className={cn('text-ink-mute transition-transform', open && 'rotate-90')} />
      </div>
      {open && (
        <div className="bg-slate-50/40 border-t border-border">
          {group.runs.map((r) => (
            <RunRow
              key={runRowKey(r)}
              run={r}
              selected={sel.isSelected(runRowKey(r))}
              onSelect={() => onSelectRun(r.id, r.cp_source?.instance_id)}
              onToggleSelect={(on) => sel.set(runRowKey(r), on)}
              indented
            />
          ))}
        </div>
      )}
    </>
  );
}

function KanbanCard({
  run, selected, onSelect, onToggleSelect,
}: {
  run: OrchestrationRunView;
  selected: boolean;
  onSelect: () => void;
  onToggleSelect: (on: boolean) => void;
}) {
  return (
    <div
      onClick={onSelect}
      className={cn(
        'bg-white border border-border rounded-md px-2.5 py-2 text-xs shadow-sm cursor-pointer hover:border-brand-200',
        selected && 'border-brand-300 ring-1 ring-brand-200',
      )}
    >
      <div className="flex items-start gap-2">
        <input
          type="checkbox"
          checked={selected}
          onChange={(e) => onToggleSelect(e.target.checked)}
          onClick={(e) => e.stopPropagation()}
          className="mt-0.5 shrink-0"
        />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="font-medium text-ink truncate">Run #{run.id}</span>
            <CPSourceChip source={run.cp_source} />
          </div>
          <div className="text-[10px] text-ink-mute uppercase tracking-wide">{run.trigger_kind}</div>
          {(run.current_step_id || run.error) && (
            <div className="text-[11px] text-ink-dim mt-1 truncate">
              {run.current_step_id && <code className="font-mono">{run.current_step_id}</code>}
              {run.error && <span className="text-red-700"> · {run.error.slice(0, 60)}</span>}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function KanbanGroupCard({
  group, sel, onSelectRun,
}: {
  group: { kind: 'group'; key: string; runs: OrchestrationRunView[] };
  sel: ReturnType<typeof useSelection<string>>;
  onSelectRun: (id: number | null, cp?: string) => void;
}) {
  const head = group.runs[0];
  const allKeys = group.runs.map((r) => runRowKey(r));
  const allSelected = allKeys.every((k) => sel.isSelected(k));
  const someSelected = allKeys.some((k) => sel.isSelected(k));
  return (
    <div
      onClick={() => onSelectRun(head.id, head.cp_source?.instance_id)}
      className="bg-white border border-brand-200 rounded-md px-2.5 py-2 text-xs shadow-sm cursor-pointer hover:border-brand-300"
    >
      <div className="flex items-start gap-2">
        <input
          type="checkbox"
          ref={(el) => { if (el) el.indeterminate = !allSelected && someSelected; }}
          checked={allSelected}
          onChange={(e) => sel.setMany(allKeys, e.target.checked)}
          onClick={(e) => e.stopPropagation()}
          className="mt-0.5 shrink-0"
        />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-brand-700 bg-brand-50 ring-brand-200">
              {group.runs.length}×
            </span>
            <span className="font-medium text-ink truncate">grouped</span>
          </div>
          <div className="text-[11px] text-ink-mute mt-0.5 truncate">
            orch {head.orchestration_id}
            {payloadStr(head.trigger_payload, 'host') && ` · ${payloadStr(head.trigger_payload, 'host')}`}
          </div>
        </div>
      </div>
    </div>
  );
}

function RunRow({
  run, selected, onSelect, onToggleSelect, indented,
}: {
  run: OrchestrationRunView;
  selected: boolean;
  onSelect: () => void;
  /** When provided, a checkbox is rendered for bulk selection. */
  onToggleSelect?: (on: boolean) => void;
  /** Slight inset for sub-rows inside an expanded group card. */
  indented?: boolean;
}) {
  return (
    <div
      onClick={onSelect}
      className={cn(
        'flex items-center gap-3 cursor-pointer hover:bg-slate-50/40',
        indented ? 'pl-12 pr-4 py-2' : 'px-4 py-3',
        selected && 'bg-brand-50/40',
      )}
    >
      {onToggleSelect && (
        <input
          type="checkbox"
          checked={selected}
          onChange={(e) => onToggleSelect(e.target.checked)}
          onClick={(e) => e.stopPropagation()}
          className="shrink-0"
          aria-label={`Select run ${run.id}`}
        />
      )}
      <RunStatusPill status={run.status} />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium text-ink">Run #{run.id}</span>
          <CPSourceChip source={run.cp_source} />
          <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
            {run.trigger_kind}
          </span>
        </div>
        <div className="text-[11px] text-ink-dim mt-0.5">
          started {fmtTime(run.started_at)}
          {run.ended_at && ` · ended ${fmtTime(run.ended_at)}`}
          {run.current_step_id && ` · at step `}
          {run.current_step_id && (
            <code className="font-mono text-ink-dim">{run.current_step_id}</code>
          )}
        </div>
      </div>
      <ChevronRight size={14} className="text-ink-mute" />
    </div>
  );
}

function RunDetail({
  runID,
  cpInstanceID,
  onClose,
  onChanged,
}: {
  runID: number;
  cpInstanceID?: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [run, setRun] = useState<OrchestrationRunView | null>(null);
  const [orchestration, setOrchestration] = useState<Orchestration | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [approving, setApproving] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);

  const refresh = () => {
    api.orchestrationRunDetail(runID, cpInstanceID)
      .then((r) => {
        setRun(r); setError(null);
        // Fetch the parent orchestration once so the canvas can
        // annotate each step with its `agent` and `node` from the
        // spec. The persisted step record only carries runtime state
        // (status, run_id, cp_instance_id) — we pull the spec for
        // the visual.
        if (orchestration === null && r.orchestration_id) {
          api.orchestration(r.orchestration_id, cpInstanceID).then(setOrchestration).catch(() => {});
        }
      })
      .catch((e) => setError(String(e)));
  };
  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 2500);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runID, cpInstanceID]);

  async function approve(stepID: string) {
    setApproving(stepID);
    try {
      await api.orchestrationStepApprove(runID, stepID, cpInstanceID);
      onChanged();
      refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setApproving(null);
    }
  }

  const [cancelling, setCancelling] = useState(false);
  async function cancelRun() {
    if (!run) return;
    if (!window.confirm(`Cancel run #${run.id}? In-flight steps continue but the run won't advance further.`)) return;
    setCancelling(true);
    try {
      await api.orchestrationRunCancel(runID, cpInstanceID);
      onChanged();
      refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setCancelling(false);
    }
  }
  // Active runs (anything not terminal) can be cancelled.
  const cancellable = run && (run.status === 'pending' || run.status === 'running' || run.status === 'approval_required');

  // Build agent + node maps from the parsed spec — fed into the run
  // canvas so each card shows what it's running and where.
  const { agentByStepID, nodeByStepID } = useMemo(() => {
    const aMap: Record<string, string> = {};
    const nMap: Record<string, string> = {};
    if (orchestration?.spec_yaml) {
      const parsed = parseSpecYAMLLite(orchestration.spec_yaml);
      if (parsed) {
        for (const s of parsed.steps) {
          aMap[s.id] = s.agent;
          if (s.node) nMap[s.id] = s.node;
        }
      }
    }
    return { agentByStepID: aMap, nodeByStepID: nMap };
  }, [orchestration]);

  const expandedStep = expanded && run?.steps
    ? run.steps.find((s) => s.step_id === expanded) ?? null
    : null;

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-3 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <div className="flex items-center gap-3">
          <button
            onClick={onClose}
            className="text-xs text-ink-dim hover:text-ink hover:bg-slate-100 px-2 py-1 rounded-md inline-flex items-center gap-1"
            title="Back to runs list"
          >
            ← Runs
          </button>
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm shrink-0">
            <Workflow size={14} />
          </span>
          <div className="min-w-0 flex items-baseline gap-2 flex-wrap">
            <h2 className="text-sm font-semibold text-ink">
              Run #{runID}
            </h2>
            {orchestration && (
              <span className="text-[11px] text-ink-dim font-mono">{orchestration.name}</span>
            )}
            {orchestration?.cp_source && <CPSourceChip source={orchestration.cp_source} />}
            {run && <RunStatusPill status={run.status} />}
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span className="text-[11px] text-ink-mute">
              {run?.started_at && `started ${fmtTime(run.started_at)}`}
              {run?.ended_at && ` · ended ${fmtTime(run.ended_at)}`}
            </span>
            {cancellable && (
              <button
                onClick={cancelRun}
                disabled={cancelling}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1 rounded-md ring-1 text-red-700 bg-red-50 ring-red-200 hover:bg-red-100 disabled:opacity-50"
                title="Cancel this run — in-flight steps continue, but the run will not advance further"
              >
                <Square size={11} />
                {cancelling ? 'Cancelling…' : 'Cancel run'}
              </button>
            )}
          </div>
        </div>
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-4">
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}
        {!run && <div className="text-xs text-ink-mute">Loading…</div>}
        {run && run.error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            <span className="font-medium">Error:</span> {run.error}
          </div>
        )}

        {/* Horizontal DAG canvas — left-to-right with running-state
            highlights. Currently-running steps pulse blue; gated steps
            pulse amber with an inline Approve button. Edges are
            colour-coded by upstream status to show data flow. */}
        {run && run.steps && run.steps.length > 0 && (
          <Suspense fallback={<EditorLoading />}>
            <OrchestrationRunCanvas
              steps={run.steps}
              approvingStepID={approving}
              onApprove={(id) => approve(id)}
              selectedStepID={expanded}
              onSelectStep={(id) => setExpanded((cur) => (cur === id ? null : id))}
              agentByStepID={agentByStepID}
              nodeByStepID={nodeByStepID}
            />
          </Suspense>
        )}

        {/* Expanded step body — shown below the canvas when an
            operator clicks a card. Same content the previous list
            view inlined, just routed through the canvas's selection. */}
        {expandedStep && (
          <section className="bg-panel border border-border rounded-lg shadow-card p-4 space-y-3">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <span className="w-1 h-4 rounded-full bg-gradient-to-b from-brand-400 to-brand-600" />
              {expandedStep.step_id}
              <span className="text-[11px] text-ink-mute font-normal">
                {expandedStep.status.replace('_', ' ')}
              </span>
            </div>
            {expandedStep.rendered_prompt && (
              <section>
                <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">Prompt</div>
                {looksLikeJSONL(expandedStep.rendered_prompt)
                  ? <HarnessOutput text={expandedStep.rendered_prompt} maxHeight={420} />
                  : <pre className="text-xs font-mono bg-slate-50 border border-border rounded p-3 whitespace-pre-wrap break-words text-ink overflow-auto" style={{ maxHeight: 420 }}>{expandedStep.rendered_prompt}</pre>}
              </section>
            )}
            {expandedStep.result && Object.keys(expandedStep.result).length > 0 && (
              <section>
                <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">Result</div>
                <div className="bg-slate-50 border border-border rounded p-3">
                  <StructuredView value={expandedStep.result} />
                </div>
              </section>
            )}
            {expandedStep.output_summary && (
              <section>
                <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1">Output (tail)</div>
                <HarnessOutput text={expandedStep.output_summary} maxHeight={420} />
              </section>
            )}
            {expandedStep.error && (
              <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
                <span className="font-medium">Error:</span> {expandedStep.error}
              </div>
            )}
            <StepActionsApplied step={expandedStep} />
            {expandedStep.run_id && (
              <a
                href={`/runs?id=${expandedStep.run_id}`}
                className="text-[11px] text-brand-700 hover:underline inline-flex items-center gap-1"
              >
                Open underlying Run →
              </a>
            )}
          </section>
        )}

        {run && run.trigger_payload && Object.keys(run.trigger_payload).length > 0 && (
          <section className="bg-slate-50 border border-border rounded-lg p-3">
            <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">
              Trigger payload
            </div>
            <StructuredView value={run.trigger_payload} />
          </section>
        )}
      </div>
    </div>
  );
}

// looksLikeJSONL — quick heuristic for "this string is a stream of
// agent-harness events" so we route it through HarnessOutput's
// conversation renderer instead of dumping as a code block.
// StepActionsApplied — renders the `actions[]` array the agent
// emitted for this step (read out of orchestration_result.attributes).
// Each entry shows the action kind as a green chip + the affected
// finding id, so the operator can see at a glance which findings
// the automation touched.
function StepActionsApplied({ step }: { step: OrchestrationStepView }) {
  const actions = extractStepActions(step.result);
  if (actions.length === 0) return null;
  return (
    <section>
      <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium mb-1.5">
        Actions applied
      </div>
      <ul className="space-y-1">
        {actions.map((a, i) => (
          <li key={i} className="flex items-center gap-2 text-xs">
            <span className="inline-flex items-center text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 text-green-800 bg-green-50 ring-green-200">
              {prettyActionKind(a.kind)}
            </span>
            {a.finding_id !== undefined && (
              <Link to={`/findings?id=${a.finding_id}`} className="font-mono text-brand-700 hover:underline">
                finding #{a.finding_id}
              </Link>
            )}
            {a.status && <span className="text-ink-dim">→ {a.status}</span>}
            {a.severity && <span className="text-ink-dim">→ {a.severity}</span>}
            {a.tag && <span className="text-ink-dim">→ <code className="text-[11px]">{a.tag}</code></span>}
            {a.reason && <span className="text-ink-mute italic truncate">"{a.reason}"</span>}
          </li>
        ))}
      </ul>
    </section>
  );
}

interface ResultAction {
  kind: string;
  finding_id?: number;
  status?: string;
  severity?: string;
  tag?: string;
  reason?: string;
}

function extractStepActions(result: Record<string, unknown> | undefined): ResultAction[] {
  if (!result) return [];
  const raw = result['actions'];
  if (!Array.isArray(raw)) return [];
  return raw.map((a) => {
    if (a && typeof a === 'object') {
      const o = a as Record<string, unknown>;
      return {
        kind:       String(o.kind ?? ''),
        finding_id: typeof o.finding_id === 'number' ? o.finding_id : undefined,
        status:     typeof o.status === 'string' ? o.status : undefined,
        severity:   typeof o.severity === 'string' ? o.severity : undefined,
        tag:        typeof o.tag === 'string' ? o.tag : undefined,
        reason:     typeof o.reason === 'string' ? o.reason : undefined,
      };
    }
    return { kind: '' };
  }).filter((a) => a.kind);
}

function prettyActionKind(kind: string): string {
  switch (kind) {
    case 'update_finding_status':         return 'status';
    case 'set_finding_severity_override': return 'severity';
    case 'add_finding_tag':                return 'tag +';
    case 'remove_finding_tag':             return 'tag −';
    case 'link_run_to_finding':            return 'linked';
    case 'escalate':                       return 'escalate';
    default:                               return kind;
  }
}

function looksLikeJSONL(s: string): boolean {
  // A run's prompt often embeds the previous step's transcript as
  // raw JSONL. Detect by looking for at least three lines that start
  // with `{"type":"…"`.
  const matches = s.match(/^\{"type":"/gm);
  return !!(matches && matches.length >= 3);
}


function RunStatusPill({ status }: { status: OrchestrationRunStatus }) {
  const tone = runStatusTone(status);
  return (
    <span className={cn('inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1', tone.cls)}>
      <tone.Icon size={9} />
      {status.replace('_', ' ')}
    </span>
  );
}

function runStatusTone(status: OrchestrationRunStatus) {
  switch (status) {
    case 'running':           return { cls: 'bg-blue-50 text-blue-700 ring-blue-200', Icon: Loader2 };
    case 'completed':         return { cls: 'bg-green-50 text-green-700 ring-green-200', Icon: CheckCircle2 };
    case 'failed':            return { cls: 'bg-red-50 text-red-700 ring-red-200', Icon: XCircle };
    case 'cancelled':         return { cls: 'bg-slate-100 text-slate-600 ring-slate-200', Icon: X };
    case 'approval_required': return { cls: 'bg-amber-50 text-amber-700 ring-amber-200', Icon: Pause };
    default:                  return { cls: 'bg-slate-100 text-slate-600 ring-slate-200', Icon: Clock };
  }
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  const now = Date.now();
  const ageS = Math.floor((now - d.getTime()) / 1000);
  if (ageS < 60) return `${ageS}s ago`;
  if (ageS < 3600) return `${Math.floor(ageS / 60)}m ago`;
  if (ageS < 86_400) return `${Math.floor(ageS / 3600)}h ago`;
  return d.toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

// Quiet imports if a future refactor prunes a token.
const _useMemo = useMemo;
void _useMemo;
