// Tiny diff-and-apply binding between a `<textarea>` value and a
// Y.Text. Computes a single replace operation per change — finds the
// common prefix and common suffix, replaces the middle.
//
// Good enough for the war-room textarea use case. For more complex
// editing surfaces (multi-paragraph, formatted text), consider
// y-prosemirror or a richer delta library.

import * as Y from 'yjs';

/**
 * Apply the difference between the Y.Text's current value and the
 * desired value, as a single Yjs transaction. Returns nothing.
 */
export function applyTextDelta(text: Y.Text, desired: string): void {
  const current = text.toString();
  if (current === desired) return;

  let start = 0;
  const minLen = Math.min(current.length, desired.length);
  while (start < minLen && current[start] === desired[start]) start += 1;

  let endA = current.length;
  let endB = desired.length;
  while (endA > start && endB > start && current[endA - 1] === desired[endB - 1]) {
    endA -= 1;
    endB -= 1;
  }

  text.doc?.transact(() => {
    if (endA > start) text.delete(start, endA - start);
    if (endB > start) text.insert(start, desired.slice(start, endB));
  });
}
