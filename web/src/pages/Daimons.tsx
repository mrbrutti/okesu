import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  ChevronRight,
  Cpu,
  FileEdit,
  Layers,
  Pause,
  Server,
} from 'lucide-react';
import { api, type DaimonItem } from '../api';
import { cn } from '../lib/cn';
import { SectionHeader, type SectionTone } from '../components/lists/SectionHeader';
import { ListCard } from '../components/lists/ListCard';
import { useInfiniteScroll } from '../lib/useInfiniteScroll';
import DaimonsLibrary from '../components/DaimonsLibrary';

const DAIMONS_PAGE_SIZE = 500;

type Tab = 'deployed' | 'library';

export default function DaimonsPage() {
  const [tab, setTab] = useState<Tab>('deployed');
  return (
    <div className="h-full flex flex-col">
      <nav className="px-6 pt-3 border-b border-border bg-panel flex gap-1">
        {([
          ['deployed', 'Deployed', Server],
          ['library',  'Library',  FileEdit],
        ] as const).map(([key, label, Icon]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={cn(
              'inline-flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 -mb-px',
              tab === key
                ? 'border-brand-500 text-brand-700 font-medium'
                : 'border-transparent text-ink-dim hover:text-ink',
            )}
          >
            <Icon size={14} />
            {label}
          </button>
        ))}
      </nav>
      <main className="flex-1 overflow-hidden">
        {tab === 'deployed' && <DaimonsDeployed />}
        {tab === 'library' && <DaimonsLibrary />}
      </main>
    </div>
  );
}

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

function DaimonsDeployed() {
  const [daimons, setDaimons] = useState<DaimonItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(true);

  // Refresh the first page; preserve already-loaded older pages by merging
  // on (name, host) identity, with newest entries overwriting tail dups.
  useEffect(() => {
    let cancelled = false;
    const refresh = () => {
      api.daimons(DAIMONS_PAGE_SIZE, 0)
        .then((list) => {
          if (cancelled) return;
          setDaimons((prev) => {
            const newest = list;
            const seen = new Set(newest.map((d) => `${d.name}@${d.host}`));
            const tail = (prev ?? []).filter((d) => !seen.has(`${d.name}@${d.host}`));
            return [...newest, ...tail];
          });
          setHasMore(list.length >= DAIMONS_PAGE_SIZE);
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
      if (!daimons) return;
      const next = await api.daimons(DAIMONS_PAGE_SIZE, daimons.length);
      if (next.length === 0) {
        setHasMore(false);
        return;
      }
      setDaimons((prev) => [...(prev ?? []), ...next]);
      if (next.length < DAIMONS_PAGE_SIZE) setHasMore(false);
    },
  });

  const buckets = useMemo(() => groupDaimons(daimons ?? []), [daimons]);

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-panel flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <Layers size={18} className="text-brand-500" />
            Daimons
          </h1>
          <p className="text-xs text-ink-dim">
            Long-running agent processes registered with the management plane.
          </p>
        </div>
        {daimons && (
          <div className="flex items-center gap-2 text-xs text-ink-dim">
            <span>{daimons.length} total</span>
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

        {daimons === null && <div className="text-ink-mute">Loading…</div>}

        {daimons && daimons.length === 0 && (
          <div className="text-center py-16 text-ink-mute bg-panel border border-border rounded-xl shadow-card">
            <Layers size={32} className="mx-auto mb-2 opacity-40" />
            <p className="mb-1">No daimons have registered yet.</p>
            <p className="text-xs">
              Issue a client cert and start one, or deploy from the <Link to="/nodes" className="text-brand-600 hover:underline">Nodes</Link> page.
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

        {daimons && daimons.length > 0 && (
          <div ref={scroll.sentinelRef} className="text-[11px] text-ink-mute text-center py-2">
            {scroll.loading
              ? 'loading more daimons…'
              : hasMore
                ? 'scroll for more'
                : `— end of ${daimons.length} daimons —`}
          </div>
        )}
      </div>
    </div>
  );
}

function BucketSection({ bucket, list }: { bucket: Bucket; list: DaimonItem[] }) {
  return (
    <section>
      <SectionHeader tone={BUCKET_TONE[bucket]} label={BUCKET_LABEL[bucket]} count={list.length} />
      <ListCard>
        {list.map((d) => (
          <DaimonRow key={`${d.name}@${d.host}`} daimon={d} />
        ))}
      </ListCard>
    </section>
  );
}

function DaimonRow({ daimon }: { daimon: DaimonItem }) {
  const initials = daimon.name
    .split(/[-_]/)
    .filter(Boolean)
    .slice(0, 2)
    .map((s) => s[0]?.toUpperCase() ?? '')
    .join('');
  return (
    <Link
      to={`/daimons/${encodeURIComponent(daimon.name)}`}
      className="block px-4 py-3 hover:bg-slate-50/60 transition-colors"
    >
      <div className="flex items-center gap-4">
        <div className="w-10 h-10 shrink-0 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white text-xs font-semibold shadow-sm">
          {initials || <Cpu size={14} />}
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-medium text-ink truncate">{daimon.name}</span>
            {daimon.desired_suspended && (
              <span className="text-[10px] uppercase tracking-wide text-yellow-700 bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">
                <Pause size={9} className="inline -mt-0.5 mr-0.5" />
                suspended
              </span>
            )}
          </div>
          <div className="text-xs text-ink-dim font-mono truncate">
            {daimon.host || 'unknown host'}
          </div>
        </div>

        <div className="hidden md:block w-44 text-xs text-ink-dim shrink-0">
          {daimon.provider ? (
            <>
              <div className="text-ink">{daimon.provider}</div>
              <div className="text-[11px] text-ink-mute truncate">{daimon.model}</div>
            </>
          ) : '—'}
        </div>

        <div className="hidden lg:flex w-32 shrink-0 flex-col items-start">
          <div className="text-xs text-ink-dim flex items-center gap-1">
            <Activity size={11} className="text-ink-mute" />
            {daimon.last_tick_count} tick{daimon.last_tick_count === 1 ? '' : 's'}
          </div>
          <TickBar count={daimon.last_tick_count} />
        </div>

        <HeartbeatBadge daimon={daimon} />

        <div className="w-28 shrink-0 text-[11px] text-ink-mute hidden xl:block truncate">
          <div className="text-ink-dim">{summarizeConfig(daimon)}</div>
        </div>

        <ChevronRight size={14} className="text-ink-mute shrink-0" />
      </div>
    </Link>
  );
}

function HeartbeatBadge({ daimon }: { daimon: DaimonItem }) {
  if (!daimon.last_heartbeat_at) {
    return (
      <span className="inline-flex items-center gap-1 text-[11px] text-ink-mute w-24 shrink-0">
        <span className="w-1.5 h-1.5 rounded-full bg-slate-300" />
        never
      </span>
    );
  }
  const age = daimon.heartbeat_age_sec;
  const healthy = daimon.healthy;
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

function groupDaimons(list: DaimonItem[]): Record<Bucket, DaimonItem[]> {
  const out: Record<Bucket, DaimonItem[]> = {
    healthy:   [],
    degraded:  [],
    suspended: [],
    never:     [],
  };
  for (const d of list) {
    if (d.desired_suspended) out.suspended.push(d);
    else if (!d.last_heartbeat_at) out.never.push(d);
    else if (d.healthy) out.healthy.push(d);
    else out.degraded.push(d);
  }
  return out;
}

function formatAge(sec: number): string {
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}

function summarizeConfig(d: DaimonItem): string {
  const parts: string[] = [];
  if (d.desired_max_turns !== undefined && d.desired_max_turns !== null) parts.push(`max=${d.desired_max_turns}`);
  if (d.desired_effort) parts.push(`effort=${d.desired_effort}`);
  if (parts.length === 0) return 'default';
  return parts.join(' · ');
}
