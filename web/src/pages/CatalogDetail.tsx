import { useEffect, useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import { ArrowLeft, FileText, Eye, Network, Library, Loader2 } from 'lucide-react';

import { api, type FederatedIOCRecord, type FederatedIOCObservation, type FederatedIOCRelationship } from '../api';
import { cn } from '../lib/cn';

type Tab = 'overview' | 'observations' | 'relationships';

export default function CatalogDetailPage() {
  const { kind, value } = useParams();
  const navigate = useNavigate();
  const decodedValue = value ? decodeURIComponent(value) : '';
  const [ioc, setIOC] = useState<FederatedIOCRecord | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [observations, setObservations] = useState<FederatedIOCObservation[] | null>(null);
  const [relationships, setRelationships] = useState<FederatedIOCRelationship[] | null>(null);

  useEffect(() => {
    if (!kind || !decodedValue) { setError('bad kv'); return; }
    api.iocByKV(kind, decodedValue).then(setIOC).catch(e => setError(String(e)));
  }, [kind, decodedValue]);

  useEffect(() => {
    if (!kind || !decodedValue || tab !== 'observations' || observations !== null) return;
    api.iocObservationsByKV(kind, decodedValue).then(r => setObservations(r ?? [])).catch(() => setObservations([]));
  }, [kind, decodedValue, tab, observations]);

  useEffect(() => {
    if (!kind || !decodedValue || tab !== 'relationships' || relationships !== null) return;
    api.iocRelationshipsByKV(kind, decodedValue).then(r => setRelationships(r ?? [])).catch(() => setRelationships([]));
  }, [kind, decodedValue, tab, relationships]);

  if (error) {
    return (
      <div className="h-full flex flex-col">
        <div className="p-6">
          <Link to="/catalog" className="text-xs text-ink-dim hover:text-ink inline-flex items-center gap-1">
            <ArrowLeft size={12} /> Back to Catalog
          </Link>
          <div className="mt-3 text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
        </div>
      </div>
    );
  }
  if (!ioc) {
    return (
      <div className="h-full flex flex-col p-6">
        <div className="flex items-center gap-2 text-ink-dim text-xs">
          <Loader2 size={14} className="animate-spin" /> Loading IOC…
        </div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <Link to="/catalog" className="text-xs text-ink-dim hover:text-ink inline-flex items-center gap-1 mb-2">
          <ArrowLeft size={12} /> Back to Catalog
        </Link>
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
            <Library size={14} />
          </span>
          <span className="font-mono text-[10px] uppercase tracking-wider text-ink-mute">{ioc.Kind}</span>
          <span className="break-all">{ioc.Name || ioc.Value}</span>
        </h1>
        {ioc.Name && <div className="text-ink-mute font-mono text-[11px] break-all mt-1 ml-9">{ioc.Value}</div>}
        {ioc.cp_sources && ioc.cp_sources.length > 0 && (
          <div className="flex flex-wrap gap-1 mt-2 ml-9">
            <span className="text-[10px] uppercase tracking-wider text-ink-mute mr-1">Seen on</span>
            {ioc.cp_sources.map(s => (
              <span
                key={s.instance_id}
                className="px-1.5 py-0.5 text-[10px] rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100"
                title={s.region || s.display_name || s.instance_id}
              >
                {s.display_name || s.instance_id}
              </span>
            ))}
          </div>
        )}
      </header>

      <div className="px-6 border-b border-border bg-panel flex items-center gap-1">
        <TabButton current={tab} value="overview"      onClick={setTab} icon={FileText} label="Overview" />
        <TabButton current={tab} value="observations"  onClick={setTab} icon={Eye}      label="Observations" count={observations?.length} />
        <TabButton current={tab} value="relationships" onClick={setTab} icon={Network}  label="Relationships" count={relationships?.length} />
      </div>

      <div className="flex-1 overflow-auto p-6 space-y-4">
        {tab === 'overview' && <OverviewTab ioc={ioc} />}
        {tab === 'observations' && <ObservationsTab rows={observations} />}
        {tab === 'relationships' && <RelationshipsTab rows={relationships} kind={kind} decodedValue={decodedValue} navigate={navigate} />}
      </div>
    </div>
  );
}

function TabButton({ current, value, onClick, icon: Icon, label, count }: {
  current: Tab; value: Tab; onClick: (t: Tab) => void; icon: typeof Library; label: string; count?: number;
}) {
  const active = current === value;
  return (
    <button
      onClick={() => onClick(value)}
      className={cn(
        'inline-flex items-center gap-1.5 px-3 py-2 text-xs border-b-2 -mb-px transition-colors',
        active
          ? 'border-brand-600 text-brand-700 font-medium'
          : 'border-transparent text-ink-dim hover:text-ink',
      )}
    >
      <Icon size={12} />
      {label}
      {typeof count === 'number' && (
        <span className={cn('px-1.5 py-0.5 rounded text-[10px]', active ? 'bg-brand-50 text-brand-700' : 'bg-slate-100 text-ink-dim')}>{count}</span>
      )}
    </button>
  );
}

function OverviewTab({ ioc }: { ioc: FederatedIOCRecord }) {
  const isRule = ioc.Kind === 'yara_rule' || ioc.Kind === 'sigma_rule';
  return (
    <div className="space-y-4 max-w-4xl">
      <Section label="Identity">
        <KV k="Source" v={ioc.Source} />
        <KV k="Normalized" v={ioc.NormalizedValue} mono />
        {ioc.DefinitionPath && <KV k="Definition" v={ioc.DefinitionPath} mono />}
      </Section>

      <Section label="Curated metadata">
        {ioc.Tags && (
          <div className="flex flex-wrap gap-1 mb-2">
            {ioc.Tags.split(',').map(t => t.trim()).filter(Boolean).map(t => (
              <span key={t} className="px-1.5 py-0.5 text-[10px] rounded bg-brand-50 text-brand-700 ring-1 ring-brand-100">{t}</span>
            ))}
          </div>
        )}
        {ioc.SeverityFloor && <KV k="Severity" v={ioc.SeverityFloor} />}
        {ioc.Classification && <KV k="Classification" v={ioc.Classification} />}
        {ioc.Attribution && <KV k="Attribution" v={ioc.Attribution} />}
        {ioc.Confidence && <KV k="Confidence" v={ioc.Confidence} />}
        {ioc.Notes && <div className="mt-2 text-xs text-ink-dim whitespace-pre-wrap">{ioc.Notes}</div>}
      </Section>

      <Section label="Observability">
        <KV k="Count" v={String(ioc.ObservationCount)} />
        <KV k="First seen" v={fmtDate(ioc.FirstSeen)} />
        <KV k="Last seen" v={fmtDate(ioc.LastSeen)} />
      </Section>

      {isRule && (
        <Section label="Rule body">
          <div className="max-h-[60vh] overflow-y-auto rounded border border-border bg-slate-50">
            <pre className="text-xs text-ink p-3 whitespace-pre-wrap break-all"><code>{ioc.Value}</code></pre>
          </div>
        </Section>
      )}
    </div>
  );
}

function ObservationsTab({ rows }: { rows: FederatedIOCObservation[] | null }) {
  if (rows === null) return <div className="flex items-center gap-2 text-ink-dim text-xs"><Loader2 size={14} className="animate-spin" /> Loading observations…</div>;
  if (rows.length === 0) return <div className="text-sm text-ink-mute border border-border rounded-md p-6 text-center bg-slate-50/50">No observations recorded.</div>;
  return (
    <div className="border border-border rounded-md bg-white overflow-hidden">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute bg-slate-50 border-b border-border">
            <th className="px-3 py-2 font-medium w-24">Finding</th>
            <th className="px-3 py-2 font-medium w-24">Run</th>
            <th className="px-3 py-2 font-medium">Host</th>
            <th className="px-3 py-2 font-medium w-32">CP</th>
            <th className="px-3 py-2 font-medium w-44">Observed at</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((o, i) => (
            <tr key={i} className="border-b border-border last:border-b-0 hover:bg-slate-50/60">
              <td className="px-3 py-2">
                {o.FindingID > 0
                  ? <Link to={`/findings?id=${o.FindingID}`} className="text-brand-700 hover:underline">#{o.FindingID}</Link>
                  : <span className="text-ink-mute">—</span>}
              </td>
              <td className="px-3 py-2">
                {o.OrchestrationRunID > 0
                  ? <Link to={`/agents?tab=runs&id=${o.OrchestrationRunID}`} className="text-brand-700 hover:underline">#{o.OrchestrationRunID}</Link>
                  : <span className="text-ink-mute">—</span>}
              </td>
              <td className="px-3 py-2 text-ink font-mono text-xs">{o.Host || '—'}</td>
              <td className="px-3 py-2 text-ink-dim text-xs">
                {o.cp_source?.display_name || o.cp_source?.instance_id || '—'}
              </td>
              <td className="px-3 py-2 text-ink-dim text-xs font-mono">{fmtDate(o.ObservedAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RelationshipsTab({ rows, kind, decodedValue, navigate }: { rows: FederatedIOCRelationship[] | null; kind: string | undefined; decodedValue: string; navigate: ReturnType<typeof useNavigate> }) {
  if (rows === null) return <div className="flex items-center gap-2 text-ink-dim text-xs"><Loader2 size={14} className="animate-spin" /> Loading relationships…</div>;
  if (rows.length === 0) return <div className="text-sm text-ink-mute border border-border rounded-md p-6 text-center bg-slate-50/50">No relationships recorded.</div>;
  return (
    <div className="border border-border rounded-md bg-white overflow-hidden">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-[11px] uppercase tracking-wide text-ink-mute bg-slate-50 border-b border-border">
            <th className="px-3 py-2 font-medium w-20">Direction</th>
            <th className="px-3 py-2 font-medium">Subject</th>
            <th className="px-3 py-2 font-medium w-32">Predicate</th>
            <th className="px-3 py-2 font-medium">Object</th>
            <th className="px-3 py-2 font-medium w-32">CPs</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((rel, i) => {
            const subjectIsThis = rel.SubjectKind === kind && rel.SubjectValue === decodedValue;
            const otherKind = subjectIsThis ? rel.ObjectKind : rel.SubjectKind;
            const otherValue = subjectIsThis ? rel.ObjectValue : rel.SubjectValue;
            return (
              <tr
                key={i}
                className="border-b border-border last:border-b-0 hover:bg-slate-50/60 cursor-pointer"
                onClick={() => navigate(`/catalog/${otherKind}/${encodeURIComponent(otherValue)}`)}
              >
                <td className="px-3 py-2 text-ink-mute text-xs">{subjectIsThis ? '→ out' : '← in'}</td>
                <td className="px-3 py-2 text-ink font-mono text-[11px]">
                  <span className="text-ink-dim">{rel.SubjectKind}</span> {rel.SubjectValue}
                  {subjectIsThis && <span className="text-ink-mute"> (this)</span>}
                </td>
                <td className="px-3 py-2 font-mono text-[11px] text-ink">{rel.Predicate}</td>
                <td className="px-3 py-2 text-ink font-mono text-[11px]">
                  <span className="text-ink-dim">{rel.ObjectKind}</span> {rel.ObjectValue}
                  {!subjectIsThis && <span className="text-ink-mute"> (this)</span>}
                </td>
                <td className="px-3 py-2 text-ink-dim text-xs">
                  {rel.cp_sources?.map(s => s.display_name || s.instance_id).join(', ') || '—'}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="border border-border rounded-md bg-white p-4">
      <div className="text-[10px] uppercase tracking-wider text-ink-mute mb-3">{label}</div>
      {children}
    </div>
  );
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex justify-between gap-3 text-[11px] mb-1 last:mb-0">
      <span className="text-ink-mute">{k}</span>
      <span className={cn(mono ? 'font-mono' : '', 'text-ink break-all text-right')}>{v}</span>
    </div>
  );
}

function fmtDate(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString(undefined, {
    month: 'short', day: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}
