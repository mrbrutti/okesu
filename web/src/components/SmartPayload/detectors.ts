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

  return null;
}
