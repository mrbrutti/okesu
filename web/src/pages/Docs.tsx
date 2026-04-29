// Documentation page — three-tab reference for authoring Agents,
// Daimons, and Orchestrations. Content is curated long-form
// markdown rendered alongside the file format examples already
// shipped in /examples.
//
// We deliberately keep this content in the React bundle (not a
// separate /docs HTTP route) so it's gated by the same auth as the
// rest of the CP and stays in lock-step with the spec versions the
// CP supports.

import { useSearchParams } from 'react-router-dom';
import { BookOpen, Cpu, Sparkles, Workflow } from 'lucide-react';
import { TabBar, TabButton } from '../components/TabBar';
import { DocsAgents } from '../components/docs/DocsAgents';
import { DocsDaimons } from '../components/docs/DocsDaimons';
import { DocsOrchestrations } from '../components/docs/DocsOrchestrations';

type Tab = 'orchestrations' | 'daimons' | 'agents';

function parseTab(s: string | null): Tab {
  if (s === 'daimons' || s === 'agents') return s;
  return 'orchestrations';
}

export default function DocsPage() {
  const [params, setParams] = useSearchParams();
  const tab = parseTab(params.get('tab'));

  function selectTab(next: Tab) {
    const p = new URLSearchParams(params);
    if (next === 'orchestrations') p.delete('tab'); else p.set('tab', next);
    setParams(p, { replace: true });
  }

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-border bg-gradient-to-r from-brand-50/60 via-panel to-panel">
        <h1 className="text-lg font-semibold flex items-center gap-2">
          <span className="inline-flex items-center justify-center w-7 h-7 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 text-white shadow-sm">
            <BookOpen size={14} />
          </span>
          Documentation
        </h1>
        <p className="text-xs text-ink-dim mt-0.5 ml-9">
          Author guide for the three artefact types — Agents, Daimons, and Orchestrations. Each tab is
          a self-contained reference with live spec details from this CP.
        </p>
      </header>

      <TabBar>
        <TabButton active={tab === 'orchestrations'} onClick={() => selectTab('orchestrations')} icon={Workflow}  label="Orchestrations" />
        <TabButton active={tab === 'daimons'}        onClick={() => selectTab('daimons')}        icon={Cpu}       label="Daimons" />
        <TabButton active={tab === 'agents'}         onClick={() => selectTab('agents')}         icon={Sparkles}  label="Agents" />
      </TabBar>

      <main className="flex-1 overflow-auto">
        {tab === 'orchestrations' && <DocsOrchestrations />}
        {tab === 'daimons'        && <DocsDaimons />}
        {tab === 'agents'         && <DocsAgents />}
      </main>
    </div>
  );
}
