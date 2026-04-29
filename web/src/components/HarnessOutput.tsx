// Renders a raw JSONL stream from the okesu agent harness (claude/codex)
// as a conversation: text bubbles, tool calls (dark terminal), tool
// results (slate), and an init/done footer. Matches the look of
// AgentMessages.tsx so daimon ticks and ad-hoc/orchestrated runs feel
// like the same surface.
//
// Input is the raw stdout (or tail) the harness writes — one JSON event
// per line. Lines that don't parse as JSON, or JSON that doesn't match
// a known event shape, fall through as a plain "raw" segment so the
// operator never silently loses output.
import { CheckCircle2, Sparkles, Terminal } from 'lucide-react';

type Move =
  | { kind: 'text'; text: string }
  | { kind: 'tool_call'; name?: string; input?: unknown; toolID?: string }
  | { kind: 'tool_result'; output?: string; toolID?: string }
  | { kind: 'raw'; text: string };

interface HarnessFooter {
  provider?: string;
  model?: string;
  stopReason?: string;
  inputTokens?: number;
  outputTokens?: number;
  turns?: number;
  error?: string;
}

// maxHeight controls the scroll container. Pass `null` to disable
// the inner scroll entirely — useful when a parent already scrolls
// (e.g. the live Runs view) and a nested overflow-auto would create
// a sticky inner scrollbar.
export function HarnessOutput({ text, maxHeight = 480 }: { text: string; maxHeight?: number | null }) {
  const { moves, footer } = parseHarnessJSONL(text);
  const containerStyle = maxHeight == null ? undefined : { maxHeight };
  const overflowClass = maxHeight == null ? '' : 'overflow-auto';

  if (moves.length === 0) {
    return (
      <pre
        className={`text-[11px] font-mono bg-slate-50 border border-border rounded p-2 whitespace-pre-wrap break-words ${overflowClass}`}
        style={containerStyle}
      >
        {text || '(no output)'}
      </pre>
    );
  }

  return (
    <div
      className={`bg-slate-50 border border-border rounded p-3 space-y-2 ${overflowClass}`}
      style={containerStyle}
    >
      {moves.map((m, i) => <MoveBubble key={i} move={m} />)}
      {footer && <FooterLine footer={footer} />}
    </div>
  );
}

function MoveBubble({ move }: { move: Move }) {
  if (move.kind === 'text') {
    return (
      <div className="flex gap-2.5">
        <div className="w-7 h-7 shrink-0 rounded-full bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center text-white">
          <Sparkles size={11} />
        </div>
        <div className="flex-1 bg-brand-50/50 border border-brand-100 rounded-2xl rounded-tl-sm px-4 py-2.5 text-sm whitespace-pre-wrap text-ink leading-relaxed">
          {move.text}
        </div>
      </div>
    );
  }
  if (move.kind === 'tool_call') {
    const name = move.name ?? 'tool';
    return (
      <div className="flex gap-2.5">
        <div className="w-7 h-7 shrink-0 rounded-full bg-slate-700 flex items-center justify-center text-white">
          <Terminal size={11} />
        </div>
        <div className="flex-1 bg-slate-900 text-slate-100 rounded-2xl rounded-tl-sm px-4 py-2.5">
          <div className="flex items-center gap-2 mb-1.5">
            <span className="text-[11px] font-semibold uppercase tracking-wide text-slate-400">tool</span>
            <code className="text-xs text-emerald-300">{name}</code>
            {move.toolID && <code className="text-[10px] text-slate-500">{move.toolID.slice(-8)}</code>}
          </div>
          <pre className="text-xs font-mono text-slate-200 whitespace-pre-wrap break-all max-h-48 overflow-auto">
            {formatToolInput(move.input)}
          </pre>
        </div>
      </div>
    );
  }
  if (move.kind === 'tool_result') {
    return (
      <div className="flex gap-2.5">
        <div className="w-7 h-7 shrink-0 rounded-full bg-slate-200 flex items-center justify-center text-slate-700">
          <CheckCircle2 size={11} />
        </div>
        <div className="flex-1 bg-white border border-slate-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
          <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-mute mb-1.5">
            tool result{move.toolID ? ` · ${move.toolID.slice(-8)}` : ''}
          </div>
          <pre className="text-xs font-mono text-ink-dim whitespace-pre-wrap break-all max-h-48 overflow-auto">
            {move.output || '(no output)'}
          </pre>
        </div>
      </div>
    );
  }
  // raw
  return (
    <pre className="text-[11px] font-mono text-ink-dim whitespace-pre-wrap break-all bg-white border border-slate-200 rounded px-3 py-2">
      {move.text}
    </pre>
  );
}

function FooterLine({ footer }: { footer: HarnessFooter }) {
  const parts: string[] = [];
  if (footer.provider || footer.model) parts.push(`${footer.provider ?? ''} ${footer.model ?? ''}`.trim());
  if (footer.stopReason) parts.push(`stop: ${footer.stopReason}`);
  if (footer.inputTokens !== undefined) {
    parts.push(`${footer.inputTokens} in / ${footer.outputTokens ?? 0} out`);
  }
  if (footer.turns) parts.push(`${footer.turns} turn${footer.turns === 1 ? '' : 's'}`);
  if (parts.length === 0 && !footer.error) return null;
  return (
    <div className="pt-2 border-t border-border/60 flex flex-wrap items-center gap-3 text-[11px] text-ink-mute">
      {parts.map((p, i) => <span key={i} className="font-mono">{p}</span>)}
      {footer.error && (
        <span className="text-red-700">error: {footer.error}</span>
      )}
    </div>
  );
}

// parseHarnessJSONL walks a JSONL string and folds it into a Move[] +
// optional footer. Consecutive `text` deltas are concatenated into a
// single bubble — the harness emits one event per token-ish chunk.
export function parseHarnessJSONL(raw: string): { moves: Move[]; footer?: HarnessFooter } {
  const moves: Move[] = [];
  let footer: HarnessFooter | undefined;
  let textBuf: string | null = null;
  const flushText = () => {
    if (textBuf !== null) {
      moves.push({ kind: 'text', text: textBuf });
      textBuf = null;
    }
  };

  const lines = raw.split(/\r?\n/);
  // The input is often a TAIL of a longer JSONL stream, so the very
  // first line is frequently a partial JSON fragment that shouldn't
  // surface as a "raw" garbled bubble. Drop the leading partial line
  // when it fails to parse and the next line does.
  let leadIdx = 0;
  if (lines.length >= 2) {
    const first = lines[0].trim();
    if (first) {
      let firstOK = false;
      try { JSON.parse(first); firstOK = true; } catch { /* partial */ }
      if (!firstOK) {
        // Look for the first parseable line; only drop if at least one
        // later line is valid JSON, so we don't silently swallow output
        // that simply isn't JSON at all.
        for (let i = 1; i < lines.length; i++) {
          const t = lines[i].trim();
          if (!t) continue;
          try { JSON.parse(t); leadIdx = i; break; } catch { /* keep looking */ }
        }
      }
    }
  }

  for (let i = leadIdx; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();
    if (!trimmed) continue;

    let obj: Record<string, unknown> | null = null;
    try {
      const parsed = JSON.parse(trimmed);
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        obj = parsed as Record<string, unknown>;
      }
    } catch {
      /* not JSON */
    }

    if (!obj || typeof obj.type !== 'string') {
      flushText();
      moves.push({ kind: 'raw', text: trimmed });
      continue;
    }

    switch (obj.type) {
      case 'init': {
        footer = {
          ...(footer ?? {}),
          provider: stringField(obj, 'provider'),
          model: stringField(obj, 'model'),
        };
        break;
      }
      case 'text': {
        const t = stringField(obj, 'text');
        if (!t) break;
        textBuf = (textBuf ?? '') + t;
        break;
      }
      case 'tool_call': {
        flushText();
        moves.push({
          kind: 'tool_call',
          name: stringField(obj, 'tool_name'),
          input: obj.input,
          toolID: stringField(obj, 'tool_id'),
        });
        break;
      }
      case 'tool_result': {
        flushText();
        moves.push({
          kind: 'tool_result',
          output: stringField(obj, 'output'),
          toolID: stringField(obj, 'tool_id'),
        });
        break;
      }
      case 'done': {
        flushText();
        const usage = obj.usage as Record<string, unknown> | undefined;
        footer = {
          ...(footer ?? {}),
          stopReason: stringField(obj, 'stop_reason'),
          inputTokens: numberField(usage, 'input_tokens'),
          outputTokens: numberField(usage, 'output_tokens'),
          turns: numberField(obj, 'turn') ?? numberField(obj, 'turns'),
        };
        break;
      }
      case 'error':
      case 'api_unavailable': {
        flushText();
        footer = {
          ...(footer ?? {}),
          error: stringField(obj, 'error') || stringField(obj, 'message'),
        };
        break;
      }
      // tick_*, finding, collector_result, action_taken, etc. — not part
      // of the per-run conversation surface; ignore so we don't clutter.
      default:
        break;
    }
  }
  flushText();
  return { moves, footer };
}

function formatToolInput(input: unknown): string {
  if (input == null) return '';
  if (typeof input === 'string') return input;
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

function stringField(o: unknown, key: string): string | undefined {
  if (!o || typeof o !== 'object') return undefined;
  const v = (o as Record<string, unknown>)[key];
  return typeof v === 'string' ? v : undefined;
}

function numberField(o: unknown, key: string): number | undefined {
  if (!o || typeof o !== 'object') return undefined;
  const v = (o as Record<string, unknown>)[key];
  return typeof v === 'number' ? v : undefined;
}
