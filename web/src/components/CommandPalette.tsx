// Cmd-K / Ctrl-K command palette. Mounted once at the Layout level so
// the hotkey is global across every authenticated route.
//
// On open we fetch findings/daimons/agents/nodes in parallel and cache
// them in component state for ~30s. Filtering is client-side using a
// tiny fuzzy matcher (substring + word-boundary bonus).
//
// Design notes:
// - Modal styling mirrors BinaryUpdateDialog (fixed inset-0 bg-black/30
//   centered panel) so the palette feels at home.
// - We only render ~5 results per group with a "+ N more" pill that
//   navigates to the section's full page when clicked.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  AlertTriangle,
  Layers,
  Loader2,
  Pause,
  Search,
  Server,
  Sparkles,
  Workflow,
  X,
} from 'lucide-react';
import {
  api,
  type AgentLibraryItem,
  type CPSourceRef,
  type DaimonItem,
  type Finding,
  type NodeItem,
  type Orchestration,
  type OrchestrationRunView,
} from '../api';
import { cn } from '../lib/cn';
import { useHotkey } from '../lib/useHotkey';
import { CPSourceChip } from './CPSourceChip';

// Per-group cap — beyond this we render a "+ N more" footer row that
// navigates to the section page so the operator can keep digging.
const PER_GROUP = 5;
// Stale-after window. Re-opening the palette later than this triggers a
// background refetch — earlier reopens reuse the cache.
const CACHE_TTL_MS = 30_000;

// `pending_gate` is special: it surfaces orchestration runs currently
// blocked on operator approval. Always renders first when populated
// so the operator sees their action items the instant they Cmd-K.
type Kind = 'pending_gate' | 'finding' | 'daimon' | 'agent' | 'node' | 'orchestration';

interface BaseRow {
  kind: Kind;
  /** Stable key for React lists. */
  key: string;
  /** Primary text shown bold. */
  title: string;
  /** Faint subtitle (severity / host / hostname). */
  subtitle?: string;
  /** Optional severity used for the badge color (findings only). */
  severity?: string;
  /** Where Enter / click navigates. */
  to: string;
  /** Phase 9.6: federated source CP, when this row came from a child. */
  cpSource?: CPSourceRef;
  /** Numeric id when the row's underlying object has one (findings,
   *  nodes, orchestrations, runs). Powers the `#<num>` lookup mode. */
  idNumber?: number;
}

interface CacheBundle {
  fetchedAt: number;
  findings: Finding[];
  daimons: DaimonItem[];
  agents: AgentLibraryItem[];
  nodes: NodeItem[];
  orchestrations: Orchestration[];
  // Only the runs that are blocking on operator approval — we keep
  // these distinct from `orchestrations` because their TTL is much
  // shorter (the gate clears the moment someone approves).
  pendingGates: OrchestrationRunView[];
}

interface ScoredRow {
  row: BaseRow;
  score: number;
  /** Indices of the matched chars in row.title (for highlight). */
  matched: number[];
}

export default function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [cache, setCache] = useState<CacheBundle | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  // Cmd-K / Ctrl-K toggle. We swallow the event so the browser's
  // default (focus URL bar on Firefox, etc.) doesn't fire.
  useHotkey({ key: 'k', metaOrCtrl: true }, useCallback((e) => {
    e.preventDefault();
    setOpen((prev) => !prev);
  }, []));

  // Esc closes — only bind while open so we don't fight other Esc users.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault();
        setOpen(false);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);

  // On open, ensure we have fresh-ish data; reset the input.
  useEffect(() => {
    if (!open) return;
    setQuery('');
    setActive(0);
    // Focus next tick so the input is mounted.
    setTimeout(() => inputRef.current?.focus(), 0);

    const stale = !cache || Date.now() - cache.fetchedAt > CACHE_TTL_MS;
    if (!stale) return;
    setLoading(true);
    setError(null);
    Promise.all([
      api.findings({ limit: 200 }).catch(() => [] as Finding[]),
      api.daimons(200).catch(() => [] as DaimonItem[]),
      api.agentLibrary().catch(() => [] as AgentLibraryItem[]),
      api.nodes().catch(() => [] as NodeItem[]),
      api.orchestrations().catch(() => [] as Orchestration[]),
      api.orchestrationRuns().catch(() => [] as OrchestrationRunView[]),
    ])
      .then(([findings, daimons, agents, nodes, orchestrations, runs]) => {
        const pendingGates = runs.filter((r) => r.status === 'approval_required');
        setCache({ fetchedAt: Date.now(), findings, daimons, agents, nodes, orchestrations, pendingGates });
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  }, [open, cache]);

  const allRows = useMemo<BaseRow[]>(() => {
    if (!cache) return [];
    const rows: BaseRow[] = [];
    // Pending gates first — they're action items the operator owns
    // *now*. Title prefixes with "approve" so a Cmd-K + typing
    // "approve" still surfaces them quickly.
    for (const r of cache.pendingGates) {
      const ageMin = Math.max(0, Math.round((Date.now() - new Date(r.started_at).getTime()) / 60_000));
      rows.push({
        kind: 'pending_gate',
        key: 'pg-' + r.id,
        title: 'approve run #' + r.id + (r.current_step_id ? ' — ' + r.current_step_id : ''),
        subtitle: ageMin < 1 ? 'just now' : ageMin + 'm waiting',
        to: '/orchestrations?tab=runs&run=' + r.id,
        idNumber: r.id,
      });
    }
    for (const o of cache.orchestrations) {
      rows.push({
        kind: 'orchestration',
        key: 'o-' + o.id + '-' + (o.cp_source?.instance_id ?? ''),
        title: o.name,
        subtitle: o.description || ('trigger: ' + o.trigger_kind),
        to: '/orchestrations?tab=library&id=' + o.id + (o.cp_source ? '&cp=' + encodeURIComponent(o.cp_source.instance_id) : ''),
        cpSource: o.cp_source,
        idNumber: o.id,
      });
    }
    for (const f of cache.findings) {
      rows.push({
        kind: 'finding',
        key: 'f-' + f.id + '-' + (f.cp_source?.instance_id ?? ''),
        title: f.title || ('finding #' + f.id),
        subtitle: [f.severity, f.host].filter(Boolean).join(' · ') || undefined,
        severity: f.severity,
        to: '/findings?id=' + f.id + (f.cp_source ? '&cp=' + encodeURIComponent(f.cp_source.instance_id) : ''),
        cpSource: f.cp_source,
        idNumber: f.id,
      });
    }
    for (const d of cache.daimons) {
      rows.push({
        kind: 'daimon',
        key: 'd-' + d.name + '-' + d.host + '-' + (d.cp_source?.instance_id ?? ''),
        title: d.name,
        subtitle: d.host || undefined,
        to: '/daimons/' + encodeURIComponent(d.name),
        cpSource: d.cp_source,
      });
    }
    // De-dupe agents by name — agentLibrary surfaces the file once per
    // search-path entry, but the Library tab is keyed by name so we
    // collapse here too.
    const seenAgent = new Set<string>();
    for (const a of cache.agents) {
      if (seenAgent.has(a.name)) continue;
      seenAgent.add(a.name);
      rows.push({
        kind: 'agent',
        key: 'a-' + a.name,
        title: a.name,
        subtitle: a.description || a.provider || undefined,
        to: '/agents?tab=library',
      });
    }
    for (const n of cache.nodes) {
      rows.push({
        kind: 'node',
        key: 'n-' + n.id + '-' + (n.cp_source?.instance_id ?? ''),
        title: n.name,
        subtitle: n.hostname || undefined,
        to: '/nodes/' + n.id,
        cpSource: n.cp_source,
        idNumber: n.id,
      });
    }
    return rows;
  }, [cache]);

  const grouped = useMemo(() => {
    const q = query.trim();
    const byKind: Record<Kind, ScoredRow[]> = {
      pending_gate: [],
      orchestration: [],
      finding: [],
      daimon: [],
      agent: [],
      node: [],
    };
    if (!q) {
      // No query ⇒ show top-N of each group by source order. Findings
      // come back newest-first from the API; daimons/nodes/agents are
      // alpha-ish — both are fine for a "type to search" hint state.
      for (const row of allRows) {
        byKind[row.kind].push({ row, score: 0, matched: [] });
      }
      return byKind;
    }
    // ID-lookup mode: `#<digits>` (or `<digits>` alone) returns every
    // row whose underlying object's id matches exactly. Useful when
    // an operator has a finding id from a chat / paged alert and just
    // wants to jump to it without thinking about which surface owns
    // it. Plain numeric queries (no #) also trigger this mode for
    // muscle-memory pasting; non-id rows still show via fuzzy match.
    const idQuery = parseIDQuery(q);
    if (idQuery !== null) {
      // Match every row whose idNumber === idQuery; rank exact-id
      // hits above name/title fuzzy matches that happen to contain
      // the digits.
      for (const row of allRows) {
        if (row.idNumber === idQuery) {
          // Score 10000 keeps id matches above any fuzzy result.
          byKind[row.kind].push({ row, score: 10_000, matched: [] });
          continue;
        }
        // Fall through to a weaker fuzzy pass so daimons/agents
        // (no numeric id) can still match by their name containing
        // the digits.
        const m = fuzzyMatch(q, row.title, row.subtitle);
        if (m) byKind[row.kind].push({ row, score: m.score, matched: m.matched });
      }
      for (const k of Object.keys(byKind) as Kind[]) {
        byKind[k].sort((a, b) => b.score - a.score);
      }
      return byKind;
    }
    for (const row of allRows) {
      const m = fuzzyMatch(q, row.title, row.subtitle);
      if (m) byKind[row.kind].push({ row, score: m.score, matched: m.matched });
    }
    for (const k of Object.keys(byKind) as Kind[]) {
      byKind[k].sort((a, b) => b.score - a.score);
    }
    return byKind;
  }, [query, allRows]);

  // parseIDQuery accepts `#123`, `# 123`, or plain `123`. Returns
  // the parsed integer or null when the query isn't a numeric id
  // lookup. Limited to ≤12 digits so a paste of a long uuid-ish
  // string doesn't accidentally count.

  // Flat ordered list used for keyboard navigation. Order follows the
  // visual order of the groups so ↓ moves to the next visible row.
  const flat = useMemo<ScoredRow[]>(() => {
    // Visual order: action items (gates) first, then catalog
    // (orchestrations), then the existing entity sections.
    const order: Kind[] = ['pending_gate', 'orchestration', 'finding', 'daimon', 'agent', 'node'];
    const out: ScoredRow[] = [];
    for (const k of order) out.push(...grouped[k].slice(0, PER_GROUP));
    return out;
  }, [grouped]);

  // Clamp the active index whenever the result set shrinks under us.
  useEffect(() => {
    if (active >= flat.length) setActive(Math.max(0, flat.length - 1));
  }, [flat.length, active]);

  const navigate = useNavigate();
  function go(to: string) {
    setOpen(false);
    navigate(to);
  }

  function onInputKey(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => Math.min(flat.length - 1, i + 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => Math.max(0, i - 1));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const sel = flat[active];
      if (sel) go(sel.row.to);
    }
  }

  if (!open) return null;

  const hasQuery = query.trim().length > 0;
  const totalShown = flat.length;

  return (
    <div
      className="fixed inset-0 bg-black/30 flex items-start justify-center pt-[12vh] p-4 z-50"
      onMouseDown={(e) => {
        // Click outside the panel closes. Use mousedown so we don't fight
        // a click that started inside and ended on the backdrop.
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div className="bg-panel border border-border rounded-xl shadow-card w-full max-w-2xl flex flex-col overflow-hidden">
        <header className="px-4 py-3 border-b border-border flex items-center gap-2">
          <Search size={16} className="text-ink-mute shrink-0" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setActive(0); }}
            onKeyDown={onInputKey}
            placeholder="Search… (try `#245` to jump to any item by id)"
            className="flex-1 bg-transparent outline-none text-sm placeholder:text-ink-mute"
            spellCheck={false}
            autoComplete="off"
          />
          {loading && <Loader2 size={14} className="animate-spin text-ink-mute" />}
          <button
            onClick={() => setOpen(false)}
            className="p-1 text-ink-dim hover:text-ink rounded-md"
            title="Close (Esc)"
          >
            <X size={14} />
          </button>
        </header>

        <div className="max-h-[60vh] overflow-auto">
          {error && (
            <div className="px-4 py-3 text-xs text-red-700 bg-red-50 border-b border-red-200">
              {error}
            </div>
          )}

          {!cache && loading && (
            <div className="px-4 py-6 text-xs text-ink-mute flex items-center gap-2">
              <Loader2 size={12} className="animate-spin" /> Loading…
            </div>
          )}

          {cache && !hasQuery && totalShown === 0 && (
            <EmptyHint />
          )}

          {cache && hasQuery && totalShown === 0 && (
            <NoMatches />
          )}

          {cache && totalShown > 0 && (
            <Results
              grouped={grouped}
              flat={flat}
              active={active}
              query={query}
              onPick={go}
              onHover={setActive}
            />
          )}
        </div>

        <footer className="px-4 py-2 border-t border-border flex items-center justify-between text-[11px] text-ink-mute">
          <div className="flex items-center gap-3">
            <Hint k="↑↓" label="Navigate" />
            <Hint k="↵" label="Open" />
            <Hint k="Esc" label="Close" />
          </div>
          <div className="flex items-center gap-3">
            {cache && cache.pendingGates.length > 0 && (
              <span className="inline-flex items-center gap-1 text-amber-700">
                <Pause size={10} />
                {cache.pendingGates.length} gate{cache.pendingGates.length === 1 ? '' : 's'}
              </span>
            )}
            <span>
              {cache ? `${cache.findings.length + cache.daimons.length + cache.agents.length + cache.nodes.length + cache.orchestrations.length} items` : ''}
            </span>
          </div>
        </footer>
      </div>
    </div>
  );
}

function Results({
  grouped,
  flat,
  active,
  query,
  onPick,
  onHover,
}: {
  grouped: Record<Kind, ScoredRow[]>;
  flat: ScoredRow[];
  active: number;
  query: string;
  onPick: (to: string) => void;
  onHover: (i: number) => void;
}) {
  const groups: { kind: Kind; label: string; sectionTo: string }[] = [
    { kind: 'pending_gate',  label: 'Pending approvals', sectionTo: '/orchestrations?tab=runs&status=approval_required' },
    { kind: 'orchestration', label: 'Orchestrations',    sectionTo: '/orchestrations?tab=library' },
    { kind: 'finding',       label: 'Findings',          sectionTo: '/findings' },
    { kind: 'daimon',        label: 'Daimons',           sectionTo: '/daimons' },
    { kind: 'agent',         label: 'Agents',            sectionTo: '/agents?tab=library' },
    { kind: 'node',          label: 'Nodes',             sectionTo: '/nodes' },
  ];

  // Map each row in `flat` to its index so the highlight calc stays O(1).
  const indexByKey = new Map<string, number>();
  flat.forEach((r, i) => indexByKey.set(r.row.key, i));

  return (
    <div className="py-1">
      {groups.map(({ kind, label, sectionTo }) => {
        const all = grouped[kind];
        if (!all.length) return null;
        const visible = all.slice(0, PER_GROUP);
        const remaining = all.length - visible.length;
        return (
          <div key={kind} className="py-1">
            <div className="px-4 py-1 text-[10px] uppercase tracking-wide text-ink-mute font-medium">
              {label}
            </div>
            {visible.map((sr) => {
              const idx = indexByKey.get(sr.row.key) ?? -1;
              const isActive = idx === active;
              return (
                <ResultRow
                  key={sr.row.key}
                  row={sr.row}
                  matched={sr.matched}
                  query={query}
                  active={isActive}
                  onClick={() => onPick(sr.row.to)}
                  onMouseEnter={() => idx >= 0 && onHover(idx)}
                />
              );
            })}
            {remaining > 0 && (
              <button
                onClick={() => onPick(sectionTo)}
                className="w-full text-left px-4 py-1.5 text-[11px] text-ink-mute hover:text-ink hover:bg-slate-100"
              >
                + {remaining} more in {label} →
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}

function ResultRow({
  row,
  matched,
  query,
  active,
  onClick,
  onMouseEnter,
}: {
  row: BaseRow;
  matched: number[];
  query: string;
  active: boolean;
  onClick: () => void;
  onMouseEnter: () => void;
}) {
  const Icon = iconFor(row.kind);
  // Pending gates get the amber active+resting accent so the
  // operator's eye lands on them before any other row.
  const isGate = row.kind === 'pending_gate';
  return (
    <button
      onClick={onClick}
      onMouseEnter={onMouseEnter}
      className={cn(
        'w-full flex items-center gap-3 px-4 py-2 text-left text-sm',
        active && (isGate ? 'bg-amber-50 text-amber-800' : 'bg-brand-50 text-brand-700'),
        !active && (isGate ? 'text-amber-800 hover:bg-amber-50/70' : 'text-ink hover:bg-slate-50'),
      )}
    >
      <Icon size={14} className={cn('shrink-0', isGate ? 'text-amber-700' : (active ? 'text-brand-700' : 'text-ink-mute'))} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 min-w-0">
          <span className="truncate">{highlight(row.title, matched, query)}</span>
          <CPSourceChip source={row.cpSource} />
        </div>
        {row.subtitle && (
          <div className={cn('text-[11px] truncate', active ? 'text-brand-700/70' : 'text-ink-mute')}>
            {row.kind === 'finding' && row.severity ? (
              <>
                <span className={cn('severity-badge mr-1.5 align-middle text-[9px]', `severity-${row.severity.toLowerCase()}`)}>
                  {row.severity}
                </span>
                {row.subtitle.replace(/^[A-Z]+ ·\s*/, '')}
              </>
            ) : row.subtitle}
          </div>
        )}
      </div>
    </button>
  );
}

function Hint({ k, label }: { k: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1">
      <kbd className="px-1.5 py-0.5 rounded border border-border bg-white text-[10px] font-mono text-ink-dim">
        {k}
      </kbd>
      <span>{label}</span>
    </span>
  );
}

function EmptyHint() {
  return (
    <div className="px-4 py-6 text-xs text-ink-mute space-y-1">
      <div>Type to search across findings, daimons, agents, nodes, and orchestrations.</div>
      <div>Tip: <code className="px-1 rounded bg-slate-100 text-ink-dim">#245</code> jumps to any item with that numeric id (finding, run, node, orchestration).</div>
      <div>Pending operator gates surface at the top automatically.</div>
    </div>
  );
}

function NoMatches() {
  return (
    <div className="px-4 py-6 text-xs text-ink-mute">
      No matches. Try the full pages:{' '}
      <a className="text-brand-500 hover:underline" href="/findings">Findings</a>{', '}
      <a className="text-brand-500 hover:underline" href="/daimons">Daimons</a>{', '}
      <a className="text-brand-500 hover:underline" href="/agents?tab=library">Agents</a>{', '}
      <a className="text-brand-500 hover:underline" href="/nodes">Nodes</a>{', '}
      <a className="text-brand-500 hover:underline" href="/orchestrations">Orchestrations</a>.
    </div>
  );
}

// ── helpers ────────────────────────────────────────────────────────

function parseIDQuery(q: string): number | null {
  const m = q.match(/^#?\s*(\d{1,12})$/);
  if (!m) return null;
  const n = Number(m[1]);
  return Number.isFinite(n) && n > 0 ? n : null;
}

function iconFor(kind: Kind) {
  switch (kind) {
    case 'pending_gate':  return Pause;
    case 'orchestration': return Workflow;
    case 'finding':       return AlertTriangle;
    case 'daimon':        return Layers;
    case 'agent':         return Sparkles;
    case 'node':          return Server;
  }
}

/**
 * Highlight matched chars in `text`. We use the indices returned by
 * `fuzzyMatch` against the title. When the query was matched against
 * the subtitle (no title indices), we fall back to a simple
 * case-insensitive substring underline.
 */
function highlight(text: string, matched: number[], query: string): React.ReactNode {
  if (!matched.length) {
    if (!query) return text;
    const i = text.toLowerCase().indexOf(query.toLowerCase());
    if (i < 0) return text;
    return (
      <>
        {text.slice(0, i)}
        <mark className="bg-transparent text-brand-700 font-semibold">{text.slice(i, i + query.length)}</mark>
        {text.slice(i + query.length)}
      </>
    );
  }
  const set = new Set(matched);
  const parts: React.ReactNode[] = [];
  for (let i = 0; i < text.length; i++) {
    if (set.has(i)) {
      parts.push(<mark key={i} className="bg-transparent text-brand-700 font-semibold">{text[i]}</mark>);
    } else {
      parts.push(text[i]);
    }
  }
  return <>{parts}</>;
}

/**
 * Simple fuzzy matcher. Returns null when no match.
 *
 * Score components, highest-impact first:
 *  - Exact substring in title: +1000, +200 if at index 0.
 *  - Subsequence char match in title: +10 per char, +5 bonus when the
 *    char follows a word boundary (start, space, dash, slash, dot, _).
 *  - Substring in subtitle: +50.
 *
 * We intentionally don't import a fuzzy library — this is ~30 lines and
 * matches well enough for the v0 palette.
 */
function fuzzyMatch(query: string, title: string, subtitle?: string):
  { score: number; matched: number[] } | null {
  const q = query.toLowerCase();
  const t = title.toLowerCase();
  const sub = (subtitle || '').toLowerCase();

  // Exact substring in title — short-circuit, return char indices for
  // the highlight.
  const direct = t.indexOf(q);
  if (direct >= 0) {
    const matched: number[] = [];
    for (let i = 0; i < q.length; i++) matched.push(direct + i);
    return { score: 1000 + (direct === 0 ? 200 : 0), matched };
  }

  // Subsequence match: walk the query, greedily pick the next char in
  // the title. A miss anywhere ⇒ no title match.
  const matched: number[] = [];
  let ti = 0;
  let score = 0;
  for (let qi = 0; qi < q.length; qi++) {
    const ch = q[qi];
    let found = -1;
    while (ti < t.length) {
      if (t[ti] === ch) { found = ti; ti++; break; }
      ti++;
    }
    if (found < 0) {
      matched.length = 0;
      break;
    }
    matched.push(found);
    score += 10;
    const prev = found > 0 ? t[found - 1] : '';
    if (found === 0 || /[\s\-/_.:]/.test(prev)) score += 5;
  }

  if (matched.length === q.length) {
    return { score, matched };
  }

  // No title match — try subtitle as a weaker fallback.
  if (sub && sub.includes(q)) {
    return { score: 50, matched: [] };
  }
  return null;
}
