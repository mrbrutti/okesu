// Settings → Groups (Phase 22.8 PR α).
//
// Admin-only section. Two-pane layout: groups list on the left, the
// selected group's detail on the right (members + roles + external
// binding). Mutation goes through the admin endpoints; reads are
// open to any authenticated user (the Profile section reuses the
// same API to show a user their own memberships).

import { useEffect, useState } from 'react';
import {
  AlertTriangle,
  Crown,
  Eye,
  Loader2,
  Plus,
  Save,
  Shield,
  Trash2,
  Users,
  X,
} from 'lucide-react';
import { ApiError, api, type Group, type GroupDetail, type User } from '../../api';
import { LabelEditor } from '../../components/labels/LabelEditor';

export default function GroupsSection() {
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  function refresh() {
    setError(null);
    api.groups.list()
      .then((rows) => {
        setGroups(rows);
        // Auto-select the first group on initial load so the right
        // pane isn't empty.
        if (selectedID === null && rows.length > 0) setSelectedID(rows[0].id);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(() => { refresh(); }, []);

  return (
    <div className="p-6 max-w-6xl space-y-4">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Users size={18} className="text-brand-500" />
          Groups
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Multi-membership RBAC. Each group carries (role, selector) tuples;
          users inherit the union across their groups. Selectors are reserved
          for the next phase (node labels) — leave blank for CP-wide.
        </p>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md flex items-start gap-2">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-[260px_1fr] gap-4">
        {/* Left pane — groups list */}
        <div className="border border-border rounded-md bg-white">
          <header className="px-3 py-2 border-b border-border flex items-center justify-between">
            <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">
              Groups {groups && `· ${groups.length}`}
            </h3>
            <button
              onClick={() => setShowCreate(true)}
              className="text-[11px] inline-flex items-center gap-1 px-2 py-1 rounded-md bg-brand-50 text-brand-700 hover:bg-brand-100 border border-brand-200"
            >
              <Plus size={11} /> New
            </button>
          </header>
          {groups === null ? (
            <p className="p-3 text-sm text-ink-mute">Loading…</p>
          ) : groups.length === 0 ? (
            <p className="p-3 text-sm text-ink-mute italic">No groups yet.</p>
          ) : (
            <ul className="divide-y divide-border">
              {groups.map((g) => (
                <li
                  key={g.id}
                  onClick={() => setSelectedID(g.id)}
                  className={`px-3 py-2 cursor-pointer hover:bg-slate-50 ${
                    selectedID === g.id ? 'bg-brand-50' : ''
                  }`}
                >
                  <div className="text-sm font-medium truncate">{g.name}</div>
                  {g.description && (
                    <div className="text-[11px] text-ink-mute truncate">{g.description}</div>
                  )}
                  {g.external_id && (
                    <div className="text-[10px] text-ink-mute font-mono mt-0.5 truncate">
                      OIDC · {g.external_id}
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Right pane — selected group detail */}
        <div>
          {selectedID === null ? (
            <div className="text-sm text-ink-mute italic px-4 py-12 text-center">
              Pick a group to see its members and roles.
            </div>
          ) : (
            <GroupDetailPane
              groupID={selectedID}
              onChanged={refresh}
              onDeleted={() => { setSelectedID(null); refresh(); }}
            />
          )}
        </div>
      </div>

      {showCreate && (
        <CreateGroupDialog
          onClose={() => setShowCreate(false)}
          onCreated={(id) => {
            setShowCreate(false);
            refresh();
            setSelectedID(id);
          }}
        />
      )}
    </div>
  );
}

// ── Group detail pane ──────────────────────────────────────────────

function GroupDetailPane({
  groupID,
  onChanged,
  onDeleted,
}: {
  groupID: number;
  onChanged: () => void;
  onDeleted: () => void;
}) {
  const [detail, setDetail] = useState<GroupDetail | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editingMeta, setEditingMeta] = useState(false);

  function refresh() {
    setError(null);
    api.groups.get(groupID).then(setDetail).catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
    api.users().then(setUsers).catch(() => { /* ignore */ });
  }

  useEffect(() => { refresh(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [groupID]);

  if (!detail) {
    return (
      <div className="text-sm text-ink-mute flex items-center gap-2 px-4 py-12">
        <Loader2 size={14} className="animate-spin" /> Loading…
      </div>
    );
  }

  const g = detail.group;
  const memberIDs = new Set(detail.members.map((m) => m.id));
  const nonMembers = users.filter((u) => !memberIDs.has(u.id));

  async function deleteGroup() {
    if (!confirm(`Delete group "${g.name}"? Users keep any membership in other groups.`)) return;
    setBusy(true); setError(null);
    try {
      await api.groups.delete(groupID);
      onDeleted();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      {/* Identity card */}
      <div className="border border-border rounded-md bg-white p-4">
        <header className="flex items-start justify-between gap-3 mb-3">
          <div className="min-w-0">
            <h3 className="text-base font-semibold flex items-center gap-2">
              <Users size={14} className="text-brand-500" />
              {g.name}
            </h3>
            {g.description && <p className="text-xs text-ink-dim mt-0.5">{g.description}</p>}
          </div>
          <div className="flex items-center gap-1">
            <button
              onClick={() => setEditingMeta(true)}
              className="text-xs px-2 py-1 rounded-md border border-border hover:bg-slate-50"
            >
              Edit
            </button>
            <button
              onClick={deleteGroup}
              disabled={busy}
              className="p-1.5 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded-md"
              title="Delete group"
            >
              <Trash2 size={13} />
            </button>
          </div>
        </header>
        <dl className="text-xs text-ink-dim grid grid-cols-1 md:grid-cols-2 gap-2">
          <div>
            <dt className="text-[10px] uppercase tracking-wide text-ink-mute">External ID</dt>
            <dd className="font-mono">{g.external_id || <span className="italic text-ink-mute">local-only</span>}</dd>
          </div>
        </dl>
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md">
          {error}
        </div>
      )}

      {/* Labels card — purely descriptive on groups (used to organise
          the team picker rather than for permission scoping; group
          membership itself is the access mechanism). */}
      <div id="labels-card" className="border border-border rounded-md bg-white p-4 transition-shadow">
        <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-3">
          Labels
        </h3>
        <LabelEditor kind="group" idOrKey={groupID} />
      </div>

      {/* Roles card */}
      <div className="border border-border rounded-md bg-white p-4">
        <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-3">
          Roles
        </h3>
        <ul className="space-y-1.5 mb-3">
          {detail.roles.length === 0 ? (
            <li className="text-xs text-ink-mute italic">No roles attached. Add one below.</li>
          ) : detail.roles.map((rr) => (
            <li key={rr.id} className="flex items-center gap-2">
              <RoleChip role={rr.role} />
              {rr.selector ? (
                <code className="text-[11px] bg-slate-100 px-2 py-0.5 rounded font-mono">{rr.selector}</code>
              ) : (
                <span className="text-[11px] text-ink-mute italic">CP-wide</span>
              )}
              <button
                onClick={async () => {
                  setBusy(true); setError(null);
                  try { await api.groups.removeRole(groupID, rr.id); refresh(); }
                  catch (e) { setError(e instanceof ApiError ? e.message : String(e)); }
                  finally { setBusy(false); }
                }}
                className="ml-auto p-1 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded-md"
                title="Remove"
              >
                <X size={11} />
              </button>
            </li>
          ))}
        </ul>
        <AddRoleRow groupID={groupID} onAdded={refresh} onError={setError} />
      </div>

      {/* Members card */}
      <div className="border border-border rounded-md bg-white p-4">
        <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-3">
          Members ({detail.members.length})
        </h3>
        <ul className="space-y-1.5 mb-3">
          {detail.members.length === 0 ? (
            <li className="text-xs text-ink-mute italic">No members. Add users below.</li>
          ) : detail.members.map((m) => (
            <li key={m.id} className="flex items-center gap-2 text-sm">
              <span className="font-mono">{m.email}</span>
              <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 px-1.5 py-0.5 rounded">{m.role}</span>
              <button
                onClick={async () => {
                  setBusy(true); setError(null);
                  try { await api.groups.removeMember(groupID, m.id); refresh(); onChanged(); }
                  catch (e) { setError(e instanceof ApiError ? e.message : String(e)); }
                  finally { setBusy(false); }
                }}
                className="ml-auto p-1 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded-md"
                title="Remove member"
              >
                <X size={11} />
              </button>
            </li>
          ))}
        </ul>
        {nonMembers.length > 0 && (
          <div className="flex items-center gap-2">
            <select
              id="add-member"
              className="text-xs px-2 py-1 rounded-md border border-border bg-white flex-1"
              defaultValue=""
              onChange={async (e) => {
                const uid = Number(e.target.value);
                if (!uid) return;
                setBusy(true); setError(null);
                try { await api.groups.addMember(groupID, uid); refresh(); onChanged(); }
                catch (err) { setError(err instanceof ApiError ? err.message : String(err)); }
                finally { setBusy(false); e.target.value = ''; }
              }}
            >
              <option value="">Add a user…</option>
              {nonMembers.map((u) => (
                <option key={u.id} value={u.id}>{u.email}</option>
              ))}
            </select>
          </div>
        )}
      </div>

      {editingMeta && (
        <EditGroupDialog
          group={g}
          onClose={() => setEditingMeta(false)}
          onSaved={() => { setEditingMeta(false); refresh(); onChanged(); }}
        />
      )}
    </div>
  );
}

// ── Add role row ───────────────────────────────────────────────────

function AddRoleRow({
  groupID,
  onAdded,
  onError,
}: {
  groupID: number;
  onAdded: () => void;
  onError: (err: string) => void;
}) {
  const [role, setRole] = useState('viewer');
  const [selector, setSelector] = useState('');
  const [busy, setBusy] = useState(false);

  return (
    <div className="flex items-center gap-2">
      <select
        value={role}
        onChange={(e) => setRole(e.target.value)}
        className="text-xs px-2 py-1 rounded-md border border-border bg-white"
      >
        <option value="admin">admin</option>
        <option value="operator">operator</option>
        <option value="viewer">viewer</option>
      </select>
      <input
        type="text"
        value={selector}
        onChange={(e) => setSelector(e.target.value)}
        placeholder="selector — empty = CP-wide (PR β: env=prod,team=…)"
        className="flex-1 text-xs px-2 py-1 rounded-md border border-border bg-white font-mono"
      />
      <button
        onClick={async () => {
          setBusy(true);
          try { await api.groups.addRole(groupID, role, selector); setSelector(''); onAdded(); }
          catch (e) { onError(e instanceof ApiError ? e.message : String(e)); }
          finally { setBusy(false); }
        }}
        disabled={busy}
        className="text-xs px-3 py-1 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 inline-flex items-center gap-1"
      >
        {busy ? <Loader2 size={11} className="animate-spin" /> : <Plus size={11} />}
        Add
      </button>
    </div>
  );
}

// ── RoleChip ────────────────────────────────────────────────────────

function RoleChip({ role }: { role: string }) {
  const tone =
    role === 'admin' ? 'bg-red-50 text-red-800 border-red-200' :
    role === 'operator' ? 'bg-blue-50 text-blue-800 border-blue-200' :
    'bg-slate-50 text-slate-700 border-slate-200';
  const Icon = role === 'admin' ? Crown : role === 'operator' ? Shield : Eye;
  return (
    <span className={`text-[11px] px-1.5 py-0.5 rounded border font-medium uppercase tracking-wide inline-flex items-center gap-1 ${tone}`}>
      <Icon size={11} /> {role}
    </span>
  );
}

// ── Create dialog ──────────────────────────────────────────────────

function CreateGroupDialog({ onClose, onCreated }: {
  onClose: () => void;
  onCreated: (id: number) => void;
}) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [externalID, setExternalID] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!name.trim()) return;
    setBusy(true); setError(null);
    try {
      const g = await api.groups.create({
        name: name.trim(),
        description: description.trim(),
        external_id: externalID.trim(),
      });
      onCreated(g.id);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="New group" onClose={onClose}>
      <Field label="Name">
        <input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
          placeholder="e.g. infra-team, security-readers"
        />
      </Field>
      <Field label="Description">
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
          placeholder="Short purpose of this group"
        />
      </Field>
      <Field label="External ID (OIDC binding)">
        <input
          value={externalID}
          onChange={(e) => setExternalID(e.target.value)}
          placeholder="leave empty for local-only groups"
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
        />
        <p className="text-[11px] text-ink-mute mt-1">
          When set, the OIDC login flow auto-attaches users whose groups claim contains this value.
        </p>
      </Field>
      {error && <Notice>{error}</Notice>}
      <Footer onCancel={onClose}>
        <button
          onClick={submit}
          disabled={busy || !name.trim()}
          className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md disabled:opacity-50 inline-flex items-center gap-1"
        >
          {busy ? <Loader2 size={12} className="animate-spin" /> : <Plus size={12} />}
          Create
        </button>
      </Footer>
    </Modal>
  );
}

// ── Edit dialog ────────────────────────────────────────────────────

function EditGroupDialog({ group, onClose, onSaved }: {
  group: Group;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description);
  const [externalID, setExternalID] = useState(group.external_id);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!name.trim()) return;
    setBusy(true); setError(null);
    try {
      await api.groups.update(group.id, {
        name: name.trim(),
        description: description.trim(),
        external_id: externalID.trim(),
      });
      onSaved();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={`Edit ${group.name}`} onClose={onClose}>
      <Field label="Name">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
        />
      </Field>
      <Field label="Description">
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
        />
      </Field>
      <Field label="External ID (OIDC binding)">
        <input
          value={externalID}
          onChange={(e) => setExternalID(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
        />
      </Field>
      {error && <Notice>{error}</Notice>}
      <Footer onCancel={onClose}>
        <button
          onClick={submit}
          disabled={busy}
          className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md disabled:opacity-50 inline-flex items-center gap-1"
        >
          {busy ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
          Save
        </button>
      </Footer>
    </Modal>
  );
}

// ── Modal primitives ────────────────────────────────────────────────

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 bg-black/30 z-50 flex items-center justify-center p-4" onClick={onClose}>
      <div
        className="bg-panel border border-border rounded-xl shadow-card w-full max-w-md flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button onClick={onClose} className="p-1 text-ink-mute hover:text-ink rounded-md">
            <X size={14} />
          </button>
        </header>
        <div className="p-5 space-y-3 text-sm">{children}</div>
      </div>
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

function Notice({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
      {children}
    </div>
  );
}

function Footer({ onCancel, children }: { onCancel: () => void; children: React.ReactNode }) {
  return (
    <div className="-mx-5 -mb-5 mt-2 px-5 py-3 border-t border-border flex items-center justify-end gap-2 bg-slate-50/40">
      <button onClick={onCancel} className="text-xs px-3 py-1.5 border border-border rounded-md">
        Cancel
      </button>
      {children}
    </div>
  );
}
