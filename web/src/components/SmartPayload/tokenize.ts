// Walks a prose+JSON-mixed string, emitting text segments and
// well-formed JSON object/array segments. Used by SmartPayload's
// prompt mode so a rendered_prompt like
//
//   "investigate the following: {"id":42, ...}"
//
// becomes [text, json] and the renderer can swap the JSON segment
// for a styled entity card.
//
// Algorithm: single-pass, depth-aware brace/bracket counter that
// respects string literals (including escaped quotes). Unbalanced
// regions fall through as plain text. O(n) on the input length.

export type Token =
  | { kind: 'text'; text: string }
  | { kind: 'json'; src: string };

export function tokenize(input: string): Token[] {
  const out: Token[] = [];
  let i = 0;
  const n = input.length;
  let textStart = 0;

  while (i < n) {
    const c = input[i];
    if (c === '{' || c === '[') {
      const end = scanJSONLiteral(input, i);
      if (end > i) {
        if (textStart < i) {
          out.push({ kind: 'text', text: input.slice(textStart, i) });
        }
        out.push({ kind: 'json', src: input.slice(i, end) });
        i = end;
        textStart = i;
        continue;
      }
    }
    i++;
  }
  if (textStart < n) {
    out.push({ kind: 'text', text: input.slice(textStart, n) });
  }
  return coalesceJSONStreams(out);
}

// coalesceJSONStreams folds runs of JSON-object tokens separated
// only by comma-and-whitespace into a single synthetic JSON array
// token. The agent's output_summary frequently looks like
//   ...,{"kind":"X","finding_id":1},{"kind":"Y","finding_id":2},...
// where the leading `[` has been truncated off the head and we
// otherwise emit one token per object. Coalescing lets the renderer
// recognise the homogeneous run and render it as a single table.
//
// Conservative: requires ≥3 consecutive JSON objects; the wrapping
// text segments (commas and surrounding noise) are preserved as
// separate text tokens so the operator still sees any prose context.
function coalesceJSONStreams(tokens: Token[]): Token[] {
  const out: Token[] = [];
  let i = 0;
  while (i < tokens.length) {
    const t = tokens[i];
    if (t.kind !== 'json' || !t.src.startsWith('{')) {
      out.push(t);
      i++;
      continue;
    }
    // Greedy lookahead: grab every (text=just-comma-or-ws, json) pair.
    const run: string[] = [t.src];
    let j = i + 1;
    while (j + 1 < tokens.length) {
      const sep = tokens[j];
      const nxt = tokens[j + 1];
      if (sep.kind !== 'text') break;
      if (!/^\s*,\s*$/.test(sep.text)) break;
      if (nxt.kind !== 'json' || !nxt.src.startsWith('{')) break;
      run.push(nxt.src);
      j += 2;
    }
    if (run.length >= 3) {
      out.push({ kind: 'json', src: '[' + run.join(',') + ']' });
      i = j;
      continue;
    }
    out.push(t);
    i++;
  }
  return out;
}

// scanJSONLiteral starts at input[start] which is '{' or '['.
// Returns the index *after* the matching close brace/bracket, or
// `start` (no progress) when the literal isn't well-formed.
function scanJSONLiteral(input: string, start: number): number {
  const open = input[start];
  let depth = 0;
  let inString = false;
  let escape = false;

  for (let i = start; i < input.length; i++) {
    const c = input[i];
    if (inString) {
      if (escape) {
        escape = false;
        continue;
      }
      if (c === '\\') {
        escape = true;
        continue;
      }
      if (c === '"') inString = false;
      continue;
    }
    if (c === '"') {
      inString = true;
      continue;
    }
    if (c === '{' || c === '[') {
      depth++;
      continue;
    }
    if (c === '}' || c === ']') {
      depth--;
      if (depth === 0) {
        // Verify the closing bracket type matches the opener at depth 0
        // (mismatched would be e.g. `{...]` — return start to skip).
        const openMatch = open === '{' ? '}' : ']';
        if (c !== openMatch) return start;
        return i + 1;
      }
      if (depth < 0) {
        return start;
      }
      continue;
    }
  }
  return start;
}
