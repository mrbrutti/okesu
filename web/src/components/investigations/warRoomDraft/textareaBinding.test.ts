import { describe, it, expect } from 'vitest';
import * as Y from 'yjs';
import { applyTextDelta } from './textareaBinding';

describe('applyTextDelta', () => {
  it('handles a pure-insert at the end', () => {
    const doc = new Y.Doc();
    const text = doc.getText('body');
    text.insert(0, 'hello');
    applyTextDelta(text, 'hello world');
    expect(text.toString()).toBe('hello world');
  });

  it('handles a pure-insert at the start', () => {
    const doc = new Y.Doc();
    const text = doc.getText('body');
    text.insert(0, 'world');
    applyTextDelta(text, 'hello world');
    expect(text.toString()).toBe('hello world');
  });

  it('handles a pure-delete at the end', () => {
    const doc = new Y.Doc();
    const text = doc.getText('body');
    text.insert(0, 'hello world');
    applyTextDelta(text, 'hello');
    expect(text.toString()).toBe('hello');
  });

  it('handles a replace in the middle', () => {
    const doc = new Y.Doc();
    const text = doc.getText('body');
    text.insert(0, 'hello world');
    applyTextDelta(text, 'hello brave world');
    expect(text.toString()).toBe('hello brave world');
  });

  it('is a no-op when text is unchanged', () => {
    const doc = new Y.Doc();
    const text = doc.getText('body');
    text.insert(0, 'unchanged');
    applyTextDelta(text, 'unchanged');
    expect(text.toString()).toBe('unchanged');
  });
});
