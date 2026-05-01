// AgentChip — library agent / catalog entry. Distinct from
// DaimonChip (which represents one *running* daimon = name@host).
// AgentChip is the *spec* / definition.

import { FileText } from 'lucide-react';
import { ChipFrame } from './common';

interface AgentSnapshot {
  name?: string;
  version?: string | number;
  description?: string;
  definition_hash?: string;
}

export function AgentChip({ snapshot, cpInstanceID }: { snapshot: AgentSnapshot; cpInstanceID?: string }) {
  const name = snapshot.name ?? 'agent';
  return (
    <ChipFrame
      kind="agent"
      cpInstanceID={cpInstanceID}
      identityKey={`agent:${name}`}
      href={`/agents?name=${encodeURIComponent(name)}`}
      icon={<FileText size={11} className="text-indigo-600" />}
      title={<span className="font-mono">{name}</span>}
      meta={[
        snapshot.version != null ? `v${snapshot.version}` : null,
        snapshot.definition_hash ? snapshot.definition_hash.slice(0, 8) : null,
      ].filter(Boolean).join(' · ') || undefined}
    />
  );
}
