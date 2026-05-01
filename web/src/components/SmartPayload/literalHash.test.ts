import { describe, it, expect } from 'vitest';
import { literalHash } from './literalHash';

describe('literalHash', () => {
  it('returns 16 hex chars', async () => {
    const h = await literalHash('{"id":42}');
    expect(h).toMatch(/^[0-9a-f]{16}$/);
  });

  it('is deterministic', async () => {
    const a = await literalHash('{"id":42}');
    const b = await literalHash('{"id":42}');
    expect(a).toEqual(b);
  });

  it('differs for different inputs', async () => {
    const a = await literalHash('{"id":1}');
    const b = await literalHash('{"id":2}');
    expect(a).not.toEqual(b);
  });
});
