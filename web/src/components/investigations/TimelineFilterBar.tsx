// TimelineFilterBar — filter inputs above the case timeline.
// Severity (5 chips, multi-select) + host typeahead + agent typeahead
// + run-status (4 chips, multi-select) + Clear. The component is
// "dumb": state lives in the parent CaseTimeline, which threads
// `filter` in and `onChange` out. Saved-search list / save / default
// pinning is handled by the existing <SavedSearchesBar> mounted as
// a sibling — same pattern as the Findings page.

import { useMemo } from 'react';
import type { InvestigationDetail } from '../../api';
import type { Severity, RunStatus, TimelineFilterConfig } from './timeline/filter';

const SEVERITIES: Severity[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];
const RUN_STATUSES: RunStatus[] = ['completed', 'failed', 'cancelled', 'running'];

interface Props {
  bundle: InvestigationDetail;
  filter: TimelineFilterConfig;
  onChange: (next: TimelineFilterConfig) => void;
}

export function TimelineFilterBar({ bundle, filter, onChange }: Props) {
  const hosts = useMemo(() => distinctHosts(bundle), [bundle]);
  const agents = useMemo(() => distinctAgents(bundle), [bundle]);

  function toggleSev(s: Severity) {
    const cur = filter.severities ?? [];
    const next = cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s];
    onChange({ ...filter, severities: next.length ? next : undefined });
  }

  function toggleRun(r: RunStatus) {
    const cur = filter.run_statuses ?? [];
    const next = cur.includes(r) ? cur.filter((x) => x !== r) : [...cur, r];
    onChange({ ...filter, run_statuses: next.length ? next : undefined });
  }

  function setHost(v: string) {
    onChange({ ...filter, host: v.trim() === '' ? undefined : v });
  }

  function setAgent(v: string) {
    onChange({ ...filter, agent: v.trim() === '' ? undefined : v });
  }

  function clear() {
    onChange({});
  }

  const sevSelected = (s: Severity) => (filter.severities ?? []).includes(s);
  const runSelected = (r: RunStatus) => (filter.run_statuses ?? []).includes(r);

  return (
    <div className="flex items-center flex-wrap gap-3 text-[11px]">
      <div className="flex items-center gap-1">
        <span className="text-ink-mute">Severity:</span>
        {SEVERITIES.map((s) => (
          <button
            key={s}
            type="button"
            onClick={() => toggleSev(s)}
            aria-pressed={sevSelected(s)}
            className={`px-2 py-0.5 rounded-md border ${sevSelected(s) ? sevTone(s) : 'border-border bg-white text-ink-mute'}`}
          >
            {s}
          </button>
        ))}
      </div>
      <label className="flex items-center gap-1">
        <span className="text-ink-mute">Host:</span>
        <input
          list="timeline-host-options"
          value={filter.host ?? ''}
          onChange={(e) => setHost(e.target.value)}
          placeholder="any"
          className="px-2 py-0.5 rounded border border-border outline-none focus:ring-1 focus:ring-brand-300"
          style={{ minWidth: 140 }}
        />
        <datalist id="timeline-host-options">
          {hosts.map((h) => <option key={h} value={h} />)}
        </datalist>
      </label>
      <label className="flex items-center gap-1">
        <span className="text-ink-mute">Agent:</span>
        <input
          list="timeline-agent-options"
          value={filter.agent ?? ''}
          onChange={(e) => setAgent(e.target.value)}
          placeholder="any"
          className="px-2 py-0.5 rounded border border-border outline-none focus:ring-1 focus:ring-brand-300"
          style={{ minWidth: 140 }}
        />
        <datalist id="timeline-agent-options">
          {agents.map((a) => <option key={a} value={a} />)}
        </datalist>
      </label>
      <div className="flex items-center gap-1">
        <span className="text-ink-mute">Runs:</span>
        {RUN_STATUSES.map((r) => (
          <button
            key={r}
            type="button"
            onClick={() => toggleRun(r)}
            aria-pressed={runSelected(r)}
            className={`px-2 py-0.5 rounded-md border ${runSelected(r) ? runTone(r) : 'border-border bg-white text-ink-mute'}`}
          >
            {r}
          </button>
        ))}
      </div>
      <button
        type="button"
        onClick={clear}
        className="px-2 py-0.5 rounded-md border border-border text-ink-mute hover:bg-slate-50"
      >
        Clear
      </button>
    </div>
  );
}

function distinctHosts(bundle: InvestigationDetail): string[] {
  const set = new Set<string>();
  for (const f of bundle.findings) {
    if (f.Host.Valid && f.Host.String) set.add(f.Host.String);
  }
  return [...set].sort();
}

function distinctAgents(bundle: InvestigationDetail): string[] {
  const set = new Set<string>();
  for (const f of bundle.findings) {
    if (f.Agent.Valid && f.Agent.String) set.add(f.Agent.String);
  }
  for (const d of bundle.daimons ?? []) {
    if (d.Agent) set.add(d.Agent);
  }
  return [...set].sort();
}

function sevTone(s: Severity): string {
  if (s === 'CRITICAL') return 'border-red-300 bg-red-50 text-red-700';
  if (s === 'HIGH') return 'border-orange-300 bg-orange-50 text-orange-700';
  if (s === 'MEDIUM') return 'border-amber-300 bg-amber-50 text-amber-700';
  if (s === 'LOW') return 'border-blue-300 bg-blue-50 text-blue-700';
  return 'border-slate-300 bg-slate-50 text-slate-700';
}

function runTone(r: RunStatus): string {
  if (r === 'completed') return 'border-emerald-300 bg-emerald-50 text-emerald-700';
  if (r === 'failed') return 'border-red-300 bg-red-50 text-red-700';
  if (r === 'cancelled') return 'border-slate-300 bg-slate-50 text-slate-700';
  return 'border-blue-300 bg-blue-50 text-blue-700';
}
