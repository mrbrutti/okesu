import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  AlertCircle,
  AlertTriangle,
  ArrowDown,
  Ban,
  CheckCircle2,
  Cog,
  FileSearch,
  Pause,
  PlayCircle,
  PlugZap,
  Power,
  Radio,
  RefreshCcw,
  ShieldAlert,
  TerminalSquare,
  Wifi,
  WifiOff,
  Zap,
} from 'lucide-react';
import { api, ApiError, subscribeEvents, type EventItem } from '../api';
import { cn } from '../lib/cn';
import { useLiveEventsPrefs } from '../lib/preferences';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';
import { CPSourceChip } from './CPSourceChip';

// Hard ceiling on the in-memory event list. Lazy loading lets the operator
// scroll back through history; this cap prevents unbounded growth on a
// long session. 5000 events × ~1KB JSON ≈ 5MB which is fine; beyond that
// the operator should narrow the time window via filters.
const MAX_DISPLAYED = 10_000;
const PAGE_SIZE = 500;

// Event types that are inherently high-volume — single occurrences carry
// real signal so we keep them as individual rows up to a configurable
// threshold, then collapse beyond that. Other types (tick_done,
// config_reloaded, daemon_start) collapse from the very first repeat.
const HIGH_VOLUME_TYPES = new Set(['finding', 'api_unavailable']);

// Event types that should NEVER group regardless of volume — too important
// to merge into a count-only row.
const NEVER_GROUP_TYPES = new Set(['error']);

// One displayable row in the timeline. Three shapes:
//   - 'single' — one event rendered as its own row
//   - 'group'  — N same-(type, agent) events within a rolling window
//   - 'tick'   — all per-tick mechanics for one (agent, host, tick) folded
//                into a single summary row. Default mode for the global
//                Live Events feed; keeps fleet-wide volume manageable
//                without hiding signal (findings + errors still surface).
type TimelineRowEntry =
  | { kind: 'single'; event: EventItem }
  | { kind: 'group'; key: string; type: string; agent?: string;
      members: EventItem[]; count: number; hosts: string[];
      firstTs: number; lastTs: number }
  | { kind: 'tick'; key: string;
      agent: string; host?: string; tick: number;
      startTs: number; endTs: number;
      result?: string; duration?: string;
      collectors: number; toolCalls: number; actionsTaken: number; actionsDenied: number;
      findingCount: number; worstSeverity?: string;
      // Per-severity counts for the rollup row's badge strip — operators
      // want to see "1× CRITICAL · 2× HIGH" at a glance instead of just
      // the worst severity, so a tick with mixed findings reads correctly.
      // Keys are upper-case severity strings (CRITICAL, HIGH, MEDIUM, LOW, INFO).
      severityCounts: Record<string, number>;
      members: EventItem[] };

type ConnState = 'connecting' | 'live' | 'reconnecting' | 'error';
type Mode = 'rollup' | 'detail' | 'all';

// Event types that carry a tick number AND describe per-tick mechanics
// (the things a tick rollup folds). `finding` is included so findings
// surface as a badge on their parent tick row. `tick_start` / `tick_done`
// also fold here as the rollup boundaries themselves. The LLM-envelope
// types (`init`, `text`, `done`) also fold so the operator sees the full
// chronology when they expand a tick — they're noise on the global
// fleet view, but useful when drilling into a specific tick.
const TICK_MECHANIC_TYPES = new Set([
  'tick_start',
  'init',
  'text',
  'collector_result',
  'tool_call',
  'tool_result',
  'action_taken',
  'action_denied',
  'finding',
  'done',
  'tick_done',
]);

interface TimelineProps {
  /** When set, only events whose `agent` field matches are shown. */
  agentFilter?: string;
  /** When set, only events whose `host` field matches are shown. */
  hostFilter?: string;
  /** Override the empty-state hint. */
  emptyHint?: string;
  /** Show or hide the page header (true on the dedicated Events page, false when embedded). */
  showHeader?: boolean;
  /** Compact rows — denser layout when embedded as a sub-section. */
  compact?: boolean;
  /** Initial page size. */
  initialLimit?: number;
  /**
   * View mode.
   *  - "rollup" (default for global Live Events): per-tick mechanics
   *    (tool_call / tool_result / action_taken / collector_result /
   *    finding / tick_start / tick_done) collapse into ONE row per
   *    (agent, host, tick). Findings surface as severity chips on the
   *    rollup row and the row expands to show the full chronology.
   *    Errors and lifecycle events (daemon_start/stop, config_reloaded,
   *    api_unavailable) stay as their own rows.
   *  - "detail": no rollup — events render as individual rows, but pure
   *    LLM-call envelope types (init / text / done) are still hidden.
   *  - "all": every type, no rollup, no hiding. Use on AgentDetail
   *    where the chronology IS the point.
   */
  defaultMode?: Mode;
  /**
   * Whether the rolling-window grouping (Settings → Display) applies here.
   * Defaults to true on the global Live Events page; AgentDetail consults
   * the user's `applyToAgentDetail` pref to decide.
   */
  enableGrouping?: boolean;
}

// Event types that are pure LLM-call metadata — hidden in summary mode
// because they describe the *envelope* of every agent turn rather than
// what the agent did. Everything operationally meaningful (the tool
// the agent ran, the RBAC decision, the tick boundary, the collector
// snapshot) stays visible so the timeline keeps a constant flow even
// in summary mode.
//
// Earlier this list also hid tool_call / tool_result / action_taken /
// collector_result / tick_start, which removed ~94% of fleet events on
// an active stack and made the page look frozen. Those are back in.
const LOOP_TYPES = new Set([
  'init', // session_init — handshake metadata, no signal
  'text', // model token stream — too verbose for fleet view
  'done', // turn_done — pair of init, same reason
]);

export default function EventTimeline({
  agentFilter,
  hostFilter,
  emptyHint,
  showHeader = true,
  compact = false,
  initialLimit = 500,
  defaultMode = 'rollup',
  enableGrouping = true,
}: TimelineProps) {
  const [mode, setMode] = useState<Mode>(defaultMode);
  const [events, setEvents] = useState<EventItem[]>([]);
  const [conn, setConn] = useState<ConnState>('connecting');
  const [liveIDs, setLiveIDs] = useState<Set<number>>(new Set());
  const [paused, setPaused] = useState(false);
  const [filter, setFilter] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());
  const [hasMoreOlder, setHasMoreOlder] = useState(true);
  const [livePrefs] = useLiveEventsPrefs();
  // Rollup mode hides clean ticks (completed result, no findings, no denied
  // actions) by default — at fleet scale they're noise, and the events/min
  // meter already conveys "things are flowing." Toggleable so an operator
  // who wants the firehose can see everything.
  const [showCleanTicks, setShowCleanTicks] = useState(false);

  const idCounter = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  // Sliding window of recent SSE arrivals (timestamps, ms). Used to
  // compute the events-per-minute meter in the header — feels like a
  // pulse for the page, makes it obvious things are flowing even when
  // grouping has collapsed visible rows.
  const recentArrivalsRef = useRef<number[]>([]);
  const [eventsPerMin, setEventsPerMin] = useState(0);

  // Initial load.
  useEffect(() => {
    let cancelled = false;
    api.events(initialLimit)
      .then((list) => {
        if (cancelled) return;
        idCounter.current = Math.max(idCounter.current, list.length + 1);
        const tagged = list.map((e, i) => ({ ...e, id: e.id || idCounter.current - i }));
        setEvents(tagged);
        setHasMoreOlder(list.length >= initialLimit);
        setError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          setError('Session expired. Reload the page to sign in again.');
        } else {
          setError(`Could not load events: ${String(err)}`);
        }
      });
    return () => { cancelled = true; };
  }, [initialLimit]);

  // Lazy-load older events when the bottom sentinel intersects. Cursor by
  // the oldest ts we already have so concurrent SSE prepends don't shift
  // the page boundary.
  const loadOlder = async () => {
    if (events.length === 0) return;
    const oldest = events[events.length - 1];
    if (!oldest) return;
    if (events.length >= MAX_DISPLAYED) {
      setHasMoreOlder(false);
      return;
    }
    try {
      const older = await api.events(PAGE_SIZE, oldest.ts);
      if (older.length === 0) {
        setHasMoreOlder(false);
        return;
      }
      idCounter.current += older.length;
      const tagged = older.map((e, i) => ({ ...e, id: e.id || idCounter.current - i }));
      setEvents((prev) => [...prev, ...tagged]);
      if (older.length < PAGE_SIZE) setHasMoreOlder(false);
    } catch {
      // Quiet — the user can scroll again to retry; no need to surface
      // every transient failure as an error banner.
    }
  };

  const { sentinelRef, loading: loadingOlder } = useInfiniteScroll({
    hasMore: hasMoreOlder,
    loadMore: loadOlder,
  });

  // SSE subscription.
  useEffect(() => {
    let unsubscribe: (() => void) | null = null;
    let reconnectTimer: number | null = null;

    const connect = () => {
      setConn('connecting');
      try {
        unsubscribe = subscribeEvents((e) => {
          idCounter.current += 1;
          const id = idCounter.current;
          setEvents((prev) => [{ ...e, id }, ...prev].slice(0, MAX_DISPLAYED));
          // Append arrival timestamp to the rolling window. The timer
          // below trims to the last 60s and recomputes the meter.
          recentArrivalsRef.current.push(Date.now());
          setLiveIDs((prev) => {
            const next = new Set(prev);
            next.add(id);
            setTimeout(() => {
              setLiveIDs((p) => {
                const n = new Set(p);
                n.delete(id);
                return n;
              });
            }, 2500);
            return next;
          });
          setConn('live');
        });
        setConn('live');
      } catch {
        setConn('error');
        reconnectTimer = window.setTimeout(connect, 3000);
      }
    };
    connect();
    // Recompute events/min every second from the 60s sliding window.
    const rateTimer = window.setInterval(() => {
      const cutoff = Date.now() - 60_000;
      recentArrivalsRef.current = recentArrivalsRef.current.filter((t) => t >= cutoff);
      setEventsPerMin(recentArrivalsRef.current.length);
    }, 1000);
    return () => {
      if (unsubscribe) unsubscribe();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      clearInterval(rateTimer);
    };
  }, []);

  useEffect(() => {
    if (paused || !scrollRef.current) return;
    if (scrollRef.current.scrollTop > 240) return;
    scrollRef.current.scrollTo({ top: 0, behavior: 'smooth' });
  }, [events.length, paused]);

  const filtered = useMemo(() => {
    let out = events;
    if (agentFilter) out = out.filter((e) => e.agent === agentFilter);
    if (hostFilter) out = out.filter((e) => e.host === hostFilter);
    // Detail mode strips pure LLM-call envelope types (init/text/done).
    // Rollup mode keeps them in the bucket so the per-tick member list
    // shown on expand is complete; the LOOP_TYPES drop is applied AFTER
    // rollup, only to the un-rolled tail.
    if (mode === 'detail') {
      out = out.filter((e) => !LOOP_TYPES.has(e.type));
    }
    if (filter) {
      const f = filter.toLowerCase();
      out = out.filter((e) =>
        [e.type, e.agent, e.host, e.severity, e.title]
          .some((v) => v?.toLowerCase().includes(f))
      );
    }
    return out;
  }, [events, agentFilter, hostFilter, filter, mode]);

  // Hidden count tracks events the operator could surface by switching
  // mode. In detail mode it's just LOOP_TYPES; in rollup mode it's
  // every event that got folded into a tick rollup (because expanding
  // the rollup brings them back).
  const hiddenCount = useMemo(() => {
    if (mode === 'all') return 0;
    if (mode === 'detail') {
      return events.filter((e) => {
        if (agentFilter && e.agent !== agentFilter) return false;
        if (hostFilter && e.host !== hostFilter) return false;
        return LOOP_TYPES.has(e.type);
      }).length;
    }
    // mode === 'rollup' — count per-tick mechanic events that will fold.
    return events.filter((e) => {
      if (agentFilter && e.agent !== agentFilter) return false;
      if (hostFilter && e.host !== hostFilter) return false;
      const tick = numberFromRaw(e.raw, 'tick');
      return TICK_MECHANIC_TYPES.has(e.type) && tick !== undefined && !!e.agent;
    }).length;
  }, [events, mode, agentFilter, hostFilter]);

  // Rolling-window grouping. Walks the time-sorted (newest-first) list once
  // and merges same-(type, agent) events whose most-recent member is within
  // the configured window. High-volume types only collapse beyond a
  // threshold; never-group types (errors) stay individual.
  // When smartGrouping is on, the threshold exception is bypassed and a
  // storm detector promotes hot keys to (type)-only across agents.
  const rows = useMemo<TimelineRowEntry[]>(() => {
    // In rollup mode, fold per-tick mechanics first. Tick rollups don't
    // run through groupRollingWindow — each (agent, tick) is unique by
    // construction, so windowed merging would never kick in anyway.
    // The un-rolled remainder (lifecycle, errors, api_unavailable, text
    // events) flows through the rolling-window grouper as before.
    if (mode === 'rollup') {
      const { tickRows, rest } = rollUpByTick(filtered);
      // Split tick rollups: "interesting" (findings, denied actions,
      // error result, skipped/suspended) surface as full rows; clean
      // completions hide unless the operator toggles them on.
      const visibleTicks = showCleanTicks
        ? tickRows
        : tickRows.filter((r) => r.kind === 'tick' && isInterestingTick(r));
      const restRows = enableGrouping
        ? groupRollingWindow(rest, livePrefs.windowSec * 1000, livePrefs.highVolumeThreshold, { smart: livePrefs.smartGrouping })
        : rest.map((e) => ({ kind: 'single', event: e } as TimelineRowEntry));
      // Interleave by timestamp so a tick that landed mid-stream sits
      // where it belongs in the chronology.
      const all: TimelineRowEntry[] = [...visibleTicks, ...restRows];
      all.sort((a, b) => rowTs(b) - rowTs(a));
      return all;
    }

    if (!enableGrouping) {
      return filtered.map((e) => ({ kind: 'single', event: e } as TimelineRowEntry));
    }
    const effectiveThreshold = livePrefs.smartGrouping ? 1 : livePrefs.highVolumeThreshold;
    // Smart mode auto-widens the window so events arriving on a regular
    // cadence (e.g. tick_done every 30s) actually fall into the same window
    // and group. Without this, the manual 15s default rarely produces a
    // visible group across realistic agent runtimes.
    const effectiveWindowMs = livePrefs.smartGrouping
      ? Math.max(livePrefs.windowSec * 1000, 90_000)
      : livePrefs.windowSec * 1000;
    return groupRollingWindow(
      filtered,
      effectiveWindowMs,
      effectiveThreshold,
      { smart: livePrefs.smartGrouping },
    );
  }, [
    filtered, enableGrouping, mode, showCleanTicks,
    livePrefs.windowSec, livePrefs.highVolumeThreshold, livePrefs.smartGrouping,
  ]);

  // Total clean (uninteresting) tick count — shown as a passive counter
  // pill in the header so operators see how much was folded out and can
  // toggle them back on with one click.
  const cleanTickCount = useMemo(() => {
    if (mode !== 'rollup') return 0;
    const { tickRows } = rollUpByTick(filtered);
    return tickRows.filter((r) => r.kind === 'tick' && !isInterestingTick(r)).length;
  }, [filtered, mode]);

  const grouped = useMemo(() => groupRowsByDay(rows), [rows]);

  return (
    <div className="h-full flex flex-col">
      {showHeader && (
        <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel flex items-center justify-between gap-4">
          <div>
            <h1 className="text-lg font-semibold flex items-center gap-2">
              <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
                <Activity size={14} />
              </span>
              Live Events
            </h1>
            <p className="text-xs text-ink-dim ml-9">
              Real-time stream of every JSONL event posted to the webhook receiver.
              <span className="ml-2 text-ink-mute">{filtered.length} of {events.length} shown</span>
            </p>
          </div>
          <div className="flex items-center gap-2">
            <RateBadge eventsPerMin={eventsPerMin} live={conn === 'live'} />
            {mode === 'rollup' && cleanTickCount > 0 && (
              <CleanTickPill
                count={cleanTickCount}
                showing={showCleanTicks}
                onToggle={() => setShowCleanTicks((v) => !v)}
              />
            )}
            <ModeToggle mode={mode} setMode={setMode} hiddenCount={hiddenCount} />
            <input
              type="search"
              placeholder="Filter…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              className="w-56 px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
            <button
              onClick={() => setPaused((p) => !p)}
              className={cn(
                'inline-flex items-center gap-1.5 text-xs px-2.5 py-1.5 rounded-md border',
                paused
                  ? 'bg-yellow-50 border-yellow-200 text-yellow-700'
                  : 'bg-panel border-border text-ink-dim hover:bg-slate-50',
              )}
              title={paused ? 'Auto-scroll paused' : 'Pause auto-scroll'}
            >
              {paused ? <PlayCircle size={12} /> : <Pause size={12} />}
              {paused ? 'paused' : 'auto-scroll'}
            </button>
            <ConnPill state={conn} />
          </div>
        </header>
      )}

      {!showHeader && (
        <div className="px-4 pt-3 flex items-center justify-end gap-2">
          <ModeToggle mode={mode} setMode={setMode} hiddenCount={hiddenCount} compact />
          <input
            type="search"
            placeholder="Filter…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-44 px-2.5 py-1 text-xs border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
          <ConnPill state={conn} />
        </div>
      )}

      {error && (
        <div className="mx-6 mt-4 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      )}

      <div ref={scrollRef} className={cn('flex-1 overflow-auto', compact ? 'px-4 py-3' : 'px-6 py-4')}>
        {filtered.length === 0 && !error && (
          <EmptyState hint={emptyHint} />
        )}

        <div className="relative">
          {filtered.length > 0 && (
            <div className="absolute left-[68px] top-0 bottom-0 w-px bg-border" />
          )}

          {grouped.map((group) => (
            <section key={group.label} className="mb-6">
              <div className="sticky top-0 z-10 -mx-2 mb-2">
                <span className="inline-block bg-panel border border-border text-[11px] uppercase tracking-wide font-medium text-ink-dim px-2 py-0.5 rounded-md ml-[28px]">
                  {group.label}
                </span>
              </div>
              <ul className="timeline-list space-y-1">
                {group.items.map((row) => {
                  if (row.kind === 'single') {
                    return (
                      <TimelineRow
                        key={`s-${row.event.id}`}
                        event={row.event}
                        live={liveIDs.has(row.event.id)}
                      />
                    );
                  }
                  if (row.kind === 'tick') {
                    return (
                      <TimelineTickRow
                        key={`t-${row.key}`}
                        row={row}
                        expanded={expandedGroups.has(row.key)}
                        onToggle={() => setExpandedGroups((prev) => {
                          const next = new Set(prev);
                          if (next.has(row.key)) next.delete(row.key); else next.add(row.key);
                          return next;
                        })}
                        live={row.members.some((m) => liveIDs.has(m.id))}
                        liveIDs={liveIDs}
                      />
                    );
                  }
                  return (
                    <TimelineGroupRow
                      key={row.key}
                      row={row}
                      expanded={expandedGroups.has(row.key)}
                      onToggle={() => setExpandedGroups((prev) => {
                        const next = new Set(prev);
                        if (next.has(row.key)) next.delete(row.key); else next.add(row.key);
                        return next;
                      })}
                      liveIDs={liveIDs}
                    />
                  );
                })}
              </ul>
            </section>
          ))}
        </div>

        {filtered.length > 0 && (
          <div ref={sentinelRef} className="h-6 flex items-center justify-center text-[11px] text-ink-mute mt-2">
            {loadingOlder
              ? 'loading older events…'
              : hasMoreOlder
                ? 'scroll for more history'
                : `— end of history (${events.length} events) —`}
          </div>
        )}

        {paused && filtered.length > 0 && (
          <button
            onClick={() => { setPaused(false); scrollRef.current?.scrollTo({ top: 0, behavior: 'smooth' }); }}
            className="fixed bottom-6 right-6 inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-xs font-medium px-3 py-2 rounded-full shadow-card"
          >
            <ArrowDown size={12} /> Jump to latest
          </button>
        )}
      </div>
    </div>
  );
}

interface RowProps {
  event: EventItem;
  live: boolean;
}

function TimelineRow({ event, live }: RowProps) {
  const meta = describeEvent(event);
  return (
    <li className={cn('timeline-item relative flex items-start gap-3 pl-2 pr-1 py-1.5 rounded-md hover:bg-slate-50/60 transition-colors', live && 'is-fresh')}>
      <time className="w-14 shrink-0 pt-1 text-[11px] font-mono text-ink-mute text-right">
        {fmtTime(event.ts)}
      </time>
      <div className="relative w-6 shrink-0 flex items-center justify-center pt-1">
        {live && (
          <span
            className={cn('timeline-dot-ring absolute inline-block w-3.5 h-3.5 rounded-full', meta.dotRing)}
          />
        )}
        <span
          className={cn(
            'relative inline-flex items-center justify-center w-6 h-6 rounded-full text-white shadow-sm',
            meta.dotBg,
            live && 'timeline-dot is-live',
          )}
        >
          <meta.icon size={12} strokeWidth={2.4} />
        </span>
      </div>
      <div className="min-w-0 flex-1 pt-0.5">
        <div className="flex items-baseline gap-2 flex-wrap">
          <span className="text-sm font-medium text-ink truncate">{meta.title}</span>
          {event.severity && (
            <span className={cn('severity-badge', `severity-${event.severity.toLowerCase()}`)}>
              {event.severity}
            </span>
          )}
          {meta.tagText && (
            <span className={cn('text-[10px] font-medium uppercase tracking-wide px-1.5 py-0.5 rounded ring-1', meta.tagCls)}>
              {meta.tagText}
            </span>
          )}
          <CPSourceChip source={event.cp_source} />
        </div>
        {meta.subtitle && (
          <div className="mt-0.5 text-xs text-ink-dim truncate">{meta.subtitle}</div>
        )}
        <div className="mt-0.5 text-[11px] text-ink-mute font-mono flex items-center gap-3 flex-wrap">
          {event.agent && <span>agent: <span className="text-ink-dim">{event.agent}</span></span>}
          {event.host && <span>host: <span className="text-ink-dim">{event.host}</span></span>}
          <span>type: <span className="text-ink-dim">{event.type}</span></span>
        </div>
      </div>
    </li>
  );
}

function EmptyState({ hint }: { hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center pt-24 pb-12 text-ink-mute">
      <Radio size={28} className="mb-3 opacity-40" />
      <p className="text-sm">No events received yet.</p>
      <p className="text-xs mt-1 text-center max-w-md">
        {hint ?? 'Daemons posting to /api/webhooks/events will land here in real time.'}
      </p>
    </div>
  );
}

function ModeToggle({
  mode,
  setMode,
  hiddenCount,
  compact,
}: {
  mode: Mode;
  setMode: (m: Mode) => void;
  hiddenCount: number;
  compact?: boolean;
}) {
  const tabs: Array<{ value: Mode; label: string; title: string }> = [
    { value: 'rollup', label: 'Rollup', title: 'One row per (agent, tick) — fleet-scale default' },
    { value: 'detail', label: 'Detail', title: 'Each event as its own row, LLM envelope hidden' },
    { value: 'all',    label: 'All',    title: 'Every event, no folding, no filtering' },
  ];
  return (
    <div className={cn('flex items-center rounded-md bg-slate-100 p-0.5', compact ? 'text-[11px]' : 'text-xs')}>
      {tabs.map((t) => (
        <button
          key={t.value}
          onClick={() => setMode(t.value)}
          title={
            mode === t.value && hiddenCount > 0 && t.value !== 'all'
              ? `${hiddenCount} event${hiddenCount === 1 ? '' : 's'} folded — switch to All to see them`
              : t.title
          }
          className={cn(
            'px-2 rounded-md font-medium',
            compact ? 'py-0.5' : 'py-1',
            mode === t.value ? 'bg-panel text-ink shadow-sm' : 'text-ink-dim hover:text-ink',
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

// TimelineGroupRow renders a collapsed bundle of N same-(type, agent)
// events. Click anywhere on the row to expand and see each member with
// its original timestamp. Inline icon buttons (none currently) would need
// stopPropagation if added.
function TimelineGroupRow({
  row, expanded, onToggle, liveIDs,
}: {
  row: Extract<TimelineRowEntry, { kind: 'group' }>;
  expanded: boolean;
  onToggle: () => void;
  liveIDs: Set<number>;
}) {
  const newest = row.members[0]; // members are pre-sorted newest-first
  const meta = describeEvent(newest);
  const Icon = meta.icon;
  return (
    <li>
      <button
        onClick={onToggle}
        className={cn(
          'w-full text-left flex items-start gap-3 pl-2 pr-1 py-1.5 rounded-md hover:bg-slate-50/60 transition-colors',
          expanded && 'bg-slate-50/40',
        )}
      >
        <time className="w-14 shrink-0 pt-1 text-[11px] font-mono text-ink-mute text-right">
          {fmtTime(row.lastTs)}
        </time>
        <div className="relative w-6 shrink-0 flex items-center justify-center pt-1">
          <span className={cn('inline-flex items-center justify-center w-6 h-6 rounded-full ring-2 ring-panel', meta.dotBg)}>
            <Icon size={12} className="text-white" />
          </span>
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-baseline gap-2 flex-wrap">
            <span className="text-sm font-medium">{meta.title}</span>
            <span
              className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-slate-100 text-ink-dim ring-1 ring-slate-200 tabular-nums"
              title={`${row.count} occurrences in the last ${Math.round((row.lastTs - row.firstTs) / 1000)}s`}
            >
              ×{row.count}
            </span>
            {row.agent && (
              <code className="text-[11px] text-ink-mute font-mono">{row.agent}</code>
            )}
            <CPSourceChip source={row.members[0]?.cp_source} />
          </div>
          <div className="mt-0.5 text-[11px] text-ink-mute flex items-center gap-2 flex-wrap">
            {row.hosts.length > 0 && (
              <>
                <span>on</span>
                {row.hosts.slice(0, 4).map((h) => (
                  <code key={h} className="font-mono bg-slate-50 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">
                    {h}
                  </code>
                ))}
                {row.hosts.length > 4 && <span>+{row.hosts.length - 4} more</span>}
              </>
            )}
            <span className="ml-auto">
              {expanded ? 'collapse' : 'expand'}
            </span>
          </div>
        </div>
      </button>
      {expanded && (
        <ul className="ml-[88px] mt-1 mb-2 space-y-0.5 border-l-2 border-slate-100 pl-3">
          {row.members.map((m) => (
            <TimelineRow key={`g-${m.id}`} event={m} live={liveIDs.has(m.id)} />
          ))}
        </ul>
      )}
    </li>
  );
}

// TimelineTickRow renders one (agent, host, tick) bundle as a single
// summary row. Compact at-a-glance: time + agent · #tick · result icon
// + colored severity chip if the tick produced any findings + counts of
// collectors / tools / actions / findings. Expand to see the full
// chronology of constituent events (each as a regular TimelineRow).
function TimelineTickRow({
  row, expanded, onToggle, live, liveIDs,
}: {
  row: Extract<TimelineRowEntry, { kind: 'tick' }>;
  expanded: boolean;
  onToggle: () => void;
  live: boolean;
  liveIDs: Set<number>;
}) {
  // Result drives the dot color. Findings push the row into the
  // sev.<worst> tone so a critical-finding tick reads at a glance.
  const sev = row.worstSeverity?.toLowerCase();
  const sevDotBg = sev
    ? ({ critical: 'bg-sev-critical', high: 'bg-sev-high', medium: 'bg-sev-medium', low: 'bg-sev-low', info: 'bg-sev-info' } as Record<string, string>)[sev]
    : undefined;
  const dotBg = row.result === 'error'
    ? 'bg-red-500'
    : row.result === 'skipped'
      ? 'bg-slate-400'
      : sevDotBg ?? 'bg-green-500';
  return (
    <li className={cn('timeline-item relative', live && 'is-fresh')}>
      <button
        onClick={onToggle}
        className={cn(
          'w-full text-left flex items-start gap-3 pl-2 pr-1 py-1.5 rounded-md hover:bg-slate-50/60 transition-colors',
          expanded && 'bg-slate-50/40',
        )}
      >
        <time className="w-14 shrink-0 pt-1 text-[11px] font-mono text-ink-mute text-right">
          {fmtTime(row.endTs)}
        </time>
        <div className="relative w-6 shrink-0 flex items-center justify-center pt-1">
          <span className={cn('inline-flex items-center justify-center w-6 h-6 rounded-full ring-2 ring-panel text-white shadow-sm', dotBg)}>
            <PlayCircle size={12} strokeWidth={2.4} />
          </span>
        </div>
        <div className="flex-1 min-w-0 pt-0.5">
          <div className="flex items-baseline gap-2 flex-wrap">
            <span className="text-sm font-medium text-ink truncate">
              {row.agent} <span className="text-ink-dim font-normal">· tick #{row.tick}</span>
            </span>
            <CPSourceChip source={row.members[0]?.cp_source} />
            {row.result && (
              <span className={cn(
                'text-[10px] font-medium uppercase tracking-wide px-1.5 py-0.5 rounded ring-1',
                row.result === 'error'    ? 'text-red-700 bg-red-50 ring-red-200'
                : row.result === 'skipped'? 'text-slate-700 bg-slate-50 ring-slate-200'
                                          : 'text-green-700 bg-green-50 ring-green-200',
              )}>
                {row.result}
              </span>
            )}
            {row.duration && (
              <span className="text-[10px] text-ink-mute font-mono tabular-nums">{row.duration}</span>
            )}
            {/* One chip per severity bucket — `1× CRITICAL · 2× HIGH · 2× MEDIUM`
                rather than a single worst-severity chip, so a tick with mixed
                findings reports its full distribution. SEV_ORDER drives the
                left-to-right ordering. */}
            {row.findingCount > 0 && SEV_ORDER.map((sev) => {
              const n = row.severityCounts[sev];
              if (!n) return null;
              return (
                <span key={sev} className={cn('severity-badge tabular-nums', `severity-${sev.toLowerCase()}`)}>
                  {n}× {sev}
                </span>
              );
            })}
            {row.host && (
              <code className="text-[11px] text-ink-mute font-mono">{row.host}</code>
            )}
          </div>
          <div className="mt-0.5 text-[11px] text-ink-mute flex items-center gap-3 flex-wrap">
            {row.collectors > 0   && <TickStat label="collector" count={row.collectors} />}
            {row.toolCalls > 0    && <TickStat label="tool"      count={row.toolCalls} />}
            {row.actionsTaken > 0 && <TickStat label="action"    count={row.actionsTaken} />}
            {row.actionsDenied > 0 && <TickStat label="denied"   count={row.actionsDenied} tone="bad" />}
            {row.findingCount > 0 && <TickStat label="finding"   count={row.findingCount} tone={row.worstSeverity ? 'sev' : undefined} />}
            <span className="ml-auto opacity-70">
              {expanded ? 'collapse' : `expand (${row.members.length})`}
            </span>
          </div>
        </div>
      </button>
      {expanded && (
        <ul className="ml-[88px] mt-1 mb-2 space-y-0.5 border-l-2 border-slate-100 pl-3">
          {[...row.members].reverse().map((m) => (
            <TimelineRow key={`tm-${m.id}`} event={m} live={liveIDs.has(m.id)} />
          ))}
        </ul>
      )}
    </li>
  );
}

function TickStat({ label, count, tone }: { label: string; count: number; tone?: 'bad' | 'sev' }) {
  const cls = tone === 'bad' ? 'text-red-700' : tone === 'sev' ? 'text-purple-700 font-medium' : 'text-ink-dim';
  return (
    <span className={cls}>
      <span className="tabular-nums font-medium">{count}</span> {label}{count === 1 ? '' : 's'}
    </span>
  );
}

function ConnPill({ state }: { state: ConnState }) {
  const cfg = {
    live:         { label: 'live',         cls: 'bg-green-50 text-green-700 ring-green-200',  icon: Wifi },
    connecting:   { label: 'connecting',   cls: 'bg-slate-50 text-ink-mute ring-slate-200',   icon: Wifi },
    reconnecting: { label: 'reconnecting', cls: 'bg-yellow-50 text-yellow-700 ring-yellow-200', icon: WifiOff },
    error:        { label: 'error',        cls: 'bg-red-50 text-red-700 ring-red-200',        icon: WifiOff },
  }[state];
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-md ring-1', cfg.cls)}>
      {state === 'live' ? (
        <span className="relative flex w-2 h-2">
          <span className="absolute inline-flex w-full h-full rounded-full bg-green-500 opacity-60 animate-ping" />
          <span className="relative inline-flex w-2 h-2 rounded-full bg-green-500" />
        </span>
      ) : (
        <cfg.icon size={12} />
      )}
      {cfg.label}
    </span>
  );
}

// Compact pill showing how many clean tick rollups are folded out of
// the visible feed. Click to flip showCleanTicks. Only appears in
// rollup mode when the count > 0.
function CleanTickPill({ count, showing, onToggle }: { count: number; showing: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      title={showing ? 'Hide clean ticks (default)' : 'Show clean ticks too'}
      className={cn(
        'inline-flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-md ring-1 transition-colors',
        showing
          ? 'bg-slate-100 text-ink-dim ring-slate-300 hover:bg-slate-200'
          : 'bg-green-50 text-green-700 ring-green-200 hover:bg-green-100',
      )}
    >
      <CheckCircle2 size={11} />
      <span className="font-medium tabular-nums">+{count}</span>
      <span className="text-[10px] opacity-80">clean tick{count === 1 ? '' : 's'}</span>
    </button>
  );
}

// Events-per-minute meter — pulses brand purple when arrivals are
// landing, fades to neutral when the stream goes quiet. Operators get
// a heartbeat-style indicator separate from the timeline rows
// themselves (which can be visually static if everything's grouped).
function RateBadge({ eventsPerMin, live }: { eventsPerMin: number; live: boolean }) {
  const isFlowing = live && eventsPerMin > 0;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-md ring-1 tabular-nums transition-colors',
        isFlowing
          ? 'bg-brand-50 text-brand-700 ring-brand-200'
          : 'bg-slate-50 text-ink-mute ring-slate-200',
      )}
      title={`Events received in the last 60 seconds`}
    >
      <span
        className={cn(
          'inline-block w-1.5 h-1.5 rounded-full',
          isFlowing ? 'bg-brand-500 animate-pulse' : 'bg-slate-300',
        )}
      />
      <span className="font-medium">{eventsPerMin}</span>
      <span className="text-[10px] opacity-70">ev/min</span>
    </span>
  );
}

function describeEvent(e: EventItem): {
  icon: typeof Activity;
  dotBg: string;
  dotRing: string;
  title: string;
  subtitle?: string;
  tagText?: string;
  tagCls?: string;
} {
  const t = e.type;
  const sev = e.severity?.toLowerCase();

  if (t === 'finding') {
    // Match the severity badge palette.
    const sevColor = {
      critical: { bg: 'bg-sev-critical', ring: 'text-sev-critical bg-purple-200/60' },
      high:     { bg: 'bg-sev-high',     ring: 'text-sev-high bg-red-200/60' },
      medium:   { bg: 'bg-sev-medium',   ring: 'text-sev-medium bg-orange-200/60' },
      low:      { bg: 'bg-sev-low',      ring: 'text-sev-low bg-yellow-200/60' },
    }[sev ?? 'low'] ?? { bg: 'bg-sev-info', ring: 'text-sev-info bg-slate-200/60' };
    return {
      icon: ShieldAlert,
      dotBg: sevColor.bg,
      dotRing: sevColor.ring,
      title: e.title || 'Finding',
      subtitle: stringFromRaw(e.raw, 'resource') || stringFromRaw(e.raw, 'evidence'),
    };
  }

  if (t === 'action_taken') {
    return {
      icon: TerminalSquare,
      dotBg: 'bg-brand-500',
      dotRing: 'text-brand-500 bg-brand-200/60',
      title: `Tool executed${stringFromRaw(e.raw, 'tool_name') ? ': ' + stringFromRaw(e.raw, 'tool_name') : ''}`,
      subtitle: shortenRawCommand(e.raw),
      tagText: 'action',
      tagCls: 'text-brand-700 bg-brand-50 ring-brand-100',
    };
  }
  if (t === 'action_denied') {
    return {
      icon: Ban,
      dotBg: 'bg-red-500',
      dotRing: 'text-red-500 bg-red-200/60',
      title: `Blocked by RBAC${stringFromRaw(e.raw, 'tool_name') ? ': ' + stringFromRaw(e.raw, 'tool_name') : ''}`,
      subtitle: stringFromRaw(e.raw, 'reason'),
      tagText: 'denied',
      tagCls: 'text-red-700 bg-red-50 ring-red-200',
    };
  }
  if (t === 'tick_start') {
    return { icon: PlayCircle, dotBg: 'bg-slate-400', dotRing: 'text-slate-400 bg-slate-200', title: 'Tick start', subtitle: stringFromRaw(e.raw, 'tick') ? `tick #${stringFromRaw(e.raw, 'tick')}` : undefined };
  }
  if (t === 'tick_done') {
    const result = stringFromRaw(e.raw, 'result') || 'completed';
    const dur = stringFromRaw(e.raw, 'duration');
    const findings = numberFromRaw(e.raw, 'findings');
    return {
      icon: CheckCircle2,
      dotBg: result === 'error' ? 'bg-red-500' : result === 'skipped' ? 'bg-slate-400' : 'bg-green-500',
      dotRing: 'text-green-500 bg-green-200/60',
      title: `Tick ${result}`,
      subtitle: [dur && `duration ${dur}`, findings ? `${findings} finding${findings === 1 ? '' : 's'}` : null].filter(Boolean).join(' · '),
      tagText: result === 'error' ? 'error' : undefined,
      tagCls: 'text-red-700 bg-red-50 ring-red-200',
    };
  }
  if (t === 'collector_result') {
    const name = stringFromRaw(e.raw, 'collector');
    const bytes = numberFromRaw(e.raw, 'bytes');
    return {
      icon: FileSearch,
      dotBg: 'bg-slate-300',
      dotRing: 'text-slate-300 bg-slate-100',
      title: `Collector: ${name || '?'}`,
      subtitle: bytes ? `${bytes} bytes captured` : stringFromRaw(e.raw, 'error'),
    };
  }
  if (t === 'daemon_start') {
    return { icon: Power, dotBg: 'bg-emerald-500', dotRing: 'text-emerald-500 bg-emerald-200/60', title: 'Daemon started', subtitle: `${stringFromRaw(e.raw, 'provider') || ''} ${stringFromRaw(e.raw, 'model') || ''}`.trim(), tagText: 'lifecycle', tagCls: 'text-emerald-700 bg-emerald-50 ring-emerald-200' };
  }
  if (t === 'daemon_stop') {
    return { icon: Power, dotBg: 'bg-slate-500', dotRing: 'text-slate-500 bg-slate-200/60', title: 'Daemon stopped', subtitle: stringFromRaw(e.raw, 'stop_reason'), tagText: 'lifecycle', tagCls: 'text-slate-700 bg-slate-50 ring-slate-200' };
  }
  if (t === 'config_reloaded') {
    return { icon: RefreshCcw, dotBg: 'bg-brand-400', dotRing: 'text-brand-400 bg-brand-200/60', title: 'Config reloaded', subtitle: stringFromRaw(e.raw, 'text'), tagText: 'config', tagCls: 'text-brand-700 bg-brand-50 ring-brand-100' };
  }
  if (t === 'api_unavailable') {
    return { icon: PlugZap, dotBg: 'bg-orange-500', dotRing: 'text-orange-500 bg-orange-200/60', title: 'API unavailable', subtitle: truncate(stringFromRaw(e.raw, 'error'), 160), tagText: stringFromRaw(e.raw, 'status_code') || 'network', tagCls: 'text-orange-700 bg-orange-50 ring-orange-200' };
  }
  if (t === 'error') {
    return { icon: AlertCircle, dotBg: 'bg-red-500', dotRing: 'text-red-500 bg-red-200/60', title: e.title || 'Error', subtitle: truncate(stringFromRaw(e.raw, 'error') || stringFromRaw(e.raw, 'text'), 160) };
  }
  if (t === 'tool_call' || t === 'tool_result') {
    return { icon: Cog, dotBg: 'bg-slate-400', dotRing: 'text-slate-400 bg-slate-100', title: t === 'tool_call' ? 'Tool call' : 'Tool result', subtitle: stringFromRaw(e.raw, 'tool_name') || truncate(stringFromRaw(e.raw, 'output'), 120) };
  }
  if (t === 'init') {
    return { icon: Zap, dotBg: 'bg-brand-400', dotRing: 'text-brand-400 bg-brand-100', title: 'Session init', subtitle: stringFromRaw(e.raw, 'model') };
  }
  return {
    icon: AlertTriangle,
    dotBg: 'bg-slate-300',
    dotRing: 'text-slate-300 bg-slate-100',
    title: e.title || t,
  };
}

function stringFromRaw(raw: unknown, key: string): string | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const v = (raw as Record<string, unknown>)[key];
  return typeof v === 'string' ? v : undefined;
}
function numberFromRaw(raw: unknown, key: string): number | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const v = (raw as Record<string, unknown>)[key];
  return typeof v === 'number' ? v : undefined;
}
function shortenRawCommand(raw: unknown): string | undefined {
  const input = raw && typeof raw === 'object' ? (raw as Record<string, unknown>).input : undefined;
  if (input && typeof input === 'object') {
    const cmd = (input as Record<string, unknown>).command;
    if (typeof cmd === 'string') return cmd.length > 120 ? cmd.slice(0, 120) + '…' : cmd;
  }
  return undefined;
}
function truncate(s: string | undefined, max: number): string | undefined {
  if (!s) return s;
  return s.length <= max ? s : s.slice(0, max) + '…';
}

function fmtTime(ms: number): string {
  if (!ms) return '—';
  return new Date(ms).toLocaleTimeString(undefined, {
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  });
}

// rowTs returns the canonical sort timestamp for a row entry — the
// newest member's timestamp regardless of kind. Used to interleave
// tick rollups with un-rolled lifecycle rows in chronological order.
function rowTs(r: TimelineRowEntry): number {
  if (r.kind === 'single') return r.event.ts;
  if (r.kind === 'group')  return r.lastTs;
  return r.endTs;
}

// A tick is "interesting" (worth surfacing on the global feed) when it
// produced a finding, denied an action, errored, or was skipped (which
// at the time of writing means the agent was paused via the management
// plane). A bare clean completion is noise at fleet scale — the
// dashboard's daimon health tile already says "33 healthy."
function isInterestingTick(r: Extract<TimelineRowEntry, { kind: 'tick' }>): boolean {
  if (r.findingCount > 0) return true;
  if (r.actionsDenied > 0) return true;
  if (r.result === 'error' || r.result === 'skipped') return true;
  return false;
}

// rollUpByTick partitions events into (a) per-tick rollups and (b) the
// un-rolled remainder. An event qualifies for rollup when it has both
// a tick number and an agent AND its type is in TICK_MECHANIC_TYPES.
// Errors and lifecycle events are deliberately excluded — they're
// rare enough at fleet scale that surfacing them as their own row is
// the right move.
//
// Severity ordering for the worst-finding chip on a rollup:
//   CRITICAL > HIGH > MEDIUM > LOW > INFO
const SEV_ORDER = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];
function worstSeverity(a: string | undefined, b: string | undefined): string | undefined {
  if (!a) return b;
  if (!b) return a;
  return SEV_ORDER.indexOf(a) <= SEV_ORDER.indexOf(b) ? a : b;
}

function rollUpByTick(events: EventItem[]): {
  tickRows: TimelineRowEntry[];
  rest: EventItem[];
} {
  type Bucket = {
    key: string;
    agent: string;
    host?: string;
    tick: number;
    members: EventItem[];
  };
  const byKey = new Map<string, Bucket>();
  const rest: EventItem[] = [];

  for (const e of events) {
    const tick = numberFromRaw(e.raw, 'tick');
    if (!e.agent || tick === undefined || !TICK_MECHANIC_TYPES.has(e.type)) {
      rest.push(e);
      continue;
    }
    const k = `${e.agent}@${e.host ?? ''}#${tick}`;
    let b = byKey.get(k);
    if (!b) {
      b = { key: k, agent: e.agent, host: e.host, tick, members: [] };
      byKey.set(k, b);
    }
    b.members.push(e);
  }

  // Walk each bucket once: counts, result/duration from the tick_done
  // event if present, severity-worst from any folded findings.
  const tickRows: TimelineRowEntry[] = [];
  for (const b of byKey.values()) {
    // Members chronological, tick_start first → tick_done last.
    b.members.sort((a, c) => a.ts - c.ts);
    const startTs = b.members[0].ts;
    const endTs = b.members[b.members.length - 1].ts;
    let collectors = 0, toolCalls = 0, actionsTaken = 0, actionsDenied = 0, findingCount = 0;
    let result: string | undefined;
    let duration: string | undefined;
    let worst: string | undefined;
    const severityCounts: Record<string, number> = {};
    for (const m of b.members) {
      switch (m.type) {
        case 'collector_result': collectors++; break;
        case 'tool_call':        toolCalls++; break;
        case 'action_taken':     actionsTaken++; break;
        case 'action_denied':    actionsDenied++; break;
        case 'finding': {
          findingCount++;
          const sev = (m.severity ?? '').toUpperCase() || 'INFO';
          severityCounts[sev] = (severityCounts[sev] ?? 0) + 1;
          worst = worstSeverity(worst, sev);
          break;
        }
        case 'tick_done':
          result = stringFromRaw(m.raw, 'result') ?? 'completed';
          duration = stringFromRaw(m.raw, 'duration');
          break;
      }
    }
    // A "tick" with only a tick_start event and nothing else isn't
    // worth a rollup row — emit those constituent events back to the
    // tail so they render as plain singles.
    if (b.members.length < 2 && !result) {
      for (const m of b.members) rest.push(m);
      continue;
    }
    tickRows.push({
      kind: 'tick',
      key: b.key,
      agent: b.agent,
      host: b.host,
      tick: b.tick,
      startTs, endTs,
      result, duration,
      collectors, toolCalls, actionsTaken, actionsDenied,
      findingCount, worstSeverity: worst, severityCounts,
      members: b.members,
    });
  }
  return { tickRows, rest };
}

function groupRowsByDay(rows: TimelineRowEntry[]): Array<{ label: string; items: TimelineRowEntry[] }> {
  const groups: Array<{ label: string; items: TimelineRowEntry[] }> = [];
  const today = new Date();
  const yesterday = new Date(); yesterday.setDate(yesterday.getDate() - 1);
  for (const r of rows) {
    const ts = rowTs(r);
    const d = new Date(ts);
    let label: string;
    if (sameDay(d, today)) label = 'Today';
    else if (sameDay(d, yesterday)) label = 'Yesterday';
    else label = d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
    let g = groups.find((x) => x.label === label);
    if (!g) {
      g = { label, items: [] };
      groups.push(g);
    }
    g.items.push(r);
  }
  return groups;
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

// SMART_PROMOTE_THRESHOLD — once a single (type, agent) key sees this many
// occurrences in the loaded feed AND smart grouping is on, the agent is
// dropped from the key so concurrent storms across multiple agents
// collapse into a single (type)-only row. Tuned low enough that realistic
// fleet noise (tick_done, config_reloaded, daemon_start) actually
// promotes — the previous value of 10 rarely fired in test fleets and
// gave operators the impression that smart grouping wasn't working.
const SMART_PROMOTE_THRESHOLD = 3;

// groupRollingWindow walks events newest-first and merges same-(type, agent)
// events whose most-recent member is within `windowMs` of the event under
// consideration. The result preserves arrival order: a group's position in
// the list is set by its NEWEST member. Triaged-status types (finding,
// api_unavailable) only collapse once the group reaches highVolumeThreshold;
// before that, every member renders as its own row.
//
// When `opts.smart` is true:
//   - the high-volume-types threshold check is bypassed at call site
//     (caller passes effectiveThreshold = 1)
//   - this function additionally rewrites the per-event group key to
//     drop `agent` when any (type, agent) key has > SMART_PROMOTE_THRESHOLD
//     occurrences in the input, so a fleet-wide storm of one type
//     consolidates into a single (type) row
function groupRollingWindow(
  events: EventItem[],
  windowMs: number,
  highVolumeThreshold: number,
  opts: { smart?: boolean } = {},
): TimelineRowEntry[] {
  // Walk in chronological order so "rolling" semantics match operator
  // intuition (a steady drip of ticks 30s apart with a 15s window does
  // NOT collapse). Then reverse for display.
  const asc = [...events].sort((a, b) => a.ts - b.ts);

  // Smart-mode storm detection: pre-pass tally of (type, agent)
  // occurrences across the entire input. Any type whose most-active
  // (type, agent) key exceeds SMART_PROMOTE_THRESHOLD is then keyed
  // by `type` alone in the main pass, collapsing fleet-wide.
  const promotedTypes = new Set<string>();
  if (opts.smart) {
    const counts = new Map<string, number>();
    for (const e of asc) {
      if (NEVER_GROUP_TYPES.has(e.type)) continue;
      const k = `${e.type}|${e.agent ?? ''}`;
      counts.set(k, (counts.get(k) ?? 0) + 1);
    }
    for (const [k, n] of counts) {
      if (n > SMART_PROMOTE_THRESHOLD) {
        promotedTypes.add(k.split('|')[0]);
      }
    }
  }

  // Pending groups keyed by (type|agent) — or just (type|) when promoted.
  type Pending = {
    key: string; type: string; agent?: string;
    members: EventItem[];
    lastTs: number;
    promoted: boolean; // when true, this group spans multiple agents
  };
  const open: Pending[] = [];
  const closed: Pending[] = [];

  function closeOlderThan(threshold: number) {
    for (let i = open.length - 1; i >= 0; i--) {
      if (threshold - open[i].lastTs > windowMs) {
        closed.push(open[i]);
        open.splice(i, 1);
      }
    }
  }

  for (const e of asc) {
    closeOlderThan(e.ts);
    if (NEVER_GROUP_TYPES.has(e.type)) {
      // Emit immediately as a singleton-group; never merge.
      closed.push({
        key: `single-${e.id}-${e.ts}`,
        type: e.type, agent: e.agent,
        members: [e], lastTs: e.ts, promoted: false,
      });
      continue;
    }
    const promoted = opts.smart === true && promotedTypes.has(e.type);
    const k = promoted ? `${e.type}|*` : `${e.type}|${e.agent ?? ''}`;
    let g = open.find((p) => p.key === k);
    if (!g) {
      g = {
        key: k, type: e.type,
        agent: promoted ? undefined : e.agent,
        members: [], lastTs: e.ts, promoted,
      };
      open.push(g);
    }
    g.members.push(e);
    g.lastTs = e.ts;
  }
  // Anything still open at the end of the input becomes a closed group.
  closed.push(...open);

  // Convert to display rows. High-volume types under the threshold expand
  // back into individual singles so the operator still sees the first N.
  const out: TimelineRowEntry[] = [];
  for (const g of closed) {
    const isHighVolume = HIGH_VOLUME_TYPES.has(g.type);
    if (g.members.length === 1 || (isHighVolume && g.members.length <= highVolumeThreshold)) {
      for (const m of g.members) out.push({ kind: 'single', event: m });
      continue;
    }
    const hosts = Array.from(new Set(g.members.map((m) => m.host).filter(Boolean) as string[]));
    out.push({
      kind: 'group',
      key: `${g.key}@${g.lastTs}`,
      type: g.type,
      agent: g.agent,
      members: [...g.members].sort((a, b) => b.ts - a.ts), // newest-first inside the group
      count: g.members.length,
      hosts,
      firstTs: g.members[0].ts,
      lastTs: g.lastTs,
    });
  }
  // Display order: newest first across the whole list.
  out.sort((a, b) => rowTs(b) - rowTs(a));
  return out;
}
