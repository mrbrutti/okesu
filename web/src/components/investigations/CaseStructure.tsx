// CaseStructure: four-card grid summarising case shape across hosts /
// IOCs / daimons / runs+orchestrations. Pure presentation over the
// existing InvestigationDetail bundle. Click-throughs use react-router
// for tab navigation; row clicks fire entity:open for drawer-bearing
// kinds (IOC) — same bus the SmartPayload chips use.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Bot, Hash, Server, Workflow } from 'lucide-react';
import type { InvestigationDetail } from '../../api';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
}

export function CaseStructure({ bundle, cpInstanceID }: Props) {
  const hosts = useMemo(() => aggregateHosts(bundle), [bundle]);
  const iocs = useMemo(() => topIOCs(bundle), [bundle]);
  const daimons = useMemo(() => topDaimons(bundle), [bundle]);
  const orchs = useMemo(() => topOrchestrations(bundle), [bundle]);

  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';
  const invID = bundle.investigation.ID;

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
      <Card
        icon={<Server size={14} className="text-emerald-600" />}
        title={`Hosts (${hosts.distinct})`}
        empty={hosts.distinct === 0 ? 'no items linked yet' : null}
        topLine={hosts.top ? `Most-hit: ${hosts.top.host} — ${hosts.top.count} finding${hosts.top.count === 1 ? '' : 's'}` : null}
        rows={hosts.list.slice(1, 4).map((h) => ({ key: h.host, label: h.host, suffix: `${h.count}` }))}
        tabHref={`/investigations/${invID}?tab=findings${cpQS}`}
      />
      <Card
        icon={<Hash size={14} className="text-purple-600" />}
        title={`IOCs (${iocs.length})`}
        empty={iocs.length === 0 ? 'no items linked yet' : null}
        topLine={iocs[0] ? `Most-observed: ${iocs[0].Kind}:${shortVal(iocs[0].Value)} — ${iocs[0].ObservationCount} obs across ${iocs[0].HostCount} host${iocs[0].HostCount === 1 ? '' : 's'}` : null}
        rows={iocs.slice(1, 4).map((i) => ({
          key: `${i.Kind}:${i.Value}`,
          label: `${i.Kind}:${shortVal(i.Value)}`,
          suffix: `${i.ObservationCount} obs`,
          onClick: () => window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'ioc', identityKey: `${i.Kind}:${i.Value}`, cpInstanceID } })),
        }))}
        tabHref={`/investigations/${invID}?tab=iocs${cpQS}`}
      />
      <Card
        icon={<Bot size={14} className="text-indigo-600" />}
        title={`Daimons (${daimons.length})`}
        empty={daimons.length === 0 ? 'no items linked yet' : null}
        topLine={daimons[0] ? `Top emitter: ${daimons[0].Agent} — ${daimons[0].FindingCount} finding${daimons[0].FindingCount === 1 ? '' : 's'}, last seen ${relTime(daimons[0].LastSeenTs)}` : null}
        rows={daimons.slice(1, 4).map((d) => ({ key: d.Agent, label: d.Agent, suffix: `${d.FindingCount}` }))}
        tabHref={`/investigations/${invID}?tab=daimons${cpQS}`}
      />
      <Card
        icon={<Workflow size={14} className="text-cyan-600" />}
        title={`Runs (${orchs.runCount} / ${orchs.orchCount} orch${orchs.orchCount === 1 ? '' : 's'})`}
        empty={orchs.runCount === 0 ? 'no items linked yet' : null}
        topLine={orchs.top ? `Most-run: ${orchs.top.name} — ${orchs.top.runs} run${orchs.top.runs === 1 ? '' : 's'} (${orchs.top.completed} ✓ ${orchs.top.failed} ✗)` : null}
        rows={orchs.list.slice(1, 4).map((o) => ({
          key: o.name,
          label: o.name,
          suffix: `${o.runs} run${o.runs === 1 ? '' : 's'}`,
        }))}
        tabHref={`/investigations/${invID}?tab=runs${cpQS}`}
      />
    </div>
  );
}

interface CardRow {
  key: string;
  label: string;
  suffix: string;
  onClick?: () => void;
}

function Card({
  icon, title, empty, topLine, rows, tabHref,
}: {
  icon: React.ReactNode;
  title: string;
  empty: string | null;
  topLine: string | null;
  rows: CardRow[];
  tabHref: string;
}) {
  return (
    <div className={`border border-border rounded-md bg-white p-3 text-sm ${empty ? 'opacity-60' : ''}`}>
      <div className="flex items-center gap-1.5 mb-1.5">
        {icon}
        <Link to={tabHref} className="font-medium text-ink hover:text-brand-700 truncate">{title}</Link>
      </div>
      {empty ? (
        <div className="text-ink-mute text-xs italic">{empty}</div>
      ) : (
        <>
          {topLine && <div className="text-xs text-ink mb-1.5 truncate">{topLine}</div>}
          {rows.length > 0 && (
            <ul className="text-[11px] text-ink-dim space-y-0.5">
              {rows.map((r) => (
                <li key={r.key} className="flex items-center justify-between gap-2">
                  <button
                    type="button"
                    onClick={r.onClick}
                    className={`truncate text-left ${r.onClick ? 'hover:text-brand-700 cursor-pointer' : 'cursor-default'}`}
                    disabled={!r.onClick}
                  >
                    {r.label}
                  </button>
                  <span className="font-mono text-ink-mute shrink-0">{r.suffix}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}

// ─── aggregators (pure, exported for testability if needed) ─────────────

function aggregateHosts(b: InvestigationDetail) {
  const counts = new Map<string, number>();
  for (const f of b.findings) {
    if (!f.Host.Valid || !f.Host.String) continue;
    counts.set(f.Host.String, (counts.get(f.Host.String) ?? 0) + 1);
  }
  const list = Array.from(counts, ([host, count]) => ({ host, count }))
    .sort((a, b) => b.count - a.count);
  return { distinct: list.length, top: list[0] ?? null, list };
}

function topIOCs(b: InvestigationDetail) {
  return [...b.iocs].sort((a, b) => b.ObservationCount - a.ObservationCount);
}

function topDaimons(b: InvestigationDetail) {
  return [...b.daimons].sort((a, b) => {
    if (a.FindingCount !== b.FindingCount) return b.FindingCount - a.FindingCount;
    return b.LastSeenTs - a.LastSeenTs;
  });
}

function topOrchestrations(b: InvestigationDetail) {
  const byID = new Map<number, { name: string; runs: number; completed: number; failed: number; cancelled: number; running: number }>();
  for (const r of b.runs) {
    const id = r.OrchestrationID;
    const name = r.OrchestrationName.Valid ? r.OrchestrationName.String : `orchestration #${id}`;
    let row = byID.get(id);
    if (!row) {
      row = { name, runs: 0, completed: 0, failed: 0, cancelled: 0, running: 0 };
      byID.set(id, row);
    }
    row.runs += 1;
    if (r.Status === 'completed') row.completed += 1;
    else if (r.Status === 'failed') row.failed += 1;
    else if (r.Status === 'cancelled') row.cancelled += 1;
    else row.running += 1;
  }
  const list = Array.from(byID.values()).sort((a, b) => b.runs - a.runs);
  return {
    runCount: b.runs.length,
    orchCount: byID.size,
    top: list[0] ?? null,
    list,
  };
}

// ─── tiny helpers ───────────────────────────────────────────

// shortVal renders IOC values compactly. Long opaque values (hashes,
// keys) get tail-truncated so the card stays readable; short, human-
// meaningful values (IPs, domains, filenames) render in full.
function shortVal(s: string): string {
  return s.length > 16 ? `…${s.slice(-8)}` : s;
}

function relTime(unixSec: number): string {
  if (!unixSec) return 'never';
  const ageMs = Date.now() - unixSec * 1000;
  const m = Math.floor(ageMs / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
