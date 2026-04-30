// Settings → LLM keys (Task Group I).
//
// Renders the per-CP fleet_env summary: Anthropic + OpenAI API keys
// that are distributed to every node's okesu-jobs.service and to
// federated child CPs. Plaintext is write-only — the GET payload only
// carries `*_set` flags + last4s. Editing is "paste new key, save",
// never round-trip the plaintext through the browser.
//
// On a federated child CP the page is read-only by default: source ===
// 'federated_from_parent' means the parent's keys are mirrored down
// here. The operator can flip the source via "Override locally" before
// editing, and revert back via "Revert to parent" (which also drops
// any local override and starts mirroring the parent again).

import { useEffect, useState } from 'react';
import { Brain, Loader2 } from 'lucide-react';
import { api, type FleetEnvSummary, type FleetEnvPatch } from '../../api';

type KeyField = 'anthropic' | 'openai';

export default function LLMKeysSection() {
  const [summary, setSummary] = useState<FleetEnvSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [anthropicInput, setAnthropicInput] = useState('');
  const [openaiInput, setOpenaiInput] = useState('');

  async function load() {
    try {
      const s = await api.fleetEnv();
      setSummary(s);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => { load(); }, []);

  async function run(label: string, fn: () => Promise<FleetEnvSummary>, onDone?: () => void) {
    setBusy(label);
    setError(null);
    try {
      await fn();
      await load();
      onDone?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function handleSave(field: KeyField) {
    const value = field === 'anthropic' ? anthropicInput : openaiInput;
    if (!value) return;
    const patch: FleetEnvPatch = field === 'anthropic'
      ? { anthropic_api_key: value }
      : { openai_api_key: value };
    await run(`save-${field}`, () => api.fleetEnvUpdate(patch), () => {
      if (field === 'anthropic') setAnthropicInput('');
      else setOpenaiInput('');
    });
  }

  async function handleClear(field: KeyField) {
    const label = field === 'anthropic' ? 'Anthropic' : 'OpenAI';
    if (!confirm(`Remove the saved ${label} key?`)) return;
    const patch: FleetEnvPatch = field === 'anthropic'
      ? { anthropic_api_key: '' }
      : { openai_api_key: '' };
    await run(`clear-${field}`, () => api.fleetEnvUpdate(patch));
  }

  async function handleOverride() {
    await run('override', () => api.fleetEnvOverrideLocal());
  }

  async function handleRevert() {
    await run('revert', () => api.fleetEnvRevertToParent());
  }

  if (summary === null && error === null) {
    return (
      <div className="p-6">
        <div className="text-xs text-ink-mute inline-flex items-center gap-2">
          <Loader2 size={12} className="animate-spin" /> Loading…
        </div>
      </div>
    );
  }

  const isFederated = summary?.source === 'federated_from_parent';
  const hasParent = summary?.parent_cp_id != null;

  return (
    <div className="p-6 space-y-6 max-w-3xl">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Brain size={18} className="text-brand-500" /> LLM API keys
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Fleet-wide Anthropic + OpenAI keys distributed to every node's <code>okesu-jobs.service</code>{' '}
          and to federated child CPs. Stored AES-GCM-encrypted at rest.
        </p>
      </header>

      {error && (
        <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
      )}

      {summary && isFederated && (
        <div className="border border-amber-200 bg-amber-50 text-amber-900 rounded-md px-3 py-2.5 text-xs flex items-start justify-between gap-3">
          <div className="leading-relaxed">
            Inherited from parent CP <code>{summary.parent_cp_id ?? '(unknown)'}</code>. Updates from
            the parent will overwrite these values automatically. Click <em>Override locally</em> to
            manage them on this CP.
          </div>
          <button
            onClick={handleOverride}
            disabled={busy !== null}
            className="text-xs px-2 py-1 bg-red-50 hover:bg-red-100 text-red-700 rounded inline-flex items-center gap-1 shrink-0 disabled:opacity-50"
          >
            {busy === 'override' && <Loader2 size={12} className="animate-spin" />}
            Override locally
          </button>
        </div>
      )}

      {summary && (
        <div className="space-y-3">
          <KeyCard
            field="anthropic"
            label="Anthropic API key"
            isSet={summary.anthropic_set}
            last4={summary.anthropic_last4}
            value={anthropicInput}
            onChange={setAnthropicInput}
            onSave={() => handleSave('anthropic')}
            onClear={() => handleClear('anthropic')}
            readOnly={isFederated}
            busy={busy}
          />
          <KeyCard
            field="openai"
            label="OpenAI API key"
            isSet={summary.openai_set}
            last4={summary.openai_last4}
            value={openaiInput}
            onChange={setOpenaiInput}
            onSave={() => handleSave('openai')}
            onClear={() => handleClear('openai')}
            readOnly={isFederated}
            busy={busy}
          />
        </div>
      )}

      {summary && !isFederated && hasParent && (
        <div className="border border-border bg-slate-50 rounded-md px-3 py-2.5 text-xs flex items-start justify-between gap-3">
          <div className="leading-relaxed text-ink-dim">
            Local override of values from <code>{summary.parent_cp_id}</code>. Click{' '}
            <em>Revert to parent</em> to start receiving updates from{' '}
            <code>{summary.parent_cp_id}</code> again.
          </div>
          <button
            onClick={handleRevert}
            disabled={busy !== null}
            className="text-xs px-2 py-1 bg-brand-50 hover:bg-brand-100 text-brand-700 rounded inline-flex items-center gap-1 shrink-0 disabled:opacity-50"
          >
            {busy === 'revert' && <Loader2 size={12} className="animate-spin" />}
            Revert to parent
          </button>
        </div>
      )}

      {summary && (
        <footer className="text-[11px] text-ink-mute pt-2 border-t border-border">
          Version {summary.version} · last updated {humanize(summary.updated_at)} by{' '}
          {summary.updated_by_user_email ?? 'system'}.
        </footer>
      )}
    </div>
  );
}

interface KeyCardProps {
  field: KeyField;
  label: string;
  isSet: boolean;
  last4: string;
  value: string;
  onChange: (v: string) => void;
  onSave: () => void;
  onClear: () => void;
  readOnly: boolean;
  busy: string | null;
}

function KeyCard({
  field, label, isSet, last4, value, onChange, onSave, onClear, readOnly, busy,
}: KeyCardProps) {
  const saveBusy = busy === `save-${field}`;
  const clearBusy = busy === `clear-${field}`;
  // Save is disabled when the input is empty OR we're in read-only
  // (federated) mode. Note the spec wording is "Disabled when input
  // is empty AND federated", but the natural UX is: empty input =
  // nothing to save (always disabled), federated = no save at all
  // (Save button hidden).
  const saveDisabled = !value || readOnly || busy !== null;

  return (
    <section className="border border-border rounded-xl bg-panel">
      <header className="px-4 py-2.5 border-b border-border flex items-center justify-between">
        <span className="text-sm font-medium">{label}</span>
        {isSet ? (
          <span className="font-mono text-xs text-ink">••••{last4}</span>
        ) : (
          <span className="text-xs text-ink-mute">Not set</span>
        )}
      </header>
      <div className="px-4 py-3 space-y-2">
        <input
          type="password"
          value={value}
          disabled={readOnly}
          onChange={(e) => onChange(e.target.value)}
          placeholder={readOnly ? 'Inherited from parent — override to edit' : 'Paste new key…'}
          className="w-full px-3 py-1.5 text-sm font-mono border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 disabled:bg-slate-50 disabled:text-ink-mute"
          autoComplete="off"
        />
        {!readOnly && (
          <div className="flex items-center gap-2">
            <button
              onClick={onSave}
              disabled={saveDisabled}
              className="text-xs px-2 py-1 bg-brand-50 hover:bg-brand-100 text-brand-700 rounded inline-flex items-center gap-1 disabled:opacity-50"
            >
              {saveBusy && <Loader2 size={12} className="animate-spin" />}
              Save
            </button>
            {isSet && (
              <button
                onClick={onClear}
                disabled={busy !== null}
                className="text-xs px-2 py-1 bg-red-50 hover:bg-red-100 text-red-700 rounded inline-flex items-center gap-1 disabled:opacity-50"
              >
                {clearBusy && <Loader2 size={12} className="animate-spin" />}
                Clear
              </button>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

// humanize formats an ISO timestamp into a relative-style string
// (e.g. "5 minutes ago"). For older values it falls back to a plain
// locale string. Mirrors the rough shape used elsewhere in Settings,
// kept tiny + dependency-free.
function humanize(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const diff = Date.now() - t;
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return 'just now';
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} minute${min === 1 ? '' : 's'} ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} hour${hr === 1 ? '' : 's'} ago`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day} day${day === 1 ? '' : 's'} ago`;
  return new Date(t).toLocaleString();
}
