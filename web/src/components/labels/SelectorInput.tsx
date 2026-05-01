// SelectorInput: filter pill bar that types like a Kubernetes label
// selector and feels like a search input. Used on Findings/Nodes/
// Daimons/etc. list pages and bound to a `?selector=` query param.
//
// Grammar (mirrors controlplane/db/selector.go):
//   selector := req (',' req)*
//   req := key '=' value | key '!=' value | key | '!' key
//
// We don't validate strictly here — invalid selectors round-trip to
// the server, which returns a 400 the caller can surface. Keeping
// the validator out lets operators paste a half-typed selector
// without the input shouting.

import { useEffect, useRef, useState } from 'react';
import { X, Filter } from 'lucide-react';
import type { LabelKind } from '../../api';
import { api } from '../../api';

interface Props {
  kind: LabelKind;
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  className?: string;
}

interface Suggestion {
  text: string;
  kind: 'key' | 'value';
}

export function SelectorInput({ kind, value, onChange, placeholder, className = '' }: Props) {
  const [input, setInput] = useState(value);
  const [open, setOpen] = useState(false);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // Sync external value changes (e.g. ?selector= URL param) into the
  // local input.
  useEffect(() => { setInput(value); }, [value]);

  // Pull autocomplete entries based on the current cursor position.
  // Simple heuristic: if the last comma-separated segment ends in
  // "=" we suggest values for that key; otherwise we suggest keys.
  useEffect(() => {
    let cancelled = false;
    const segs = input.split(',');
    const last = (segs[segs.length - 1] || '').trim();
    const eqIdx = last.indexOf('=');
    if (eqIdx >= 0) {
      const key = last.slice(0, eqIdx).trim().replace(/^!/, '');
      if (!key) {
        setSuggestions([]);
        return;
      }
      api.labelValues(kind, key)
        .then((vs) => { if (!cancelled) setSuggestions(vs.map((v) => ({ text: v, kind: 'value' as const }))); })
        .catch(() => { if (!cancelled) setSuggestions([]); });
    } else {
      api.labelKeys(kind)
        .then((ks) => { if (!cancelled) setSuggestions(ks.map((k) => ({ text: k, kind: 'key' as const }))); })
        .catch(() => { if (!cancelled) setSuggestions([]); });
    }
    return () => { cancelled = true; };
  }, [input, kind]);

  // Click outside closes the dropdown.
  useEffect(() => {
    function onDocClick(e: MouseEvent) {
      if (!containerRef.current) return;
      if (!containerRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, []);

  function commit(next: string) {
    setInput(next);
    onChange(next);
  }

  function applySuggestion(s: Suggestion) {
    const segs = input.split(',');
    const last = (segs[segs.length - 1] || '').trim();
    if (s.kind === 'key') {
      // Replace the trailing partial key.
      segs[segs.length - 1] = s.text + '=';
    } else {
      // Append after `key=`.
      const eqIdx = last.indexOf('=');
      if (eqIdx >= 0) {
        segs[segs.length - 1] = last.slice(0, eqIdx + 1) + s.text;
      }
    }
    const next = segs.join(',');
    setInput(next);
    onChange(next);
    inputRef.current?.focus();
  }

  return (
    <div ref={containerRef} className={`relative ${className}`}>
      <div className="flex items-center gap-1 px-2 py-1 rounded-md border border-border bg-panel focus-within:ring-2 focus-within:ring-brand-300">
        <Filter size={12} className="text-ink-mute flex-shrink-0" />
        <input
          ref={inputRef}
          type="text"
          value={input}
          placeholder={placeholder ?? `selector — e.g. env=prod, role=db`}
          onChange={(e) => setInput(e.target.value)}
          onFocus={() => setOpen(true)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              commit(input);
              setOpen(false);
            }
            if (e.key === 'Escape') {
              setOpen(false);
            }
          }}
          className="flex-1 text-xs font-mono outline-none bg-transparent min-w-[120px]"
        />
        {input && (
          <button
            type="button"
            onClick={() => commit('')}
            className="text-ink-mute hover:text-red-600"
            title="Clear selector"
          >
            <X size={12} />
          </button>
        )}
      </div>
      {open && suggestions.length > 0 && (
        <div className="absolute z-20 mt-1 w-full max-h-56 overflow-auto bg-panel border border-border rounded-md shadow-card">
          {suggestions.slice(0, 30).map((s, i) => (
            <button
              key={`${s.kind}:${s.text}:${i}`}
              type="button"
              onClick={() => applySuggestion(s)}
              className="w-full text-left text-xs font-mono px-2 py-1 hover:bg-brand-50 flex items-center gap-2"
            >
              <span className="text-ink-mute text-[10px] uppercase tracking-wide w-9">{s.kind}</span>
              {s.text}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
