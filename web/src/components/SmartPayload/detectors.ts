// Conservative duck-type matchers per entity kind. Order matters:
// finding wins ties over investigation (because operators see findings
// far more often, and investigation has the explicit external_key
// disambiguator). Returns the first match or null.
//
// All snapshots are returned as plain Record<string, unknown> so the
// chip components can pick the fields they need; no transformation
// here beyond null-safe field access.

import type { PromptEntityKind } from '../../api';

export interface DetectedEntity {
  kind: PromptEntityKind;
  snapshot: Record<string, unknown>;
}

type Obj = Record<string, unknown>;

function isObj(v: unknown): v is Obj {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function has(o: Obj, ...keys: string[]): boolean {
  for (const k of keys) if (!(k in o)) return false;
  return true;
}

export function detectEntity(value: unknown): DetectedEntity | null {
  if (!isObj(value)) return null;

  // Finding has both severity AND category — strongest signal, check first.
  if (has(value, 'id', 'severity', 'title', 'category')) {
    return {
      kind: 'finding',
      snapshot: {
        id: value.id,
        severity: value.severity,
        title: value.title,
        category: value.category,
        status: value.status,
        host: value.host,
      },
    };
  }

  // Investigation: severity+status+title without category, plus an
  // investigation-specific marker.
  if (
    has(value, 'id', 'title') &&
    !has(value, 'category') &&
    (has(value, 'external_key') || (has(value, 'severity') && has(value, 'status')))
  ) {
    return {
      kind: 'investigation',
      snapshot: {
        id: value.id,
        title: value.title,
        status: value.status,
        severity: value.severity,
      },
    };
  }

  // IOC: kind + value + at least one IOC-specific marker.
  if (
    has(value, 'kind', 'value') &&
    (has(value, 'observation_count') || has(value, 'severity_max') || value.type === 'ioc')
  ) {
    const v = String(value.value ?? '');
    return {
      kind: 'ioc',
      snapshot: {
        kind: value.kind,
        value: v,
        last4: v.length > 4 ? v.slice(-4) : v,
        observation_count: value.observation_count,
        severity_max: value.severity_max,
      },
    };
  }

  // Node: id + hostname, no severity.
  if (has(value, 'id', 'hostname') && !has(value, 'severity')) {
    return {
      kind: 'node',
      snapshot: {
        id: value.id,
        name: value.name,
        hostname: value.hostname,
        status: value.status,
      },
    };
  }

  // Daimon: name + host + agent_id-or-suspended.
  if (has(value, 'name', 'host') && (has(value, 'agent_id') || has(value, 'suspended'))) {
    return {
      kind: 'daimon',
      snapshot: {
        id: value.agent_id,
        name: value.name,
        host: value.host,
        suspended: value.suspended,
      },
    };
  }

  // Run: id + status + started_at-or-agent_name.
  if (has(value, 'id', 'status') && (has(value, 'started_at') || has(value, 'agent_name'))) {
    return {
      kind: 'run',
      snapshot: {
        id: value.id,
        status: value.status,
        started_at: value.started_at,
        ended_at: value.ended_at,
      },
    };
  }

  // Orchestration: id + name + version.
  if (has(value, 'id', 'name', 'version')) {
    return {
      kind: 'orchestration',
      snapshot: {
        id: value.id,
        name: value.name,
        version: value.version,
      },
    };
  }

  // Cluster (IOC-cluster or finding-cluster rollup). Two shapes:
  //   - IOC cluster:    cluster_id + (findings_count | severity_max | attribution)
  //   - Finding cluster: dedup_key + (count | first_seen)
  // We surface both as kind='cluster' so a single ClusterChip renders them
  // with the cluster's headline counts.
  if (has(value, 'cluster_id') && (has(value, 'findings_count') || has(value, 'severity_max') || has(value, 'attribution'))) {
    return {
      kind: 'cluster',
      snapshot: {
        cluster_id: value.cluster_id,
        findings_count: value.findings_count,
        severity_max: value.severity_max,
        attribution: value.attribution,
        kind_label: 'ioc',
      },
    };
  }
  if (has(value, 'dedup_key') && (has(value, 'count') || has(value, 'first_seen'))) {
    return {
      kind: 'cluster',
      snapshot: {
        dedup_key: value.dedup_key,
        count: value.count,
        first_seen: value.first_seen,
        last_seen: value.last_seen,
        severity: value.severity,
        title: value.title,
        kind_label: 'finding',
      },
    };
  }

  // Agent (library / catalog) — distinct from daimon (a runner).
  // Library agents are spec definitions. Shape: `name` + (`version` |
  // `description`) and crucially NOT a host (which is the daimon
  // disambiguator). Skip when severity/category/title are set so a
  // finding row never matches.
  if (
    has(value, 'name') &&
    !has(value, 'host') &&
    !has(value, 'severity') &&
    !has(value, 'category') &&
    (has(value, 'version') || has(value, 'description') || has(value, 'definition_hash'))
  ) {
    // Disambiguate from orchestration which also has name+version: an
    // orchestration carries `id` + `trigger_kind` or `steps`. If we
    // already saw those above we wouldn't be here.
    return {
      kind: 'agent',
      snapshot: {
        name: value.name,
        version: value.version,
        description: value.description,
        definition_hash: value.definition_hash,
      },
    };
  }

  return null;
}
