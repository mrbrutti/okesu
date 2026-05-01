// Legacy-shape normalizer for SmartPayload's prompt mode.
//
// Pre-projection orchestration runs (≤ ~run 1102 on the test stack)
// stored prompts where Go structs serialised with Title-case field
// names AND sql.NullString-style wrappers — e.g.
//   {"ID":4281,"Severity":{"String":"INFO","Valid":true},"Title":{"String":"…","Valid":true}}
//
// The frozen prompt text is what the agent saw; we don't rewrite it
// on read. But the chip detector keys off lowercase fields
// (id/severity/title/category/host/status/...), so the legacy shape
// slides past unchipped.
//
// normalizeLegacyShape converts a parsed value in-place to the
// canonical contract:
//   - Title-case keys → lowercase (ID → id, Severity → severity)
//   - {String:"x", Valid:true}  → "x"
//   - {Int64:N,    Valid:true}  → N
//   - {Float64:N,  Valid:true}  → N
//   - {Bool:b,     Valid:true}  → b
//   - {Time:"…",   Valid:true}  → "…"
//   - any of the above with Valid:false → undefined (key dropped)
// Recurses into nested objects and arrays. Non-object scalars pass
// through untouched.

type Json = unknown;
type Obj = Record<string, Json>;

const NULL_STRING_KEYS = new Set(['String', 'Valid']);
const NULL_INT_KEYS = new Set(['Int64', 'Valid']);
const NULL_FLOAT_KEYS = new Set(['Float64', 'Valid']);
const NULL_BOOL_KEYS = new Set(['Bool', 'Valid']);
const NULL_TIME_KEYS = new Set(['Time', 'Valid']);

function isObj(v: unknown): v is Obj {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function keysMatch(o: Obj, allowed: Set<string>): boolean {
  const ks = Object.keys(o);
  if (ks.length !== allowed.size) return false;
  for (const k of ks) if (!allowed.has(k)) return false;
  return true;
}

// unwrapNullable returns the inner value of a sql.Null* wrapper, or
// undefined when the row is invalid (Valid:false). Returns the
// special sentinel `null` (typed as undefined) so the caller can drop
// the key entirely — leaving Valid:false noise out of the detector
// path that's about to test for key presence.
function unwrapNullable(o: Obj): { hit: true; value: Json | undefined } | { hit: false } {
  if (keysMatch(o, NULL_STRING_KEYS)) {
    return { hit: true, value: o.Valid === true ? o.String : undefined };
  }
  if (keysMatch(o, NULL_INT_KEYS)) {
    return { hit: true, value: o.Valid === true ? o.Int64 : undefined };
  }
  if (keysMatch(o, NULL_FLOAT_KEYS)) {
    return { hit: true, value: o.Valid === true ? o.Float64 : undefined };
  }
  if (keysMatch(o, NULL_BOOL_KEYS)) {
    return { hit: true, value: o.Valid === true ? o.Bool : undefined };
  }
  if (keysMatch(o, NULL_TIME_KEYS)) {
    return { hit: true, value: o.Valid === true ? o.Time : undefined };
  }
  return { hit: false };
}

// titleToLower lowercases the first letter and collapses common
// Go-style camel-case acronyms (CVE → cve, ID → id, EventID → event_id,
// CreatedAt → created_at). Conservative: only converts keys that
// start with an uppercase letter; leaves already-lowercase and
// snake_case keys untouched.
function titleToLower(k: string): string {
  if (k.length === 0) return k;
  if (k[0] !== k[0].toUpperCase() || k[0] === k[0].toLowerCase()) return k;
  // Go-style acronym handling: "ID" → "id", "CVE" → "cve",
  // "EventID" → "event_id", "DedupKey" → "dedup_key".
  const out: string[] = [];
  for (let i = 0; i < k.length; i++) {
    const c = k[i];
    const upper = c === c.toUpperCase() && c !== c.toLowerCase();
    if (i === 0) {
      out.push(c.toLowerCase());
      continue;
    }
    if (!upper) {
      out.push(c);
      continue;
    }
    // Insert underscore before uppercase boundary, unless previous
    // char was also uppercase (acronym run, e.g. "CVE").
    const prev = k[i - 1];
    const prevUpper = prev === prev.toUpperCase() && prev !== prev.toLowerCase();
    if (!prevUpper) out.push('_');
    out.push(c.toLowerCase());
  }
  return out.join('');
}

export function normalizeLegacyShape(value: Json): Json {
  if (Array.isArray(value)) {
    return value.map(normalizeLegacyShape);
  }
  if (!isObj(value)) return value;

  // sql.NullX wrapper → inner value.
  const unwrap = unwrapNullable(value);
  if (unwrap.hit) {
    return normalizeLegacyShape(unwrap.value ?? null);
  }

  // Recurse + key-rename.
  const out: Obj = {};
  for (const [k, v] of Object.entries(value)) {
    const nk = titleToLower(k);
    const nv = normalizeLegacyShape(v);
    if (nv === undefined) continue; // dropped Null{Valid:false}
    out[nk] = nv;
  }
  return out;
}
