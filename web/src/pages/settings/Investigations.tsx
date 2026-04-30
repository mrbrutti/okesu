// Investigations settings — tunable knobs for the suggestion engine.
//
// Admin-only (route-gated upstream). Exposes:
//
//   • Score threshold      — candidates below this don't surface
//   • Per-signal weights   — dedup_key, ioc, ioc_cross_cp, host_window, daimon_sev
//   • Window sizes         — host ±N min, daimon_sev N h, ioc_cross_cp N h + min observations
//
// "Reset to defaults" wipes the persisted blob; the server then
// answers GETs with built-in defaults (the meta row is not strictly
// required to exist).

import { useEffect, useState } from 'react';
import { AlertTriangle, RefreshCw, Save, Sparkles } from 'lucide-react';
import { api, ApiError, type SuggestionSettings, type SuggestionSignal } from '../../api';

const SIGNAL_LABEL: Record<SuggestionSignal, { name: string; help: string }> = {
  dedup_key: {
    name: 'dedup_key',
    help: 'Two findings share the same dedup_key (the daimon emitted them as the same logical event).',
  },
  ioc: {
    name: 'ioc',
    help: 'Two findings have observations on the same IOC.',
  },
  ioc_cross_cp: {
    name: 'ioc_cross_cp',
    help: 'Same IOC as the linked finding, AND the IOC has been observed widely (≥ N times in the configured window). Approximates a "campaign" IOC.',
  },
  host_window: {
    name: 'host_window',
    help: 'Same host as a linked finding, within ±N minutes of its ts.',
  },
  daimon_sev: {
    name: 'daimon_sev',
    help: 'Same emitting daimon + severity as a linked finding, within the last N hours.',
  },
};

const SIGNAL_ORDER: SuggestionSignal[] = [
  'dedup_key',
  'ioc',
  'ioc_cross_cp',
  'host_window',
  'daimon_sev',
];

export default function InvestigationsSection() {
  const [settings, setSettings] = useState<SuggestionSettings | null>(null);
  const [draft, setDraft] = useState<SuggestionSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  function load() {
    setError(null); setSaved(false);
    api.investigations.settings.get()
      .then((s) => { setSettings(s); setDraft(s); })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(() => { load(); }, []);

  async function save() {
    if (!draft) return;
    setBusy(true); setError(null); setSaved(false);
    try {
      const after = await api.investigations.settings.put(draft);
      setSettings(after); setDraft(after); setSaved(true);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  function reset() {
    // "Reset to defaults" — clear all weight overrides and the
    // threshold; the server merges defaults back in on save.
    if (!confirm('Reset to default thresholds and weights? Persisted overrides will be lost.')) return;
    setDraft({
      threshold: 0,
      weights: {},
      host_window_minutes: 0,
      daimon_sev_window_hours: 0,
      ioc_cross_cp_min_observations: 0,
      ioc_cross_cp_window_hours: 0,
      autolink_threshold: 0,
    });
  }

  if (settings === null || draft === null) {
    return (
      <div className="p-6 text-sm text-ink-mute">Loading…</div>
    );
  }

  const dirty = JSON.stringify(settings) !== JSON.stringify(draft);

  return (
    <div className="p-6 max-w-3xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Sparkles size={18} className="text-brand-500" />
          Investigations
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Tune the suggestion engine. Changes apply immediately to the workspace card and Findings drawer banner.
        </p>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-xs px-3 py-2 rounded-md flex items-start gap-2">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      )}
      {saved && !dirty && (
        <div className="bg-green-50 border border-green-200 text-green-800 text-xs px-3 py-2 rounded-md">
          Settings saved. The next workspace refresh will use the new values.
        </div>
      )}

      <Card title="Score threshold">
        <p className="text-xs text-ink-dim mb-3">
          Findings whose total score falls below this don't surface as suggestions. Each fired signal contributes its weight; sums across all signals.
        </p>
        <NumberRow
          label="Threshold"
          value={draft.threshold}
          onChange={(v) => setDraft({ ...draft, threshold: v })}
          min={0}
          step={5}
          suffix="points"
          help={`Default 30. Raise to hide weaker correlations (e.g. lone daimon_sev hits at weight ${draft.weights.daimon_sev ?? 30}).`}
        />
      </Card>

      <Card title="Autolink">
        <p className="text-xs text-ink-dim mb-3">
          When a newly-projected finding scores at or above this against an active case, the engine auto-links it without operator action. <strong>0 disables autolink</strong> (default — opt-in). Set higher than the suggestion threshold by design: surfacing a hint is cheap, auto-linking is a commitment.
        </p>
        <NumberRow
          label="Autolink threshold"
          value={draft.autolink_threshold}
          onChange={(v) => setDraft({ ...draft, autolink_threshold: v })}
          min={0}
          step={20}
          suffix={draft.autolink_threshold === 0 ? 'points (disabled)' : 'points'}
          help="Suggested values: 200 (dedup_key + ioc, very high confidence) or 180 (ioc + ioc_cross_cp, campaign IOC)."
        />
      </Card>

      <Card title="Signal weights">
        <p className="text-xs text-ink-dim mb-3">
          Each signal contributes its weight when it fires for a candidate. Higher weight = stronger correlation.
        </p>
        <div className="space-y-3">
          {SIGNAL_ORDER.map((sig) => (
            <NumberRow
              key={sig}
              label={SIGNAL_LABEL[sig].name}
              value={draft.weights[sig] ?? 0}
              onChange={(v) => setDraft({
                ...draft,
                weights: { ...draft.weights, [sig]: v },
              })}
              min={0}
              step={10}
              help={SIGNAL_LABEL[sig].help}
              mono
            />
          ))}
        </div>
      </Card>

      <Card title="Window sizes">
        <p className="text-xs text-ink-dim mb-3">
          Time bounds applied to the temporal signals. Tighter windows reduce false positives; looser windows catch longer-running incidents.
        </p>
        <div className="space-y-3">
          <NumberRow
            label="Host window"
            value={draft.host_window_minutes}
            onChange={(v) => setDraft({ ...draft, host_window_minutes: v })}
            min={0}
            step={15}
            suffix="minutes (±)"
            help="Same-host signal: candidate must be within ±N minutes of a linked finding's ts."
          />
          <NumberRow
            label="Daimon+severity window"
            value={draft.daimon_sev_window_hours}
            onChange={(v) => setDraft({ ...draft, daimon_sev_window_hours: v })}
            min={0}
            step={1}
            suffix="hours"
            help="Daimon+severity signal: lookback window for matching findings."
          />
          <NumberRow
            label="Cross-CP IOC window"
            value={draft.ioc_cross_cp_window_hours}
            onChange={(v) => setDraft({ ...draft, ioc_cross_cp_window_hours: v })}
            min={0}
            step={1}
            suffix="hours"
            help="Cross-CP IOC signal: lookback for counting observations on the matched IOC."
          />
          <NumberRow
            label="Cross-CP IOC min observations"
            value={draft.ioc_cross_cp_min_observations}
            onChange={(v) => setDraft({ ...draft, ioc_cross_cp_min_observations: v })}
            min={1}
            step={1}
            suffix="observations"
            help="Cross-CP IOC signal: only fires when the IOC has at least this many observations within the window."
          />
        </div>
      </Card>

      <footer className="flex items-center gap-2">
        <button
          onClick={save}
          disabled={!dirty || busy}
          className="text-xs px-3 py-1.5 rounded-md bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 inline-flex items-center gap-1.5"
        >
          <Save size={12} />
          {busy ? 'Saving…' : 'Save changes'}
        </button>
        <button
          onClick={reset}
          disabled={busy}
          className="text-xs px-3 py-1.5 rounded-md border border-border text-ink hover:bg-slate-100 disabled:opacity-50 inline-flex items-center gap-1.5"
        >
          <RefreshCw size={12} />
          Reset to defaults
        </button>
        {dirty && (
          <span className="text-[11px] text-amber-700 font-medium ml-2">Unsaved changes</span>
        )}
      </footer>
    </div>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-border rounded-md bg-white p-4">
      <h3 className="text-[11px] uppercase tracking-wide font-semibold text-ink-mute mb-3">
        {title}
      </h3>
      {children}
    </div>
  );
}

function NumberRow({
  label, value, onChange, min, step, suffix, help, mono,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  min?: number;
  step?: number;
  suffix?: string;
  help?: string;
  mono?: boolean;
}) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-2 items-start">
      <div>
        <div className={`text-sm ${mono ? 'font-mono' : 'font-medium'}`}>{label}</div>
        {help && <p className="text-[11px] text-ink-mute mt-0.5">{help}</p>}
      </div>
      <div className="md:col-span-2 flex items-center gap-2">
        <input
          type="number"
          value={value}
          min={min}
          step={step}
          onChange={(e) => onChange(Number(e.target.value))}
          className="w-28 px-2.5 py-1.5 rounded-md border border-border text-sm font-mono text-right"
        />
        {suffix && <span className="text-xs text-ink-mute">{suffix}</span>}
      </div>
    </div>
  );
}
