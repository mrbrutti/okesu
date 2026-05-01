// Settings → Credentials (Phase 22.8 PR γ).
//
// Admin-only section. Two-pane layout: secrets list on the left,
// the selected secret's bindings + metadata on the right.
//
// Plaintext is never displayed. Creation takes the value once,
// then it's gone — rotation requires re-entering the new value.

import { useEffect, useState } from 'react';
import {
  AlertTriangle,
  Key,
  KeyRound,
  Loader2,
  Lock,
  Plus,
  Save,
  Settings,
  Terminal,
  Trash2,
  X,
} from 'lucide-react';
import {
  ApiError,
  api,
  type Secret,
  type SecretBinding,
  type SecretDetail,
  type SecretKind,
  type SecretScope,
} from '../../api';
import { LabelEditor } from '../../components/labels/LabelEditor';

const KIND_LABELS: Record<SecretKind, string> = {
  env_var: 'ENV var',
  ssh_key: 'SSH key',
  api_key: 'API key',
};

const SCOPE_OPTIONS: SecretScope[] = ['any', 'node', 'daimon', 'agent_run'];
const SCOPE_LABELS: Record<SecretScope, string> = {
  any: 'any (all consumers)',
  node: 'node (deploys)',
  daimon: 'daimon (scheduled runs)',
  agent_run: 'agent_run (ad-hoc)',
};

export default function CredentialsSection() {
  const [secrets, setSecrets] = useState<Secret[] | null>(null);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  function refresh() {
    setError(null);
    api.secrets.list()
      .then((rows) => {
        setSecrets(rows);
        if (selectedID === null && rows.length > 0) setSelectedID(rows[0].id);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }
  useEffect(() => { refresh(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  return (
    <div className="p-6 max-w-6xl space-y-4">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <KeyRound size={18} className="text-brand-500" />
          Credentials
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Encrypted secrets bound to label selectors. ENV vars, SSH keys, and
          API keys are scoped via <code className="bg-slate-100 px-1 rounded">env=prod,team=infra</code>-style
          selectors so deploys + agent runs pick the right credential per node.
          Plaintext is never displayed after save.
        </p>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md flex items-start gap-2">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-[260px_1fr] gap-4">
        <div className="border border-border rounded-md bg-white">
          <header className="px-3 py-2 border-b border-border flex items-center justify-between">
            <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute">
              Secrets {secrets && `· ${secrets.length}`}
            </h3>
            <button
              onClick={() => setShowCreate(true)}
              className="text-[11px] inline-flex items-center gap-1 px-2 py-1 rounded-md bg-brand-50 text-brand-700 hover:bg-brand-100 border border-brand-200"
            >
              <Plus size={11} /> New
            </button>
          </header>
          {secrets === null ? (
            <p className="p-3 text-sm text-ink-mute">Loading…</p>
          ) : secrets.length === 0 ? (
            <p className="p-3 text-sm text-ink-mute italic">No secrets yet.</p>
          ) : (
            <ul className="divide-y divide-border">
              {secrets.map((s) => (
                <li
                  key={s.id}
                  onClick={() => setSelectedID(s.id)}
                  className={`px-3 py-2 cursor-pointer hover:bg-slate-50 ${
                    selectedID === s.id ? 'bg-brand-50' : ''
                  }`}
                >
                  <div className="flex items-center gap-1.5">
                    <KindIcon kind={s.kind as SecretKind} />
                    <span className="text-sm font-medium truncate">{s.name}</span>
                  </div>
                  <div className="text-[10px] uppercase tracking-wide text-ink-mute mt-0.5">
                    {KIND_LABELS[s.kind as SecretKind] ?? s.kind}
                  </div>
                  {s.description && (
                    <div className="text-[11px] text-ink-mute truncate mt-0.5">{s.description}</div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>

        <div>
          {selectedID === null ? (
            <div className="text-sm text-ink-mute italic px-4 py-12 text-center">
              Pick a secret to see its bindings, or click <strong>New</strong> to add one.
            </div>
          ) : (
            <SecretDetailPane
              secretID={selectedID}
              onChanged={refresh}
              onDeleted={() => { setSelectedID(null); refresh(); }}
            />
          )}
        </div>
      </div>

      {showCreate && (
        <CreateSecretDialog
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

function KindIcon({ kind }: { kind: SecretKind }) {
  switch (kind) {
    case 'ssh_key': return <Lock size={11} className="text-violet-700" />;
    case 'api_key': return <Key size={11} className="text-amber-700" />;
    case 'env_var': return <Terminal size={11} className="text-cyan-700" />;
    default:        return <Settings size={11} className="text-ink-mute" />;
  }
}

// ── Secret detail pane ─────────────────────────────────────────────

function SecretDetailPane({
  secretID,
  onChanged,
  onDeleted,
}: {
  secretID: number;
  onChanged: () => void;
  onDeleted: () => void;
}) {
  const [detail, setDetail] = useState<SecretDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [showRotate, setShowRotate] = useState(false);

  function refresh() {
    setError(null);
    api.secrets.get(secretID)
      .then(setDetail)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }
  useEffect(() => { refresh(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [secretID]);

  if (!detail) {
    return (
      <div className="text-sm text-ink-mute flex items-center gap-2 px-4 py-12">
        <Loader2 size={14} className="animate-spin" /> Loading…
      </div>
    );
  }

  const s = detail.secret;

  async function destroy() {
    if (!confirm(`Delete secret "${s.name}"? Bindings will be removed too. This cannot be undone.`)) return;
    setBusy(true);
    try {
      await api.secrets.delete(secretID);
      onDeleted();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      <div className="border border-border rounded-md bg-white p-4">
        <header className="flex items-start justify-between gap-3 mb-3">
          <div className="min-w-0">
            <h3 className="text-base font-semibold flex items-center gap-2">
              <KindIcon kind={s.kind as SecretKind} />
              {s.name}
            </h3>
            {s.description && <p className="text-xs text-ink-dim mt-0.5">{s.description}</p>}
          </div>
          <div className="flex items-center gap-1">
            <button
              onClick={() => setShowRotate(true)}
              className="text-xs px-2 py-1 rounded-md border border-border hover:bg-slate-50"
            >
              Rotate value
            </button>
            <button
              onClick={destroy}
              disabled={busy}
              className="p-1.5 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded-md"
              title="Delete secret"
            >
              <Trash2 size={13} />
            </button>
          </div>
        </header>
        <dl className="text-xs text-ink-dim grid grid-cols-2 gap-2">
          <div>
            <dt className="text-[10px] uppercase tracking-wide text-ink-mute">Kind</dt>
            <dd className="font-mono">{KIND_LABELS[s.kind as SecretKind] ?? s.kind}</dd>
          </div>
          <div>
            <dt className="text-[10px] uppercase tracking-wide text-ink-mute">Created</dt>
            <dd className="font-mono">{s.created_at}</dd>
          </div>
          {s.created_by_email && (
            <div className="col-span-2">
              <dt className="text-[10px] uppercase tracking-wide text-ink-mute">Created by</dt>
              <dd className="font-mono">{s.created_by_email}</dd>
            </div>
          )}
        </dl>
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md">
          {error}
        </div>
      )}

      <div id="labels-card" className="border border-border rounded-md bg-white p-4 transition-shadow">
        <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-3">
          Labels
        </h3>
        <LabelEditor kind="secret" idOrKey={secretID} />
      </div>

      <div className="border border-border rounded-md bg-white p-4">
        <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-3">
          Bindings ({detail.bindings.length})
        </h3>
        <p className="text-[11px] text-ink-mute mb-3">
          The secret applies wherever the selector matches a node's labels and the scope matches the consumer.
        </p>
        <ul className="space-y-1.5 mb-3">
          {detail.bindings.length === 0 ? (
            <li className="text-xs text-ink-mute italic">No bindings yet — secret is unused until you add one.</li>
          ) : detail.bindings.map((b) => (
            <BindingRow key={b.id} secretID={secretID} binding={b} onChanged={refresh} />
          ))}
        </ul>
        <AddBindingRow secretID={secretID} onAdded={refresh} onError={setError} />
      </div>

      {showRotate && (
        <RotateValueDialog
          secret={s}
          onClose={() => setShowRotate(false)}
          onRotated={() => { setShowRotate(false); refresh(); onChanged(); }}
        />
      )}
    </div>
  );
}

function BindingRow({
  secretID,
  binding,
  onChanged,
}: {
  secretID: number;
  binding: SecretBinding;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <li className="flex items-center gap-2 text-sm">
      <span className="text-[10px] uppercase tracking-wide bg-slate-100 text-slate-700 px-1.5 py-0.5 rounded font-medium">
        {binding.scope}
      </span>
      <code className="text-xs font-mono bg-slate-50 px-2 py-0.5 rounded flex-1 truncate">
        {binding.selector || '(CP-wide)'}
      </code>
      <button
        onClick={async () => {
          setBusy(true);
          try { await api.secrets.removeBinding(secretID, binding.id); onChanged(); }
          finally { setBusy(false); }
        }}
        disabled={busy}
        className="p-1 text-ink-mute hover:text-red-700 hover:bg-red-50 rounded-md"
        title="Remove binding"
      >
        <X size={11} />
      </button>
    </li>
  );
}

function AddBindingRow({
  secretID,
  onAdded,
  onError,
}: {
  secretID: number;
  onAdded: () => void;
  onError: (msg: string) => void;
}) {
  const [scope, setScope] = useState<SecretScope>('any');
  const [selector, setSelector] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <select
        value={scope}
        onChange={(e) => setScope(e.target.value as SecretScope)}
        className="text-xs px-2 py-1 rounded-md border border-border bg-white"
      >
        {SCOPE_OPTIONS.map((s) => (
          <option key={s} value={s}>{SCOPE_LABELS[s]}</option>
        ))}
      </select>
      <input
        type="text"
        value={selector}
        onChange={(e) => setSelector(e.target.value)}
        placeholder="selector — empty = CP-wide; e.g. env=prod,team=infra"
        className="flex-1 text-xs px-2 py-1 rounded-md border border-border bg-white font-mono"
      />
      <button
        onClick={async () => {
          setBusy(true);
          try { await api.secrets.addBinding(secretID, selector, scope); setSelector(''); onAdded(); }
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

// ── Create dialog ──────────────────────────────────────────────────

function CreateSecretDialog({ onClose, onCreated }: {
  onClose: () => void;
  onCreated: (id: number) => void;
}) {
  const [name, setName] = useState('');
  const [kind, setKind] = useState<SecretKind>('env_var');
  const [description, setDescription] = useState('');
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!name.trim() || !value) return;
    setBusy(true); setError(null);
    try {
      const s = await api.secrets.create({
        name: name.trim(),
        kind,
        description: description.trim(),
        value,
      });
      onCreated(s.id);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="New secret" onClose={onClose}>
      <Field label="Name">
        <input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
          placeholder="e.g. PROD_ANTHROPIC_API_KEY"
        />
      </Field>
      <Field label="Kind">
        <select
          value={kind}
          onChange={(e) => setKind(e.target.value as SecretKind)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
        >
          <option value="env_var">ENV var (injected at run time)</option>
          <option value="ssh_key">SSH key (deploys)</option>
          <option value="api_key">API key (generic)</option>
        </select>
      </Field>
      <Field label="Description">
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md"
          placeholder="Short purpose of this credential"
        />
      </Field>
      <Field label="Value">
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          rows={kind === 'ssh_key' ? 8 : 3}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
          placeholder="Plaintext credential — sealed at rest, never displayed again"
        />
        <p className="text-[11px] text-ink-mute mt-1">
          Encrypted with AES-256-GCM under a key derived from the CP master.
          You won't be able to view this value after save — only rotate or delete.
        </p>
      </Field>
      {error && <Notice>{error}</Notice>}
      <Footer onCancel={onClose}>
        <button
          onClick={submit}
          disabled={busy || !name.trim() || !value}
          className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md disabled:opacity-50 inline-flex items-center gap-1"
        >
          {busy ? <Loader2 size={12} className="animate-spin" /> : <Plus size={12} />}
          Create
        </button>
      </Footer>
    </Modal>
  );
}

function RotateValueDialog({ secret, onClose, onRotated }: {
  secret: Secret;
  onClose: () => void;
  onRotated: () => void;
}) {
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!value) return;
    setBusy(true); setError(null);
    try {
      await api.secrets.update(secret.id, { value });
      onRotated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={`Rotate ${secret.name}`} onClose={onClose}>
      <Field label="New value">
        <textarea
          autoFocus
          value={value}
          onChange={(e) => setValue(e.target.value)}
          rows={secret.kind === 'ssh_key' ? 8 : 3}
          className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md font-mono"
          placeholder="New plaintext"
        />
        <p className="text-[11px] text-ink-mute mt-1">
          Replaces the current value under a new nonce. Existing bindings are
          unchanged.
        </p>
      </Field>
      {error && <Notice>{error}</Notice>}
      <Footer onCancel={onClose}>
        <button
          onClick={submit}
          disabled={busy || !value}
          className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md disabled:opacity-50 inline-flex items-center gap-1"
        >
          {busy ? <Loader2 size={12} className="animate-spin" /> : <Save size={12} />}
          Rotate
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
