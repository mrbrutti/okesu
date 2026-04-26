import { FormEvent, useEffect, useState } from 'react';
import {
  AlertTriangle,
  Check,
  Copy,
  Key,
  Plug,
  Plus,
  Trash2,
  X,
} from 'lucide-react';
import { api, ApiError, type Token } from '../../api';
import { cn } from '../../lib/cn';

const SCOPES = [
  { value: 'findings:write', label: 'findings:write — POST /api/findings/ingest' },
];

export default function IntegrationsSection() {
  const [tokens, setTokens] = useState<Token[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [newToken, setNewToken] = useState<{ row: Token; plaintext: string } | null>(null);

  const refresh = () => {
    api.tokens().then(setTokens).catch((e) => setError(String(e)));
  };
  useEffect(() => { refresh(); }, []);

  return (
    <div className="p-6 max-w-4xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Plug size={18} className="text-brand-500" />
          Integrations
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          API tokens for programmatic access. The token plaintext is shown
          once at creation — store it immediately in your secrets manager.
        </p>
      </header>

      <Card>
        <div className="flex justify-end mb-3">
          <button
            onClick={() => setShowAdd(true)}
            className="inline-flex items-center gap-1.5 bg-brand-500 hover:bg-brand-600 text-white text-xs font-medium px-2.5 py-1.5 rounded-md"
          >
            <Plus size={12} /> New token
          </button>
        </div>
        {error && <Notice tone="err">{error}</Notice>}
        {tokens === null ? (
          <p className="text-sm text-ink-mute">Loading…</p>
        ) : tokens.length === 0 ? (
          <p className="text-sm text-ink-mute">No tokens issued yet.</p>
        ) : (
          <ul className="space-y-1.5">
            {tokens.map((t) => (
              <TokenRow key={t.id} token={t} onChanged={refresh} />
            ))}
          </ul>
        )}
      </Card>

      <Card title="Use an API token" subtitle="Endpoints that accept token auth.">
        <ul className="text-sm space-y-1.5 list-disc pl-5">
          <li>
            <code className="font-mono text-xs bg-slate-100 px-1 py-0.5 rounded">POST /api/findings/ingest</code>
            {' '}— scope <code className="font-mono text-xs">findings:write</code>. Body matches the
            finding shape (severity, title, agent, host, evidence, recommended_action, dedup_key).
          </li>
        </ul>
        <pre className="mt-3 bg-slate-900 text-slate-100 text-[11px] font-mono rounded-md p-3 overflow-auto whitespace-pre-wrap">
{`curl -k https://cp.example.com:8443/api/findings/ingest \\
  -H "Authorization: Bearer okesu_..." \\
  -H "Content-Type: application/json" \\
  -d '{
    "severity": "HIGH",
    "title": "CVE-2024-XXXX present in build",
    "agent": "ci-scan",
    "host": "build-runner-3",
    "evidence": "package.lock: react@17.0.2 (vulnerable)",
    "recommended_action": "Upgrade react to >= 17.0.3",
    "dedup_key": "build:react@17.0.2"
  }'`}
        </pre>
      </Card>

      {showAdd && (
        <CreateTokenModal
          onClose={() => setShowAdd(false)}
          onCreated={(payload) => {
            setShowAdd(false);
            setNewToken(payload);
            refresh();
          }}
        />
      )}
      {newToken && (
        <NewTokenModal token={newToken.row} plaintext={newToken.plaintext} onClose={() => setNewToken(null)} />
      )}
    </div>
  );
}

function TokenRow({ token, onChanged }: { token: Token; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const revoked = !!token.revoked_at;
  const expired = token.expires_at ? new Date(token.expires_at) < new Date() : false;
  const inactive = revoked || expired;

  async function revoke() {
    if (!confirm(`Revoke token "${token.name}"? This cannot be undone.`)) return;
    setBusy(true); setError(null);
    try { await api.revokeToken(token.id); onChanged(); }
    catch (e) { setError(e instanceof ApiError ? e.message : String(e)); }
    finally { setBusy(false); }
  }

  return (
    <li className="bg-slate-50 border border-border rounded-md px-3 py-2 flex items-start gap-3">
      <div className={cn('w-9 h-9 shrink-0 rounded-md flex items-center justify-center text-white shadow-sm',
        inactive ? 'bg-slate-400' : 'bg-gradient-to-br from-brand-500 to-brand-700')}>
        <Key size={14} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className={cn('text-sm font-medium', inactive && 'line-through text-ink-mute')}>
            {token.name}
          </span>
          {revoked && (
            <span className="text-[10px] uppercase tracking-wide text-ink-mute bg-slate-100 ring-1 ring-slate-200 px-1.5 py-0.5 rounded">revoked</span>
          )}
          {expired && !revoked && (
            <span className="text-[10px] uppercase tracking-wide text-yellow-700 bg-yellow-50 ring-1 ring-yellow-200 px-1.5 py-0.5 rounded">expired</span>
          )}
        </div>
        <div className="text-[11px] text-ink-mute font-mono truncate">
          {token.prefix}…{'  ·  '}
          {token.scopes.length > 0 ? token.scopes.join(' ') : 'no scopes'}
          {token.last_used_at ? ` · last used ${fmtRel(token.last_used_at)}` : ' · never used'}
          {token.expires_at && !revoked ? ` · expires ${fmtAbs(token.expires_at)}` : ''}
        </div>
        {error && <div className="text-[11px] text-red-700 mt-1">{error}</div>}
      </div>
      {!inactive && (
        <button
          onClick={revoke}
          disabled={busy}
          className="text-xs px-2 py-1 border border-border rounded-md hover:bg-red-50 hover:text-red-700 inline-flex items-center gap-1"
        >
          <Trash2 size={11} /> Revoke
        </button>
      )}
    </li>
  );
}

function CreateTokenModal({ onClose, onCreated }: { onClose: () => void; onCreated: (p: { row: Token; plaintext: string }) => void }) {
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>(['findings:write']);
  const [expiresIn, setExpiresIn] = useState<number>(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function toggleScope(s: string) {
    setScopes((prev) => prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]);
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      const resp = await api.createToken({
        name,
        scopes,
        expires_in_days: expiresIn || undefined,
      });
      const { token, ...row } = resp;
      onCreated({ row, plaintext: token });
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
          <h3 className="text-sm font-semibold">New API token</h3>
          <button type="button" onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <Field label="Name (for your records)">
            <input value={name} onChange={(e) => setName(e.target.value)} required className={inputCls} placeholder="e.g. CI scanner" />
          </Field>
          <Field label="Scopes">
            <div className="space-y-1.5">
              {SCOPES.map((s) => (
                <label key={s.value} className="flex items-center gap-2 px-2.5 py-1.5 border border-border rounded-md cursor-pointer hover:bg-slate-50">
                  <input
                    type="checkbox"
                    checked={scopes.includes(s.value)}
                    onChange={() => toggleScope(s.value)}
                  />
                  <span className="text-xs">{s.label}</span>
                </label>
              ))}
            </div>
          </Field>
          <Field label="Expires (days)">
            <input
              type="number"
              min={0}
              value={expiresIn}
              onChange={(e) => setExpiresIn(Number(e.target.value))}
              className={inputCls}
              placeholder="0 = no expiry"
            />
          </Field>
          {error && <Notice tone="err">{error}</Notice>}
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button type="button" onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md">Cancel</button>
          <button type="submit" disabled={busy || !name || scopes.length === 0} className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white rounded-md font-medium">
            {busy ? 'Issuing…' : 'Issue token'}
          </button>
        </footer>
      </form>
    </div>
  );
}

function NewTokenModal({ token, plaintext, onClose }: { token: Token; plaintext: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(plaintext);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch { /* ignore */ }
  }
  return (
    <div className="fixed inset-0 bg-black/30 flex items-center justify-center p-4 z-50">
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-lg">
        <header className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h3 className="text-sm font-semibold">Token issued</h3>
          <button type="button" onClick={onClose} className="p-1 text-ink-dim hover:text-ink rounded-md"><X size={16} /></button>
        </header>
        <div className="p-5 space-y-3 text-sm">
          <p className="text-ink-dim">
            Copy <strong>{token.name}</strong> now — you will not be able to retrieve it again.
          </p>
          <div className="flex items-stretch gap-2">
            <code className="flex-1 font-mono text-xs bg-slate-900 text-slate-100 px-3 py-2 rounded-md break-all select-all">
              {plaintext}
            </code>
            <button
              onClick={copy}
              className="inline-flex items-center gap-1.5 px-3 py-2 border border-border rounded-md hover:bg-slate-50 text-xs"
            >
              {copied ? <Check size={12} className="text-green-700" /> : <Copy size={12} />}
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <Notice tone="warn">
            Treat this like a password. Anyone with this token can call the
            scoped endpoints as if they were you. Revoke it from the list if
            it leaks.
          </Notice>
        </div>
        <footer className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 bg-brand-500 hover:bg-brand-600 text-white rounded-md font-medium">
            Done
          </button>
        </footer>
      </div>
    </div>
  );
}

// ── shared ─────────────────────────────────────────────────────────────────

function Card({ title, subtitle, children }: { title?: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5">
      {title && <h3 className="text-sm font-semibold">{title}</h3>}
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
    <div className={cn('text-xs px-3 py-2 rounded-md border flex items-start gap-2', cls)}>
      <AlertTriangle size={12} className="mt-0.5 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

const inputCls = "w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white";

function fmtRel(iso: string): string {
  const d = new Date(iso);
  const sec = (Date.now() - d.getTime()) / 1000;
  if (sec < 60) return `${Math.floor(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
function fmtAbs(iso: string): string {
  return new Date(iso).toLocaleDateString();
}
