import { Cpu, Github, Globe, Info, ServerCog } from 'lucide-react';
import { type AboutInfo } from '../../api';
import { cn } from '../../lib/cn';

export default function AboutSection({ about }: { about: AboutInfo | null }) {
  return (
    <div className="p-6 max-w-3xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold">About</h2>
        <p className="text-xs text-ink-dim mt-0.5">Build info and capabilities of this Control Plane.</p>
      </header>

      {about === null ? (
        <p className="text-ink-mute">Loading…</p>
      ) : (
        <>
          <Card>
            <Row label="Version" mono>{about.version}</Row>
            <Row label="Go" mono>{about.go_version}</Row>
            <Row label="OS / Arch" mono>{about.os}/{about.arch}</Row>
          </Card>

          <Card title="Features" icon={Cpu}>
            <FeatureRow on={about.features.mgmt_plane} label="Management plane" hint="Daemons can register, heartbeat, and pull config" />
            <FeatureRow on={about.features.tunnel}     label="Reverse tunnel" hint="Ad-hoc agent runs against connected nodes" />
            <FeatureRow on={about.features.deploy}     label="Node SSH deploy" hint="Bootstrap daemons via SSH push" />
            <FeatureRow on={about.features.webhook_ingest} label="Webhook ingest" hint="Daemons post JSONL events to /api/webhooks/events" />
            <FeatureRow on={about.features.oidc}       label="OIDC sign-in" hint="Operators can log in via Oracle Identity Domains or other OIDC IdPs" />
          </Card>
        </>
      )}

      <Card title="Resources" icon={Globe}>
        <ul className="space-y-1.5 text-sm">
          <li>
            <a href="https://github.com/section9labs/okesu" target="_blank" rel="noreferrer"
              className="inline-flex items-center gap-1.5 text-ink-dim hover:text-ink">
              <Github size={12} /> Source on GitHub
            </a>
          </li>
          <li>
            <a href="/" className="inline-flex items-center gap-1.5 text-ink-dim hover:text-ink">
              <ServerCog size={12} /> /api/system/about
            </a>
          </li>
          <li>
            <a href="https://github.com/section9labs/okesu/blob/main/INSTALL.md" target="_blank" rel="noreferrer"
              className="inline-flex items-center gap-1.5 text-ink-dim hover:text-ink">
              <Info size={12} /> INSTALL.md
            </a>
          </li>
        </ul>
      </Card>
    </div>
  );
}

function FeatureRow({ on, label, hint }: { on: boolean; label: string; hint: string }) {
  return (
    <div className="flex items-start gap-3 text-sm py-1.5 first:pt-0 last:pb-0">
      <span className={cn(
        'mt-0.5 inline-flex items-center justify-center w-4 h-4 rounded-full text-[10px] font-bold',
        on ? 'bg-green-100 text-green-700' : 'bg-slate-100 text-ink-mute',
      )}>
        {on ? '✓' : '·'}
      </span>
      <div className="min-w-0">
        <div className={on ? 'text-ink' : 'text-ink-mute'}>{label}</div>
        <div className="text-[11px] text-ink-mute">{hint}</div>
      </div>
    </div>
  );
}

function Card({ title, icon: Icon, children }: {
  title?: string; icon?: typeof Cpu; children: React.ReactNode;
}) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card">
      {title && (
        <header className="px-5 pt-4 pb-2 border-b border-border flex items-center gap-2">
          {Icon && <Icon size={14} className="text-ink-dim" />}
          <h3 className="text-sm font-semibold">{title}</h3>
        </header>
      )}
      <div className="p-5">{children}</div>
    </section>
  );
}

function Row({ label, children, mono }: { label: string; children: React.ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[120px_1fr] items-center text-sm py-1 first:pt-0 last:pb-0">
      <dt className="text-ink-dim">{label}</dt>
      <dd className={cn(mono && 'font-mono text-xs')}>{children}</dd>
    </div>
  );
}
