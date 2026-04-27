// Agents page — Library/Runs tab container. Mirrors Daimons's
// Deployed/Library split: Library shows the .md definitions; Runs
// shows in-flight + recent invocations.
//
// Tab state syncs to ?tab= so /agents?tab=runs is a stable bookmark
// and /runs (legacy URL) redirects here with the right tab selected.

import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { FileEdit, Terminal } from 'lucide-react';
import { cn } from '../lib/cn';
import AgentsLibrary from '../components/AgentsLibrary';
import RunsPage from './Runs';

type Tab = 'library' | 'runs';

function parseTab(s: string | null): Tab {
  return s === 'runs' ? 'runs' : 'library';
}

export default function AgentsPage() {
  const [params, setParams] = useSearchParams();
  const tab = parseTab(params.get('tab'));
  const [, setLocalTab] = useState<Tab>(tab); // tracked for tab-click feedback before URL writes

  function selectTab(next: Tab) {
    setLocalTab(next);
    const p = new URLSearchParams(params);
    if (next === 'library') p.delete('tab'); else p.set('tab', next);
    setParams(p, { replace: true });
  }

  return (
    <div className="h-full flex flex-col">
      <nav className="px-6 pt-3 border-b border-border bg-panel flex gap-1">
        {([
          ['library', 'Library', FileEdit],
          ['runs',    'Runs',    Terminal],
        ] as const).map(([key, label, Icon]) => (
          <button
            key={key}
            onClick={() => selectTab(key)}
            className={cn(
              'inline-flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 -mb-px',
              tab === key
                ? 'border-brand-500 text-brand-700 font-medium'
                : 'border-transparent text-ink-dim hover:text-ink',
            )}
          >
            <Icon size={14} />
            {label}
          </button>
        ))}
      </nav>
      <main className="flex-1 overflow-hidden">
        {tab === 'library' && <AgentsLibrary />}
        {tab === 'runs' && <RunsPage />}
      </main>
    </div>
  );
}
