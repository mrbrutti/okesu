import { Boxes } from 'lucide-react';
import { ChipFrame } from './common';

interface ClusterSnapshot {
  // IOC cluster shape
  cluster_id?: string | number;
  findings_count?: number;
  severity_max?: string;
  attribution?: string;
  // Finding cluster (dedup) shape
  dedup_key?: string;
  count?: number;
  first_seen?: string;
  last_seen?: string;
  severity?: string;
  title?: string;
  // Disambiguator emitted by the detector
  kind_label?: 'ioc' | 'finding';
}

export function ClusterChip({ snapshot, cpInstanceID }: { snapshot: ClusterSnapshot; cpInstanceID?: string }) {
  const isIOC = snapshot.kind_label === 'ioc' || snapshot.cluster_id !== undefined;
  const cpQS = cpInstanceID ? `&cp=${encodeURIComponent(cpInstanceID)}` : '';

  if (isIOC) {
    const id = snapshot.cluster_id ?? '';
    return (
      <ChipFrame
        kind="cluster"
        cpInstanceID={cpInstanceID}
        identityKey={`cluster:${id}`}
        href={`/findings?cluster_id=${encodeURIComponent(String(id))}${cpQS}`}
        icon={<Boxes size={11} className="text-amber-600" />}
        title={
          <>
            cluster <span className="font-mono">{String(id).slice(0, 12)}</span>
          </>
        }
        meta={[
          snapshot.findings_count != null ? `${snapshot.findings_count} findings` : null,
          snapshot.severity_max ? `max ${snapshot.severity_max}` : null,
          snapshot.attribution || null,
        ].filter(Boolean).join(' · ') || undefined}
      />
    );
  }

  // Finding-cluster (dedup_key rollup)
  const dk = snapshot.dedup_key ?? '';
  return (
    <ChipFrame
      kind="cluster"
      cpInstanceID={cpInstanceID}
      identityKey={`dedup:${dk}`}
      href={`/findings?dedup_key=${encodeURIComponent(dk)}${cpQS}`}
      icon={<Boxes size={11} className="text-amber-600" />}
      title={
        <>
          {snapshot.severity && (
            <span className={`severity-badge mr-1 align-middle text-[9px] severity-${snapshot.severity.toLowerCase()}`}>
              {snapshot.severity}
            </span>
          )}
          {snapshot.title || <span className="font-mono">{dk.slice(0, 32)}</span>}
        </>
      }
      meta={snapshot.count != null ? `${snapshot.count}× recent` : undefined}
    />
  );
}
