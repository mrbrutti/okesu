import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { SmartPayload } from './index';
import { literalHash } from './literalHash';
import type { PromptEntities } from '../../api';

afterEach(cleanup);

describe('SmartPayload', () => {
  it('prompt mode: renders prose around an embedded finding JSON literal', () => {
    const prompt = 'Investigate: {"id":42,"severity":"HIGH","title":"Cron","category":"process"} now';
    render(<SmartPayload value={prompt} variant="prompt" />);
    expect(screen.getByText(/Investigate:/)).toBeTruthy();
    expect(screen.getByText(/Cron/)).toBeTruthy();
    expect(screen.getByText('HIGH')).toBeTruthy();
    expect(screen.getByText(/now/)).toBeTruthy();
  });

  it('tree mode: detects a finding-shaped subtree in result', () => {
    const result = {
      matched: { id: 7, severity: 'LOW', title: 'X', category: 'p' },
    };
    render(<SmartPayload value={result} variant="tree" />);
    expect(screen.getByText(/X/)).toBeTruthy();
  });

  it('falls back to plain text + inline JSON for unrecognised JSON in prompt', () => {
    const prompt = 'just text {"weird":"shape"} and prose';
    render(<SmartPayload value={prompt} variant="prompt" />);
    expect(screen.getByText(/just text/)).toBeTruthy();
    expect(screen.getByText(/and prose/)).toBeTruthy();
  });

  it('tree mode: non-entity object renders via StructuredView fallback', () => {
    render(<SmartPayload value={{ a: 1, b: 'x' }} variant="tree" />);
    // StructuredView shows keys as labels; assert key text appears
    expect(screen.getByText('a')).toBeTruthy();
    expect(screen.getByText('b')).toBeTruthy();
  });

  it('auto variant treats string as prompt mode', () => {
    render(<SmartPayload value={'Hello {"id":1,"severity":"LOW","title":"t","category":"c"}'} />);
    expect(screen.getByText(/Hello/)).toBeTruthy();
    expect(screen.getByText(/t/)).toBeTruthy();
  });

  it('auto variant treats object as tree mode', () => {
    render(<SmartPayload value={{ id: 9, severity: 'HIGH', title: 'auto', category: 'k' }} />);
    expect(screen.getByText(/auto/)).toBeTruthy();
    expect(screen.getByText('HIGH')).toBeTruthy();
  });

  it('prompt mode: hash-matches a server-emitted ref over the heuristic', async () => {
    // Server-emitted snapshot title differs from what the
    // detector would produce — proves the ref is what's being
    // rendered, not the detector fallback.
    const findingJSON = '{"id":42,"severity":"LOW","title":"DetectorFallback","category":"x"}';
    const hash = await literalHash(findingJSON);
    const entities: PromptEntities = {
      refs: [{
        kind: 'finding',
        id: 42,
        snapshot: { id: 42, severity: 'LOW', title: 'FromServer', category: 'x' },
        literal_hash: hash,
      }],
    };
    render(<SmartPayload value={`see ${findingJSON}`} entities={entities} variant="prompt" />);
    // Hash compute is async — wait for the server-snapshot title
    // to appear (and confirm the detector-fallback title does NOT).
    await waitFor(() => {
      expect(screen.getByText(/FromServer/)).toBeTruthy();
    });
    expect(screen.queryByText(/DetectorFallback/)).toBeNull();
  });
});
