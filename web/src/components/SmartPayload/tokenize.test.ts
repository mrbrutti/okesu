import { describe, it, expect } from 'vitest';
import { tokenize } from './tokenize';

describe('tokenize', () => {
  it('returns single text segment for plain prose', () => {
    const out = tokenize('plain prose, no JSON');
    expect(out).toEqual([{ kind: 'text', text: 'plain prose, no JSON' }]);
  });

  it('extracts a top-level JSON object literal', () => {
    const out = tokenize('see {"id":42}');
    expect(out).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'json', src: '{"id":42}' },
    ]);
  });

  it('extracts a top-level JSON array literal', () => {
    const out = tokenize('items: [1,2,3]');
    expect(out).toEqual([
      { kind: 'text', text: 'items: ' },
      { kind: 'json', src: '[1,2,3]' },
    ]);
  });

  it('handles trailing prose after JSON', () => {
    const out = tokenize('prefix {"a":1} suffix');
    expect(out).toEqual([
      { kind: 'text', text: 'prefix ' },
      { kind: 'json', src: '{"a":1}' },
      { kind: 'text', text: ' suffix' },
    ]);
  });

  it('respects quoted braces inside strings', () => {
    const out = tokenize('see {"title":"a}b","id":1}');
    expect(out).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'json', src: '{"title":"a}b","id":1}' },
    ]);
  });

  it('handles nested objects', () => {
    const out = tokenize('x={"outer":{"inner":1}} y');
    expect(out).toEqual([
      { kind: 'text', text: 'x=' },
      { kind: 'json', src: '{"outer":{"inner":1}}' },
      { kind: 'text', text: ' y' },
    ]);
  });

  it('handles two consecutive objects with prose between', () => {
    const out = tokenize('A {"a":1} B {"b":2} C');
    expect(out).toHaveLength(5);
    expect(out[1]).toEqual({ kind: 'json', src: '{"a":1}' });
    expect(out[3]).toEqual({ kind: 'json', src: '{"b":2}' });
  });

  it('does not split on { inside string literals containing escaped quotes', () => {
    const out = tokenize(String.raw`{"name":"with \"quoted\" {brace}"}`);
    expect(out).toHaveLength(1);
    expect(out[0]).toEqual({ kind: 'json', src: String.raw`{"name":"with \"quoted\" {brace}"}` });
  });

  it('falls through unmatched open-brace as text', () => {
    const out = tokenize('text { unbalanced');
    expect(out).toEqual([{ kind: 'text', text: 'text { unbalanced' }]);
  });
});
