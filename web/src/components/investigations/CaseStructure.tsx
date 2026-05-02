// CaseStructure: four-card grid summarising case shape across hosts /
// IOCs / daimons / runs+orchestrations. Fetches the pre-aggregated
// /structure endpoint on mount; falls back to client-side derivation
// over the bundle if the fetch fails (federated child running an
// older binary, transient network error). Click-throughs use
// react-router for tab navigation; row clicks fire entity:open.
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Bot, Hash, Server, Workflow } from 'lucide-react';
import type { InvestigationDetail, InvestigationStructure } from '../../api';
import { api } from '../../api';
import { deriveFromBundle } from './caseStructure/derive';

interface Props {
  bundle: InvestigationDetail;
  cpInstanceID?: string;
}

export function CaseStructure({ bundle, cpInstanceID }: Props) {
  const [data, setData] = useState<InvestigationStructure | null>(null);
  const invID = bundle.investigation.ID;

  useEffect(() => {
    let cancelled = false;
    api.investigations.structure(invID, cpInstanceID)
      .then((r) => { if (!cancelled) setData(r); })
      .catch(() => {
        if (!cancelled) {
          setData(null);
          console.warn('case-structure fetch failed; using bundle-derived view', invID);
        }
      });
    return () => { cancelled = true; };
  }, [invID, cpInstanceID]);

  // data === null → either still loading OR fetch failed; either
  // way render from the bundle-derived view. Wire shape is identical
  // so the rest of the render is shared.
  const view: InvestigationStructure = useMemo(
    () => data ?? deriveFromBundle(bundle),
    [data, bundle],
  );

  const hostsList = view.hosts;
  const iocsList = view.iocs;
  const daimonsList = view.daimons;
  const orchList = view.orchestrations;
  // totalRuns sums per-orch RunCount instead of bundle.runs.length:
  // the server path (no raw runs array) and bundle/fallback path
  // produce the same value when every run has an orchestration ID.
  const totalRuns = useMemo(() => orchList.reduce((acc, o) => acc + o.RunCount, 0), [orchList]);

  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
      <Card
        icon={<Server size={14} className="text-emerald-600" />}
        title={`Hosts (${hostsList.length})`}
        empty={hostsList.length === 0 ? 'no items linked yet' : null}
        topLine={hostsList[0] ? `Most-hit: ${hostsList[0].Host} — ${hostsList[0].Count} finding${hostsList[0].Count === 1 ? '' : 's'}` : null}
        rows={hostsList.slice(1, 4).map((h) => ({ key: h.Host, label: h.Host, suffix: `${h.Count}` }))}
        tabHref={`/investigations/${invID}?tab=findings${cpQS}`}
      />
      <Card
        icon={<Hash size={14} className="text-purple-600" />}
        title={`IOCs (${iocsList.length})`}
        empty={iocsList.length === 0 ? 'no items linked yet' : null}
        topLine={iocsList[0] ? `Most-observed: ${iocsList[0].Kind}:${shortVal(iocsList[0].Value)} — ${iocsList[0].ObservationCount} obs across ${iocsList[0].HostCount} host${iocsList[0].HostCount === 1 ? '' : 's'}` : null}
        rows={iocsList.slice(1, 4).map((i) => ({
          key: `${i.Kind}:${i.Value}`,
          label: `${i.Kind}:${shortVal(i.Value)}`,
          suffix: `${i.ObservationCount} obs`,
          onClick: () => window.dispatchEvent(new CustomEvent('entity:open', { detail: { kind: 'ioc', identityKey: `${i.Kind}:${i.Value}`, cpInstanceID } })),
        }))}
        tabHref={`/investigations/${invID}?tab=iocs${cpQS}`}
      />
      <Card
        icon={<Bot size={14} className="text-indigo-600" />}
        title={`Daimons (${daimonsList.length})`}
        empty={daimonsList.length === 0 ? 'no items linked yet' : null}
        topLine={daimonsList[0] ? `Top emitter: ${daimonsList[0].Agent} — ${daimonsList[0].FindingCount} finding${daimonsList[0].FindingCount === 1 ? '' : 's'}, last seen ${relTime(daimonsList[0].LastSeenTs)}` : null}
        rows={daimonsList.slice(1, 4).map((d) => ({ key: d.Agent, label: d.Agent, suffix: `${d.FindingCount}` }))}
        tabHref={`/investigations/${invID}?tab=daimons${cpQS}`}
      />
      <Card
        icon={<Workflow size={14} className="text-cyan-600" />}
        title={`Runs (${totalRuns} / ${orchList.length} orch${orchList.length === 1 ? '' : 's'})`}
        empty={totalRuns === 0 ? 'no items linked yet' : null}
        topLine={orchList[0] ? `Most-run: ${orchList[0].OrchestrationName} — ${orchList[0].RunCount} run${orchList[0].RunCount === 1 ? '' : 's'} (${orchList[0].Completed} ✓ ${orchList[0].Failed} ✗)` : null}
        rows={orchList.slice(1, 4).map((o) => ({
          key: o.OrchestrationName,
          label: o.OrchestrationName,
          suffix: `${o.RunCount} run${o.RunCount === 1 ? '' : 's'}`,
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
