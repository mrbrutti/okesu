// Agentic conversation view for a single agent.
//
// Groups events by tick number and renders each tick as a chronological
// "conversation" — the system telemetry that went in, the assistant's text,
// the tool calls and their results, and any findings that came out. Reads
// from the same /api/events stream + SSE that the Live Events tab uses,
// just rendered differently.
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  Clock,
  FileSearch,
  MessageSquare,
  PlayCircle,
  ShieldAlert,
  Sparkles,
  Terminal,
  XCircle,
} from 'lucide-react';
import { api, ApiError, subscribeEvents, type EventItem } from '../api';
import { cn } from '../lib/cn';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';

const INITIAL_LIMIT = 1000;
const PAGE_SIZE = 500;
// Hard cap on retained events. Each tick produces ~10–50 events so this
// covers thousands of ticks; beyond that the operator should narrow with
// filters. Prevents the conversation view from chewing memory on a
// long-lived session.
const MAX_RETAINED_EVENTS = 20_000;

interface Props {
  agentName: string;
  /** Host the daimon runs on. Required to disambiguate when multiple
   *  daimons share the same agent name across the fleet — without this
   *  the Messages view conflates every "edr" daimon's events into one
   *  stream and tick 16 ends up showing 32 collectors (8 per host × 4
   *  hosts that happen to be on tick 16). */
  host?: string;
  /** Federation source. When set, the conversation includes only
   *  events that came from the same child CP — same reasoning as host
   *  but at the inter-CP boundary. */
  cpInstanceID?: string;
}

interface Turn {
  tick: number;
  startedAt?: number;
  finishedAt?: number;
  result?: string;
  duration?: string;
  collectors: { name: string; bytes?: number; error?: string }[];
  // Ordered stream of "moves" within the tick — assistant text segments,
  // tool calls, tool results — interleaved so we can render them as a
  // conversation in arrival order.
  moves: Move[];
  findings: EventItem[];
  init?: { provider: string; model: string };
  done?: { stopReason?: string; inputTokens?: number; outputTokens?: number; turns?: number };
  apiError?: { error: string; status: number };
  // Number of agentic-loop turns the model used. Captured from the `done`
  // event's `turn` field, falling back to counting tool_call moves + 1.
  turnCount: number;
}

type Move =
  | { kind: 'text'; text: string; ts: number }
  | { kind: 'tool_call'; name?: string; input?: unknown; ts: number; toolID?: string }
  | { kind: 'tool_result'; output?: string; ts: number; toolID?: string };

export default function AgentMessages({ agentName, host, cpInstanceID }: Props) {
  const [events, setEvents] = useState<EventItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [openTicks, setOpenTicks] = useState<Set<number>>(new Set());
  const [autoOpenLatest, setAutoOpenLatest] = useState(true);
  const [hasMoreOlder, setHasMoreOlder] = useState(true);

  const idCounter = useRef(0);

  // Initial load.
  useEffect(() => {
    let cancelled = false;
    api.events(INITIAL_LIMIT, undefined, { agent: agentName, host })
      .then((list) => {
        if (cancelled) return;
        idCounter.current = Math.max(idCounter.current, list.length + 1);
        setEvents(list.map((e, i) => ({ ...e, id: e.id || idCounter.current - i })));
        setHasMoreOlder(list.length >= INITIAL_LIMIT);
        setError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof ApiError && err.status === 401
          ? 'Session expired. Reload the page.'
          : `Could not load events: ${String(err)}`);
      });
    return () => { cancelled = true; };
  }, []);

  // Live subscription.
  useEffect(() => {
    const unsubscribe = subscribeEvents((e) => {
      idCounter.current += 1;
      setEvents((prev) => [{ ...e, id: idCounter.current }, ...prev].slice(0, MAX_RETAINED_EVENTS));
    });
    return () => unsubscribe();
  }, []);

  // Lazy-load older events using a `before_ts` cursor anchored on the
  // oldest event we already have. SSE prepends new events at the head, so
  // pagination using offset would skew; the cursor stays stable.
  const { sentinelRef, loading: loadingOlder } = useInfiniteScroll({
    hasMore: hasMoreOlder,
    loadMore: async () => {
      if (events.length === 0) return;
      const oldest = events[events.length - 1];
      if (!oldest) return;
      if (events.length >= MAX_RETAINED_EVENTS) {
        setHasMoreOlder(false);
        return;
      }
      try {
        const older = await api.events(PAGE_SIZE, oldest.ts, { agent: agentName, host });
        if (older.length === 0) {
          setHasMoreOlder(false);
          return;
        }
        idCounter.current += older.length;
        const tagged = older.map((e, i) => ({ ...e, id: e.id || idCounter.current - i }));
        setEvents((prev) => [...prev, ...tagged]);
        if (older.length < PAGE_SIZE) setHasMoreOlder(false);
      } catch {
        // Quiet — operator can scroll again to retry.
      }
    },
  });

  // Group events by tick — only events for THIS specific daimon.
  // Filter by (agent, host) pair to avoid conflating multiple daimons
  // with the same agent name on different hosts. When the daimon is
  // federated, also pin to the source CP so the same (agent, host)
  // pair on a different child doesn't bleed in.
  const turns = useMemo(() => buildTurns(
    events.filter((e) => {
      if (e.agent !== agentName) return false;
      if (host && e.host && e.host !== host) return false;
      if (cpInstanceID && e.cp_source && e.cp_source.instance_id !== cpInstanceID) return false;
      // Local daimons (no cp_source on the event) shouldn't surface
      // for a federated daimon view, and vice versa.
      if (cpInstanceID && !e.cp_source) return false;
      if (!cpInstanceID && e.cp_source) return false;
      return true;
    })
  ), [events, agentName, host, cpInstanceID]);

  // Auto-open the most recent turn when new events arrive.
  useEffect(() => {
    if (!autoOpenLatest) return;
    if (turns.length === 0) return;
    const latest = turns[0].tick;
    setOpenTicks((prev) => {
      if (prev.has(latest)) return prev;
      const next = new Set(prev);
      next.add(latest);
      return next;
    });
  }, [turns, autoOpenLatest]);

  if (error) {
    return (
      <div className="m-6 text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
        {error}
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto p-6 space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-xs text-ink-dim">
          {turns.length === 0
            ? `Waiting for the next tick from ${agentName}…`
            : `Showing ${turns.length} tick${turns.length === 1 ? '' : 's'}, newest first.`}
        </p>
        <label className="text-xs text-ink-dim flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={autoOpenLatest}
            onChange={(e) => setAutoOpenLatest(e.target.checked)}
            className="rounded border-border text-brand-500"
          />
          auto-open new turns
        </label>
      </div>

      {turns.length === 0 && (
        <div className="text-center py-16 text-ink-mute">
          <MessageSquare size={28} className="mx-auto mb-2 opacity-40" />
          <p className="text-sm">No turns recorded for this agent yet.</p>
          <p className="text-xs mt-1">Each tick will appear here as a conversation: telemetry → assistant text → tool calls → tool results → findings.</p>
        </div>
      )}

      {turns.map((turn) => (
        <TurnCard
          key={turn.tick}
          turn={turn}
          open={openTicks.has(turn.tick)}
          onToggle={() => {
            setOpenTicks((prev) => {
              const next = new Set(prev);
              if (next.has(turn.tick)) next.delete(turn.tick); else next.add(turn.tick);
              return next;
            });
          }}
        />
      ))}

      {turns.length > 0 && (
        <div ref={sentinelRef} className="h-6 flex items-center justify-center text-[11px] text-ink-mute mt-2">
          {loadingOlder
            ? 'loading older ticks…'
            : hasMoreOlder
              ? 'scroll for more history'
              : `— end of history (${turns.length} ticks) —`}
        </div>
      )}
    </div>
  );
}

function TurnCard({ turn, open, onToggle }: { turn: Turn; open: boolean; onToggle: () => void }) {
  const accent = turn.apiError
    ? 'border-orange-300 bg-orange-50/30'
    : turn.result === 'error'
      ? 'border-red-300 bg-red-50/30'
      : turn.findings.length > 0
        ? 'border-purple-300 bg-purple-50/20'
        : 'border-border bg-panel';

  return (
    <article className={cn('border rounded-xl shadow-card overflow-hidden transition-colors', accent)}>
      <button
        onClick={onToggle}
        className="w-full flex items-center gap-3 px-5 py-3 text-left hover:bg-slate-50/40"
      >
        {open ? <ChevronDown size={14} className="text-ink-mute" /> : <ChevronRight size={14} className="text-ink-mute" />}
        <PlayCircle size={14} className="text-brand-500" />
        <span className="font-medium text-sm">Tick {turn.tick}</span>
        {turn.startedAt && (
          <span className="text-xs text-ink-mute font-mono">
            {fmtTime(turn.startedAt)}
          </span>
        )}
        <span className="ml-auto flex items-center gap-3 text-xs">
          {turn.duration && <span className="text-ink-mute">{turn.duration}</span>}
          {turn.turnCount > 0 && (
            <span
              className="inline-flex items-center gap-1 text-ink-dim bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded font-mono text-[10px]"
              title="Number of agentic-loop turns the model executed in this tick"
            >
              {turn.turnCount} turn{turn.turnCount === 1 ? '' : 's'}
            </span>
          )}
          {turn.findings.length > 0 && (
            <span className="inline-flex items-center gap-1 text-purple-700">
              <ShieldAlert size={12} />
              {turn.findings.length} finding{turn.findings.length === 1 ? '' : 's'}
            </span>
          )}
          {turn.moves.filter((m) => m.kind === 'tool_call').length > 0 && (
            <span className="inline-flex items-center gap-1 text-brand-700">
              <Terminal size={12} />
              {turn.moves.filter((m) => m.kind === 'tool_call').length} tool call{turn.moves.filter((m) => m.kind === 'tool_call').length === 1 ? '' : 's'}
            </span>
          )}
          <ResultPill result={turn.result} apiError={!!turn.apiError} />
        </span>
      </button>

      {open && (
        <div className="px-5 pb-5 space-y-3">
          {/* Telemetry collected */}
          {turn.collectors.length > 0 && (
            <Section icon={ClipboardList} title="Telemetry">
              <ul className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-1.5 text-xs">
                {turn.collectors.map((c, i) => (
                  <li key={i} className={cn(
                    'flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-md ring-1',
                    c.error ? 'bg-red-50 ring-red-200 text-red-700' : 'bg-slate-50 ring-slate-200 text-ink-dim',
                  )}>
                    <span className="flex items-center gap-1.5 font-mono truncate">
                      <FileSearch size={10} className="text-ink-mute" />
                      {c.name}
                    </span>
                    {c.error
                      ? <span className="text-[10px] truncate">{c.error}</span>
                      : <span className="text-[10px] text-ink-mute">{c.bytes ?? 0}B</span>}
                  </li>
                ))}
              </ul>
            </Section>
          )}

          {/* Conversation */}
          {turn.moves.length > 0 && (
            <Section icon={MessageSquare} title="Conversation">
              <div className="space-y-2">
                {turn.moves.map((m, i) => <MoveBubble key={i} move={m} />)}
              </div>
            </Section>
          )}

          {/* Findings */}
          {turn.findings.length > 0 && (
            <Section icon={ShieldAlert} title="Findings">
              <div className="space-y-1.5">
                {turn.findings.map((f) => (
                  <FindingPreview key={f.id} event={f} />
                ))}
              </div>
            </Section>
          )}

          {/* API error */}
          {turn.apiError && (
            <div className="text-xs bg-orange-50 border border-orange-200 text-orange-800 rounded-md p-3">
              <div className="font-semibold mb-1 flex items-center gap-1.5">
                <XCircle size={12} /> API unavailable
                {turn.apiError.status > 0 && (
                  <span className="font-mono text-[10px] bg-orange-100 px-1 py-0.5 rounded">HTTP {turn.apiError.status}</span>
                )}
              </div>
              <code className="font-mono text-[11px] break-all whitespace-pre-wrap">{turn.apiError.error}</code>
            </div>
          )}

          {/* Footer */}
          <footer className="pt-2 border-t border-border/60 flex flex-wrap items-center gap-3 text-[11px] text-ink-mute">
            <Clock size={11} />
            {turn.startedAt && <span>started {fmtTime(turn.startedAt)}</span>}
            {turn.finishedAt && <span>· finished {fmtTime(turn.finishedAt)}</span>}
            {turn.init && <span>· {turn.init.provider} {turn.init.model}</span>}
            {turn.done?.stopReason && <span>· stop: {turn.done.stopReason}</span>}
            {turn.done?.inputTokens !== undefined && (
              <span className="font-mono">· {turn.done.inputTokens} in / {turn.done.outputTokens ?? 0} out</span>
            )}
          </footer>
        </div>
      )}
    </article>
  );
}

function MoveBubble({ move }: { move: Move }) {
  if (move.kind === 'text') {
    return (
      <div className="flex gap-2.5">
        <div className="w-7 h-7 shrink-0 rounded-full bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white text-[10px] font-bold">
          <Sparkles size={11} />
        </div>
        <div className="flex-1 bg-brand-50/50 border border-brand-100 rounded-2xl rounded-tl-sm px-4 py-2.5 text-sm whitespace-pre-wrap text-ink leading-relaxed">
          {move.text}
        </div>
      </div>
    );
  }
  if (move.kind === 'tool_call') {
    const name = move.name ?? 'tool';
    return (
      <div className="flex gap-2.5">
        <div className="w-7 h-7 shrink-0 rounded-full bg-slate-700 flex items-center justify-center text-white">
          <Terminal size={11} />
        </div>
        <div className="flex-1 bg-slate-900 text-slate-100 rounded-2xl rounded-tl-sm px-4 py-2.5">
          <div className="flex items-center gap-2 mb-1.5">
            <span className="text-[11px] font-semibold uppercase tracking-wide text-slate-400">tool</span>
            <code className="text-xs text-emerald-300">{name}</code>
            {move.toolID && <code className="text-[10px] text-slate-500">{move.toolID.slice(-8)}</code>}
          </div>
          <pre className="text-xs font-mono text-slate-200 whitespace-pre-wrap break-all max-h-48 overflow-auto">
            {formatToolInput(move.input)}
          </pre>
        </div>
      </div>
    );
  }
  // tool_result
  return (
    <div className="flex gap-2.5">
      <div className="w-7 h-7 shrink-0 rounded-full bg-slate-200 flex items-center justify-center text-slate-700">
        <CheckCircle2 size={11} />
      </div>
      <div className="flex-1 bg-slate-50 border border-slate-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
        <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-mute mb-1.5">
          tool result{move.toolID ? ` · ${move.toolID.slice(-8)}` : ''}
        </div>
        <pre className="text-xs font-mono text-ink-dim whitespace-pre-wrap break-all max-h-48 overflow-auto">
          {move.output || '(no output)'}
        </pre>
      </div>
    </div>
  );
}

// Static class lookups — Tailwind's content scanner needs literals it can find.
const SEV_BG: Record<string, string> = {
  critical: 'bg-purple-50/60 ring-purple-200',
  high:     'bg-red-50/60 ring-red-200',
  medium:   'bg-orange-50/60 ring-orange-200',
  low:      'bg-yellow-50/60 ring-yellow-200',
  info:     'bg-slate-50/60 ring-slate-200',
};

function FindingPreview({ event }: { event: EventItem }) {
  const sev = (event.severity || 'info').toLowerCase();
  const bg = SEV_BG[sev] || SEV_BG.info;
  return (
    <div className={cn('flex items-start gap-3 px-3 py-2 rounded-md ring-1', bg)}>
      <span className={cn('severity-badge mt-0.5', `severity-${sev}`)}>{event.severity}</span>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium text-ink truncate">{event.title || '(untitled)'}</div>
        <div className="text-[11px] text-ink-mute font-mono truncate">
          {stringFromRaw(event.raw, 'resource') || stringFromRaw(event.raw, 'evidence')}
        </div>
      </div>
    </div>
  );
}

function ResultPill({ result, apiError }: { result?: string; apiError?: boolean }) {
  if (apiError) {
    return <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-orange-100 text-orange-800 ring-1 ring-orange-200">api error</span>;
  }
  if (!result) {
    return <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100"><Activity size={9} className="animate-pulse" /> running</span>;
  }
  if (result === 'error') {
    return <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-red-50 text-red-700 ring-1 ring-red-200">error</span>;
  }
  if (result === 'skipped') {
    return <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-slate-50 text-ink-mute ring-1 ring-slate-200">skipped</span>;
  }
  return <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-green-50 text-green-700 ring-1 ring-green-200">completed</span>;
}

function Section({ icon: Icon, title, children }: { icon: typeof Activity; title: string; children: React.ReactNode }) {
  return (
    <section>
      <h4 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-1.5 flex items-center gap-1.5">
        <Icon size={11} />
        {title}
      </h4>
      {children}
    </section>
  );
}

function buildTurns(events: EventItem[]): Turn[] {
  // Events come in newest-first; we want them ordered chronologically
  // within each turn for the conversation flow. Sort ascending by ts.
  const byTick = new Map<number, EventItem[]>();
  for (const e of events) {
    const tick = numberFromRaw(e.raw, 'tick');
    if (tick === undefined || tick === 0) continue;
    if (!byTick.has(tick)) byTick.set(tick, []);
    byTick.get(tick)!.push(e);
  }

  const turns: Turn[] = [];
  for (const [tick, evs] of byTick) {
    evs.sort((a, b) => a.ts - b.ts);
    const turn: Turn = {
      tick,
      collectors: [],
      moves: [],
      findings: [],
      turnCount: 0,
    };
    // Pending text — concatenate consecutive text deltas into a single message.
    let textBuf: { text: string; ts: number } | null = null;
    const flushText = () => {
      if (textBuf) {
        turn.moves.push({ kind: 'text', text: textBuf.text, ts: textBuf.ts });
        textBuf = null;
      }
    };

    for (const e of evs) {
      switch (e.type) {
        case 'tick_start':
          turn.startedAt = e.ts;
          break;
        case 'tick_done':
          turn.finishedAt = e.ts;
          turn.result = stringFromRaw(e.raw, 'result');
          turn.duration = stringFromRaw(e.raw, 'duration');
          break;
        case 'collector_result':
          turn.collectors.push({
            name: stringFromRaw(e.raw, 'collector') || '?',
            bytes: numberFromRaw(e.raw, 'bytes'),
            error: stringFromRaw(e.raw, 'error'),
          });
          break;
        case 'init':
          turn.init = {
            provider: stringFromRaw(e.raw, 'provider') || '',
            model: stringFromRaw(e.raw, 'model') || '',
          };
          break;
        case 'text': {
          const t = stringFromRaw(e.raw, 'text');
          if (!t) break;
          if (textBuf) textBuf.text += t;
          else textBuf = { text: t, ts: e.ts };
          break;
        }
        case 'tool_call': {
          flushText();
          turn.moves.push({
            kind: 'tool_call',
            name: stringFromRaw(e.raw, 'tool_name'),
            input: (e.raw as Record<string, unknown>)?.input,
            ts: e.ts,
            toolID: stringFromRaw(e.raw, 'tool_id'),
          });
          break;
        }
        case 'tool_result': {
          flushText();
          turn.moves.push({
            kind: 'tool_result',
            output: stringFromRaw(e.raw, 'output'),
            ts: e.ts,
            toolID: stringFromRaw(e.raw, 'tool_id'),
          });
          break;
        }
        case 'done':
          flushText();
          turn.done = {
            stopReason: stringFromRaw(e.raw, 'stop_reason'),
            inputTokens: numberFromRaw((e.raw as Record<string, unknown>)?.usage, 'input_tokens'),
            outputTokens: numberFromRaw((e.raw as Record<string, unknown>)?.usage, 'output_tokens'),
            turns: numberFromRaw(e.raw, 'turn'),
          };
          if (turn.done.turns) turn.turnCount = turn.done.turns;
          break;
        case 'finding':
          turn.findings.push(e);
          break;
        case 'api_unavailable':
          turn.apiError = {
            error: stringFromRaw(e.raw, 'error') || '',
            status: numberFromRaw(e.raw, 'status_code') || 0,
          };
          break;
      }
    }
    flushText();
    // Fallback when the `done` event hasn't arrived yet (mid-tick): a single
    // assistant text segment is one turn; each tool_call adds another round-trip.
    if (!turn.turnCount) {
      const textCount = turn.moves.filter((m) => m.kind === 'text').length;
      const toolCallCount = turn.moves.filter((m) => m.kind === 'tool_call').length;
      turn.turnCount = textCount + toolCallCount;
    }
    turns.push(turn);
  }

  // Newest tick first.
  turns.sort((a, b) => b.tick - a.tick);
  return turns;
}

function formatToolInput(input: unknown): string {
  if (input == null) return '';
  if (typeof input === 'string') return input;
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
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
function fmtTime(ms: number): string {
  if (!ms) return '—';
  return new Date(ms).toLocaleTimeString(undefined, {
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  });
}
