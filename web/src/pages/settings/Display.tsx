import { Activity, Eye, Layers, RefreshCw, ShieldAlert, Sparkles } from 'lucide-react';
import {
  useIOCDisplayPrefs,
  useLiveEventsPrefs,
  DEFAULTS,
} from '../../lib/preferences';

// Settings → Display
//
// Local UI preferences (persisted to localStorage). Currently exposes the
// Live Events grouping knobs. Add new sections here as more display
// settings accumulate; if a setting needs to be fleet-wide, move it to a
// server-side endpoint instead.

export default function DisplaySection() {
  const [prefs, setPrefs] = useLiveEventsPrefs();
  const [iocPrefs, setIOCPrefs] = useIOCDisplayPrefs();

  return (
    <div className="p-6 max-w-4xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Eye size={18} className="text-brand-500" />
          Display
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Local preferences that control how the dashboard renders. Stored in
          your browser only — they don't sync across browsers and don't apply
          to other operators.
        </p>
      </header>

      <Card title="Live Events grouping" subtitle="Collapse repeated events of the same kind into a single row.">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label="Rolling window"
            hint="Events of the same (type, agent) arriving within this many seconds of the most recent collapse into one row. Range 5–60s."
            icon={Layers}
          >
            <NumberInput
              value={prefs.windowSec}
              min={5}
              max={60}
              step={1}
              suffix="s"
              onChange={(n) => setPrefs({ windowSec: n })}
            />
          </Field>

          <Field
            label="Threshold for high-volume types"
            hint="Findings and api_unavailable events stay as individual rows until they exceed this count in the window — then they collapse retroactively. Range 1–50."
            icon={Activity}
          >
            <NumberInput
              value={prefs.highVolumeThreshold}
              min={1}
              max={50}
              step={1}
              onChange={(n) => setPrefs({ highVolumeThreshold: n })}
            />
          </Field>

          <div className="sm:col-span-2 space-y-2">
            <label className="flex items-start gap-2.5 text-sm cursor-pointer hover:bg-slate-50/50 rounded-md px-2 py-2 -mx-2">
              <input
                type="checkbox"
                checked={prefs.smartGrouping}
                onChange={(e) => setPrefs({ smartGrouping: e.target.checked })}
                className="mt-0.5 rounded border-border text-brand-500 focus:ring-brand-500/30"
              />
              <span>
                <span className="font-medium inline-flex items-center gap-1.5">
                  <Sparkles size={12} className="text-brand-500" />
                  Smart grouping
                </span>
                <span className="block text-xs text-ink-dim mt-0.5">
                  Aggressive grouping mode: collapses every event type
                  from the 2nd occurrence (ignores the high-volume
                  threshold), auto-widens the rolling window to at least
                  90 seconds so cyclical events like <code>tick_done</code>
                  actually merge, and promotes hot keys to coarser
                  <code>(type)</code>-only grouping once any
                  <code>(type, agent)</code> exceeds 3 occurrences — a
                  fleet-wide storm renders as one row instead of one per
                  agent. <code>error</code> still never groups.
                </span>
              </span>
            </label>

            <label className="flex items-start gap-2.5 text-sm cursor-pointer hover:bg-slate-50/50 rounded-md px-2 py-2 -mx-2">
              <input
                type="checkbox"
                checked={prefs.applyToAgentDetail}
                onChange={(e) => setPrefs({ applyToAgentDetail: e.target.checked })}
                className="mt-0.5 rounded border-border text-brand-500 focus:ring-brand-500/30"
              />
              <span>
                <span className="font-medium">Also apply to the agent detail page</span>
                <span className="block text-xs text-ink-dim mt-0.5">
                  Without this, the per-agent Live Events tab shows every
                  event individually — useful when you've already drilled into
                  one agent and want maximum detail.
                </span>
              </span>
            </label>
          </div>
        </div>

        <div className="mt-4 pt-3 border-t border-border flex items-center justify-between">
          <p className="text-xs text-ink-mute">
            Changes apply immediately on every Live Events view.
          </p>
          <button
            onClick={() => setPrefs(DEFAULTS.liveEvents)}
            className="inline-flex items-center gap-1.5 text-xs px-2.5 py-1 border border-border rounded-md hover:bg-slate-50 text-ink-dim"
          >
            <RefreshCw size={11} />
            Reset to defaults
          </button>
        </div>
      </Card>

      <Card
        title="IOC display"
        subtitle="How indicator-of-compromise values render across the dashboard."
      >
        <label className="flex items-start gap-2.5 text-sm cursor-pointer hover:bg-slate-50/50 rounded-md px-2 py-2 -mx-2">
          <input
            id="defang_iocs"
            type="checkbox"
            checked={iocPrefs.defang}
            onChange={(e) => setIOCPrefs({ defang: e.target.checked })}
            className="mt-0.5 rounded border-border text-brand-500 focus:ring-brand-500/30"
          />
          <span>
            <span className="font-medium inline-flex items-center gap-1.5">
              <ShieldAlert size={12} className="text-brand-500" />
              Defang IOCs
            </span>
            <span className="block text-xs text-ink-dim mt-0.5">
              Display{' '}
              <code className="font-mono text-[11px] bg-slate-100 px-1 rounded">
                1.2.3[.]4
              </code>{' '}
              instead of{' '}
              <code className="font-mono text-[11px] bg-slate-100 px-1 rounded">
                1.2.3.4
              </code>{' '}
              and{' '}
              <code className="font-mono text-[11px] bg-slate-100 px-1 rounded">
                hxxps://evil[.]com
              </code>{' '}
              instead of the live URL — so a stray click or copy doesn't
              accidentally hit a malicious indicator. Hashes, CVE IDs, and
              MITRE IDs render unchanged regardless. Default on.
            </span>
          </span>
        </label>
      </Card>
    </div>
  );
}

// ── Local primitives ────────────────────────────────────────────────────────

function Card({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5">
      <h3 className="text-sm font-semibold">{title}</h3>
      {subtitle && <p className="text-xs text-ink-dim mt-0.5 mb-4">{subtitle}</p>}
      {children}
    </section>
  );
}

function Field({
  label, hint, icon: Icon, children,
}: {
  label: string;
  hint?: string;
  icon?: typeof Eye;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="flex items-center gap-1.5 text-xs uppercase tracking-wide text-ink-mute font-medium mb-1">
        {Icon && <Icon size={11} />}
        {label}
      </div>
      {children}
      {hint && <p className="mt-1 text-[11px] text-ink-mute leading-snug">{hint}</p>}
    </div>
  );
}

function NumberInput({
  value, min, max, step, suffix, onChange,
}: {
  value: number;
  min: number;
  max: number;
  step: number;
  suffix?: string;
  onChange: (v: number) => void;
}) {
  return (
    <div className="relative">
      <input
        type="number"
        value={value}
        min={min}
        max={max}
        step={step}
        onChange={(e) => {
          const n = Number(e.target.value);
          if (!Number.isFinite(n)) return;
          onChange(Math.max(min, Math.min(max, Math.round(n))));
        }}
        className="w-full px-2.5 py-1.5 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 bg-white pr-8"
      />
      {suffix && (
        <span className="absolute right-2.5 top-1/2 -translate-y-1/2 text-xs text-ink-mute pointer-events-none">
          {suffix}
        </span>
      )}
    </div>
  );
}
