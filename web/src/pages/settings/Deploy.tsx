import { ChangeEvent, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
  Cpu,
  Fingerprint,
  HardDrive,
  Trash2,
  Upload,
} from 'lucide-react';
import { api, ApiError, type BinaryItem, type KnownHostItem } from '../../api';
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
          Daemon binaries for each target architecture, and the SSH host keys
          pinned for each registered node.
        </p>
      </header>

      <BinariesCard />
      <KnownHostsCard />
    </div>
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

// ── known hosts ────────────────────────────────────────────────────────────

function KnownHostsCard() {
  const [list, setList] = useState<KnownHostItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = () => {
    api.knownHosts().then(setList).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  async function clearOne(id: number, name: string) {
    if (!confirm(`Clear pinned host key for "${name}"? The next deploy will re-establish trust on first connect (TOFU).`)) return;
    try {
      await api.clearNodeKnownHost(id);
      refresh();
    } catch (e) {
      alert(String(e));
    }
  }

  return (
    <Card title="Pinned SSH host keys" subtitle="The CP TOFU-pins each node's host key on first deploy. Mismatches abort all subsequent deploys until cleared.">
      {error && <Notice tone="err">{error}</Notice>}

      {list === null ? (
        <p className="text-sm text-ink-mute">Loading…</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-ink-mute">No nodes have been deployed yet — the table fills up as deploys run.</p>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-left text-[11px] uppercase tracking-wide text-ink-mute border-b border-border">
            <tr>
              <th className="py-2 font-medium">Node</th>
              <th className="py-2 font-medium">Key type</th>
              <th className="py-2 font-medium">Fingerprint</th>
              <th className="py-2 font-medium w-32">Pinned</th>
              <th className="py-2 font-medium w-20"></th>
            </tr>
          </thead>
          <tbody>
            {list.map((kh) => (
              <tr key={kh.node_id} className="border-b border-border/60 last:border-0 align-top">
                <td className="py-2.5">
                  <div className="text-sm font-medium">{kh.node_name}</div>
                  <div className="text-[11px] text-ink-mute font-mono">{kh.hostname}:{kh.ssh_port}</div>
                </td>
                <td className="py-2.5 text-xs font-mono text-ink-dim">{kh.key_type}</td>
                <td className="py-2.5 text-xs font-mono text-ink-dim break-all">
                  <Fingerprint size={11} className="inline -mt-0.5 mr-0.5 text-ink-mute" />
                  {kh.fingerprint}
                </td>
                <td className="py-2.5 text-[11px] text-ink-mute">
                  {fmtRel(kh.accepted_at)}
                  {kh.accepted_by_email && <div>by {kh.accepted_by_email}</div>}
                </td>
                <td className="py-2.5 text-right">
                  <button
                    onClick={() => clearOne(kh.node_id, kh.node_name || `#${kh.node_id}`)}
                    className="text-xs px-2 py-1 border border-border rounded-md hover:bg-red-50 hover:text-red-700 inline-flex items-center gap-1"
                  >
                    <Trash2 size={11} />
                    Clear
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <Notice tone="info">
        After legitimate host-key rotation, click <strong>Clear</strong> and the next
        deploy will re-pin via TOFU. If you weren't expecting a change, treat the
        mismatch as a security event and investigate before clearing.
      </Notice>
    </Card>
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
