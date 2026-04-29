// Agents page — Library/Runs tab container. Mirrors Daimons's
// Deployed/Library split: Library shows the .md definitions; Runs
// shows in-flight + recent invocations.
//
// Tab state syncs to ?tab= so /agents?tab=runs is a stable bookmark
// and /runs (legacy URL) redirects here with the right tab selected.

import { useSearchParams } from 'react-router-dom';
import { FileEdit, Terminal, Zap } from 'lucide-react';
import AgentsLibrary from '../components/AgentsLibrary';
import RunsPage from './Runs';
import { TabBar, TabButton } from '../components/TabBar';

type Tab = 'library' | 'runs';

function parseTab(s: string | null): Tab {
  return s === 'runs' ? 'runs' : 'library';
}

export default function AgentsPage() {
  const [params, setParams] = useSearchParams();
  const tab = parseTab(params.get('tab'));

  function selectTab(next: Tab) {
    const p = new URLSearchParams(params);
    if (next === 'library') p.delete('tab'); else p.set('tab', next);
    setParams(p, { replace: true });
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
            <Zap size={14} />
          </span>
          Agents
        </h1>
        <p className="text-xs text-ink-dim mt-0.5 ml-9">
          On-demand agents you can run from the CLI or REST. Library is the catalog of definitions; Runs is the invocation history.
        </p>
      </header>

      <TabBar>
        <TabButton active={tab === 'library'} onClick={() => selectTab('library')} icon={FileEdit} label="Library" />
        <TabButton active={tab === 'runs'}    onClick={() => selectTab('runs')}    icon={Terminal} label="Runs" />
      </TabBar>

      <main className="flex-1 overflow-hidden">
        {tab === 'library' && <AgentsLibrary />}
        {tab === 'runs' && <RunsPage />}
      </main>
    </div>
  );
}
