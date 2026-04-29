import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  ArrowUpRight,
  Check,
  CheckCircle2,
  ChevronRight,
  Clock,
  Columns,
  Cpu,
  ExternalLink,
  Hash,
  Inbox,
  Layers,
  Lightbulb,
  List,
  Repeat,
  Server,
  ShieldAlert,
  Sparkles,
  Tag,
  ThumbsDown,
  X,
} from 'lucide-react';
import { api, type Finding, type FindingGroup, type FindingsSummary, type FindingStatus, type RunListItem } from '../api';
import { FindingHistory, History as HistoryIcon } from '../components/FindingHistory';
import { cn } from '../lib/cn';
import { SectionHeader, type SectionTone } from '../components/lists/SectionHeader';
import { ListCard } from '../components/lists/ListCard';
import { StatusMenu } from '../components/StatusMenu';
import { SeverityMenu, type SeverityChange } from '../components/SeverityMenu';
import { InvestigateDialog } from '../components/InvestigateDialog';
import { StatusPill } from '../components/StatusPill';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';
import { useSelection } from '../lib/useSelection';
import { BulkActionBar, BulkActionButton } from '../components/BulkActionBar';
import { CPSourceChip } from '../components/CPSourceChip';
import FindingsKanban from '../components/FindingsKanban';

const PAGE_SIZE = 250;

const SEV_TONE: Record<Sev, SectionTone> = {
  CRITICAL: 'critical',
  HIGH:     'bad',
  MEDIUM:   'warn',
  LOW:      'low',
  INFO:     'muted',
};

type State = 'open' | 'acked' | 'all';
type Sev = 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';
type View = 'grouped' | 'recent' | 'kanban';

const ALL_SEVS: Sev[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];

export default function FindingsPage() {
  const [findings, setFindings] = useState<Finding[] | null>(null);
  const [groups, setGroups] = useState<FindingGroup[] | null>(null);
  const [summary, setSummary] = useState<FindingsSummary | null>(null);
  const [view, setView] = useState<View>('grouped');
  const [state, setState] = useState<State>('open');
  const [selectedSevs, setSelectedSevs] = useState<Sev[]>([]);
  const [agentFilter, setAgentFilter] = useState('');
  const [hostFilter, setHostFilter] = useState('');
  const [categoryFilter, setCategoryFilter] = useState('');
  // ?id=N opens the detail drawer for finding N — used as a
  // deep-link target by Cmd-K and external bookmarks. Two-way bound:
  // closing the drawer drops the param so the URL stays canonical.
  const [params, setParams] = useSearchParams();
  const idParam = params.get('id');
  const selectedId = idParam !== null ? Number(idParam) : null;
  // ?cp=<instance_id> identifies which federated child a finding came
  // from; finding ids are scoped per-CP, so without this the parent's
  // /api/findings/{id} 404s on rows that originated on a child.
  const selectedCp = params.get('cp') || undefined;
  const setSelectedId = (id: number | null, cpInstanceID?: string) => {
    const p = new URLSearchParams(params);
    if (id === null) {
      p.delete('id');
      p.delete('cp');
    } else {
      p.set('id', String(id));
      if (cpInstanceID) p.set('cp', cpInstanceID);
      else p.delete('cp');
    }
    setParams(p, { replace: true });
  };
  const [error, setError] = useState<string | null>(null);
  const [hasMoreFindings, setHasMoreFindings] = useState(true);
  // Bulk selection — keyed by FindingGroup.group_key (only relevant in
  // the grouped view; flat "recent" view doesn't get bulk actions yet
  // because the action vocabulary changes per-finding rather than
  // per-group).
  const sel = useSelection<string>();
  const [bulkBusy, setBulkBusy] = useState<null | FindingStatus>(null);

  const refresh = useMemo(() => () => {
    api.findings({
      state,
      severity: selectedSevs.length ? selectedSevs : undefined,
      agent: agentFilter || undefined,
      host: hostFilter || undefined,
      category: categoryFilter || undefined,
      limit: PAGE_SIZE,
    })
      .then((list) => {
        setFindings(list);
        setHasMoreFindings(list.length >= PAGE_SIZE);
      })
      .catch((e) => setError(String(e)));
    // Grouped view always operates on open findings — that's its purpose
    // (operator focus on what still needs attention across the fleet).
    api.findingsGrouped({
      severity: selectedSevs.length ? selectedSevs : undefined,
      agent: agentFilter || undefined,
      host: hostFilter || undefined,
      category: categoryFilter || undefined,
      limit: 200,
    })
      .then(setGroups)
      .catch(() => { /* ignore — show empty */ });
    api.findingsSummary().then(setSummary).catch(() => { /* ignore */ });
  }, [state, selectedSevs, agentFilter, hostFilter, categoryFilter]);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 15_000);
    return () => clearInterval(t);
  }, [refresh]);

  // Lazy-load older findings when the bottom sentinel is visible. Pages
  // by `offset` against the same filter set; resets whenever a filter
  // changes (because `refresh` re-fetches with offset=0).
  const loadMoreFindings = async () => {
    if (!findings) return;
    const next = await api.findings({
      state,
      severity: selectedSevs.length ? selectedSevs : undefined,
      agent: agentFilter || undefined,
      host: hostFilter || undefined,
      category: categoryFilter || undefined,
      limit: PAGE_SIZE,
      offset: findings.length,
    });
    if (next.length === 0) {
      setHasMoreFindings(false);
      return;
    }
    setFindings((prev) => [...(prev ?? []), ...next]);
    if (next.length < PAGE_SIZE) setHasMoreFindings(false);
  };

  const findingsScroll = useInfiniteScroll({
    hasMore: view === 'recent' && hasMoreFindings,
    loadMore: loadMoreFindings,
  });

  function toggleSev(s: Sev) {
    setSelectedSevs((prev) => prev.includes(s) ? prev.filter(x => x !== s) : [...prev, s]);
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel">
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <ShieldAlert size={18} className="text-sev-critical" />
          Findings
        </h1>
        <p className="text-xs text-ink-dim">
          Structured security and operational findings emitted by daemon agents.
        </p>
      </header>

      {/* Dashboard: severity cards + per-agent breakdown + 24h trend */}
      {summary && (
        <div className="px-6 pt-4 grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2 grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-7 gap-3">
            <SummaryCard label="Open"     value={summary.open}     accent="brand"    />
            <SummaryCard label="Critical" value={summary.critical} accent="critical" />
            <SummaryCard label="High"     value={summary.high}     accent="high"     />
            <SummaryCard label="Medium"   value={summary.medium}   accent="medium"   />
            <SummaryCard label="Low"      value={summary.low}      accent="low"      />
            <SummaryCard label="Info"     value={summary.info}     accent="info"     />
            <SummaryCard label="Last 24h" value={summary.last_24h} accent="ink"      />
          </div>
          <TrendPanel buckets={summary.trend ?? []} total24h={summary.last_24h} />
          {((summary.by_agent ?? []).length > 0 || (summary.by_category ?? []).length > 0) && (
            <div className="lg:col-span-3 grid grid-cols-1 lg:grid-cols-2 gap-4">
              {(summary.by_category ?? []).length > 0 && (
                <ChipPanel
                  title="Open findings by category"
                  rows={(summary.by_category ?? []).map((c) => ({
                    key: c.category,
                    label: CATEGORY_LABELS[c.category] ?? c.category,
                    open: c.open,
                    critical: c.critical,
                    high: c.high,
                  }))}
                  activeKey={categoryFilter}
                  onPick={(c) => setCategoryFilter(c === categoryFilter ? '' : c)}
                />
              )}
              {(summary.by_agent ?? []).length > 0 && (
                <ChipPanel
                  title="Open findings by agent"
                  rows={(summary.by_agent ?? []).map((a) => ({
                    key: a.agent,
                    label: a.agent,
                    open: a.open,
                    critical: a.critical,
                    high: a.high,
                  }))}
                  activeKey={agentFilter}
                  onPick={(a) => setAgentFilter(a === agentFilter ? '' : a)}
                />
              )}
            </div>
          )}
        </div>
      )}

      {/* Filter bar */}
      <div className="px-6 py-3 flex flex-wrap items-center gap-3 border-b border-border bg-panel/40">
        {/* View toggle: Grouped (one row per issue, multi-host) | Recent (flat list) */}
        <div className="flex items-center gap-1 rounded-lg bg-slate-100 p-0.5">
          <button
            onClick={() => setView('grouped')}
            className={cn(
              'inline-flex items-center gap-1 px-2.5 py-1 text-xs font-medium rounded-md',
              view === 'grouped' ? 'bg-panel text-ink shadow-sm' : 'text-ink-dim hover:text-ink',
            )}
            title="Collapse repeated issues (same dedup key) across hosts into one row"
          >
            <Layers size={11} /> Grouped
          </button>
          <button
            onClick={() => setView('recent')}
            className={cn(
              'inline-flex items-center gap-1 px-2.5 py-1 text-xs font-medium rounded-md',
              view === 'recent' ? 'bg-panel text-ink shadow-sm' : 'text-ink-dim hover:text-ink',
            )}
            title="One row per occurrence, newest first"
          >
            <List size={11} /> Recent
          </button>
          <button
            onClick={() => setView('kanban')}
            className={cn(
              'inline-flex items-center gap-1 px-2.5 py-1 text-xs font-medium rounded-md',
              view === 'kanban' ? 'bg-panel text-ink shadow-sm' : 'text-ink-dim hover:text-ink',
            )}
            title="Drag-and-drop board: move issues between status columns"
          >
            <Columns size={11} /> Kanban
          </button>
        </div>

        {view === 'recent' && (
        <div className="flex items-center gap-1 rounded-lg bg-slate-100 p-0.5">
          {(['open', 'acked', 'all'] as State[]).map((s) => (
            <button
              key={s}
              onClick={() => setState(s)}
              className={cn(
                'px-2.5 py-1 text-xs font-medium rounded-md capitalize',
                state === s ? 'bg-panel text-ink shadow-sm' : 'text-ink-dim hover:text-ink'
              )}
            >
              {s}
            </button>
          ))}
        </div>
        )}

        <div className="flex items-center gap-1.5">
          {ALL_SEVS.map((s) => {
            const active = selectedSevs.includes(s);
            return (
              <button
                key={s}
                onClick={() => toggleSev(s)}
                className={cn(
                  'severity-badge text-xs',
                  active ? `severity-${s.toLowerCase()}` : 'bg-slate-50 text-ink-mute ring-1 ring-slate-200',
                  'hover:opacity-80 transition-opacity'
                )}
              >
                {s}
              </button>
            );
          })}
        </div>

        <input
          type="search"
          placeholder="Agent name…"
          value={agentFilter}
          onChange={(e) => setAgentFilter(e.target.value)}
          className="px-2.5 py-1 text-xs border border-border rounded-md w-40 focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
        <input
          type="search"
          placeholder="Host…"
          value={hostFilter}
          onChange={(e) => setHostFilter(e.target.value)}
          className="px-2.5 py-1 text-xs border border-border rounded-md w-40 focus:outline-none focus:ring-2 focus:ring-brand-500/30"
        />
        {categoryFilter && (
          <button
            onClick={() => setCategoryFilter('')}
            className="inline-flex items-center gap-1 text-[11px] bg-brand-50 text-brand-700 ring-1 ring-brand-200 hover:bg-brand-100 px-1.5 py-1 rounded-md"
          >
            <Tag size={10} /> {categoryFilter} <X size={10} />
          </button>
        )}
        <span className="ml-auto text-[11px] text-ink-mute">
          {findings ? `${findings.length} result${findings.length === 1 ? '' : 's'}` : ''}
        </span>
      </div>

      {/* Body: list + drawer */}
      <div className="flex-1 flex overflow-hidden">
        <div className={cn('flex-1', view === 'kanban' ? 'overflow-hidden' : 'overflow-auto')}>
          {error && view !== 'kanban' && (
            <div className="m-6 text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}
          {view === 'kanban' && (
            <FindingsKanban
              agent={agentFilter || undefined}
              host={hostFilter || undefined}
              category={categoryFilter || undefined}
              severity={selectedSevs.length ? selectedSevs : undefined}
              onChanged={refresh}
              onOpenCard={(id, cp) => setSelectedId(id, cp)}
            />
          )}
          {view === 'recent' && findings === null && <div className="p-6 text-ink-mute">Loading…</div>}
          {view === 'recent' && findings && findings.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full text-ink-mute">
              <Inbox size={32} className="mb-2 opacity-50" />
              <p className="text-sm">No findings match the current filters.</p>
            </div>
          )}
          {view === 'recent' && findings && findings.length > 0 && (
            <>
            <ul className="divide-y divide-border">
              {findings.map((f) => (
                <li
                  key={f.id}
                  onClick={() => setSelectedId(f.id, f.cp_source?.instance_id)}
                  className={cn(
                    'cursor-pointer hover:bg-slate-50/60 flex items-stretch',
                    selectedId === f.id && 'bg-brand-50/40',
                  )}
                >
                  <span className={cn('w-1 shrink-0', sevBar(f.severity))} />
                  <div className="px-5 py-3 flex items-start gap-3 flex-1 min-w-0">
                    {f.severity && (
                      <span className={cn('severity-badge mt-0.5 shrink-0', `severity-${f.severity.toLowerCase()}`)}>
                        {f.severity}
                      </span>
                    )}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-baseline gap-2">
                        <span className={cn('text-sm font-medium truncate', f.acknowledged && 'line-through text-ink-mute')}>
                          {f.title || '(untitled)'}
                        </span>
                        {f.acknowledged && (
                          <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 px-1.5 py-0.5 rounded">
                            ack
                          </span>
                        )}
                        <CPSourceChip source={f.cp_source} />
                      </div>
                      <div className="mt-0.5 text-xs text-ink-dim flex items-center gap-3 flex-wrap">
                        <span className="font-mono">{f.agent || '—'}</span>
                        <span>·</span>
                        <span className="font-mono">{f.host || '—'}</span>
                        {f.resource && <><span>·</span><span className="font-mono truncate">{f.resource}</span></>}
                        <span className="ml-auto text-ink-mute">{fmtTime(f.ts)}</span>
                      </div>
                    </div>
                    <ChevronRight size={14} className="text-ink-mute mt-1 shrink-0" />
                  </div>
                </li>
              ))}
            </ul>
            <div ref={findingsScroll.sentinelRef} className="h-6 flex items-center justify-center text-[11px] text-ink-mute">
              {findingsScroll.loading
                ? 'loading older findings…'
                : hasMoreFindings
                  ? 'scroll for more'
                  : `— end of ${findings.length} findings —`}
            </div>
            </>
          )}

          {view === 'grouped' && groups === null && <div className="p-6 text-ink-mute">Loading…</div>}
          {view === 'grouped' && groups && groups.length === 0 && (
            <div className="flex flex-col items-center justify-center h-full text-ink-mute">
              <Inbox size={32} className="mb-2 opacity-50" />
              <p className="text-sm">No open issues match the current filters.</p>
              <p className="text-xs mt-1 max-w-md text-center">
                Switch to <strong>Recent</strong> above to see acknowledged findings, or relax the severity filter.
              </p>
            </div>
          )}
          {view === 'grouped' && groups && groups.length > 0 && (
            <div className="px-6 py-4 space-y-4">
              {ALL_SEVS.map((sev) => {
                const list = groups.filter((g) => (g.severity || 'INFO').toUpperCase() === sev);
                if (list.length === 0) return null;
                const totalOccurrences = list.reduce((acc, g) => acc + g.count, 0);
                const sevKeys = list.map((g) => g.group_key);
                const allSelected = sevKeys.length > 0 && sevKeys.every((k) => sel.isSelected(k));
                const someSelected = sevKeys.some((k) => sel.isSelected(k));
                return (
                  <section key={sev}>
                    <SectionHeader
                      tone={SEV_TONE[sev]}
                      label={sev}
                      count={list.length}
                      hint={
                        totalOccurrences !== list.length
                          ? `${totalOccurrences} occurrence${totalOccurrences === 1 ? '' : 's'}`
                          : undefined
                      }
                      leading={
                        <input
                          type="checkbox"
                          checked={allSelected}
                          ref={(el) => { if (el) el.indeterminate = !allSelected && someSelected; }}
                          onChange={(e) => sel.setMany(sevKeys, e.target.checked)}
                          title={allSelected ? `Deselect all ${sev}` : `Select all ${sev}`}
                          className="rounded border-border text-brand-500 focus:ring-brand-500/30 cursor-pointer"
                        />
                      }
                    />
                    <ListCard>
                      {list.map((g) => (
                        <GroupRow
                          key={g.group_key}
                          g={g}
                          selected={sel.isSelected(g.group_key)}
                          onToggleSelected={(on) => sel.set(g.group_key, on)}
                          onOpen={() => setSelectedId(g.latest_id, g.cp_source?.instance_id ?? g.cp_sources?.[0]?.instance_id)}
                          onPickAgent={(a) => setAgentFilter(a)}
                          onPickHost={(h) => setHostFilter(h)}
                          onAcked={refresh}
                        />
                      ))}
                    </ListCard>
                  </section>
                );
              })}

              {/* Bulk action bar — visible when ≥ 1 group selected. */}
              <BulkActionBar count={sel.count} onClear={sel.clear} label="Groups">
                <BulkActionButton
                  tone="good"
                  icon={Check}
                  label="Acknowledge"
                  busy={bulkBusy === 'acknowledged'}
                  disabled={!!bulkBusy}
                  title={`Acknowledge ${sel.count} group${sel.count === 1 ? '' : 's'}`}
                  onClick={async () => {
                    setBulkBusy('acknowledged');
                    await bulkSetGroupStatus(sel.all, 'acknowledged', groups ?? []);
                    setBulkBusy(null); sel.clear(); refresh();
                  }}
                />
                <BulkActionButton
                  tone="good"
                  icon={CheckCircle2}
                  label="Resolve"
                  busy={bulkBusy === 'resolved'}
                  disabled={!!bulkBusy}
                  title={`Mark ${sel.count} group${sel.count === 1 ? '' : 's'} resolved`}
                  onClick={async () => {
                    setBulkBusy('resolved');
                    await bulkSetGroupStatus(sel.all, 'resolved', groups ?? []);
                    setBulkBusy(null); sel.clear(); refresh();
                  }}
                />
                <BulkActionButton
                  tone="warn"
                  icon={ThumbsDown}
                  label="False positive"
                  busy={bulkBusy === 'false_positive'}
                  disabled={!!bulkBusy}
                  title={`Mark ${sel.count} group${sel.count === 1 ? '' : 's'} as false positive`}
                  onClick={async () => {
                    setBulkBusy('false_positive');
                    await bulkSetGroupStatus(sel.all, 'false_positive', groups ?? []);
                    setBulkBusy(null); sel.clear(); refresh();
                  }}
                />
              </BulkActionBar>
            </div>
          )}
        </div>

        {selectedId !== null && (
          <FindingDrawer
            id={selectedId}
            cpInstanceID={selectedCp}
            onClose={() => setSelectedId(null)}
            onChanged={refresh}
          />
        )}
      </div>
    </div>
  );
}

// Fan-out POST `/api/findings/group/status` across the selection. Uses
// allSettled so a single failed group doesn't abort the rest. The
// caller is expected to refresh() afterward — the response here is just
// {changed, status} per call and doesn't carry the updated FindingGroup
// row, so we can't splice the local list optimistically the way Daimons
// does.
async function bulkSetGroupStatus(
  groupKeys: string[],
  status: FindingStatus,
  groups: FindingGroup[],
) {
  const byKey = new Map(groups.map((g) => [g.group_key, g]));
  const targets = groupKeys.map((k) => byKey.get(k)).filter((g): g is FindingGroup => !!g);
  await Promise.allSettled(
    targets.map((g) => api.setGroupStatus({
      dedup_key: g.dedup_key,
      title: g.title,
      severity: g.severity,
      agent: g.agent,
      status,
      note: 'bulk action via UI',
    })),
  );
}

// ── Trend sparkline ────────────────────────────────────────────────────────

function TrendPanel({ buckets, total24h }: { buckets: { hour_ts: number; count: number }[]; total24h: number }) {
  const max = Math.max(1, ...buckets.map((b) => b.count));
  const peak = buckets.reduce((a, b) => (b.count > a.count ? b : a), buckets[0] ?? { hour_ts: 0, count: 0 });
  return (
    <div className="bg-panel border border-border rounded-xl px-4 py-3 shadow-card flex flex-col">
      <div className="flex items-baseline justify-between">
        <div>
          <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">Last 24h trend</div>
          <div className="text-2xl font-semibold mt-0.5">{total24h.toLocaleString()}</div>
        </div>
        {peak.count > 0 && (
          <div className="text-[11px] text-ink-mute text-right">
            peak {peak.count} <span className="opacity-70">@ {fmtHour(peak.hour_ts)}</span>
          </div>
        )}
      </div>
      <div className="mt-3 flex-1 flex items-end gap-px h-10">
        {buckets.map((b) => {
          const h = b.count === 0 ? 2 : Math.max(2, Math.round((b.count / max) * 36));
          return (
            <div
              key={b.hour_ts}
              title={`${fmtHour(b.hour_ts)} — ${b.count} finding${b.count === 1 ? '' : 's'}`}
              className={cn(
                'flex-1 rounded-sm transition-colors',
                b.count === 0 ? 'bg-slate-100' : 'bg-brand-400 hover:bg-brand-500',
              )}
              style={{ height: `${h}px` }}
            />
          );
        })}
      </div>
    </div>
  );
}

function fmtHour(ms: number): string {
  return new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
}

// (Per-agent / per-category breakdowns are rendered via ChipPanel below.)

// ── Pill-chip filter rows ──────────────────────────────────────────────────
//
// Compact horizontal flex of pill chips, one per agent or per category.
// Click toggles the corresponding filter on the Findings list. Active chip
// renders in brand color; passive in slate. Replaces the older
// stacked-progress-bar treatment which was visually heavy and hard to scan
// at a glance.

const CATEGORY_LABELS: Record<string, string> = {
  process:           'Process',
  file:              'File',
  network:           'Network',
  cert:              'Certificate',
  cloud:             'Cloud',
  identity:          'Identity',
  config:            'Config',
  other:             'Other',
  '(uncategorized)': 'Uncategorized',
};

interface ChipRow {
  key: string;
  label: string;
  open: number;
  critical: number;
  high: number;
}

function ChipPanel({
  title, rows, activeKey, onPick,
}: {
  title: string;
  rows: ChipRow[];
  activeKey: string;
  onPick: (k: string) => void;
}) {
  if (rows.length === 0) return null;
  return (
    <div className="bg-panel border border-border rounded-xl px-4 py-3 shadow-card">
      <div className="flex items-baseline justify-between mb-2">
        <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">
          {title}
        </div>
        <div className="text-[11px] text-ink-mute">click to filter</div>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {rows.map((r) => (
          <FilterChip
            key={r.key}
            label={r.label}
            count={r.open}
            critical={r.critical}
            high={r.high}
            active={activeKey === r.key}
            onClick={() => onPick(r.key)}
          />
        ))}
      </div>
    </div>
  );
}

function FilterChip({
  label, count, critical, high, active, onClick,
}: {
  label: string;
  count: number;
  critical: number;
  high: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs ring-1 transition-colors',
        active
          ? 'bg-brand-500 text-white ring-brand-500 shadow-sm'
          : 'bg-slate-50 text-ink ring-slate-200 hover:bg-slate-100',
      )}
    >
      <span className="font-medium">{label}</span>
      <span className={cn('tabular-nums', active ? 'text-white/90' : 'text-ink-dim')}>
        {count}
      </span>
      {(critical > 0 || high > 0) && !active && (
        <span className="inline-flex items-center gap-0.5 ml-0.5">
          {critical > 0 && (
            <span className="w-1.5 h-1.5 rounded-full bg-sev-critical" title={`${critical} critical`} />
          )}
          {high > 0 && (
            <span className="w-1.5 h-1.5 rounded-full bg-sev-high" title={`${high} high`} />
          )}
        </span>
      )}
    </button>
  );
}

// ── Grouped row ────────────────────────────────────────────────────────────

interface GroupRowProps {
  g: FindingGroup;
  selected: boolean;
  onToggleSelected: (on: boolean) => void;
  onOpen: () => void;
  onPickAgent: (a: string) => void;
  onPickHost: (h: string) => void;
  onAcked: () => void;
}

function GroupRow({ g, selected, onToggleSelected, onOpen, onPickAgent, onPickHost, onAcked }: GroupRowProps) {
  const sev = (g.severity || 'INFO').toLowerCase();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const stop = (e: React.MouseEvent) => { e.stopPropagation(); e.preventDefault(); };

  // Open groups always show as "open" — non-open findings are already
  // filtered out by the grouped query. The menu is the operator's way to
  // change status for the WHOLE group in one click.
  async function setStatus(status: FindingStatus, note: string) {
    setBusy(true); setError(null);
    try {
      await api.setGroupStatus({
        dedup_key: g.dedup_key,
        title: g.title,
        severity: g.severity,
        agent: g.agent,
        status,
        note,
      });
      onAcked();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  // Whole row is clickable. Inline buttons (agent / host filter pills,
  // Ack-all) call stopPropagation so they don't trigger row navigation.
  return (
    <button
      type="button"
      onClick={onOpen}
      className="w-full text-left flex items-stretch hover:bg-slate-50/60 transition-colors"
    >
      <span className={cn('w-1 shrink-0', sevBar(g.severity))} />
      <div className="pl-4 pt-3.5 shrink-0">
        <input
          type="checkbox"
          checked={selected}
          onChange={(e) => onToggleSelected(e.target.checked)}
          onClick={(e) => e.stopPropagation()}
          onMouseDown={(e) => e.stopPropagation()}
          aria-label={`Select ${g.title || 'finding group'}`}
          className="rounded border-border text-brand-500 focus:ring-brand-500/30 cursor-pointer"
        />
      </div>
      <div className="px-4 py-3 flex items-start gap-3 flex-1 min-w-0">
        <span className={cn('severity-badge mt-0.5 shrink-0', `severity-${sev}`)}>
          {g.severity || 'INFO'}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2 flex-wrap">
            <span className="text-sm font-medium truncate">
              {g.title || '(untitled)'}
            </span>
            {g.count > 1 && (
              <span
                className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-slate-100 text-ink-dim ring-1 ring-slate-200 tabular-nums"
                title={`${g.count} occurrences across ${g.hosts.length} host${g.hosts.length === 1 ? '' : 's'}`}
              >
                ×{g.count}
              </span>
            )}
            <CPSourceChip source={g.cp_source} />
            {g.cp_sources && g.cp_sources.map((s) => (
              <CPSourceChip key={s.instance_id} source={s} />
            ))}
          </div>
          <div className="mt-0.5 text-xs text-ink-dim flex items-center gap-2 flex-wrap">
            {g.agent && (
              <span
                role="button"
                tabIndex={0}
                onClick={(e) => { stop(e); onPickAgent(g.agent!); }}
                className="font-mono cursor-pointer hover:text-ink hover:underline"
              >
                {g.agent}
              </span>
            )}
            {g.hosts.length > 0 && (
              <>
                <span className="text-ink-mute">on</span>
                {g.hosts.slice(0, 4).map((h) => (
                  <span
                    key={h}
                    role="button"
                    tabIndex={0}
                    onClick={(e) => { stop(e); onPickHost(h); }}
                    className="inline-flex items-center gap-1 font-mono text-[11px] bg-slate-50 ring-1 ring-slate-200 hover:ring-brand-300 cursor-pointer px-1.5 py-0.5 rounded"
                  >
                    <Server size={9} /> {h}
                  </span>
                ))}
                {g.hosts.length > 4 && (
                  <span className="text-ink-mute text-[11px]">+{g.hosts.length - 4} more</span>
                )}
              </>
            )}
            {g.resource && (
              <>
                <span className="text-ink-mute">·</span>
                <span className="font-mono truncate">{g.resource}</span>
              </>
            )}
            <span className="ml-auto text-ink-mute">{fmtTime(g.last_seen)}</span>
          </div>
          {error && <div className="mt-1 text-[11px] text-red-700">{error}</div>}
        </div>
        <div className="flex items-center gap-1.5 shrink-0" onClick={stop}>
          <StatusMenu current="open" onPick={setStatus} compact withNote busy={busy} />
          <ChevronRight size={14} className="text-ink-mute" />
        </div>
      </div>
    </button>
  );
}

interface SummaryCardProps {
  label: string;
  value: number;
  accent: 'brand' | 'critical' | 'high' | 'medium' | 'low' | 'info' | 'ink';
}

function SummaryCard({ label, value, accent }: SummaryCardProps) {
  const accents: Record<SummaryCardProps['accent'], string> = {
    brand:    'text-brand-600',
    critical: 'text-sev-critical',
    high:     'text-sev-high',
    medium:   'text-sev-medium',
    low:      'text-sev-low',
    info:     'text-sev-info',
    ink:      'text-ink',
  };
  return (
    <div className="bg-panel border border-border rounded-xl px-4 py-3 shadow-card">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">{label}</div>
      <div className={cn('text-2xl font-semibold mt-0.5', accents[accent])}>{value.toLocaleString()}</div>
    </div>
  );
}

interface DrawerProps {
  id: number;
  /** When the finding originated on a federated child CP, pass that
   *  child's instance_id so detail/runs lookups proxy to the right
   *  store. Local rows leave this undefined. */
  cpInstanceID?: string;
  onClose: () => void;
  onChanged: () => void;
}

export function FindingDrawer({ id, cpInstanceID, onClose, onChanged }: DrawerProps) {
  const [f, setF] = useState<Finding | null>(null);
  const [related, setRelated] = useState<Finding[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showRaw, setShowRaw] = useState(false);
  const [investigations, setInvestigations] = useState<RunListItem[]>([]);
  const [investigateOpen, setInvestigateOpen] = useState(false);

  useEffect(() => {
    setF(null);
    setRelated([]);
    setShowRaw(false);
    api.finding(id, cpInstanceID).then(setF).catch((e) => setError(String(e)));
    api.runsForFinding(id, cpInstanceID).then(setInvestigations).catch(() => setInvestigations([]));
  }, [id, cpInstanceID]);

  function refreshInvestigations() {
    api.runsForFinding(id, cpInstanceID).then(setInvestigations).catch(() => { /* ignore */ });
  }

  // Look up related findings (same dedup_key) once we have the finding.
  useEffect(() => {
    if (!f?.dedup_key) return;
    let cancelled = false;
    api.findings({ state: 'all', limit: 200 })
      .then((all) => {
        if (cancelled) return;
        const same = all.filter((x) => x.dedup_key === f.dedup_key && x.id !== f.id);
        setRelated(same);
      })
      .catch(() => { /* ignore */ });
    return () => { cancelled = true; };
  }, [f?.dedup_key, f?.id]);

  async function setStatus(status: FindingStatus, note: string) {
    setBusy(true); setError(null);
    try { setF(await api.setFindingStatus(id, status, { note, cpInstanceID })); onChanged(); }
    catch (e) { setError(String(e)); }
    finally { setBusy(false); }
  }

  async function setSeverity(change: SeverityChange) {
    setBusy(true); setError(null);
    try {
      setF(await api.setFindingSeverity(id, {
        severity: change.severity,
        apply_to_fingerprint: change.applyToFingerprint,
        apply_to_group: change.applyToGroup,
        note: change.note,
      }));
      onChanged();
    } catch (e) { setError(String(e)); }
    finally { setBusy(false); }
  }

  if (!f && error) {
    return (
      <aside className="w-[520px] shrink-0 border-l border-border bg-panel flex flex-col">
        <header className="px-5 py-3 border-b border-border flex items-start justify-between gap-3">
          <h2 className="text-sm font-semibold">Error</h2>
          <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink hover:bg-slate-100 rounded-md"><X size={16} /></button>
        </header>
        <div className="m-5 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
      </aside>
    );
  }
  if (!f) {
    return (
      <aside className="w-[520px] shrink-0 border-l border-border bg-panel flex flex-col">
        <div className="p-6 text-ink-mute text-sm">Loading…</div>
      </aside>
    );
  }

  const sev = (f.severity || 'info').toLowerCase();
  const evidenceLines = parseEvidence(f);
  const recommendedAction = stringFromRaw(f.raw, 'recommended_action');
  const allOccurrences = [f, ...related].sort((a, b) => b.ts - a.ts);
  const firstSeen = [...allOccurrences].sort((a, b) => a.ts - b.ts)[0];
  const isDuplicate = related.length > 0;

  return (
    <aside className="w-[520px] shrink-0 border-l border-border bg-panel flex flex-col">
      {/* Severity-colored top bar */}
      <div className={cn('h-1 shrink-0', sevBar(f.severity))} />

      <header className="px-5 py-4 border-b border-border flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 mb-1.5">
            <SeverityMenu
              current={f.severity}
              original={f.original_severity}
              onPick={setSeverity}
              busy={busy}
            />
            {f.acknowledged && (
              <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded inline-flex items-center gap-1">
                <Check size={9} /> acknowledged
              </span>
            )}
            {isDuplicate && (
              <span className="text-[10px] uppercase tracking-wide text-brand-700 bg-brand-50 ring-1 ring-brand-200 px-1.5 py-0.5 rounded inline-flex items-center gap-1">
                <Repeat size={9} /> {related.length + 1}× seen
              </span>
            )}
          </div>
          <h2 className={cn(
            'text-base font-semibold leading-tight',
            f.acknowledged && 'line-through text-ink-mute',
            sev === 'critical' && 'text-sev-critical',
            sev === 'high' && 'text-sev-high',
            sev === 'medium' && 'text-sev-medium',
            sev === 'low' && 'text-sev-low',
            sev === 'info' && 'text-ink',
          )}>
            {f.title || '(untitled)'}
          </h2>
        </div>
        <button onClick={onClose} className="p-1 text-ink-dim hover:text-ink hover:bg-slate-100 rounded-md shrink-0">
          <X size={16} />
        </button>
      </header>

      {error && (
        <div className="mx-5 mt-4 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      )}

      <div className="flex-1 overflow-auto p-5 space-y-5 text-sm">
        {/* Identity strip */}
        <div className="grid grid-cols-2 gap-3 text-xs">
          <DetailTile icon={Cpu} label="Agent">
            <Link to={`/daimons/${encodeURIComponent(f.agent || '')}`} className="text-ink hover:text-brand-600 inline-flex items-center gap-1">
              {f.agent || '—'}
              {f.agent && <ExternalLink size={9} />}
            </Link>
          </DetailTile>
          <DetailTile icon={Server} label="Host">
            <span className="font-mono">{f.host || '—'}</span>
          </DetailTile>
          <DetailTile icon={Clock} label="First seen">
            {firstSeen ? <span className="font-mono">{fmtFull(firstSeen.ts)}</span> : <span>—</span>}
          </DetailTile>
          <DetailTile icon={Clock} label="Most recent">
            <span className="font-mono">{fmtFull(f.ts)}</span>
          </DetailTile>
        </div>

        {/* Resource */}
        {f.resource && (
          <Section icon={Tag} title="Affected resource">
            <code className="font-mono text-xs bg-slate-100 text-slate-800 px-2 py-1 rounded inline-block break-all">
              {f.resource}
            </code>
          </Section>
        )}

        {/* Evidence */}
        {evidenceLines.length > 0 && (
          <Section icon={ShieldAlert} title="Evidence" count={evidenceLines.length}>
            <ul className="space-y-1">
              {evidenceLines.map((line, i) => (
                <li key={i} className="font-mono text-[11px] bg-slate-50 border border-border rounded-md px-2.5 py-1.5 whitespace-pre-wrap break-all">
                  {line}
                </li>
              ))}
            </ul>
          </Section>
        )}

        {/* Recommended action */}
        {recommendedAction && (
          <Section icon={Lightbulb} title="Recommended action">
            <div className={cn(
              'text-sm rounded-lg p-3 border-l-4',
              sev === 'critical' && 'bg-purple-50/60 border-sev-critical text-purple-900',
              sev === 'high'     && 'bg-red-50/60 border-sev-high text-red-900',
              sev === 'medium'   && 'bg-orange-50/60 border-sev-medium text-orange-900',
              sev === 'low'      && 'bg-yellow-50/60 border-sev-low text-yellow-900',
              sev === 'info'     && 'bg-slate-50 border-slate-400 text-ink',
            )}>
              {recommendedAction}
            </div>
          </Section>
        )}

        {/* Dedup key & lineage */}
        <Section icon={Hash} title="Identity">
          <dl className="text-xs space-y-1">
            <KV label="Dedup key">
              <code className="font-mono text-ink-dim">{f.dedup_key || <span className="italic">none</span>}</code>
            </KV>
            <KV label="Event ID">
              <code className="font-mono text-ink-dim">#{f.event_id}</code>
            </KV>
            <KV label="Finding ID">
              <code className="font-mono text-ink-dim">#{f.id}</code>
            </KV>
          </dl>
        </Section>

        {/* Related findings */}
        {related.length > 0 && (
          <Section icon={Repeat} title="Related occurrences" count={related.length}>
            <ul className="space-y-1">
              {related.slice(0, 8).map((r) => (
                <li
                  key={r.id}
                  className="flex items-center gap-2 text-xs px-2 py-1.5 rounded-md bg-slate-50 hover:bg-slate-100"
                >
                  <span className={cn('w-1.5 h-1.5 rounded-full', sevDotBg(r.severity))} />
                  <span className="font-mono text-ink-mute">{fmtShort(r.ts)}</span>
                  <span className="font-mono text-ink-dim truncate">{r.host || '—'}</span>
                  {r.acknowledged && <span className="ml-auto text-[10px] text-ink-mute">ack</span>}
                </li>
              ))}
              {related.length > 8 && (
                <li className="text-[11px] text-ink-mute pl-3">+ {related.length - 8} more</li>
              )}
            </ul>
          </Section>
        )}

        {/* Triage metadata */}
        {f.status && f.status !== 'open' && (
          <Section icon={Check} title="Triage">
            <div className="text-xs bg-slate-50 border border-slate-200 rounded-md p-3 space-y-1">
              <div className="flex items-center gap-2">
                <StatusPill status={f.status as FindingStatus} />
                {f.triaged_at && (
                  <span className="text-ink-mute">
                    at <span className="font-mono">{f.triaged_at}</span>
                  </span>
                )}
                {f.triaged_by_email && (
                  <span className="text-ink-mute">by <code>{f.triaged_by_email}</code></span>
                )}
              </div>
              {f.triage_note && (
                <div className="italic text-ink-dim">&ldquo;{f.triage_note}&rdquo;</div>
              )}
              <div className="text-[11px] text-ink-mute pt-1 border-t border-slate-200">
                The daemon's <code>{f.agent}</code> instances pull this triage every ~60s.
                {f.status === 'false_positive' && ' Future occurrences of this fingerprint are silently suppressed.'}
                {(f.status === 'acknowledged' || f.status === 'investigating' || f.status === 'resolved' || f.status === 'wontfix') &&
                  ' Future occurrences are suppressed until you reopen.'}
              </div>
            </div>
          </Section>
        )}

        {/* Investigations attached to this finding */}
        {investigations.length > 0 && (
          <Section icon={Sparkles} title="Investigations" count={investigations.length}>
            <ul className="space-y-1.5">
              {investigations.map((r) => (
                <li key={r.id}>
                  <Link
                    to={`/runs?run=${encodeURIComponent(r.id)}`}
                    className={cn(
                      'block px-2.5 py-2 rounded-md ring-1 hover:bg-slate-50/60',
                      r.status === 'running'   && 'ring-brand-200 bg-brand-50/30',
                      r.status === 'succeeded' && 'ring-green-200 bg-green-50/30',
                      r.status === 'failed'    && 'ring-red-200 bg-red-50/30',
                      r.status === 'cancelled' && 'ring-slate-200 bg-slate-50/30',
                    )}
                  >
                    <div className="flex items-center gap-2">
                      <code className="text-[11px] text-ink-dim font-mono">{r.id.slice(-8)}</code>
                      <span className="text-[10px] uppercase tracking-wide text-ink-mute">{r.status}</span>
                      {r.agent && <span className="text-xs">· {r.agent}</span>}
                      <span className="ml-auto text-[10px] text-ink-mute">{fmtAge(r.started_at)}</span>
                    </div>
                    {r.prompt && (
                      <p className="text-[11px] text-ink-dim mt-0.5 line-clamp-2">{r.prompt}</p>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          </Section>
        )}

        {/* Edit history — every status change, severity override, tag
            mutation, or run linkage. Auto-actions show a Bot icon and
            link to the orchestration run that made the change. */}
        <Section icon={HistoryIcon} title="History">
          <FindingHistory findingId={f.id} refreshKey={busy ? 0 : 1} />
        </Section>

        {/* Raw event — collapsible since it duplicates the above */}
        <Section icon={ArrowUpRight} title="Raw event">
          <button
            onClick={() => setShowRaw((v) => !v)}
            className="text-[11px] text-ink-dim hover:text-ink underline mb-1"
          >
            {showRaw ? 'hide' : 'show'} JSON
          </button>
          {showRaw && f.raw && (
            <pre className="text-[11px] font-mono bg-slate-900 text-slate-100 rounded-md p-3 overflow-auto max-h-64">
{JSON.stringify(f.raw, null, 2)}
            </pre>
          )}
        </Section>
      </div>

      <footer className="border-t border-border p-3 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <button
            onClick={() => setInvestigateOpen(true)}
            className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded-md bg-brand-50 text-brand-700 ring-1 ring-brand-100 hover:bg-brand-100"
            title="Run an agent on the affected host with this finding's context"
          >
            <Sparkles size={11} /> Investigate
          </button>
          <Link
            to={`/events?agent=${encodeURIComponent(f.agent || '')}`}
            className="text-xs text-ink-dim hover:text-ink inline-flex items-center gap-1"
          >
            See agent timeline
            <ArrowUpRight size={11} />
          </Link>
        </div>
        <div className="flex items-center gap-2">
          {error && <span className="text-[11px] text-red-700">{error}</span>}
          <span className="text-[11px] text-ink-mute">Triage:</span>
          <StatusMenu
            current={(f.status as FindingStatus) ?? 'open'}
            onPick={setStatus}
            withNote
            busy={busy}
          />
        </div>
      </footer>
      {investigateOpen && (
        <InvestigateDialog
          finding={f}
          onClose={() => setInvestigateOpen(false)}
          onLaunched={() => { setInvestigateOpen(false); refreshInvestigations(); }}
        />
      )}
    </aside>
  );
}

// ── helpers ─────────────────────────────────────────────────────────────────

function Section({ icon: Icon, title, count, children }: {
  icon: typeof Check;
  title: string;
  count?: number;
  children: React.ReactNode;
}) {
  return (
    <section>
      <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-1.5 flex items-center gap-1.5">
        <Icon size={11} />
        {title}
        {count !== undefined && <span className="text-ink-mute font-normal">({count})</span>}
      </h4>
      {children}
    </section>
  );
}

function DetailTile({ icon: Icon, label, children }: {
  icon: typeof Check; label: string; children: React.ReactNode;
}) {
  return (
    <div className="bg-slate-50 border border-border rounded-md px-3 py-2">
      <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium flex items-center gap-1">
        <Icon size={10} /> {label}
      </div>
      <div className="text-xs mt-0.5 truncate">{children}</div>
    </div>
  );
}

function KV({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-2">
      <dt className="text-ink-mute">{label}</dt>
      <dd className="text-ink truncate">{children}</dd>
    </div>
  );
}

function parseEvidence(f: Finding): string[] {
  // The agent file contract says evidence MAY be an array. The webhook stores
  // the value verbatim in raw_json — handle string, array, and missing.
  const raw = f.raw as Record<string, unknown> | undefined;
  if (raw) {
    const ev = raw.evidence;
    if (Array.isArray(ev)) {
      return ev.map(String).filter(Boolean);
    }
  }
  if (f.evidence) {
    return f.evidence
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean);
  }
  return [];
}

function stringFromRaw(raw: unknown, key: string): string | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const v = (raw as Record<string, unknown>)[key];
  return typeof v === 'string' ? v : undefined;
}

function sevBar(sev?: string): string {
  switch ((sev || '').toLowerCase()) {
    case 'critical': return 'bg-sev-critical';
    case 'high':     return 'bg-sev-high';
    case 'medium':   return 'bg-sev-medium';
    case 'low':      return 'bg-sev-low';
    default:         return 'bg-sev-info';
  }
}

function sevDotBg(sev?: string): string {
  switch ((sev || '').toLowerCase()) {
    case 'critical': return 'bg-sev-critical';
    case 'high':     return 'bg-sev-high';
    case 'medium':   return 'bg-sev-medium';
    case 'low':      return 'bg-sev-low';
    default:         return 'bg-sev-info';
  }
}

function fmtFull(ms: number): string {
  if (!ms) return '—';
  return new Date(ms).toLocaleString(undefined, {
    month: 'short', day: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  });
}

function fmtShort(ms: number): string {
  if (!ms) return '—';
  return new Date(ms).toLocaleString(undefined, {
    month: 'short', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  });
}

function fmtTime(ms: number): string {
  if (!ms) return '—';
  return new Date(ms).toLocaleString(undefined, {
    month: 'short', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  });
}

function fmtAge(iso: string): string {
  if (!iso) return '';
  const sec = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
