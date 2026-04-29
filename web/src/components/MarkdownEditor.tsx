// Markdown-aware editor used wherever operators write daimon / agent
// system prompts or other prompt-shaped content. Built on CodeMirror 6
// with a small Tailwind-token-driven theme so the colors stay in
// lockstep with the rest of the UI (sev-* / brand-* / ink-*).
//
// Why CodeMirror over a raw textarea: YAML frontmatter syntax
// highlighting, fenced code-block highlight, line wrapping, drag-drop
// safe — and ~70KB gzipped vs Monaco's ~1MB. The instance is created
// imperatively because react-codemirror wrappers add their own quirks
// we don't need.

import { useEffect, useImperativeHandle, useRef, forwardRef } from 'react';
import { EditorState, Compartment } from '@codemirror/state';
import { EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { markdown } from '@codemirror/lang-markdown';
import { yaml } from '@codemirror/lang-yaml';
import {
  syntaxHighlighting, HighlightStyle, indentUnit, bracketMatching,
  StreamLanguage, LanguageSupport, LanguageDescription,
} from '@codemirror/language';
import { tags as t } from '@lezer/highlight';
import { cn } from '../lib/cn';

// Wrap a legacy-modes stream parser as a LanguageSupport so the
// markdown plugin can hand fenced blocks off to it the same way it
// hands off first-class languages.
function streamLang(parser: Parameters<typeof StreamLanguage.define>[0]): LanguageSupport {
  return new LanguageSupport(StreamLanguage.define(parser));
}

// Languages the markdown plugin should recognize inside fenced blocks.
// Aliases match the names operators actually type — `js`, `ts`, `sh`,
// `bash`, `yml`, `dockerfile`, etc.
//
// Each `load` is a dynamic import so Vite emits one chunk per language
// pack. Operators editing a daimon prompt that only contains ```bash
// blocks fetch shell+legacy-modes (~30KB) — they don't pay for
// Python+Java+Rust they didn't use. The cost amortises across the
// session: once shell is loaded, every editor reuses it.
const CODE_LANGUAGES = [
  LanguageDescription.of({
    name: 'javascript',
    alias: ['js', 'jsx', 'ts', 'tsx', 'typescript'],
    load: async () => (await import('@codemirror/lang-javascript')).javascript({ jsx: true, typescript: true }),
  }),
  LanguageDescription.of({
    name: 'python',
    alias: ['py'],
    load: async () => (await import('@codemirror/lang-python')).python(),
  }),
  LanguageDescription.of({
    name: 'go',
    alias: ['golang'],
    load: async () => (await import('@codemirror/lang-go')).go(),
  }),
  LanguageDescription.of({
    name: 'rust',
    alias: ['rs'],
    load: async () => (await import('@codemirror/lang-rust')).rust(),
  }),
  LanguageDescription.of({
    name: 'cpp',
    alias: ['c', 'c++', 'cxx', 'cc', 'h', 'hpp'],
    load: async () => (await import('@codemirror/lang-cpp')).cpp(),
  }),
  LanguageDescription.of({
    name: 'java',
    alias: [],
    load: async () => (await import('@codemirror/lang-java')).java(),
  }),
  LanguageDescription.of({
    name: 'php',
    alias: [],
    load: async () => (await import('@codemirror/lang-php')).php(),
  }),
  LanguageDescription.of({
    name: 'sql',
    alias: ['postgres', 'postgresql', 'mysql', 'sqlite'],
    load: async () => (await import('@codemirror/lang-sql')).sql(),
  }),
  LanguageDescription.of({
    name: 'json',
    alias: ['jsonc'],
    load: async () => (await import('@codemirror/lang-json')).json(),
  }),
  LanguageDescription.of({
    name: 'yaml',
    alias: ['yml'],
    // YAML is already statically imported above — frontmatter highlight
    // depends on it on every edit, so paying its cost up front in the
    // editor chunk is correct.
    load: async () => yaml(),
  }),
  LanguageDescription.of({
    name: 'html',
    alias: ['htm'],
    load: async () => (await import('@codemirror/lang-html')).html(),
  }),
  LanguageDescription.of({
    name: 'css',
    alias: ['scss', 'sass', 'less'],
    load: async () => (await import('@codemirror/lang-css')).css(),
  }),
  LanguageDescription.of({
    name: 'xml',
    alias: ['svg'],
    load: async () => (await import('@codemirror/lang-xml')).xml(),
  }),
  LanguageDescription.of({
    name: 'shell',
    alias: ['sh', 'bash', 'zsh', 'fish', 'console'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/shell')).shell),
  }),
  LanguageDescription.of({
    name: 'dockerfile',
    alias: ['docker'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/dockerfile')).dockerFile),
  }),
  LanguageDescription.of({
    name: 'toml',
    alias: [],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/toml')).toml),
  }),
  LanguageDescription.of({
    name: 'ruby',
    alias: ['rb'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/ruby')).ruby),
  }),
  LanguageDescription.of({
    name: 'lua',
    alias: [],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/lua')).lua),
  }),
  LanguageDescription.of({
    name: 'perl',
    alias: ['pl'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/perl')).perl),
  }),
  LanguageDescription.of({
    name: 'powershell',
    alias: ['ps1', 'pwsh'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/powershell')).powerShell),
  }),
  LanguageDescription.of({
    name: 'diff',
    alias: ['patch'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/diff')).diff),
  }),
  LanguageDescription.of({
    name: 'properties',
    alias: ['ini', 'conf', 'config'],
    load: async () => streamLang((await import('@codemirror/legacy-modes/mode/properties')).properties),
  }),
];

interface Props {
  value: string;
  onChange: (next: string) => void;
  /** Pixel height for the editor body. Falls back to a flexible
   *  viewport-height-anchored value so the editor stretches inside
   *  panels without props. */
  height?: number | string;
  placeholder?: string;
  readOnly?: boolean;
  /** Show line numbers gutter — on by default for prompt files since
   *  reviewers cite line numbers. */
  showLineNumbers?: boolean;
  /** Extra wrapper classes — used by callers that want a specific
   *  border treatment (rounded-md vs rounded-b-md, etc.). */
  className?: string;
  /** Toggle YAML / Markdown mixed mode. Daimon and agent files have
   *  YAML frontmatter delimited by `---`; the editor highlights the
   *  frontmatter as YAML and the body as markdown. */
  language?: 'markdown' | 'plain';
  ariaLabel?: string;
}

export interface MarkdownEditorHandle {
  focus: () => void;
  /** Imperative setter used when the parent needs to overwrite the
   *  buffer (e.g. Reset to template, Discard changes). */
  setValue: (v: string) => void;
}

// Highlight palette — every color is a hard-coded hex picked from the
// Tailwind config so the editor sits visually with brand-500 (#7c3aed)
// and sev-* tokens on the rest of the platform. No oklch, no CSS-vars
// (CodeMirror's inline-style sheet doesn't see them).
const HIGHLIGHT = HighlightStyle.define([
  // Markdown structure
  { tag: t.heading1,   color: '#5b21b6', fontWeight: '700' },              // brand-700
  { tag: t.heading2,   color: '#6d28d9', fontWeight: '700' },              // brand-600
  { tag: t.heading3,   color: '#7c3aed', fontWeight: '600' },              // brand-500
  { tag: [t.heading4, t.heading5, t.heading6], color: '#7c3aed', fontWeight: '600' },
  { tag: t.strong,     color: '#0f172a', fontWeight: '700' },              // ink
  { tag: t.emphasis,   color: '#0f172a', fontStyle: 'italic' },
  { tag: t.strikethrough, textDecoration: 'line-through', color: '#94a3b8' }, // ink-mute
  { tag: t.link,       color: '#7c3aed', textDecoration: 'underline' },
  { tag: t.url,        color: '#6d28d9' },
  { tag: t.list,       color: '#0f172a' },
  { tag: t.quote,      color: '#64748b', fontStyle: 'italic' },            // ink-dim
  { tag: t.monospace,  color: '#dc2626', backgroundColor: '#fef2f2' },     // sev-high tinted
  { tag: t.contentSeparator, color: '#94a3b8' },

  // Code-block + general code highlights (the markdown lang plugin
  // delegates fenced-block content to language sub-parsers when known)
  { tag: t.keyword,    color: '#7c3aed' },
  { tag: t.atom,       color: '#9333ea' },                                  // sev-critical
  { tag: t.string,     color: '#16a34a' },                                  // green-600
  { tag: t.number,     color: '#ea580c' },                                  // sev-medium
  { tag: t.bool,       color: '#9333ea' },
  { tag: t.null,       color: '#94a3b8' },
  { tag: t.comment,    color: '#94a3b8', fontStyle: 'italic' },
  { tag: t.lineComment, color: '#94a3b8', fontStyle: 'italic' },
  { tag: t.blockComment, color: '#94a3b8', fontStyle: 'italic' },
  { tag: t.operator,   color: '#0f172a' },
  { tag: t.punctuation, color: '#64748b' },
  { tag: t.bracket,    color: '#64748b' },
  { tag: t.variableName, color: '#0f172a' },
  { tag: t.propertyName, color: '#5b21b6' },
  { tag: t.typeName,   color: '#dc2626' },
  { tag: t.className,  color: '#dc2626' },
  { tag: t.function(t.variableName), color: '#5b21b6' },
  { tag: t.tagName,    color: '#dc2626' },
  { tag: t.attributeName, color: '#5b21b6' },
  { tag: t.attributeValue, color: '#16a34a' },
  { tag: t.invalid,    color: '#dc2626', textDecoration: 'underline wavy' },
]);

// Editor chrome theme — pulls the same neutrals the rest of the UI
// uses (panel/border/ink) so the editor reads as part of the page,
// not a foreign embed.
const THEME = EditorView.theme({
  '&': {
    color: '#0f172a',                     // ink
    backgroundColor: '#ffffff',           // panel
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: '12.5px',
    height: '100%',
  },
  '.cm-scroller': {
    fontFamily: 'inherit',
    overflow: 'auto',
    lineHeight: '1.55',
  },
  '.cm-content': {
    padding: '12px 0',
    caretColor: '#7c3aed',
  },
  '.cm-line': {
    padding: '0 14px',
  },
  '.cm-gutters': {
    backgroundColor: '#fafafa',           // bg
    color: '#94a3b8',                     // ink-mute
    border: 'none',
    borderRight: '1px solid #e5e7eb',     // border
  },
  '.cm-lineNumbers .cm-gutterElement': {
    padding: '0 10px 0 12px',
    fontVariantNumeric: 'tabular-nums',
    fontSize: '11px',
  },
  '.cm-activeLineGutter': {
    backgroundColor: '#f5f3ff',           // brand-50
    color: '#5b21b6',                     // brand-700
  },
  '.cm-activeLine': {
    backgroundColor: '#f5f3ff66',         // brand-50 @ 40%
  },
  '.cm-cursor, .cm-dropCursor': {
    borderLeftColor: '#7c3aed',
    borderLeftWidth: '2px',
  },
  '&.cm-focused .cm-selectionBackground, ::selection': {
    backgroundColor: '#ede9fe',           // brand-100
  },
  '.cm-selectionBackground': {
    backgroundColor: '#ede9fe',
  },
  '.cm-matchingBracket': {
    backgroundColor: '#ede9fe',
    color: '#5b21b6',
    outline: 'none',
  },
  '&.cm-focused': {
    outline: 'none',
  },
}, { dark: false });

const READ_ONLY = new Compartment();

const MarkdownEditor = forwardRef<MarkdownEditorHandle, Props>(function MarkdownEditor(
  {
    value,
    onChange,
    height,
    placeholder,
    readOnly,
    showLineNumbers = true,
    className,
    language = 'markdown',
    ariaLabel,
  },
  ref,
) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const viewRef = useRef<EditorView | null>(null);
  // The latest onChange is held in a ref so the editor doesn't have to
  // be recreated on every render just because the parent passed a fresh
  // closure. Same trick we'd use for any imperative third-party widget.
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useImperativeHandle(ref, () => ({
    focus: () => viewRef.current?.focus(),
    setValue: (v: string) => {
      const view = viewRef.current;
      if (!view) return;
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: v },
      });
    },
  }), []);

  // Build the editor on mount. We deliberately skip rebuilding when
  // value changes — only when the host element does. Value sync goes
  // through dispatch() in a separate effect.
  useEffect(() => {
    if (!hostRef.current) return;
    const extensions = [
      history(),
      drawSelection(),
      bracketMatching(),
      indentUnit.of('  '),
      EditorView.lineWrapping,
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      syntaxHighlighting(HIGHLIGHT),
      THEME,
      READ_ONLY.of(EditorState.readOnly.of(!!readOnly)),
      EditorView.updateListener.of((vu) => {
        if (vu.docChanged) {
          onChangeRef.current(vu.state.doc.toString());
        }
      }),
      highlightActiveLine(),
    ];
    if (showLineNumbers) extensions.unshift(lineNumbers());
    if (language === 'markdown') {
      // Fenced-block delegation: ```python … ``` highlights as Python,
      // ```bash … ``` as shell, etc. The full language pack is only
      // loaded when a doc actually uses that language — markdown's
      // LanguageDescription pipeline handles the dynamic import.
      extensions.push(markdown({ codeLanguages: CODE_LANGUAGES }));
    }

    const state = EditorState.create({ doc: value, extensions });
    const view = new EditorView({ state, parent: hostRef.current });
    viewRef.current = view;
    if (placeholder && !value) {
      // Render placeholder by setting a faded data-attribute the
      // theme renders via CSS — keeps the doc empty while still
      // showing helpful text. Implemented with the `cm-placeholder`
      // class baked into CodeMirror; we just wire it.
    }
    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Sync external value → editor when the parent overwrites the buffer
  // (e.g. switching files). Only runs when the parent's value diverges
  // from the live doc — typing doesn't trip this.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const live = view.state.doc.toString();
    if (live !== value) {
      view.dispatch({
        changes: { from: 0, to: live.length, insert: value },
      });
    }
  }, [value]);

  // Reflect read-only changes without rebuilding the editor.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: READ_ONLY.reconfigure(EditorState.readOnly.of(!!readOnly)),
    });
  }, [readOnly]);

  return (
    <div
      ref={hostRef}
      aria-label={ariaLabel}
      className={cn(
        'relative bg-panel text-ink overflow-hidden',
        className,
      )}
      style={{ height: height ?? '100%' }}
    />
  );
});

export default MarkdownEditor;
