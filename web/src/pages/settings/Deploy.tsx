import { ChangeEvent, ReactNode, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
  Cpu,
  Fingerprint,
  HardDrive,
  Key,
  Trash2,
  Upload,
} from 'lucide-react';
import { api, ApiError, type BinaryItem, type DeployKeyStatus } from '../../api';
import { cn } from '../../lib/cn';

export default function DeploySection() {
  return (
    <div className="p-6 max-w-5xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <HardDrive size={18} className="text-brand-500" />
          Deploy
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Daemon binaries for each target architecture and the SSH key used to
          reach nodes.
        </p>
      </header>

      <SSHKeyCard />
      <BinariesCard />
    </div>
  );
}

// ── SSH key (CP-wide fallback) ─────────────────────────────────────────────

function SSHKeyCard() {
  const [status, setStatus] = useState<DeployKeyStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [keyText, setKeyText] = useState('');
  const [saving, setSaving] = useState(false);

  const refresh = () => {
    setError(null);
    api.deployKeyStatus().then(setStatus).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  const onSave = async () => {
    setError(null);
    if (!keyText.trim()) {
      setError('paste a PEM-encoded private key first');
      return;
    }
    setSaving(true);
    try {
      const next = await api.setDeployKey(keyText);
      setStatus(next);
      setKeyText('');
      setEditing(false);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const onClear = async () => {
    if (!confirm('Remove the stored deploy SSH key? Future deploys will require a per-request private_key again.')) return;
    setError(null);
    try {
      await api.clearDeployKey();
      refresh();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  };

  return (
    <Card
      title={<><Key size={14} className="inline -mt-0.5 mr-1.5 text-brand-500" />SSH key</>}
      subtitle="A single private key the CP uses to reach every node, when a deploy request doesn't carry its own. The key bytes never leave the server after being saved — only the fingerprint is shown here."
    >
      {status && status.configured && !editing && (
        <div className="space-y-2 text-sm">
          <div className="flex items-center gap-2 flex-wrap">
            <Fingerprint size={14} className="text-ink-mute" />
            <span className="font-mono text-xs break-all">{status.fingerprint || 'unparseable'}</span>
            {status.key_type && (
              <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 px-1.5 py-0.5 rounded">
                {status.key_type}
              </span>
            )}
          </div>
          {status.comment && <div className="text-xs text-ink-dim">{status.comment}</div>}
          <div className="flex gap-2 pt-2">
            <button
              onClick={() => setEditing(true)}
              disabled={!status.adapter_writable}
              className="text-xs px-2.5 py-1.5 rounded-md bg-brand-500 text-white hover:bg-brand-600 disabled:opacity-50 disabled:cursor-not-allowed"
              title={status.adapter_writable ? 'Replace the stored key' : 'The configured secrets adapter is read-only'}
            >
              Replace
            </button>
            <button
              onClick={onClear}
              disabled={!status.adapter_writable}
              className="text-xs px-2.5 py-1.5 rounded-md border border-border hover:bg-slate-50 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Trash2 size={11} className="inline -mt-0.5 mr-1" />Remove
            </button>
          </div>
        </div>
      )}

      {(!status?.configured || editing) && (
        <div className="space-y-2">
          {!status?.adapter_writable && (
            <Notice tone="warn">
              The configured secrets adapter is read-only. To store a key, point the CP at a writable
              source (e.g. <code className="font-mono bg-yellow-100 px-1 py-0.5 rounded">--secrets-source file://&hellip;</code>)
              or rotate via the underlying store directly.
            </Notice>
          )}
          <textarea
            value={keyText}
            onChange={(e) => setKeyText(e.target.value)}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;…&#10;-----END OPENSSH PRIVATE KEY-----"
            rows={8}
            spellCheck={false}
            disabled={!status?.adapter_writable}
            className="w-full font-mono text-[11px] p-2.5 border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 disabled:bg-slate-50"
          />
          <div className="flex gap-2 items-center">
            <button
              onClick={onSave}
              disabled={saving || !status?.adapter_writable}
              className="text-xs px-3 py-1.5 rounded-md bg-brand-500 text-white hover:bg-brand-600 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
            {editing && (
              <button
                onClick={() => { setEditing(false); setKeyText(''); setError(null); }}
                className="text-xs px-3 py-1.5 rounded-md border border-border hover:bg-slate-50"
              >
                Cancel
              </button>
            )}
            <span className="text-[11px] text-ink-mute ml-auto">
              Encrypted (passphrase-protected) keys are rejected — decrypt locally first.
            </span>
          </div>
        </div>
      )}

      {error && <Notice tone="err">{error}</Notice>}
    </Card>
  );
}

// ── binaries ───────────────────────────────────────────────────────────────

function BinariesCard() {
  const [data, setData] = useState<{ dir: string; binaries: BinaryItem[] } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = () => {
    api.deployBinaries().then(setData).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  return (
    <Card title="Daemon binaries" subtitle="Per-arch executables uploaded to nodes during a deploy.">
      {data && data.dir === '' && (
        <Notice tone="warn">
          The CP has no <code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--daemon-binaries-dir</code>
          {' '}configured. Multi-arch deploys are disabled; the deploy will fall back to
          {' '}<code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--daemon-binary</code>.
        </Notice>
      )}

      {error && <Notice tone="err">{error}</Notice>}

      {data && data.dir !== '' && (
        <>
          <p className="text-xs text-ink-dim mb-3 font-mono">
            Storage directory: <span className="text-ink">{data.dir}</span>
          </p>
          {data.binaries.length === 0 ? (
            <p className="text-sm text-ink-mute mb-4">No binaries uploaded yet.</p>
          ) : (
            <ul className="space-y-1.5 mb-4">
              {data.binaries.map((b) => (
                <BinaryRow key={b.name} binary={b} onChanged={refresh} />
              ))}
            </ul>
          )}
          <UploadForm onUploaded={refresh} />
        </>
      )}
    </Card>
  );
}

function BinaryRow({ binary, onChanged }: { binary: BinaryItem; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function remove() {
    if (!confirm(`Delete ${binary.name}?`)) return;
    setBusy(true); setError(null);
    try {
      await api.deleteBinary(binary.name);
      onChanged();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="bg-slate-50 border border-border rounded-md px-3 py-2 flex items-start gap-3">
      <div className="w-9 h-9 shrink-0 rounded-md bg-gradient-to-br from-slate-700 to-slate-900 flex items-center justify-center text-white">
        <Cpu size={14} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium font-mono">{binary.name}</span>
          <span className="text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100">
            {binary.os}/{binary.arch}
          </span>
        </div>
        <div className="text-[11px] text-ink-mute font-mono truncate">
          sha256:{binary.sha256.slice(0, 16)}… · {fmtBytes(binary.size_bytes)} · uploaded {fmtRel(binary.uploaded_at)}
          {binary.uploaded_by_email && <> by {binary.uploaded_by_email}</>}
        </div>
        {error && <div className="text-xs text-red-700 mt-1">{error}</div>}
      </div>
      <button
        onClick={remove}
        disabled={busy}
        className="text-xs px-1.5 py-1 text-ink-mute hover:text-red-600 hover:bg-red-50 rounded-md inline-flex items-center"
      >
        <Trash2 size={11} />
      </button>
    </li>
  );
}

function UploadForm({ onUploaded }: { onUploaded: () => void }) {
  const [osName, setOsName] = useState('linux');
  const [arch, setArch] = useState('amd64');
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  function pickFile(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0] ?? null;
    setFile(f);
    setError(null);
  }

  async function submit() {
    if (!file) {
      setError('Select a binary file to upload.');
      return;
    }
    setBusy(true); setError(null);
    try {
      await api.uploadBinary(osName, arch, file);
      setFile(null);
      if (fileRef.current) fileRef.current.value = '';
      onUploaded();
    } catch (e) {
      if (e instanceof ApiError) setError(e.message);
      else setError(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="border border-dashed border-border rounded-md p-3 space-y-2">
      <div className="text-[11px] uppercase tracking-wide text-ink-mute font-medium">Upload binary</div>
      <div className="flex flex-wrap items-end gap-2 text-sm">
        <Field label="OS">
          <select value={osName} onChange={(e) => setOsName(e.target.value)} className={inputCls}>
            <option value="linux">linux</option>
            <option value="darwin">darwin</option>
            <option value="windows">windows</option>
          </select>
        </Field>
        <Field label="Arch">
          <select value={arch} onChange={(e) => setArch(e.target.value)} className={inputCls}>
            <option value="amd64">amd64</option>
            <option value="arm64">arm64</option>
            <option value="arm">arm</option>
            <option value="386">386</option>
            <option value="riscv64">riscv64</option>
            <option value="s390x">s390x</option>
            <option value="ppc64le">ppc64le</option>
          </select>
        </Field>
        <Field label="Binary file">
          <input
            ref={fileRef}
            type="file"
            onChange={pickFile}
            className="text-xs file:mr-2 file:px-2 file:py-1 file:rounded file:border-0 file:bg-slate-100 file:text-ink"
          />
        </Field>
        <button
          onClick={submit}
          disabled={busy || !file}
          className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 disabled:bg-brand-500/50 text-white text-xs font-medium px-3 py-1.5 rounded-md"
        >
          <Upload size={12} />
          {busy ? 'Uploading…' : 'Upload'}
        </button>
      </div>
      {error && <Notice tone="err">{error}</Notice>}
      <p className="text-[11px] text-ink-mute">
        Stored as <code className="font-mono bg-slate-100 px-1 py-0.5 rounded">okesu-{osName}-{arch}</code>.
        Re-uploading replaces an existing binary with the same os/arch.
      </p>
    </div>
  );
}

// ── shared ─────────────────────────────────────────────────────────────────

function Card({ title, subtitle, children }: { title: ReactNode; subtitle?: string; children: React.ReactNode }) {
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

const inputCls = "px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

function fmtRel(iso: string): string {
  const d = new Date(iso);
  const sec = (Date.now() - d.getTime()) / 1000;
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
