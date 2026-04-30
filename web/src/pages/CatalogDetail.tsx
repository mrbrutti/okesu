import { useEffect, useState } from 'react';
import { Link, useParams, useNavigate } from 'react-router-dom';

import { api, type IOCRecord, type IOCObservation, type IOCRelationship } from '../api';

type Tab = 'overview' | 'observations' | 'relationships';

export default function CatalogDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const iocID = Number(id);
  const [ioc, setIOC] = useState<IOCRecord | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [observations, setObservations] = useState<IOCObservation[] | null>(null);
  const [relationships, setRelationships] = useState<IOCRelationship[] | null>(null);

  useEffect(() => {
    if (!iocID) { setError('bad id'); return; }
    api.ioc(iocID).then(setIOC).catch(e => setError(String(e)));
  }, [iocID]);

  useEffect(() => {
    if (!iocID || tab !== 'observations' || observations !== null) return;
    api.iocObservations(iocID).then(setObservations).catch(() => setObservations([]));
  }, [iocID, tab, observations]);

  useEffect(() => {
    if (!iocID || tab !== 'relationships' || relationships !== null) return;
    api.iocRelationships(iocID).then(setRelationships).catch(() => setRelationships([]));
  }, [iocID, tab, relationships]);

  if (error) return <div className="p-6 text-red-400 text-sm">{error}</div>;
  if (!ioc) return <div className="p-6 text-zinc-500 text-sm">Loading…</div>;

  return (
    <div className="p-6 space-y-4">
      <Link to="/catalog" className="text-sm text-zinc-400 hover:text-zinc-200">← Back to Catalog</Link>

      <div>
        <div className="font-mono text-xs text-zinc-500 uppercase tracking-wider">{ioc.Kind}</div>
        <h1 className="text-xl font-semibold text-zinc-100 break-all">{ioc.Name || ioc.Value}</h1>
        {ioc.Name && <div className="text-zinc-500 font-mono text-sm break-all mt-1">{ioc.Value}</div>}
      </div>

      <div className="flex gap-1 border-b border-zinc-800">
        <TabBtn active={tab === 'overview'} onClick={() => setTab('overview')}>Overview</TabBtn>
        <TabBtn active={tab === 'observations'} onClick={() => setTab('observations')}>Observations</TabBtn>
        <TabBtn active={tab === 'relationships'} onClick={() => setTab('relationships')}>Relationships</TabBtn>
      </div>

      {tab === 'overview' && <OverviewTab ioc={ioc} />}
      {tab === 'observations' && <ObservationsTab rows={observations} />}
      {tab === 'relationships' && <RelationshipsTab rows={relationships} currentID={iocID} navigate={navigate} />}
    </div>
  );
}

function TabBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={
        'px-3 py-2 text-sm border-b-2 -mb-px ' +
        (active ? 'border-zinc-100 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300')
      }
    >
      {children}
    </button>
  );
}

function OverviewTab({ ioc }: { ioc: IOCRecord }) {
  const isRule = ioc.Kind === 'yara_rule' || ioc.Kind === 'sigma_rule';
  return (
    <div className="space-y-4">
      <Section label="Identity">
        <KV k="Source" v={ioc.Source} />
        <KV k="Normalized" v={ioc.NormalizedValue} mono />
        {ioc.DefinitionPath && <KV k="Definition" v={ioc.DefinitionPath} mono />}
      </Section>

      <Section label="Curated metadata">
        {ioc.Tags && (
          <div className="flex flex-wrap gap-1 mb-2">
            {ioc.Tags.split(',').map(t => t.trim()).filter(Boolean).map(t => (
              <span key={t} className="px-1.5 py-0.5 text-xs rounded bg-zinc-800 text-zinc-300">{t}</span>
            ))}
          </div>
        )}
        {ioc.SeverityFloor && <KV k="Severity" v={ioc.SeverityFloor} />}
        {ioc.Classification && <KV k="Classification" v={ioc.Classification} />}
        {ioc.Attribution && <KV k="Attribution" v={ioc.Attribution} />}
        {ioc.Confidence && <KV k="Confidence" v={ioc.Confidence} />}
        {ioc.Notes && <div className="mt-2 text-xs text-zinc-400 whitespace-pre-wrap">{ioc.Notes}</div>}
      </Section>

      <Section label="Observability">
        <KV k="Count" v={String(ioc.ObservationCount)} />
        <KV k="First seen" v={ioc.FirstSeen} />
        <KV k="Last seen" v={ioc.LastSeen} />
      </Section>

      {isRule && (
        <Section label="Rule body">
          <div className="max-h-[60vh] overflow-y-auto rounded bg-zinc-950 border border-zinc-800">
            <pre className="text-xs text-zinc-200 p-3 whitespace-pre-wrap break-all"><code>{ioc.Value}</code></pre>
          </div>
        </Section>
      )}
    </div>
  );
}

function ObservationsTab({ rows }: { rows: IOCObservation[] | null }) {
  if (rows === null) return <div className="text-sm text-zinc-500">Loading…</div>;
  if (rows.length === 0) return <div className="text-sm text-zinc-500">No observations recorded.</div>;
  return (
    <table className="w-full text-sm border-collapse">
      <thead className="text-zinc-400 border-b border-zinc-800">
        <tr>
          <th className="text-left py-2 pr-3">Finding</th>
          <th className="text-left py-2 pr-3">Run</th>
          <th className="text-left py-2 pr-3">Host</th>
          <th className="text-left py-2 pr-3">Observed at</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((o, i) => (
          <tr key={i} className="border-b border-zinc-900">
            <td className="py-2 pr-3">
              {o.FindingID > 0
                ? <Link to={`/findings?id=${o.FindingID}`} className="text-zinc-200 hover:text-zinc-100">#{o.FindingID}</Link>
                : <span className="text-zinc-600">—</span>}
            </td>
            <td className="py-2 pr-3">
              {o.OrchestrationRunID > 0
                ? <Link to={`/agents?tab=runs&id=${o.OrchestrationRunID}`} className="text-zinc-200 hover:text-zinc-100">#{o.OrchestrationRunID}</Link>
                : <span className="text-zinc-600">—</span>}
            </td>
            <td className="py-2 pr-3 text-zinc-300 font-mono text-xs">{o.Host || '—'}</td>
            <td className="py-2 pr-3 text-zinc-500 text-xs">{o.ObservedAt}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function RelationshipsTab({ rows, currentID, navigate }: { rows: IOCRelationship[] | null; currentID: number; navigate: ReturnType<typeof useNavigate> }) {
  if (rows === null) return <div className="text-sm text-zinc-500">Loading…</div>;
  if (rows.length === 0) return <div className="text-sm text-zinc-500">No relationships recorded.</div>;
  return (
    <table className="w-full text-sm border-collapse">
      <thead className="text-zinc-400 border-b border-zinc-800">
        <tr>
          <th className="text-left py-2 pr-3">Direction</th>
          <th className="text-left py-2 pr-3">Subject</th>
          <th className="text-left py-2 pr-3">Predicate</th>
          <th className="text-left py-2 pr-3">Object</th>
          <th className="text-left py-2 pr-3">Source</th>
        </tr>
      </thead>
      <tbody>
        {rows.map(rel => {
          const outgoing = rel.SubjectID === currentID;
          const otherID = outgoing ? rel.ObjectID : rel.SubjectID;
          return (
            <tr key={rel.ID} className="border-b border-zinc-900 hover:bg-zinc-900/40 cursor-pointer" onClick={() => navigate(`/catalog/${otherID}`)}>
              <td className="py-2 pr-3 text-zinc-500 text-xs">{outgoing ? '→ out' : '← in'}</td>
              <td className="py-2 pr-3 text-zinc-300">#{rel.SubjectID}{rel.SubjectID === currentID && ' (this)'}</td>
              <td className="py-2 pr-3 font-mono text-xs text-zinc-200">{rel.Predicate}</td>
              <td className="py-2 pr-3 text-zinc-300">#{rel.ObjectID}{rel.ObjectID === currentID && ' (this)'}</td>
              <td className="py-2 pr-3 text-zinc-500 text-xs">{rel.Source}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded border border-zinc-800 bg-zinc-950 p-4">
      <div className="text-xs uppercase tracking-wider text-zinc-500 mb-3">{label}</div>
      {children}
    </div>
  );
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex justify-between gap-3 text-xs mb-1">
      <span className="text-zinc-500">{k}</span>
      <span className={mono ? 'font-mono text-zinc-300 break-all text-right' : 'text-zinc-300 text-right'}>{v}</span>
    </div>
  );
}
