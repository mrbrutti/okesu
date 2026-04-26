import { FormEvent, useEffect, useState } from 'react';
import { Check, KeyRound, LogOut, Monitor } from 'lucide-react';
import { api, ApiError, type SessionInfo, type User } from '../../api';

interface Props {
  user: User;
}

export default function ProfileSection({ user }: Props) {
  return (
    <div className="p-6 max-w-2xl space-y-6">
      <Header title="Profile" subtitle="Your account, password, and active sessions." />

      <Card>
        <Row label="Email"><span className="font-mono">{user.email}</span></Row>
        <Row label="Role">
          <span className="inline-flex items-center text-xs font-medium px-1.5 py-0.5 rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100 capitalize">
            {user.role}
          </span>
        </Row>
        <Row label="User ID"><span className="font-mono text-xs text-ink-dim">#{user.id}</span></Row>
      </Card>

      <PasswordCard />
      <SessionsCard />
    </div>
  );
}

function PasswordCard() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    if (next.length < 8) {
      setMsg({ kind: 'err', text: 'New password must be at least 8 characters.' });
      return;
    }
    if (next !== confirm) {
      setMsg({ kind: 'err', text: 'Passwords do not match.' });
      return;
    }
    setBusy(true);
    try {
      await api.changeMyPassword(current, next);
      setCurrent(''); setNext(''); setConfirm('');
      setMsg({ kind: 'ok', text: 'Password updated.' });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setMsg({ kind: 'err', text: 'Current password is incorrect.' });
      } else if (err instanceof ApiError && err.status === 400) {
        setMsg({ kind: 'err', text: err.message });
      } else {
        setMsg({ kind: 'err', text: String(err) });
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Change password" icon={KeyRound}>
      <form onSubmit={submit} className="space-y-3">
        <PasswordField label="Current password"     value={current} onChange={setCurrent} autoComplete="current-password" />
        <PasswordField label="New password"          value={next}    onChange={setNext}    autoComplete="new-password" />
        <PasswordField label="Confirm new password"  value={confirm} onChange={setConfirm} autoComplete="new-password" />
        {msg && (
          <div className={
            msg.kind === 'ok'
              ? 'text-xs text-green-700 bg-green-50 border border-green-200 px-2.5 py-1.5 rounded-md'
              : 'text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1.5 rounded-md'
          }>
            {msg.text}
          </div>
        )}
        <button
          type="submit"
          disabled={busy || !current || !next || !confirm}
          className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 disabled:bg-brand-500/50 text-white text-sm font-medium px-3 py-1.5 rounded-md"
        >
          <Check size={14} />
          {busy ? 'Saving…' : 'Update password'}
        </button>
      </form>
    </Card>
  );
}

function SessionsCard() {
  const [list, setList] = useState<SessionInfo[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = () => {
    api.mySessions().then(setList).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  async function revoke() {
    if (!confirm('Sign out of all other sessions?')) return;
    setBusy(true); setError(null);
    try {
      await api.revokeOtherSessions();
      refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Active sessions" icon={Monitor}>
      {error && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1.5 rounded-md mb-3">{error}</div>
      )}
      {list === null ? (
        <p className="text-sm text-ink-mute">Loading…</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-ink-mute">No active sessions.</p>
      ) : (
        <ul className="space-y-1.5 mb-3">
          {list.map((s) => (
            <li key={s.id} className="flex items-center justify-between text-sm bg-slate-50 border border-border rounded-md px-3 py-2">
              <div>
                <div className="font-mono text-xs text-ink-dim">{s.id}</div>
                <div className="text-[11px] text-ink-mute">
                  Issued {fmtRel(s.created_at)} · expires {fmtRel(s.expires_at)}
                </div>
              </div>
              {s.is_current && (
                <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-green-50 text-green-700 ring-1 ring-green-200">
                  current
                </span>
              )}
            </li>
          ))}
        </ul>
      )}

      {list && list.length > 1 && (
        <button
          onClick={revoke}
          disabled={busy}
          className="inline-flex items-center gap-1.5 text-xs px-2.5 py-1.5 border border-border rounded-md hover:bg-slate-50"
        >
          <LogOut size={11} />
          {busy ? '…' : 'Sign out of other sessions'}
        </button>
      )}
    </Card>
  );
}

function PasswordField({ label, value, onChange, autoComplete }: {
  label: string; value: string; onChange: (v: string) => void; autoComplete?: string;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-ink-dim mb-1.5">{label}</label>
      <input
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoComplete={autoComplete}
        className="w-full max-w-md px-3 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
      />
    </div>
  );
}

function Header({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <header>
      <h2 className="text-lg font-semibold">{title}</h2>
      <p className="text-xs text-ink-dim mt-0.5">{subtitle}</p>
    </header>
  );
}

function Card({ title, icon: Icon, children }: {
  title?: string; icon?: typeof Check; children: React.ReactNode;
}) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card">
      {title && (
        <header className="px-5 pt-4 pb-2 border-b border-border flex items-center gap-2">
          {Icon && <Icon size={14} className="text-ink-dim" />}
          <h3 className="text-sm font-semibold">{title}</h3>
        </header>
      )}
      <div className="p-5">
        {children}
      </div>
    </section>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] items-center text-sm py-1.5 first:pt-0 last:pb-0">
      <dt className="text-ink-dim">{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function fmtRel(iso: string): string {
  const d = new Date(iso);
  const sec = (Date.now() - d.getTime()) / 1000;
  if (sec >= 0) {
    if (sec < 60) return `${Math.floor(sec)}s ago`;
    if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
    if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
    return `${Math.floor(sec / 86400)}d ago`;
  }
  const future = -sec;
  if (future < 3600) return `in ${Math.floor(future / 60)}m`;
  if (future < 86400) return `in ${Math.floor(future / 3600)}h`;
  return `in ${Math.floor(future / 86400)}d`;
}
