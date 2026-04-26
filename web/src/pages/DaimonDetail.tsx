import { FormEvent, useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  Activity,
  ArrowLeft,
  Clock,
  Cpu,
  Hash,
  MessageSquare,
  Pause,
  Save,
  Settings2,
  ShieldAlert,
  Tag,
} from 'lucide-react';
import { api, type DaimonItem } from '../api';
import { cn } from '../lib/cn';
import EventTimeline from '../components/EventTimeline';
import AgentMessages from '../components/AgentMessages';
import AgentFindings from '../components/AgentFindings';
import { useLiveEventsPrefs } from '../lib/preferences';

type Tab = 'overview' | 'events' | 'messages' | 'findings' | 'config';

export default function DaimonDetailPage() {
  const { name = '' } = useParams<{ name: string }>();
  const [daimon, setDaimon] = useState<DaimonItem | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('messages');
  const [livePrefs] = useLiveEventsPrefs();

  useEffect(() => {
    let cancelled = false;
    const refresh = () => {
      api.daimon(name)
        .then((d) => { if (!cancelled) setDaimon(d); })
        .catch((err) => { if (!cancelled) setError(String(err)); });
    };
    refresh();
    const t = setInterval(refresh, 6_000);
    return () => { cancelled = true; clearInterval(t); };
  }, [name]);

  if (error && !daimon) {
    return (
      <div className="p-6">
        <BackLink />
        <div className="mt-4 text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
          {error}
        </div>
      </div>
    );
  }

  if (!daimon) {
    return <div className="p-6 text-ink-mute">Loading…</div>;
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 pt-4 pb-0 border-b border-border bg-panel">
        <BackLink />
        <div className="mt-2 flex items-center gap-4">
          <div className="w-12 h-12 shrink-0 rounded-xl bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white font-semibold shadow-sm">
            {daimon.name.slice(0, 2).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 flex-wrap">
              <h1 className="text-lg font-semibold">{daimon.name}</h1>
              {daimon.healthy && daimon.last_heartbeat_at && (
                <span className="inline-flex items-center gap-1 text-[11px] text-green-700 bg-green-50 ring-1 ring-green-200 px-1.5 py-0.5 rounded">
                  <span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
                  healthy
                </span>
              )}
              {!daimon.healthy && daimon.last_heartbeat_at && (
                <span className="inline-flex items-center gap-1 text-[11px] text-yellow-700 bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">
                  stale heartbeat
                </span>
              )}
              {daimon.desired_suspended && (
                <span className="inline-flex items-center gap-1 text-[11px] text-yellow-700 bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">
                  <Pause size={9} /> suspended
                </span>
              )}
            </div>
            <p className="text-xs text-ink-dim font-mono mt-0.5">
              {daimon.host || 'unknown host'} · {daimon.provider || '—'} {daimon.model || ''}
            </p>
          </div>

          {/* Quick stat tiles */}
          <div className="hidden md:flex items-stretch gap-3">
            <Stat label="Ticks" value={String(daimon.last_tick_count)} icon={Activity} />
            <Stat
              label="Heartbeat"
              value={daimon.last_heartbeat_at ? formatAge(daimon.heartbeat_age_sec) : 'never'}
              icon={Clock}
            />
            <Stat label="Version" value={daimon.version || '—'} icon={Hash} />
          </div>
        </div>

        {/* Tabs */}
        <nav className="mt-4 -mb-px flex gap-1">
          {([
            ['messages', 'Messages',     MessageSquare],
            ['findings', 'Findings',     ShieldAlert],
            ['events',   'Live Events',  Activity],
            ['overview', 'Overview',     Cpu],
            ['config',   'Config',       Settings2],
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
      </header>

      <main className="flex-1 overflow-hidden">
        {tab === 'messages' && <AgentMessages agentName={daimon.name} />}
        {tab === 'findings' && <AgentFindings agentName={daimon.name} />}
        {tab === 'events' && (
          <EventTimeline
            agentFilter={daimon.name}
            showHeader={false}
            compact
            defaultMode="all"
            enableGrouping={livePrefs.applyToAgentDetail}
            emptyHint={`No events from ${daimon.name} yet. Heartbeats and tick lifecycle events will appear here as they arrive.`}
          />
        )}
        {tab === 'overview' && <OverviewTab daimon={daimon} />}
        {tab === 'config' && <ConfigTab daimon={daimon} onChanged={setDaimon} />}
      </main>
    </div>
  );
}

function BackLink() {
  return (
    <Link to="/daimons" className="inline-flex items-center text-xs text-ink-dim hover:text-ink">
      <ArrowLeft size={12} className="mr-1" /> Daimons
    </Link>
  );
}

function Stat({ label, value, icon: Icon }: { label: string; value: string; icon: typeof Activity }) {
  return (
    <div className="bg-panel border border-border rounded-lg px-3 py-1.5 flex items-center gap-2 shadow-card">
      <Icon size={14} className="text-ink-mute" />
      <div>
        <div className="text-[10px] uppercase tracking-wide text-ink-mute font-medium">{label}</div>
        <div className="text-xs font-medium text-ink">{value}</div>
      </div>
    </div>
  );
}

function OverviewTab({ daimon }: { daimon: DaimonItem }) {
  return (
    <div className="p-6 grid grid-cols-1 lg:grid-cols-2 gap-4">
      <Card title="Identity">
        <Row label="Name" mono>{daimon.name}</Row>
        <Row label="Host" mono>{daimon.host}</Row>
        <Row label="Provider">{daimon.provider || '—'}</Row>
        <Row label="Model" mono>{daimon.model || '—'}</Row>
        <Row label="Version" mono>{daimon.version || '—'}</Row>
      </Card>
      <Card title="Lifecycle">
        <Row label="Registered" mono>{daimon.registered_at}</Row>
        <Row label="Last heartbeat" mono>{daimon.last_heartbeat_at ?? 'never'}</Row>
        <Row label="Tick count">{daimon.last_tick_count}</Row>
        <Row label="Healthy">{daimon.healthy ? 'yes' : 'no'}</Row>
      </Card>
      <Card title="Desired Configuration" wide>
        <Row label="Max turns">{daimon.desired_max_turns ?? <span className="text-ink-mute italic">no override</span>}</Row>
        <Row label="Effort">{daimon.desired_effort || <span className="text-ink-mute italic">no override</span>}</Row>
        <Row label="Suspended">{daimon.desired_suspended ? 'yes' : 'no'}</Row>
        {daimon.config_updated_at && (
          <Row label="Updated" mono>{daimon.config_updated_at}</Row>
        )}
      </Card>
    </div>
  );
}

function ConfigTab({ daimon, onChanged }: { daimon: DaimonItem; onChanged: (d: DaimonItem) => void }) {
  const [maxTurns, setMaxTurns] = useState<string>(daimon.desired_max_turns?.toString() ?? '');
  const [effort, setEffort] = useState<string>(daimon.desired_effort ?? '');
  const [suspended, setSuspended] = useState<boolean>(daimon.desired_suspended);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(null); setSaved(false);
    try {
      const updated = await api.patchDaimon(daimon.name, {
        max_turns: maxTurns === '' ? 0 : parseInt(maxTurns, 10),
        effort,
        suspended,
      });
      onChanged(updated);
      setSaved(true);
      setTimeout(() => setSaved(false), 2200);
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="p-6 max-w-2xl">
      <Card title="Hot configuration push">
        <p className="text-xs text-ink-dim mb-4">
          Pushed to the daemon on its next config-poll tick. Empty fields mean
          &ldquo;use the value from the agent file.&rdquo;
        </p>
        <form onSubmit={handleSave} className="space-y-4">
          <div>
            <label className="block text-xs font-medium text-ink-dim mb-1.5">
              <Tag size={10} className="inline mr-1" /> Max turns
            </label>
            <input
              type="number"
              min={0}
              value={maxTurns}
              onChange={(e) => setMaxTurns(e.target.value)}
              placeholder="(no override)"
              className="w-48 px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-ink-dim mb-1.5">Effort</label>
            <select
              value={effort}
              onChange={(e) => setEffort(e.target.value)}
              className="w-48 px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white"
            >
              <option value="">(no override)</option>
              <option value="low">low</option>
              <option value="medium">medium</option>
              <option value="high">high</option>
              <option value="xhigh">xhigh</option>
              <option value="max">max</option>
            </select>
          </div>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={suspended}
              onChange={(e) => setSuspended(e.target.checked)}
              className="rounded border-border text-brand-500 focus:ring-brand-500/30"
            />
            <span className="text-sm">Suspended (daemon skips ticks until cleared)</span>
          </label>

          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}

          <div className="pt-2 flex items-center gap-3">
            <button
              type="submit"
              disabled={busy}
              className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 disabled:bg-brand-500/60 text-white text-sm font-medium px-3 py-1.5 rounded-md"
            >
              <Save size={14} />
              {busy ? 'Saving…' : 'Save'}
            </button>
            {saved && <span className="text-xs text-green-700">✓ Saved</span>}
            {daimon.config_updated_at && !saved && (
              <span className="text-xs text-ink-mute">
                Last updated {daimon.config_updated_at}
              </span>
            )}
          </div>
        </form>
      </Card>
    </div>
  );
}

function Card({ title, children, wide }: { title: string; children: React.ReactNode; wide?: boolean }) {
  return (
    <section className={cn('bg-panel border border-border rounded-xl shadow-card p-5', wide && 'lg:col-span-2')}>
      <h3 className="text-sm font-semibold mb-3">{title}</h3>
      <dl className="space-y-1.5">{children}</dl>
    </section>
  );
}

function Row({ label, children, mono }: { label: string; children: React.ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[140px_1fr] text-sm gap-2">
      <dt className="text-ink-dim">{label}</dt>
      <dd className={cn('text-ink truncate', mono && 'font-mono text-xs')}>{children}</dd>
    </div>
  );
}

function formatAge(sec: number): string {
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
