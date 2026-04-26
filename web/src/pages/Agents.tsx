import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  ChevronRight,
  Cpu,
  Layers,
  Pause,
} from 'lucide-react';
import { api, type AgentItem } from '../api';
import { cn } from '../lib/cn';
import { SectionHeader, type SectionTone } from '../components/lists/SectionHeader';
import { ListCard } from '../components/lists/ListCard';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';

const AGENTS_PAGE_SIZE = 500;

type Bucket = 'healthy' | 'degraded' | 'suspended' | 'never';

const BUCKET_ORDER: Bucket[] = ['healthy', 'degraded', 'suspended', 'never'];
const BUCKET_LABEL: Record<Bucket, string> = {
  healthy:   'Healthy',
  degraded:  'Stale',
  suspended: 'Suspended',
  never:     'Never reported',
};
const BUCKET_TONE: Record<Bucket, SectionTone> = {
  healthy:   'good',
  degraded:  'warn',
  suspended: 'muted',
  never:     'muted',
};

export default function AgentsPage() {
  const [agents, setAgents] = useState<AgentItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(true);

  // Refresh the first page; preserve already-loaded older pages by merging
  // on (name, host) identity, with newest entries overwriting tail dups.
  useEffect(() => {
    let cancelled = false;
    const refresh = () => {
      api.agents(AGENTS_PAGE_SIZE, 0)
        .then((list) => {
          if (cancelled) return;
          setAgents((prev) => {
            const newest = list;
            const seen = new Set(newest.map((a) => `${a.name}@${a.host}`));
            const tail = (prev ?? []).filter((a) => !seen.has(`${a.name}@${a.host}`));
            return [...newest, ...tail];
          });
          setHasMore(list.length >= AGENTS_PAGE_SIZE);
        })
        .catch((err) => { if (!cancelled) setError(String(err)); });
    };
    refresh();
    const t = setInterval(refresh, 8_000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  const scroll = useInfiniteScroll({
    hasMore,
    loadMore: async () => {
      if (!agents) return;
      const next = await api.agents(AGENTS_PAGE_SIZE, agents.length);
      if (next.length === 0) {
        setHasMore(false);
        return;
      }
      setAgents((prev) => [...(prev ?? []), ...next]);
      if (next.length < AGENTS_PAGE_SIZE) setHasMore(false);
    },
  });

  const buckets = useMemo(() => groupAgents(agents ?? []), [agents]);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <Layers size={18} className="text-brand-500" />
            Agents
          </h1>
          <p className="text-xs text-ink-dim">
            Daemon agents currently registered with the management plane.
          </p>
        </div>
        {agents && (
          <div className="flex items-center gap-2 text-xs text-ink-dim">
            <span>{agents.length} total</span>
            <span>·</span>
            <span className="text-green-700">{buckets.healthy.length} healthy</span>
            {buckets.degraded.length > 0 && <><span>·</span><span className="text-yellow-700">{buckets.degraded.length} stale</span></>}
          </div>
        )}
      </header>

      <div className="flex-1 overflow-auto p-6 space-y-6">
        {error && (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
            {error}
          </div>
        )}

        {agents === null && <div className="text-ink-mute">Loading…</div>}

        {agents && agents.length === 0 && (
          <div className="text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <Layers size={32} className="mx-auto mb-2 opacity-40" />
            <p className="mb-1">No agents have registered yet.</p>
            <p className="text-xs">
              Issue a client cert and start a daemon, or deploy one from the <Link to="/nodes" className="text-brand-600 hover:underline">Nodes</Link> page.
            </p>
          </div>
        )}

        {BUCKET_ORDER.map((bucket) => {
          const list = buckets[bucket];
          if (list.length === 0) return null;
          return (
            <BucketSection key={bucket} bucket={bucket} list={list} />
          );
        })}

        {agents && agents.length > 0 && (
          <div ref={scroll.sentinelRef} className="text-[11px] text-ink-mute text-center py-2">
            {scroll.loading
              ? 'loading more agents…'
              : hasMore
                ? 'scroll for more'
                : `— end of ${agents.length} agents —`}
          </div>
        )}
      </div>
    </div>
  );
}

function BucketSection({ bucket, list }: { bucket: Bucket; list: AgentItem[] }) {
  return (
    <section>
      <SectionHeader tone={BUCKET_TONE[bucket]} label={BUCKET_LABEL[bucket]} count={list.length} />
      <ListCard>
        {list.map((a) => (
          <AgentRow key={a.name} agent={a} />
        ))}
      </ListCard>
    </section>
  );
}

function AgentRow({ agent }: { agent: AgentItem }) {
  const initials = agent.name
    .split(/[-_]/)
    .filter(Boolean)
    .slice(0, 2)
    .map((s) => s[0]?.toUpperCase() ?? '')
    .join('');
  return (
    <Link
      to={`/agents/${encodeURIComponent(agent.name)}`}
      className="block px-4 py-3 hover:bg-slate-50/60 transition-colors"
    >
      <div className="flex items-center gap-4">
        {/* avatar / type tile */}
        <div className="w-10 h-10 shrink-0 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white text-xs font-semibold shadow-sm">
          {initials || <Cpu size={14} />}
        </div>

        {/* identity */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-medium text-ink truncate">{agent.name}</span>
            {agent.desired_suspended && (
              <span className="text-[10px] uppercase tracking-wide text-yellow-700 bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">
                <Pause size={9} className="inline -mt-0.5 mr-0.5" />
                suspended
              </span>
            )}
          </div>
          <div className="text-xs text-ink-dim font-mono truncate">
            {agent.host || 'unknown host'}
          </div>
        </div>

        {/* provider/model */}
        <div className="hidden md:block w-44 text-xs text-ink-dim shrink-0">
          {agent.provider ? (
            <>
              <div className="text-ink">{agent.provider}</div>
              <div className="text-[11px] text-ink-mute truncate">{agent.model}</div>
            </>
          ) : '—'}
        </div>

        {/* tick activity */}
        <div className="hidden lg:flex w-32 shrink-0 flex-col items-start">
          <div className="text-xs text-ink-dim flex items-center gap-1">
            <Activity size={11} className="text-ink-mute" />
            {agent.last_tick_count} tick{agent.last_tick_count === 1 ? '' : 's'}
          </div>
          <TickBar count={agent.last_tick_count} />
        </div>

        {/* heartbeat */}
        <HeartbeatBadge agent={agent} />

        {/* config indicator */}
        <div className="w-28 shrink-0 text-[11px] text-ink-mute hidden xl:block truncate">
          <div className="text-ink-dim">{summarizeConfig(agent)}</div>
        </div>

        <ChevronRight size={14} className="text-ink-mute shrink-0" />
      </div>
    </Link>
  );
}

function HeartbeatBadge({ agent }: { agent: AgentItem }) {
  if (!agent.last_heartbeat_at) {
    return (
      <span className="inline-flex items-center gap-1 text-[11px] text-ink-mute w-24 shrink-0">
        <span className="w-1.5 h-1.5 rounded-full bg-slate-300" />
        never
      </span>
    );
  }
  const age = agent.heartbeat_age_sec;
  const healthy = agent.healthy;
  return (
    <span className={cn(
      'inline-flex items-center gap-1.5 text-[11px] w-24 shrink-0',
      healthy ? 'text-green-700' : 'text-yellow-700',
    )}>
      <span className={cn(
        'w-1.5 h-1.5 rounded-full',
        healthy ? 'bg-green-500 animate-pulse' : 'bg-yellow-500',
      )} />
      {formatAge(age)}
    </span>
  );
}

function TickBar({ count }: { count: number }) {
  // tiny visual scaffolding — log-ish scale up to "many"
  const segments = 12;
  const filled = Math.min(segments, Math.ceil(Math.log2(count + 1) * 2));
  return (
    <div className="mt-1 flex items-center gap-0.5">
      {Array.from({ length: segments }).map((_, i) => (
        <span
          key={i}
          className={cn(
            'w-1.5 h-2.5 rounded-sm',
            i < filled ? 'bg-brand-400' : 'bg-slate-200',
          )}
        />
      ))}
    </div>
  );
}

function groupAgents(list: AgentItem[]): Record<Bucket, AgentItem[]> {
  const out: Record<Bucket, AgentItem[]> = {
    healthy:   [],
    degraded:  [],
    suspended: [],
    never:     [],
  };
  for (const a of list) {
    if (a.desired_suspended) out.suspended.push(a);
    else if (!a.last_heartbeat_at) out.never.push(a);
    else if (a.healthy) out.healthy.push(a);
    else out.degraded.push(a);
  }
  return out;
}

function formatAge(sec: number): string {
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}

function summarizeConfig(a: AgentItem): string {
  const parts: string[] = [];
  if (a.desired_max_turns !== undefined && a.desired_max_turns !== null) parts.push(`max=${a.desired_max_turns}`);
  if (a.desired_effort) parts.push(`effort=${a.desired_effort}`);
  if (parts.length === 0) return 'default';
  return parts.join(' · ');
}

