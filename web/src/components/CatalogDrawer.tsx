import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { api, type IOCRecord } from '../api';
import { cn } from '../lib/cn';

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

  useEffect(() => {
    api.iocRelationships(ioc.ID).then(rels => setRelCount(rels?.length ?? 0)).catch(() => setRelCount(0));
  }, [ioc.ID]);

  return (
    <>
      <div className="fixed inset-0 bg-black/20 z-40" onClick={onClose} />
      <div className="fixed inset-y-0 right-0 w-96 bg-panel border-l border-border z-50 overflow-y-auto shadow-lg">
        <div className="px-5 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel flex items-center justify-between">
          <span className="font-mono text-[10px] uppercase tracking-wider text-ink-mute">{ioc.Kind}</span>
          <button onClick={onClose} className="text-ink-mute hover:text-ink" aria-label="Close drawer">
            <X size={16} />
          </button>
        </div>

        <div className="px-5 py-4 space-y-4">
          <div>
            <div className="text-ink font-mono text-sm break-all">{ioc.Name || ioc.Value}</div>
            {ioc.Name && <div className="text-ink-mute font-mono text-[11px] break-all mt-1">{ioc.Value}</div>}
          </div>

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
          )}

          <Section label="Observability">
            <KV k="Count" v={String(ioc.ObservationCount)} />
            <KV k="First seen" v={fmtDate(ioc.FirstSeen)} />
            <KV k="Last seen" v={fmtDate(ioc.LastSeen)} />
          </Section>

          <div className="pt-2 space-y-2">
            <button
              onClick={onOpenFullPage}
              className="w-full text-xs px-3 py-2 rounded-md bg-brand-600 text-white hover:bg-brand-700 transition-colors"
            >
              Observations · Relationships{relCount !== null ? ` (${relCount})` : ''} →
            </button>
          </div>
        </div>
      </div>
    </>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="border border-border rounded-md bg-white p-3">
      <div className="text-[10px] uppercase tracking-wider text-ink-mute mb-2">{label}</div>
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
