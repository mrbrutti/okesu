// Lazy wrapper around MarkdownEditor. CodeMirror + markdown/yaml
// grammars add ~180 KB gzipped — we don't want to ship that on the
// initial dashboard load, since most operators never edit a daimon
// or write a Run prompt in a session. The first time an editor mounts
// we pay the lazy-import cost; after that it's cached for the session.

import { lazy, Suspense, forwardRef } from 'react';
import { Loader2 } from 'lucide-react';
import { cn } from '../lib/cn';
import type { MarkdownEditorHandle } from './MarkdownEditor';

const MarkdownEditor = lazy(() => import('./MarkdownEditor'));

interface Props {
  value: string;
  onChange: (next: string) => void;
  height?: number | string;
  placeholder?: string;
  readOnly?: boolean;
  showLineNumbers?: boolean;
  className?: string;
  ariaLabel?: string;
}

const LazyMarkdownEditor = forwardRef<MarkdownEditorHandle, Props>(function LazyMarkdownEditor(props, ref) {
  return (
    <Suspense fallback={<EditorSkeleton height={props.height} />}>
      <MarkdownEditor {...props} ref={ref} />
    </Suspense>
  );
});

function EditorSkeleton({ height }: { height?: number | string }) {
  return (
    <div
      className={cn(
        'flex items-center justify-center bg-bg text-ink-mute text-xs gap-2',
      )}
      style={{ height: height ?? '100%', minHeight: typeof height === 'number' ? height : 180 }}
    >
      <Loader2 size={14} className="animate-spin" />
      Loading editor…
    </div>
  );
}

export default LazyMarkdownEditor;
