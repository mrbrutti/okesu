// Investigation workspace — the operator's home for a T2 case.
//
// Tabs surface every linked entity:
//   • Overview  — summary + identity + the live note composer
//   • Findings  — enriched table; click → finding detail; unlink
//   • Runs      — orchestration runs; click → run detail; unlink
//   • IOCs      — IOCs observed via linked findings; deep-link to
//                 catalog (separate session is building it)
//   • Daimons   — distinct emitting daimons with last-seen + count
//   • Orchs     — orchestrations with run count; "Run again" button
//   • Notes     — the analyst-note timeline
//
// War-room mode: when any linked finding has the `war-bridge` tag,
// a red banner is rendered at top + the bundle auto-refreshes every
// 5s instead of the default 30s. Mirror of the dashboard's red
// banner so an operator deep-linked from there sees the same level
// of urgency in the case view.

import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  Archive,
  Bot,
  Check,
  CheckCircle2,
  ClipboardList,
  Clock,
  Eye,
  FileText,
  Hash,
  Loader2,
  Lock,
  MessageSquarePlus,
  Pencil,
  Play,
  Plus,
  Save,
  Sparkles,
  Trash2,
  Wifi,
  X,
  XCircle,
} from 'lucide-react';
import {
  api,
  ApiError,
  type Finding,
  type Investigation,
  type InvestigationDaimonItem,
  type InvestigationDetail,
  type InvestigationFindingItem,
  type InvestigationIOCItem,
  type InvestigationNote,
  type InvestigationOrchestrationItem,
  type InvestigationRunItem,
  type Orchestration,
} from '../api';
import { cn } from '../lib/cn';
import { RunAgentDialog } from '../components/RunAgentDialog';

type Resolution = 'resolved' | 'false_positive' | 'duplicate' | 'wont_fix';
type Tab = 'overview' | 'findings' | 'runs' | 'iocs' | 'daimons' | 'orchestrations' | 'notes';

const RESOLUTION_OPTIONS: { value: Resolution; label: string; icon: typeof Check; tone: string }[] = [
  { value: 'resolved',       label: 'Resolved',        icon: CheckCircle2, tone: 'text-green-700' },
  { value: 'false_positive', label: 'False positive',  icon: XCircle,      tone: 'text-slate-600' },
  { value: 'duplicate',      label: 'Duplicate',       icon: Archive,      tone: 'text-slate-600' },
  { value: 'wont_fix',       label: "Won't fix",       icon: Lock,         tone: 'text-orange-700' },
];

export default function InvestigationDetailPage() {
  const { id = '' } = useParams<{ id: string }>();
  const invID = Number(id);
  const navigate = useNavigate();
  // ?cp=<instance_id> is set when navigating from the federated
  // investigations list. The detail proxy on the parent forwards
  // GET /api/investigations/{id} to the owning child via the
  // existing ?cp= convention.
  const [search] = useSearchParams();
  const cpInstanceID = search.get('cp') || undefined;

  const [bundle, setBundle] = useState<InvestigationDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draftTitle, setDraftTitle] = useState('');
  const [draftSummary, setDraftSummary] = useState('');
  const [closeOpen, setCloseOpen] = useState(false);
  const [runAgentOpen, setRunAgentOpen] = useState(false);
  const [tab, setTab] = useState<Tab>('overview');

  const reload = () => {
    setError(null);
    api.investigations
      .get(invID, cpInstanceID)
      .then((b) => {
        setBundle(b);
        setDraftTitle(b.investigation.Title);
        setDraftSummary(b.investigation.Summary);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  };

  useEffect(() => {
    if (!Number.isFinite(invID) || invID <= 0) {
      setError('Bad investigation id.');
      return;
    }
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [invID]);

  // War-room polling: 5s when active, 30s otherwise. The faster
  // cadence kicks in only for the case in front of the operator;
  // background tabs pause via the visibility API.
  useEffect(() => {
    if (!bundle) return;
    const interval = bundle.war_room ? 5_000 : 30_000;
    const t = setInterval(() => {
      if (document.visibilityState === 'visible') reload();
    }, interval);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bundle?.war_room, invID]);

  async function saveEdits() {
    if (!bundle) return;
    setBusy(true); setError(null);
    try {
      await api.investigations.update(invID, {
        title: draftTitle.trim() || bundle.investigation.Title,
        summary: draftSummary,
      });
      setEditing(false);
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function closeCase(resolution: Resolution) {
    setBusy(true); setError(null);
    try {
      await api.investigations.update(invID, { status: 'closed', resolution });
      setCloseOpen(false);
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function reopen() {
    setBusy(true); setError(null);
    try {
      await api.investigations.update(invID, { status: 'active' });
      reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  if (error && !bundle) {
    return (
      <div className="p-6">
        <button
          onClick={() => navigate('/investigations')}
          className="text-xs text-ink-dim hover:text-ink inline-flex items-center gap-1 mb-3"
        >
          <ArrowLeft size={12} /> Back to investigations
        </button>
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      </div>
    );
  }

  if (!bundle) {
    return (
      <div className="p-6 text-ink-dim text-xs flex items-center gap-2">
        <Loader2 size={14} className="animate-spin" /> Loading investigation…
      </div>
    );
  }

  const inv = bundle.investigation;
  const isActive = inv.Status === 'active';

  return (
    <div className="h-full flex flex-col">
      {bundle.war_room && (
        <div className="px-6 py-2 bg-red-600 text-white text-xs flex items-center gap-2 shadow-sm">
          <AlertTriangle size={14} className="animate-pulse" />
          <span className="font-semibold uppercase tracking-wide">War room</span>
          <span className="opacity-90">
            — at least one linked finding has the <code className="bg-red-700/40 px-1 rounded">war-bridge</code> tag.
            Auto-refresh every 5s.
          </span>
          <Wifi size={12} className="ml-auto opacity-70" />
        </div>
      )}

      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <div className="flex items-center gap-2 text-xs text-ink-dim mb-2">
          <Link to="/investigations" className="hover:text-ink inline-flex items-center gap-1">
            <ArrowLeft size={12} /> Investigations
          </Link>
          <span>·</span>
          <code className="font-mono">#{inv.ID}</code>
        </div>
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-1">
              <span className="inline-flex items-center justify-center w-6 h-6 rounded-md bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
                <ClipboardList size={11} />
              </span>
              <StatusChip status={inv.Status} />
              {inv.Resolution && <ResolutionChip res={inv.Resolution} />}
            </div>
            {editing ? (
              <input
                value={draftTitle}
                onChange={(e) => setDraftTitle(e.target.value)}
                className="w-full px-2 py-1 rounded-md border border-border text-lg font-semibold"
              />
            ) : (
              <h1 className="text-lg font-semibold leading-tight">{inv.Title || '(untitled)'}</h1>
            )}
          </div>
          <div className="flex items-center gap-2 shrink-0">
            {!editing && (
              <button
                onClick={() => setEditing(true)}
                className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
              >
                <Pencil size={11} /> Edit
              </button>
            )}
            {editing && (
              <>
                <button
                  onClick={() => { setEditing(false); setDraftTitle(inv.Title); setDraftSummary(inv.Summary); }}
                  className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
                >
                  <X size={11} /> Cancel
                </button>
                <button
                  onClick={saveEdits}
                  disabled={busy}
                  className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
                >
                  <Save size={11} /> Save
                </button>
              </>
            )}
            {!editing && (
              <button
                onClick={() => setRunAgentOpen(true)}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-500 text-white hover:bg-brand-600"
                title="Run an agent on this case's context"
              >
                <Play size={12} /> Run agent
              </button>
            )}
            {isActive && !editing && (
              <button
                onClick={() => setCloseOpen(true)}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-green-600 text-white hover:bg-green-700"
              >
                <CheckCircle2 size={12} /> Close case
              </button>
            )}
            {!isActive && !editing && (
              <button
                onClick={reopen}
                disabled={busy}
                className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
              >
                <Sparkles size={12} /> Reopen
              </button>
            )}
          </div>
        </div>
      </header>

      {/* Tab strip */}
      <nav className="px-6 border-b border-border flex items-center gap-1 bg-panel">
        <TabButton current={tab} value="overview"      onClick={setTab} icon={FileText}     label="Overview" />
        <TabButton current={tab} value="findings"      onClick={setTab} icon={Hash}         label="Findings"        count={bundle.findings.length} />
        <TabButton current={tab} value="runs"          onClick={setTab} icon={Sparkles}     label="Runs"            count={bundle.runs.length} />
        <TabButton current={tab} value="iocs"          onClick={setTab} icon={Eye}          label="IOCs"            count={bundle.iocs.length} />
        <TabButton current={tab} value="daimons"       onClick={setTab} icon={Bot}          label="Daimons"         count={bundle.daimons.length} />
        <TabButton current={tab} value="orchestrations" onClick={setTab} icon={Activity}    label="Orchestrations" count={bundle.orchestrations.length} />
        <TabButton current={tab} value="notes"         onClick={setTab} icon={MessageSquarePlus} label="Notes"     count={bundle.notes.length} />
      </nav>

      <div className="flex-1 overflow-auto p-6 space-y-5">
        {error && (
          <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {tab === 'overview' && (
          <OverviewPanel
            inv={inv}
            editing={editing}
            draftSummary={draftSummary}
            setDraftSummary={setDraftSummary}
            bundle={bundle}
          />
        )}

        {tab === 'findings' && (
          <FindingsPanel invID={invID} cpInstanceID={cpInstanceID} findings={bundle.findings} onChange={reload} />
        )}

        {tab === 'runs' && (
          <RunsPanel invID={invID} runs={bundle.runs} onChange={reload} />
        )}

        {tab === 'iocs' && <IOCsPanel iocs={bundle.iocs} />}

        {tab === 'daimons' && <DaimonsPanel daimons={bundle.daimons} />}

        {tab === 'orchestrations' && (
          <OrchestrationsPanel invID={invID} cpInstanceID={cpInstanceID} orchs={bundle.orchestrations} onChange={reload} />
        )}

        {tab === 'notes' && (
          <NotesPanel invID={invID} notes={bundle.notes} onChange={reload} />
        )}
      </div>

      {closeOpen && (
        <CloseDialog
          onClose={() => setCloseOpen(false)}
          onPick={(r) => closeCase(r)}
          busy={busy}
        />
      )}

      {runAgentOpen && (
        <RunAgentDialog
          bundle={bundle}
          cpInstanceID={cpInstanceID}
          onClose={() => setRunAgentOpen(false)}
          onLaunched={() => {
            setRunAgentOpen(false);
            reload();
          }}
        />
      )}
    </div>
  );
}

// ── Tab button ──────────────────────────────────────────────────────

function TabButton<T extends string>({
  current, value, onClick, icon: Icon, label, count,
}: {
  current: T;
  value: T;
  onClick: (v: T) => void;
  icon: typeof Hash;
  label: string;
  count?: number;
}) {
  const active = current === value;
  return (
    <button
      onClick={() => onClick(value)}
      className={cn(
        'inline-flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 -mb-px transition',
        active
          ? 'border-brand-500 text-brand-700'
          : 'border-transparent text-ink-dim hover:text-ink hover:bg-slate-50',
      )}
    >
      <Icon size={12} />
      {label}
      {typeof count === 'number' && (
        <span className={cn(
          'text-[10px] px-1.5 py-0.5 rounded-full',
          active ? 'bg-brand-100 text-brand-700' : 'bg-slate-100 text-ink-mute',
        )}>
          {count}
        </span>
      )}
    </button>
  );
}

// ── Overview panel ──────────────────────────────────────────────────

function OverviewPanel({
  inv, editing, draftSummary, setDraftSummary, bundle,
}: {
  inv: Investigation;
  editing: boolean;
  draftSummary: string;
  setDraftSummary: (s: string) => void;
  bundle: InvestigationDetail;
}) {
  return (
    <section className="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <div className="lg:col-span-2 border border-border rounded-md bg-white p-4">
        <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2">
          Summary
        </h4>
        {editing ? (
          <textarea
            value={draftSummary}
            onChange={(e) => setDraftSummary(e.target.value)}
            rows={6}
            className="w-full px-2.5 py-1.5 rounded-md border border-border bg-white text-sm"
            placeholder="Hypothesis, scope, working theory…"
          />
        ) : inv.Summary ? (
          <div className="text-sm whitespace-pre-wrap text-ink">{inv.Summary}</div>
        ) : (
          <div className="text-sm text-ink-mute italic">No summary yet.</div>
        )}
      </div>

      <div className="space-y-3">
        <div className="border border-border rounded-md bg-white p-4 text-xs space-y-1.5">
          <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2">
            Identity
          </h4>
          <KV label="ID"><code className="font-mono">#{inv.ID}</code></KV>
          <KV label="Created"><span className="font-mono text-ink-dim">{fmtDate(inv.CreatedAt)}</span></KV>
          <KV label="Updated"><span className="font-mono text-ink-dim">{fmtDate(inv.UpdatedAt)}</span></KV>
          {inv.CreatedBy && <KV label="Created by"><code className="font-mono">{inv.CreatedBy}</code></KV>}
          {!isZeroTime(inv.ClosedAt) && <KV label="Closed"><span className="font-mono text-ink-dim">{fmtDate(inv.ClosedAt)}</span></KV>}
        </div>

        <div className="border border-border rounded-md bg-white p-4 text-xs space-y-1.5">
          <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-2">
            Linked entities
          </h4>
          <KV label="Findings"><span className="font-mono">{bundle.findings.length}</span></KV>
          <KV label="Runs"><span className="font-mono">{bundle.runs.length}</span></KV>
          <KV label="IOCs"><span className="font-mono">{bundle.iocs.length}</span></KV>
          <KV label="Daimons"><span className="font-mono">{bundle.daimons.length}</span></KV>
          <KV label="Orchestrations"><span className="font-mono">{bundle.orchestrations.length}</span></KV>
          <KV label="Notes"><span className="font-mono">{bundle.notes.length}</span></KV>
        </div>
      </div>
    </section>
  );
}

// ── Findings panel ──────────────────────────────────────────────────

function FindingsPanel({ invID, cpInstanceID, findings, onChange }: {
  invID: number;
  cpInstanceID?: string;
  findings: InvestigationFindingItem[];
  onChange: () => void;
}) {
  const [linkOpen, setLinkOpen] = useState(false);
  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        <button
          onClick={() => setLinkOpen(true)}
          className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700"
        >
          <Plus size={12} /> Link finding
        </button>
      </div>
      {findings.length === 0 ? (
        <EmptyTabState icon={Hash} label="No findings linked yet." hint="Click 'Link finding' above, or open a finding from the Findings page and use 'Add to existing'." />
      ) : (
        <div className="border border-border rounded-md bg-white overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-slate-50 border-b border-border text-[11px] uppercase tracking-wide text-ink-mute">
              <tr>
                <th className="px-3 py-2 text-left w-16">ID</th>
                <th className="px-3 py-2 text-left w-24">Severity</th>
                <th className="px-3 py-2 text-left">Title</th>
                <th className="px-3 py-2 text-left w-32">Daimon</th>
                <th className="px-3 py-2 text-left w-28">Host</th>
                <th className="px-3 py-2 text-left w-28">Status</th>
                <th className="px-3 py-2 text-right w-12"></th>
              </tr>
            </thead>
            <tbody>
              {findings.map((f) => (
                <tr key={f.ID} className="border-b border-border/60 last:border-0 hover:bg-slate-50/40">
                  <td className="px-3 py-2"><Link to={`/findings?id=${f.ID}${cpInstanceID ? `&cp=${cpInstanceID}` : ''}`} className="text-brand-700 hover:underline font-mono text-xs">#{f.ID}</Link></td>
                  <td className="px-3 py-2"><SeverityChip sev={nullStr(f.Severity)} /></td>
                  <td className="px-3 py-2 truncate max-w-md">{nullStr(f.Title) || <span className="text-ink-mute italic">(no title)</span>}</td>
                  <td className="px-3 py-2 text-xs text-ink-dim">{nullStr(f.Agent) || '—'}</td>
                  <td className="px-3 py-2 text-xs text-ink-dim">{nullStr(f.Host) || '—'}</td>
                  <td className="px-3 py-2"><StatusPill v={nullStr(f.Status)} /></td>
                  <td className="px-3 py-2 text-right">
                    <UnlinkButton onClick={async () => {
                      if (!confirm(`Unlink finding #${f.ID} from this investigation?`)) return;
                      await api.investigations.unlinkFinding(invID, f.ID);
                      onChange();
                    }} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {linkOpen && (
        <LinkFindingDialog
          invID={invID}
          cpInstanceID={cpInstanceID}
          alreadyLinkedIDs={new Set(findings.map((f) => f.ID))}
          onClose={() => setLinkOpen(false)}
          onLinked={() => { setLinkOpen(false); onChange(); }}
        />
      )}
    </div>
  );
}

// ── Runs panel ──────────────────────────────────────────────────────

function RunsPanel({ invID, runs, onChange }: {
  invID: number;
  runs: InvestigationRunItem[];
  onChange: () => void;
}) {
  if (runs.length === 0) {
    return <EmptyTabState icon={Sparkles} label="No orchestration runs linked yet." hint="An orchestration step's `link_run_to_finding` action automatically links the run when the finding is on a case." />;
  }
  return (
    <div className="border border-border rounded-md bg-white overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-slate-50 border-b border-border text-[11px] uppercase tracking-wide text-ink-mute">
          <tr>
            <th className="px-3 py-2 text-left w-16">ID</th>
            <th className="px-3 py-2 text-left w-32">Status</th>
            <th className="px-3 py-2 text-left">Orchestration</th>
            <th className="px-3 py-2 text-left w-28">Trigger</th>
            <th className="px-3 py-2 text-left w-36">Started</th>
            <th className="px-3 py-2 text-right w-12"></th>
          </tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.ID} className="border-b border-border/60 last:border-0 hover:bg-slate-50/40">
              <td className="px-3 py-2"><Link to={`/orchestration-runs/${r.ID}`} className="text-brand-700 hover:underline font-mono text-xs">#{r.ID}</Link></td>
              <td className="px-3 py-2"><RunStatusChip s={r.Status} /></td>
              <td className="px-3 py-2 truncate">
                {nullStr(r.OrchestrationName) || <span className="text-ink-mute italic">(deleted)</span>}
              </td>
              <td className="px-3 py-2 text-xs text-ink-dim">{r.TriggerKind || '—'}</td>
              <td className="px-3 py-2 text-xs text-ink-dim font-mono">{fmtDate(r.StartedAt)}</td>
              <td className="px-3 py-2 text-right">
                <UnlinkButton onClick={async () => {
                  if (!confirm(`Unlink run #${r.ID} from this investigation?`)) return;
                  await api.investigations.unlinkRun(invID, r.ID);
                  onChange();
                }} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── IOCs panel ──────────────────────────────────────────────────────

function IOCsPanel({ iocs }: { iocs: InvestigationIOCItem[] }) {
  if (iocs.length === 0) {
    return <EmptyTabState icon={Eye} label="No IOCs observed via this case's findings." hint="IOCs are derived from ioc_observations on the case's linked findings — they appear here automatically." />;
  }
  return (
    <div className="border border-border rounded-md bg-white overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-slate-50 border-b border-border text-[11px] uppercase tracking-wide text-ink-mute">
          <tr>
            <th className="px-3 py-2 text-left w-20">Kind</th>
            <th className="px-3 py-2 text-left">Value</th>
            <th className="px-3 py-2 text-left w-24">Severity</th>
            <th className="px-3 py-2 text-right w-16">Obs</th>
            <th className="px-3 py-2 text-right w-16">Hosts</th>
            <th className="px-3 py-2 text-left w-36">Last seen</th>
          </tr>
        </thead>
        <tbody>
          {iocs.map((i) => (
            <tr key={i.ID} className="border-b border-border/60 last:border-0 hover:bg-slate-50/40">
              <td className="px-3 py-2 font-mono text-xs uppercase">{i.Kind}</td>
              <td className="px-3 py-2 truncate max-w-md">
                <Link to={`/iocs/${i.ID}`} className="text-brand-700 hover:underline font-mono text-xs">
                  {i.Value}
                </Link>
              </td>
              <td className="px-3 py-2"><SeverityChip sev={nullStr(i.Severity)} /></td>
              <td className="px-3 py-2 text-right text-xs font-mono text-ink-dim">{i.ObservationCount}</td>
              <td className="px-3 py-2 text-right text-xs font-mono text-ink-dim">{i.HostCount}</td>
              <td className="px-3 py-2 text-xs text-ink-dim font-mono">{fmtDate(i.LastSeen)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── Daimons panel ───────────────────────────────────────────────────

function DaimonsPanel({ daimons }: { daimons: InvestigationDaimonItem[] }) {
  if (daimons.length === 0) {
    return <EmptyTabState icon={Bot} label="No daimon emissions linked." hint="The case's findings haven't been emitted by any agent yet — add some findings on the Findings tab." />;
  }
  return (
    <div className="border border-border rounded-md bg-white overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-slate-50 border-b border-border text-[11px] uppercase tracking-wide text-ink-mute">
          <tr>
            <th className="px-3 py-2 text-left">Daimon</th>
            <th className="px-3 py-2 text-right w-32">Findings</th>
            <th className="px-3 py-2 text-left w-40">Last seen</th>
          </tr>
        </thead>
        <tbody>
          {daimons.map((d) => (
            <tr key={d.Agent} className="border-b border-border/60 last:border-0 hover:bg-slate-50/40">
              <td className="px-3 py-2">
                <Link to={`/agents/${encodeURIComponent(d.Agent)}`} className="text-brand-700 hover:underline">
                  {d.Agent}
                </Link>
              </td>
              <td className="px-3 py-2 text-right text-xs font-mono text-ink-dim">{d.FindingCount}</td>
              <td className="px-3 py-2 text-xs text-ink-dim font-mono">
                {d.LastSeenTs ? new Date(d.LastSeenTs).toLocaleString() : '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ── Orchestrations panel ───────────────────────────────────────────

function OrchestrationsPanel({ invID, cpInstanceID, orchs, onChange }: {
  invID: number;
  cpInstanceID?: string;
  orchs: InvestigationOrchestrationItem[];
  onChange: () => void;
}) {
  const [runOpen, setRunOpen] = useState(false);
  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        <button
          onClick={() => setRunOpen(true)}
          className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700"
        >
          <Play size={12} /> Run orchestration
        </button>
      </div>
      {orchs.length === 0 ? (
        <EmptyTabState icon={Activity} label="No orchestrations have run on this case." hint="Click 'Run orchestration' above to launch one. The resulting run will be auto-linked to this case." />
      ) : (
        <div className="border border-border rounded-md bg-white overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-slate-50 border-b border-border text-[11px] uppercase tracking-wide text-ink-mute">
              <tr>
                <th className="px-3 py-2 text-left">Orchestration</th>
                <th className="px-3 py-2 text-right w-24">Runs</th>
                <th className="px-3 py-2 text-left w-40">Last started</th>
              </tr>
            </thead>
            <tbody>
              {orchs.map((o) => (
                <tr key={`${o.OrchestrationID.Int64}:${o.OrchestrationName}`} className="border-b border-border/60 last:border-0 hover:bg-slate-50/40">
                  <td className="px-3 py-2">
                    {o.OrchestrationID.Valid ? (
                      <Link to={`/orchestrations/${o.OrchestrationID.Int64}${cpInstanceID ? `?cp=${cpInstanceID}` : ''}`} className="text-brand-700 hover:underline">
                        {o.OrchestrationName}
                      </Link>
                    ) : (
                      <span className="text-ink-mute italic">{o.OrchestrationName}</span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-right text-xs font-mono text-ink-dim">{o.RunCount}</td>
                  <td className="px-3 py-2 text-xs text-ink-dim font-mono">{fmtDate(o.LastStartedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {runOpen && (
        <RunOrchestrationDialog
          invID={invID}
          cpInstanceID={cpInstanceID}
          onClose={() => setRunOpen(false)}
          onRan={() => { setRunOpen(false); onChange(); }}
        />
      )}
    </div>
  );
}

// ── Notes panel ─────────────────────────────────────────────────────

function NotesPanel({ invID, notes, onChange }: {
  invID: number;
  notes: InvestigationNote[];
  onChange: () => void;
}) {
  const [author, setAuthor] = useState('');
  const [body, setBody] = useState('');
  const [busy, setBusy] = useState(false);

  async function add() {
    if (!body.trim()) return;
    setBusy(true);
    try {
      await api.investigations.addNote(invID, author.trim() || 'operator', body.trim());
      setBody('');
      onChange();
    } finally {
      setBusy(false);
    }
  }

  const ordered = useMemo(
    () => [...notes].sort((a, b) => (a.CreatedAt < b.CreatedAt ? 1 : -1)),
    [notes],
  );

  return (
    <div className="border border-border rounded-md bg-white p-4">
      <div className="mb-4 space-y-2">
        <input
          type="text"
          value={author}
          onChange={(e) => setAuthor(e.target.value)}
          className="w-full px-2.5 py-1 rounded-md border border-border text-xs"
          placeholder="Author (defaults to 'operator')"
        />
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          rows={3}
          className="w-full px-2.5 py-1.5 rounded-md border border-border text-sm"
          placeholder="Add a note — observations, hypotheses, next steps…"
        />
        <div className="flex justify-end">
          <button
            onClick={add}
            disabled={busy || !body.trim()}
            className="inline-flex items-center gap-1 text-xs px-2.5 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <MessageSquarePlus size={12} /> Add note
          </button>
        </div>
      </div>

      {ordered.length === 0 ? (
        <div className="text-xs text-ink-mute italic">No notes yet — add the first one above.</div>
      ) : (
        <ul className="space-y-3">
          {ordered.map((n) => (
            <li key={n.ID} className="border border-border rounded-md bg-slate-50/50 p-3">
              <div className="flex items-center gap-2 text-[11px] text-ink-mute mb-1.5">
                <code className="font-mono text-ink-dim">{n.Author || 'operator'}</code>
                <span>·</span>
                <Clock size={10} />
                <span className="font-mono">{fmtDate(n.CreatedAt)}</span>
              </div>
              <div className="text-sm whitespace-pre-wrap text-ink">{n.Body}</div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ── Reusable bits ───────────────────────────────────────────────────

function EmptyTabState({ icon: Icon, label, hint }: { icon: typeof Hash; label: string; hint?: string }) {
  return (
    <div className="border border-border rounded-md bg-white p-8 text-center">
      <Icon size={28} className="mx-auto opacity-30 mb-2" />
      <div className="text-sm text-ink-dim mb-1">{label}</div>
      {hint && <div className="text-xs text-ink-mute max-w-md mx-auto">{hint}</div>}
    </div>
  );
}

function UnlinkButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      title="Unlink from this investigation"
      className="text-ink-mute hover:text-red-700 p-1 rounded hover:bg-red-50"
    >
      <Trash2 size={12} />
    </button>
  );
}

function SeverityChip({ sev }: { sev: string }) {
  const tone =
    sev === 'CRITICAL' ? 'text-red-700 bg-red-50 ring-red-200' :
    sev === 'HIGH'     ? 'text-orange-700 bg-orange-50 ring-orange-200' :
    sev === 'MEDIUM'   ? 'text-amber-700 bg-amber-50 ring-amber-200' :
    sev === 'LOW'      ? 'text-blue-700 bg-blue-50 ring-blue-200' :
                         'text-slate-600 bg-slate-50 ring-slate-200';
  return (
    <span className={cn('text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1', tone)}>
      {sev || '—'}
    </span>
  );
}

function StatusPill({ v }: { v: string }) {
  if (!v) return <span className="text-[10px] text-ink-mute">—</span>;
  const tone =
    v === 'open'           ? 'text-brand-700 bg-brand-50 ring-brand-200' :
    v === 'resolved'       ? 'text-green-700 bg-green-50 ring-green-200' :
    v === 'false_positive' ? 'text-slate-600 bg-slate-50 ring-slate-200' :
    v === 'wontfix'        ? 'text-orange-700 bg-orange-50 ring-orange-200' :
                             'text-ink-dim bg-slate-50 ring-slate-200';
  return <span className={cn('text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1', tone)}>{v.replace('_', ' ')}</span>;
}

function RunStatusChip({ s }: { s: string }) {
  const tone =
    s === 'completed'        ? 'text-green-700 bg-green-50 ring-green-200' :
    s === 'running'          ? 'text-brand-700 bg-brand-50 ring-brand-200' :
    s === 'failed'           ? 'text-red-700 bg-red-50 ring-red-200' :
    s === 'cancelled'        ? 'text-slate-600 bg-slate-50 ring-slate-200' :
    s === 'approval_required'? 'text-amber-700 bg-amber-50 ring-amber-200' :
                               'text-ink-dim bg-slate-50 ring-slate-200';
  return <span className={cn('text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1', tone)}>{s}</span>;
}

function nullStr(v: { String: string; Valid: boolean }): string {
  return v && v.Valid ? v.String : '';
}

function CloseDialog({ onClose, onPick, busy }: {
  onClose: () => void;
  onPick: (r: Resolution) => void;
  busy: boolean;
}) {
  return (
    <div className="fixed inset-0 z-50 bg-black/30 flex items-center justify-center" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-lg shadow-lg w-[420px] max-w-full"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-4 py-3 border-b border-border">
          <h3 className="text-sm font-semibold">Close investigation</h3>
          <p className="text-xs text-ink-dim mt-0.5">Pick a resolution. The case can be reopened later.</p>
        </header>
        <div className="p-3 space-y-1.5">
          {RESOLUTION_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => onPick(opt.value)}
              disabled={busy}
              className={cn(
                'w-full inline-flex items-center gap-2 text-sm px-3 py-2 rounded-md ring-1 ring-border hover:bg-slate-50 text-left disabled:opacity-50',
                opt.tone,
              )}
            >
              <opt.icon size={14} />
              <span>{opt.label}</span>
            </button>
          ))}
        </div>
        <footer className="px-4 py-3 border-t border-border flex items-center justify-end">
          <button
            onClick={onClose}
            className="text-xs px-2.5 py-1.5 rounded-md text-ink-dim hover:text-ink hover:bg-slate-100"
          >
            Cancel
          </button>
        </footer>
      </div>
    </div>
  );
}

function StatusChip({ status }: { status: Investigation['Status'] }) {
  const styles: Record<Investigation['Status'], string> = {
    active:   'text-brand-700 bg-brand-50 ring-brand-200',
    closed:   'text-green-700 bg-green-50 ring-green-200',
    archived: 'text-ink-mute bg-slate-100 ring-slate-200',
  };
  return (
    <span
      className={cn(
        'text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1',
        styles[status] ?? 'text-ink-mute bg-slate-100 ring-slate-200',
      )}
    >
      {status}
    </span>
  );
}

function ResolutionChip({ res }: { res: Investigation['Resolution'] }) {
  if (!res) return null;
  return (
    <span className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ring-1 ring-slate-200 bg-slate-50 text-ink-dim">
      {res}
    </span>
  );
}

function KV({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[110px_1fr] gap-2">
      <dt className="text-ink-mute">{label}</dt>
      <dd className="text-ink truncate">{children}</dd>
    </div>
  );
}

function fmtDate(iso: string): string {
  if (!iso || isZeroTime(iso)) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString(undefined, {
    month: 'short', day: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

function isZeroTime(iso: string): boolean {
  return iso === '' || iso === '0001-01-01T00:00:00Z';
}

// ── Workspace action dialogs ────────────────────────────────────────

function LinkFindingDialog({
  invID, cpInstanceID, alreadyLinkedIDs, onClose, onLinked,
}: {
  invID: number;
  cpInstanceID?: string;
  alreadyLinkedIDs: Set<number>;
  onClose: () => void;
  onLinked: () => void;
}) {
  const [items, setItems] = useState<Finding[] | null>(null);
  const [filter, setFilter] = useState('');
  const [pickedID, setPickedID] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.findings({ state: 'queue', limit: 200 })
      .then((rows) => {
        // Federated case → only allow linking findings on the same
        // child (FK on investigation_findings.finding_id wouldn't
        // resolve cross-CP). Local case → only local findings.
        const filtered = rows.filter((f) =>
          cpInstanceID
            ? (f as Finding & { cp_source?: { instance_id: string } }).cp_source?.instance_id === cpInstanceID
            : !(f as Finding & { cp_source?: unknown }).cp_source,
        );
        setItems(filtered);
      })
      .catch((e) => setError(String(e)));
  }, [cpInstanceID]);

  async function submit() {
    if (!pickedID) return;
    setBusy(true); setError(null);
    try {
      await api.investigations.linkFinding(invID, pickedID, cpInstanceID);
      onLinked();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  const filtered = (items ?? [])
    .filter((f) => !alreadyLinkedIDs.has(f.id))
    .filter((f) => !filter || (f.title || '').toLowerCase().includes(filter.toLowerCase()) || (f.host || '').toLowerCase().includes(filter.toLowerCase()));

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-lg flex flex-col"
        onClick={(e) => e.stopPropagation()}
        style={{ maxHeight: '75vh' }}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Hash size={14} className="text-brand-500" />
            Link finding
          </h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>
        <div className="p-5 space-y-3 text-sm flex flex-col flex-1 overflow-hidden">
          <p className="text-xs text-ink-dim">
            Pick a finding{cpInstanceID ? <> on <code className="font-mono">{cpInstanceID}</code></> : <> on this CP</>} to add to this investigation. Already-linked findings are filtered out. Showing the most recent 200 open findings; refine with the filter box.
          </p>
          <input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by title or host…"
            className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
          />
          <div className="flex-1 overflow-auto border border-border rounded-md">
            {items === null ? (
              <div className="p-3 text-xs text-ink-mute">Loading…</div>
            ) : filtered.length === 0 ? (
              <div className="p-3 text-xs text-ink-mute">No matching open findings.</div>
            ) : (
              <ul className="divide-y divide-border">
                {filtered.map((f) => (
                  <li key={f.id}>
                    <label className="flex items-start gap-2 px-3 py-2 cursor-pointer hover:bg-slate-50">
                      <input
                        type="radio"
                        checked={pickedID === f.id}
                        onChange={() => setPickedID(f.id)}
                        className="mt-1"
                      />
                      <div className="min-w-0 flex-1">
                        <div className="text-sm truncate">
                          <span className={cn(
                            'inline-block text-[10px] uppercase tracking-wide px-1 py-0.5 rounded ring-1 mr-2',
                            f.severity === 'CRITICAL' ? 'text-red-700 bg-red-50 ring-red-200' :
                            f.severity === 'HIGH' ? 'text-orange-700 bg-orange-50 ring-orange-200' :
                            f.severity === 'MEDIUM' ? 'text-amber-700 bg-amber-50 ring-amber-200' :
                            f.severity === 'LOW' ? 'text-blue-700 bg-blue-50 ring-blue-200' :
                            'text-slate-600 bg-slate-50 ring-slate-200',
                          )}>{f.severity || '—'}</span>
                          {f.title || `(no title) #${f.id}`}
                        </div>
                        <div className="text-[11px] text-ink-mute font-mono">
                          #{f.id} · {f.agent || '—'} · {f.host || '—'}
                        </div>
                      </div>
                    </label>
                  </li>
                ))}
              </ul>
            )}
          </div>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={busy || !pickedID}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy && <Loader2 size={12} className="animate-spin" />}
            Link
          </button>
        </footer>
      </div>
    </div>
  );
}

function RunOrchestrationDialog({
  invID, cpInstanceID, onClose, onRan,
}: {
  invID: number;
  cpInstanceID?: string;
  onClose: () => void;
  onRan: () => void;
}) {
  const [items, setItems] = useState<Orchestration[] | null>(null);
  const [pickedID, setPickedID] = useState<number | null>(null);
  const [filter, setFilter] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.orchestrations()
      .then((rows) => {
        // Federated case → only orchestrations defined on the same
        // child (a parent-side orchestration would create runs in
        // the wrong CP's row space).
        const cpFiltered = rows.filter((o) =>
          cpInstanceID
            ? (o as Orchestration & { cp_source?: { instance_id: string } }).cp_source?.instance_id === cpInstanceID
            : !(o as Orchestration & { cp_source?: unknown }).cp_source,
        );
        setItems(cpFiltered.filter((o) => o.enabled));
      })
      .catch((e) => setError(String(e)));
  }, [cpInstanceID]);

  async function submit() {
    if (!pickedID) return;
    setBusy(true); setError(null);
    try {
      // Manual trigger; pass `investigation_id` as an input so the
      // orchestration's prompt template can reference it via
      // `{{trigger.investigation_id}}` if it knows about the case
      // schema.
      const { run_id } = await api.orchestrationRun(
        pickedID,
        { investigation_id: invID },
        cpInstanceID,
      );
      // Auto-link the run to this case. Engine's link_run_to_finding
      // hook handles this automatically when steps emit the action,
      // but a brand-new run that hasn't yet emitted anything won't
      // be linked. Belt-and-suspenders: explicit link here too.
      await api.investigations.linkRun(invID, run_id, cpInstanceID);
      onRan();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  const filtered = (items ?? []).filter((o) =>
    !filter || o.name.toLowerCase().includes(filter.toLowerCase()) || (o.description || '').toLowerCase().includes(filter.toLowerCase()),
  );

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-lg flex flex-col"
        onClick={(e) => e.stopPropagation()}
        style={{ maxHeight: '75vh' }}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Play size={14} className="text-brand-500" />
            Run orchestration on this case
          </h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md">
            <X size={16} />
          </button>
        </header>
        <div className="p-5 space-y-3 text-sm flex flex-col flex-1 overflow-hidden">
          <p className="text-xs text-ink-dim">
            Triggers a manual run of the chosen orchestration. The investigation id is passed as a trigger input (<code className="font-mono">{`{{trigger.investigation_id}}`}</code>), and the resulting run is auto-linked to this case.
            {cpInstanceID && <> Showing orchestrations defined on <code className="font-mono">{cpInstanceID}</code>.</>}
          </p>
          <input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by name or description…"
            className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
          />
          <div className="flex-1 overflow-auto border border-border rounded-md">
            {items === null ? (
              <div className="p-3 text-xs text-ink-mute">Loading…</div>
            ) : filtered.length === 0 ? (
              <div className="p-3 text-xs text-ink-mute">
                No enabled orchestrations{cpInstanceID ? ` on ${cpInstanceID}` : ''}.
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {filtered.map((o) => (
                  <li key={o.id}>
                    <label className="flex items-start gap-2 px-3 py-2 cursor-pointer hover:bg-slate-50">
                      <input
                        type="radio"
                        checked={pickedID === o.id}
                        onChange={() => setPickedID(o.id)}
                        className="mt-1"
                      />
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-medium truncate">{o.name}</div>
                        <div className="text-[11px] text-ink-mute truncate">
                          {o.description || <span className="italic">(no description)</span>}
                        </div>
                      </div>
                    </label>
                  </li>
                ))}
              </ul>
            )}
          </div>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex items-center justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            onClick={submit}
            disabled={busy || !pickedID}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium inline-flex items-center gap-1.5"
          >
            {busy && <Loader2 size={12} className="animate-spin" />}
            Run
          </button>
        </footer>
      </div>
    </div>
  );
}
