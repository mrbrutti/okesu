// Lightweight YAML <-> spec helpers used by both the lazy editor
// canvas AND the eager run-detail page (to map agents/nodes onto the
// run canvas before the editor chunk has loaded).
//
// Pulling these out of OrchestrationEditorCanvas.tsx keeps the
// react-flow + js-yaml chunks lazy-loaded — the main bundle only
// imports js-yaml here, which is small (~17 KB gzipped).

import yaml from 'js-yaml';

export interface ParsedStep {
  id: string;
  agent: string;
  /** Single-host target (mutex with `nodes`). Templates allowed. */
  node?: string;
  /** Multi-host fan-out target. Engine runs the step on every host
   *  in parallel and aggregates findings + outputs. */
  nodes?: string[];
  prompt: string;
  timeout?: string;
  approval?: 'required' | '';
}

export interface ParsedSpecLite {
  name: string;
  description: string;
  steps: ParsedStep[];
  rawHeader?: string;
  passthrough?: Record<string, unknown>;
}

export function parseSpecYAMLLite(content: string): ParsedSpecLite | null {
  const match = content.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
  if (!match) return null;
  let parsed: Record<string, unknown> = {};
  try {
    parsed = yaml.load(match[1]) as Record<string, unknown>;
    if (parsed === null || typeof parsed !== 'object') return null;
  } catch {
    return null;
  }
  const stepsArr = (parsed['steps'] as Array<Record<string, unknown>>) ?? [];
  const steps: ParsedStep[] = stepsArr.map((s, i) => ({
    id: String(s.id ?? `step-${i + 1}`),
    agent: String(s.agent ?? ''),
    node: typeof s.node === 'string' ? s.node : undefined,
    nodes: Array.isArray(s.nodes)
      ? (s.nodes as unknown[]).filter((x) => typeof x === 'string') as string[]
      : undefined,
    prompt: typeof s.prompt === 'string' ? s.prompt : '',
    timeout: typeof s.timeout === 'string' ? s.timeout : undefined,
    approval: s.approval === 'required' ? 'required' : '',
  }));

  const passthrough: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(parsed)) {
    if (k !== 'name' && k !== 'description' && k !== 'steps') {
      passthrough[k] = v;
    }
  }

  return {
    name: String(parsed['name'] ?? ''),
    description: String(parsed['description'] ?? ''),
    steps,
    rawHeader: match[2].trim(),
    passthrough,
  };
}
