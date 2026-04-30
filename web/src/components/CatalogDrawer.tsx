import { useEffect, useState } from 'react';
import { api, type IOCRecord } from '../api';

export default function CatalogDrawer({
  ioc,
  onClose,
  onOpenFullPage,
}: {
  ioc: IOCRecord;
  onClose: () => void;
  onOpenFullPage: () => void;
}) {
  const [relCount, setRelCount] = useState<number | null>(null);

  // Fetch relationship count once. We don't have a HEAD endpoint; the
  // array length from the existing /relationships handler is fine for
  // v1 since edges per IOC tend to be small (<20 in practice).
  useEffect(() => {
    api.iocRelationships(ioc.ID).then(rels => setRelCount(rels.length)).catch(() => setRelCount(0));
  }, [ioc.ID]);

  return (
    <div className="fixed inset-y-0 right-0 w-96 bg-zinc-950 border-l border-zinc-800 p-4 overflow-y-auto">
      <div className="flex items-center justify-between mb-4">
        <span className="font-mono text-xs text-zinc-500 uppercase tracking-wider">{ioc.Kind}</span>
        <button onClick={onClose} className="text-zinc-500 hover:text-zinc-200" aria-label="Close drawer">✕</button>
      </div>

      <div className="text-zinc-100 font-mono text-sm break-all mb-1">{ioc.Name || ioc.Value}</div>
      {ioc.Name && <div className="text-zinc-500 font-mono text-xs break-all mb-3">{ioc.Value}</div>}

      <Section label="Identity">
        <KV k="Source" v={ioc.Source} />
        <KV k="Normalized" v={ioc.NormalizedValue} mono />
        {ioc.DefinitionPath && <KV k="Definition" v={ioc.DefinitionPath} mono />}
      </Section>

      {(ioc.Tags || ioc.SeverityFloor || ioc.Classification || ioc.Attribution || ioc.Confidence || ioc.Notes) && (
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
      )}

      <Section label="Observability">
        <KV k="Count" v={String(ioc.ObservationCount)} />
        <KV k="First seen" v={ioc.FirstSeen} />
        <KV k="Last seen" v={ioc.LastSeen} />
      </Section>

      <div className="mt-6 space-y-2">
        <button
          onClick={onOpenFullPage}
          className="w-full text-sm bg-zinc-800 hover:bg-zinc-700 text-zinc-100 py-2 rounded"
        >
          Observations · Relationships{relCount !== null ? ` (${relCount})` : ''} →
        </button>
        <button
          onClick={onOpenFullPage}
          className="w-full text-xs text-zinc-500 hover:text-zinc-300"
        >
          Open full page
        </button>
      </div>
    </div>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-4 pb-4 border-b border-zinc-900 last:border-b-0">
      <div className="text-xs uppercase tracking-wider text-zinc-500 mb-2">{label}</div>
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
