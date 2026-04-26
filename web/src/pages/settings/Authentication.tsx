import { Lock, ShieldCheck } from 'lucide-react';
import { type AboutInfo } from '../../api';
import { cn } from '../../lib/cn';

export default function AuthenticationSection({ about }: { about: AboutInfo | null }) {
  return (
    <div className="p-6 max-w-3xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <ShieldCheck size={18} className="text-brand-500" />
          Authentication
        </h2>
        <p className="text-xs text-ink-dim mt-0.5">
          How operators sign in. OIDC settings are configured at startup; this view is read-only.
        </p>
      </header>

      <Card title="Local accounts" subtitle="Email + bcrypt password, manageable in the Users section.">
        <Status enabled={true} label="Enabled — primary login" />
      </Card>

      <Card title="OIDC / SSO" subtitle="Single sign-on via an OpenID Connect issuer (e.g. Oracle Identity Domains).">
        <Status enabled={about?.features.oidc ?? false} label={about?.features.oidc ? 'Enabled' : 'Disabled'} />
        {!about?.features.oidc && (
          <p className="text-xs text-ink-mute mt-3">
            Configure with <code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--oidc-issuer</code>,
            {' '}<code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--oidc-client-id</code>,
            {' '}<code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--oidc-client-secret</code>,
            {' '}<code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--oidc-redirect-url</code>,
            {' '}and <code className="font-mono bg-slate-100 px-1 py-0.5 rounded">--oidc-role-map</code> (or the
            equivalent <code className="font-mono bg-slate-100 px-1 py-0.5 rounded">OKESU_CP_OIDC_*</code> env vars).
            See INSTALL.md §8b.
          </p>
        )}
      </Card>

      <Card title="Roles" subtitle="Hierarchy: admin &gt; operator &gt; viewer. Higher roles inherit lower permissions.">
        <ul className="text-sm space-y-1.5">
          <RoleRow role="viewer"   desc="Read-only access. Sees findings, agents, nodes, events." />
          <RoleRow role="operator" desc="Plus: acknowledge findings, push agent config, register/deploy nodes, run ad-hoc agents." />
          <RoleRow role="admin"    desc="Plus: user management, audit log, future system settings." />
        </ul>
      </Card>
    </div>
  );
}

function Card({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="bg-panel border border-border rounded-xl shadow-card p-5">
      <h3 className="text-sm font-semibold">{title}</h3>
      {subtitle && <p className="text-xs text-ink-dim mt-0.5 mb-3">{subtitle}</p>}
      {children}
    </section>
  );
}

function Status({ enabled, label }: { enabled: boolean; label: string }) {
  return (
    <div className={cn(
      'inline-flex items-center gap-1.5 text-xs font-medium px-2 py-1 rounded-md ring-1',
      enabled ? 'bg-green-50 text-green-700 ring-green-200' : 'bg-slate-50 text-ink-mute ring-slate-200',
    )}>
      <Lock size={11} />
      {label}
    </div>
  );
}

function RoleRow({ role, desc }: { role: string; desc: string }) {
  const cls = {
    admin:    'bg-purple-50 text-purple-700 ring-purple-200',
    operator: 'bg-brand-50 text-brand-700 ring-brand-100',
    viewer:   'bg-slate-50 text-slate-700 ring-slate-200',
  }[role] || 'bg-slate-50 text-slate-700 ring-slate-200';
  return (
    <li className="flex items-start gap-3">
      <span className={cn('text-[10px] uppercase tracking-wide font-medium px-1.5 py-0.5 rounded ring-1 shrink-0 mt-0.5', cls)}>
        {role}
      </span>
      <span className="text-ink-dim">{desc}</span>
    </li>
  );
}
