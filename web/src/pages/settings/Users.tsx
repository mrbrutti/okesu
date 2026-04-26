import { FormEvent, useEffect, useState } from 'react';
import { Plus, Trash2, UserCog, X } from 'lucide-react';
import { api, ApiError, type UserItem } from '../../api';
import { cn } from '../../lib/cn';

export default function UsersSection() {
  const [users, setUsers] = useState<UserItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  const refresh = () => {
    api.users().then(setUsers).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  return (
    <div className="p-6 max-w-4xl space-y-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold">Users</h2>
          <p className="text-xs text-ink-dim mt-0.5">Manage operators and their roles.</p>
        </div>
        <button
          onClick={() => setShowAdd(true)}
          className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-sm font-medium px-3 py-1.5 rounded-md"
        >
          <Plus size={14} />
          New user
        </button>
      </header>

      {error && (
        <div className="text-sm text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
      )}

      {users === null && <p className="text-ink-mute">Loading…</p>}

      {users && users.length > 0 && (
        <div className="bg-panel border border-border rounded-xl shadow-card divide-y divide-border overflow-hidden">
          {users.map((u) => (
            <UserRow key={u.id} user={u} onChanged={refresh} />
          ))}
        </div>
      )}

      {showAdd && (
        <CreateUserModal
          onClose={() => setShowAdd(false)}
          onCreated={() => { setShowAdd(false); refresh(); }}
        />
      )}
    </div>
  );
}

function UserRow({ user, onChanged }: { user: UserItem; onChanged: () => void }) {
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [role, setRole] = useState(user.role);
  const [resetPw, setResetPw] = useState('');

  const initials = user.email.split('@')[0].slice(0, 2).toUpperCase();

  async function save() {
    setBusy(true); setError(null);
    try {
      const patch: { role?: string; password?: string } = {};
      if (role !== user.role) patch.role = role;
      if (resetPw) patch.password = resetPw;
      if (Object.keys(patch).length === 0) {
        setEditing(false);
        return;
      }
      await api.patchUser(user.id, patch);
      setEditing(false);
      setResetPw('');
      onChanged();
    } catch (e) {
      if (e instanceof ApiError) setError(e.message);
      else setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!confirm(`Remove user ${user.email}?`)) return;
    setBusy(true); setError(null);
    try {
      await api.deleteUser(user.id);
      onChanged();
    } catch (e) {
      if (e instanceof ApiError) setError(e.message);
      else setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="px-4 py-3">
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 shrink-0 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white text-xs font-semibold shadow-sm">
          {initials}
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-ink truncate">{user.email}</div>
          <div className="text-[11px] text-ink-mute">
            {user.has_password ? 'Local password' : 'SSO only'} · created {fmtDate(user.created_at)}
          </div>
        </div>

        {!editing ? (
          <>
            <RolePill role={user.role} />
            <button
              onClick={() => setEditing(true)}
              className="text-xs px-2.5 py-1 border border-border rounded-md hover:bg-slate-50 inline-flex items-center gap-1"
            >
              <UserCog size={11} />
              Edit
            </button>
            <button
              onClick={remove}
              disabled={busy}
              className="text-xs px-1.5 py-1 text-ink-mute hover:text-red-600 hover:bg-red-50 rounded-md inline-flex items-center"
            >
              <Trash2 size={11} />
            </button>
          </>
        ) : (
          <>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value as UserItem['role'])}
              className="text-xs px-2 py-1 border border-border rounded-md bg-white"
            >
              <option value="admin">admin</option>
              <option value="operator">operator</option>
              <option value="viewer">viewer</option>
            </select>
            <button
              onClick={save}
              disabled={busy}
              className="text-xs px-2.5 py-1 bg-brand-500 hover:bg-brand-600 text-white rounded-md font-medium"
            >
              Save
            </button>
            <button
              onClick={() => { setEditing(false); setError(null); setRole(user.role); setResetPw(''); }}
              className="text-xs px-2.5 py-1 border border-border rounded-md"
            >
              Cancel
            </button>
          </>
        )}
      </div>

      {editing && (
        <div className="mt-3 ml-12 max-w-md">
          <label className="block text-[11px] uppercase tracking-wide text-ink-mute font-medium mb-1">
            Reset password (optional, ≥ 8 chars)
          </label>
          <input
            type="password"
            value={resetPw}
            onChange={(e) => setResetPw(e.target.value)}
            placeholder="leave blank to keep existing password"
            className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30"
          />
        </div>
      )}

      {error && (
        <div className="mt-2 ml-12 text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1 rounded-md">
          {error}
        </div>
      )}
    </div>
  );
}

function RolePill({ role }: { role: UserItem['role'] }) {
  const cls = {
    admin:    'bg-purple-50 text-purple-700 ring-purple-200',
    operator: 'bg-brand-50 text-brand-700 ring-brand-100',
    viewer:   'bg-slate-50 text-slate-700 ring-slate-200',
  }[role];
  return (
    <span className={cn('text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1', cls)}>
      {role}
    </span>
  );
}

function CreateUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<UserItem['role']>('viewer');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      await api.createUser({ email, role, password });
      onCreated();
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
      else setError(String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <form onSubmit={submit} className="bg-panel border border-border rounded-xl shadow-card w-full max-w-md">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold">New user</h3>
          <button type="button" onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <Field label="Email">
            <input value={email} onChange={(e) => setEmail(e.target.value)} type="email" required className={inputCls} />
          </Field>
          <Field label="Role">
            <select value={role} onChange={(e) => setRole(e.target.value as UserItem['role'])} className={inputCls}>
              <option value="viewer">viewer</option>
              <option value="operator">operator</option>
              <option value="admin">admin</option>
            </select>
          </Field>
          <Field label="Password (≥ 8 chars)">
            <input value={password} onChange={(e) => setPassword(e.target.value)} type="password" required minLength={8} className={inputCls} />
          </Field>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-2.5 py-1.5 rounded-md">{error}</div>
          )}
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button type="button" onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button
            type="submit"
            disabled={busy || !email || password.length < 8}
            className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium"
          >
            {busy ? 'Creating…' : 'Create user'}
          </button>
        </footer>
      </form>
    </div>
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

const inputCls = "w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white";

function fmtDate(iso: string): string {
  return new Date(iso).toLocaleDateString();
}
