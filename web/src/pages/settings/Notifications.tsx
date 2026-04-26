import { FormEvent, useEffect, useState } from 'react';
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  Loader2,
  Mail,
  MessageSquare,
  Plus,
  Send,
  Trash2,
  Webhook,
  X,
} from 'lucide-react';
import { api, ApiError, type Channel, type Delivery, type Rule } from '../../api';
import { cn } from '../../lib/cn';

const SEVERITIES = ['INFO', 'LOW', 'MEDIUM', 'HIGH', 'CRITICAL'] as const;

export default function NotificationsSection() {
  return (
    <div className="p-6 max-w-5xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Bell size={18} className="text-brand-500" />
          Notifications
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Route findings to Slack, email, or generic webhooks via configurable rules.
        </p>
      </header>

      <ChannelsCard />
      <RulesCard />
      <DeliveriesCard />
    </div>
  );
}

// ── Channels ───────────────────────────────────────────────────────────────

function ChannelsCard() {
  const [list, setList] = useState<Channel[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  const refresh = () => {
    api.channels().then(setList).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  return (
    <Card title="Channels" subtitle="Where notifications are delivered.">
      <div className="flex justify-end mb-3">
        <button
          onClick={() => setShowAdd(true)}
          className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-xs font-medium px-2.5 py-1.5 rounded-md"
        >
          <Plus size={12} /> Add channel
        </button>
      </div>
      {error && <Notice tone="err">{error}</Notice>}
      {list === null ? (
        <p className="text-sm text-ink-mute">Loading…</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-ink-mute">No channels configured yet.</p>
      ) : (
        <ul className="space-y-1.5">
          {list.map((c) => (
            <ChannelRow key={c.id} channel={c} onChanged={refresh} />
          ))}
        </ul>
      )}
      {showAdd && (
        <ChannelModal onClose={() => setShowAdd(false)} onSaved={() => { setShowAdd(false); refresh(); }} />
      )}
    </Card>
  );
}

function ChannelRow({ channel, onChanged }: { channel: Channel; onChanged: () => void }) {
  const [busy, setBusy] = useState<'test' | 'delete' | 'toggle' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<'ok' | 'err' | null>(null);
  const [showEdit, setShowEdit] = useState(false);

  const Icon = channelTypeIcon(channel.type);

  async function test() {
    setBusy('test'); setError(null); setTestResult(null);
    try {
      await api.testChannel(channel.id);
      setTestResult('ok');
      setTimeout(() => setTestResult(null), 4000);
    } catch (e) {
      setTestResult('err');
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function toggle() {
    setBusy('toggle'); setError(null);
    try {
      await api.patchChannel(channel.id, { enabled: !channel.enabled });
      onChanged();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function remove() {
    if (!confirm(`Delete channel "${channel.name}"? Linked rules will be deleted too.`)) return;
    setBusy('delete'); setError(null);
    try {
      await api.deleteChannel(channel.id);
      onChanged();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <li className="bg-slate-50 border border-border rounded-md px-3 py-2">
      <div className="flex items-center gap-3">
        <div className={cn('w-9 h-9 shrink-0 rounded-md flex items-center justify-center text-white shadow-sm', channelTypeBg(channel.type))}>
          <Icon size={14} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-medium">{channel.name}</span>
            <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-slate-100 text-ink-dim ring-1 ring-slate-200">
              {channel.type}
            </span>
            {!channel.enabled && (
              <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">
                disabled
              </span>
            )}
          </div>
          <div className="text-[11px] text-ink-mute font-mono truncate">
            {channelSummary(channel)}
          </div>
        </div>
        <button
          onClick={test}
          disabled={busy !== null}
          className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-100 inline-flex items-center gap-1"
        >
          {busy === 'test' ? <Loader2 size={11} className="animate-spin" /> : <Send size={11} />}
          Test
        </button>
        <button
          onClick={toggle}
          disabled={busy !== null}
          className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-100"
        >
          {channel.enabled ? 'Disable' : 'Enable'}
        </button>
        <button
          onClick={() => setShowEdit(true)}
          className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-100"
        >
          Edit
        </button>
        <button
          onClick={remove}
          disabled={busy !== null}
          className="text-xs px-1.5 py-1 text-ink-mute hover:text-red-600 hover:bg-red-50 rounded-md inline-flex items-center"
        >
          <Trash2 size={11} />
        </button>
      </div>
      {testResult === 'ok' && (
        <div className="mt-2 ml-12 text-[11px] text-green-700 inline-flex items-center gap-1">
          <CheckCircle2 size={11} /> Test message sent.
        </div>
      )}
      {testResult === 'err' && error && (
        <div className="mt-2 ml-12 text-[11px] text-red-700 break-all">{error}</div>
      )}
      {error && testResult !== 'err' && (
        <div className="mt-2 ml-12 text-[11px] text-red-700">{error}</div>
      )}
      {showEdit && (
        <ChannelModal
          onClose={() => setShowEdit(false)}
          onSaved={() => { setShowEdit(false); onChanged(); }}
          existing={channel}
        />
      )}
    </li>
  );
}

function ChannelModal({
  onClose, onSaved, existing,
}: { onClose: () => void; onSaved: () => void; existing?: Channel }) {
  const [name, setName] = useState(existing?.name ?? '');
  const [type, setType] = useState<Channel['type']>(existing?.type ?? 'webhook');
  const [config, setConfig] = useState<string>(
    existing ? JSON.stringify(existing.config, null, 2) : defaultConfigFor('webhook'),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(null);
    let parsed: unknown;
    try { parsed = JSON.parse(config); }
    catch { setError('Config must be valid JSON.'); setBusy(false); return; }
    try {
      if (existing) {
        await api.patchChannel(existing.id, { name, config: parsed });
      } else {
        await api.createChannel({ name, type, config: parsed });
      }
      onSaved();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="bg-panel border border-border rounded-xl shadow-card w-full max-w-xl">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold">{existing ? 'Edit channel' : 'New channel'}</h3>
          <button type="button" onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <Field label="Name">
            <input value={name} onChange={(e) => setName(e.target.value)} required className={inputCls} />
          </Field>
          <Field label="Type">
            <select
              value={type}
              onChange={(e) => {
                const t = e.target.value as Channel['type'];
                setType(t);
                setConfig(defaultConfigFor(t));
              }}
              disabled={!!existing}
              className={inputCls}
            >
              <option value="webhook">webhook</option>
              <option value="slack">slack</option>
              <option value="email">email</option>
            </select>
          </Field>
          <Field label="Config (JSON)">
            <textarea
              value={config}
              onChange={(e) => setConfig(e.target.value)}
              rows={10}
              className={`${inputCls} font-mono text-xs`}
            />
            <p className="text-[11px] text-ink-mute mt-1">
              {configHelpFor(type)}
            </p>
          </Field>
          {error && <Notice tone="err">{error}</Notice>}
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button type="button" onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            type="submit"
            disabled={busy || !name}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium"
          >
            {busy ? 'Saving…' : (existing ? 'Save' : 'Create')}
          </button>
        </footer>
      </form>
    </div>
  );
}

function defaultConfigFor(type: Channel['type']): string {
  if (type === 'slack') {
    return JSON.stringify({ webhook_url: 'https://hooks.slack.com/services/T.../B.../...', channel_override: '' }, null, 2);
  }
  if (type === 'email') {
    return JSON.stringify({
      smtp_host: 'smtp.example.com',
      smtp_port: 587,
      username: '',
      password: '',
      from: 'Okesu Alerts <alerts@example.com>',
      to: ['ops@example.com'],
      starttls: true,
    }, null, 2);
  }
  return JSON.stringify({ url: 'https://example.com/webhook', secret: '', extra_headers: {} }, null, 2);
}

function configHelpFor(type: Channel['type']): string {
  if (type === 'slack') return 'webhook_url is your Slack incoming webhook. channel_override (optional) targets a specific channel.';
  if (type === 'email') return 'tls=true uses implicit TLS (port 465); starttls=true upgrades plain SMTP via STARTTLS (port 587).';
  return 'url is the destination. When secret is set, the body is signed with HMAC-SHA256 in X-Okesu-Signature.';
}

function channelTypeIcon(t: Channel['type']) {
  return t === 'slack' ? MessageSquare : t === 'email' ? Mail : Webhook;
}
function channelTypeBg(t: Channel['type']): string {
  return t === 'slack' ? 'bg-purple-600' : t === 'email' ? 'bg-blue-600' : 'bg-slate-700';
}
function channelSummary(c: Channel): string {
  const cfg = c.config as Record<string, unknown>;
  if (c.type === 'slack') return String(cfg.webhook_url || '').replace(/^(https?:\/\/[^/]+).*/, '$1/…');
  if (c.type === 'email') return `${cfg.smtp_host}:${cfg.smtp_port} → ${(cfg.to as string[] || []).join(', ')}`;
  return String(cfg.url || '');
}

// ── Rules ──────────────────────────────────────────────────────────────────

function RulesCard() {
  const [rules, setRules] = useState<Rule[] | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  const refresh = () => {
    api.rules().then(setRules).catch((e) => setError(String(e)));
    api.channels().then(setChannels).catch(() => { /* ignore */ });
  };
  useEffect(() => { refresh(); }, []);

  return (
    <Card title="Routing rules" subtitle="Decide which findings reach which channels.">
      <div className="flex justify-end mb-3">
        <button
          onClick={() => setShowAdd(true)}
          disabled={channels.length === 0}
          className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white text-xs font-medium px-2.5 py-1.5 rounded-md"
        >
          <Plus size={12} /> Add rule
        </button>
      </div>
      {error && <Notice tone="err">{error}</Notice>}
      {rules === null ? (
        <p className="text-sm text-ink-mute">Loading…</p>
      ) : rules.length === 0 ? (
        <p className="text-sm text-ink-mute">No rules yet — add a channel first, then create a rule to route findings to it.</p>
      ) : (
        <ul className="space-y-1.5">
          {rules.map((r) => (
            <RuleRow key={r.id} rule={r} channels={channels} onChanged={refresh} />
          ))}
        </ul>
      )}
      {showAdd && (
        <RuleModal channels={channels} onClose={() => setShowAdd(false)} onSaved={() => { setShowAdd(false); refresh(); }} />
      )}
    </Card>
  );
}

function RuleRow({ rule, channels, onChanged }: { rule: Rule; channels: Channel[]; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showEdit, setShowEdit] = useState(false);
  const channel = channels.find((c) => c.id === rule.channel_id);
  return (
    <li className="bg-slate-50 border border-border rounded-md px-3 py-2 flex items-center gap-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium">{rule.name}</span>
          <span className={cn('severity-badge', `severity-${rule.min_severity.toLowerCase()}`)}>
            ≥ {rule.min_severity}
          </span>
          {!rule.enabled && (
            <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">disabled</span>
          )}
        </div>
        <div className="text-[11px] text-ink-mute font-mono truncate">
          → {channel?.name ?? `channel:${rule.channel_id}`}
          {rule.agent_substring ? ` · agent contains "${rule.agent_substring}"` : ''}
          {rule.host_substring  ? ` · host contains "${rule.host_substring}"`   : ''}
        </div>
        {error && <div className="text-[11px] text-red-700 mt-1">{error}</div>}
      </div>
      <button
        onClick={async () => {
          setBusy(true); setError(null);
          try { await api.patchRule(rule.id, { enabled: !rule.enabled }); onChanged(); }
          catch (e) { setError(e instanceof ApiError ? e.message : String(e)); }
          finally { setBusy(false); }
        }}
        disabled={busy}
        className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-100"
      >
        {rule.enabled ? 'Disable' : 'Enable'}
      </button>
      <button onClick={() => setShowEdit(true)} className="text-xs px-2 py-1 border border-border rounded-md hover:bg-slate-100">
        Edit
      </button>
      <button
        onClick={async () => {
          if (!confirm(`Delete rule "${rule.name}"?`)) return;
          setBusy(true); setError(null);
          try { await api.deleteRule(rule.id); onChanged(); }
          catch (e) { setError(e instanceof ApiError ? e.message : String(e)); }
          finally { setBusy(false); }
        }}
        disabled={busy}
        className="text-xs px-1.5 py-1 text-ink-mute hover:text-red-600 hover:bg-red-50 rounded-md"
      >
        <Trash2 size={11} />
      </button>
      {showEdit && (
        <RuleModal
          channels={channels}
          existing={rule}
          onClose={() => setShowEdit(false)}
          onSaved={() => { setShowEdit(false); onChanged(); }}
        />
      )}
    </li>
  );
}

function RuleModal({
  channels, onClose, onSaved, existing,
}: {
  channels: Channel[];
  onClose: () => void;
  onSaved: () => void;
  existing?: Rule;
}) {
  const [name, setName] = useState(existing?.name ?? '');
  const [channelID, setChannelID] = useState(existing?.channel_id ?? channels[0]?.id ?? 0);
  const [minSev, setMinSev] = useState(existing?.min_severity ?? 'HIGH');
  const [agent, setAgent] = useState(existing?.agent_substring ?? '');
  const [host, setHost] = useState(existing?.host_substring ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      if (existing) {
        await api.patchRule(existing.id, {
          name, channel_id: channelID, min_severity: minSev,
          agent_substring: agent, host_substring: host,
        });
      } else {
        await api.createRule({
          name, channel_id: channelID, min_severity: minSev,
          agent_substring: agent || undefined,
          host_substring: host || undefined,
        });
      }
      onSaved();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="bg-panel border border-border rounded-xl shadow-card w-full max-w-md">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold">{existing ? 'Edit rule' : 'New rule'}</h3>
          <button type="button" onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <Field label="Name"><input value={name} onChange={(e) => setName(e.target.value)} required className={inputCls} /></Field>
          <Field label="Channel">
            <select value={channelID} onChange={(e) => setChannelID(Number(e.target.value))} className={inputCls}>
              {channels.map((c) => (
                <option key={c.id} value={c.id}>{c.name} ({c.type})</option>
              ))}
            </select>
          </Field>
          <Field label="Minimum severity (≥)">
            <select value={minSev} onChange={(e) => setMinSev(e.target.value)} className={inputCls}>
              {SEVERITIES.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </Field>
          <Field label="Agent contains (optional)">
            <input value={agent} onChange={(e) => setAgent(e.target.value)} className={inputCls} placeholder="e.g. edr" />
          </Field>
          <Field label="Host contains (optional)">
            <input value={host} onChange={(e) => setHost(e.target.value)} className={inputCls} placeholder="e.g. prod-" />
          </Field>
          {error && <Notice tone="err">{error}</Notice>}
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button type="button" onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button type="submit" disabled={busy || !name} className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium">
            {busy ? 'Saving…' : (existing ? 'Save' : 'Create')}
          </button>
        </footer>
      </form>
    </div>
  );
}

// ── Deliveries ─────────────────────────────────────────────────────────────

function DeliveriesCard() {
  const [list, setList] = useState<Delivery[] | null>(null);
  useEffect(() => {
    const fetch = () => api.deliveries(50).then(setList).catch(() => { /* ignore */ });
    fetch();
    const t = setInterval(fetch, 6000);
    return () => clearInterval(t);
  }, []);
  return (
    <Card title="Recent deliveries" subtitle="The last 50 outbound notification attempts.">
      {list === null ? (
        <p className="text-sm text-ink-mute">Loading…</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-ink-mute">No deliveries yet — once a finding matches an enabled rule it will appear here.</p>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-[11px] uppercase tracking-wide text-ink-mute border-b border-border">
            <tr className="text-left">
              <th className="py-2 font-medium w-32">When</th>
              <th className="py-2 font-medium w-20">Status</th>
              <th className="py-2 font-medium w-20">Severity</th>
              <th className="py-2 font-medium">Title</th>
              <th className="py-2 font-medium w-12 text-center">Try</th>
            </tr>
          </thead>
          <tbody>
            {list.map((d) => (
              <tr key={d.id} className="border-b border-border/60 last:border-0 align-top">
                <td className="py-2 text-[11px] font-mono text-ink-dim">{fmt(d.created_at)}</td>
                <td className="py-2"><DeliveryPill status={d.status} /></td>
                <td className="py-2 text-xs">
                  {d.severity ? (
                    <span className={cn('severity-badge', `severity-${d.severity.toLowerCase()}`)}>{d.severity}</span>
                  ) : '—'}
                </td>
                <td className="py-2 text-sm text-ink truncate max-w-md">
                  {d.title || '—'}
                  {d.error && <div className="text-[11px] text-red-700 break-all">{d.error}</div>}
                </td>
                <td className="py-2 text-center text-[11px] text-ink-mute">{d.attempt}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}

function DeliveryPill({ status }: { status: Delivery['status'] }) {
  const cfg = {
    succeeded: { icon: CheckCircle2, cls: 'text-green-700 bg-green-50 ring-green-200' },
    failed:    { icon: AlertTriangle, cls: 'text-red-700 bg-red-50 ring-red-200' },
    pending:   { icon: Loader2,      cls: 'text-brand-700 bg-brand-50 ring-brand-100' },
  }[status];
  return (
    <span className={cn('inline-flex items-center gap-1 text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1', cfg.cls)}>
      <cfg.icon size={9} className={status === 'pending' ? 'animate-spin' : ''} />
      {status}
    </span>
  );
}

// ── shared ─────────────────────────────────────────────────────────────────

function Card({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5">
      <h3 className="text-sm font-semibold">{title}</h3>
      {subtitle && <p className="text-xs text-ink-dim mt-0.5 mb-4">{subtitle}</p>}
      {children}
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">{label}</div>
      {children}
    </div>
  );
}

function Notice({ tone, children }: { tone: 'err' | 'warn' | 'info'; children: React.ReactNode }) {
  const cls = {
    err:  'bg-red-50 border-red-200 text-red-700',
    warn: 'bg-yellow-50 border-yellow-200 text-yellow-800',
    info: 'bg-slate-50 border-slate-200 text-ink-dim',
  }[tone];
  return (
    <div className={cn('text-xs px-3 py-2 rounded-md border mt-3 flex items-start gap-2', cls)}>
      <AlertTriangle size={12} className="mt-0.5 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

const inputCls = "w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white";

function fmt(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  });
}
