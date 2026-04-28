import { Link, NavLink, Outlet, useNavigate } from 'react-router-dom';
import { Activity, AlertTriangle, LayoutDashboard, Network, Server, Layers, Settings, LogOut, Sparkles } from 'lucide-react';
import { api, type User } from '../api';
import { cn } from '../lib/cn';
import CommandPalette from './CommandPalette';

interface Props {
  user: User;
  onLogout: () => void;
}

const nav = [
  { to: '/dashboard',  label: 'Dashboard',    icon: LayoutDashboard, enabled: true  },
  { to: '/findings',   label: 'Findings',     icon: AlertTriangle,  enabled: true  },
  { to: '/events',     label: 'Live Events',  icon: Activity,       enabled: true  },
  { to: '/daimons',    label: 'Daimons',      icon: Layers,         enabled: true  },
  { to: '/agents',     label: 'Agents',       icon: Sparkles,       enabled: true  },
  { to: '/nodes',      label: 'Nodes',        icon: Server,         enabled: true  },
  { to: '/federation', label: 'Federation',   icon: Network,        enabled: true  },
  { to: '/settings',   label: 'Settings',     icon: Settings,       enabled: true  },
];

export default function Layout({ user, onLogout }: Props) {
  const navigate = useNavigate();

  async function handleLogout() {
    try { await api.logout(); } catch { /* ignore */ }
    onLogout();
    navigate('/login', { replace: true });
  }

  return (
    <div className="flex h-screen overflow-hidden">
      <aside className="w-60 shrink-0 border-r border-border bg-panel flex flex-col">
        <Link to="/" className="px-5 py-5 flex items-center gap-2 border-b border-border">
          <div className="w-7 h-7 rounded-md bg-brand-500 flex items-center justify-center">
            <div className="w-3 h-3 rounded-full bg-white"/>
          </div>
          <div>
            <div className="font-semibold text-sm leading-tight">Okesu</div>
            <div className="text-[11px] text-ink-mute leading-tight">Control Plane</div>
          </div>
        </Link>

        <nav className="flex-1 py-3 px-2 space-y-0.5">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/dashboard' || item.to === '/findings'}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2.5 px-3 py-2 rounded-md text-sm transition-colors',
                  item.enabled
                    ? isActive
                      ? 'bg-brand-50 text-brand-700 font-medium'
                      : 'text-ink hover:bg-slate-100'
                    : 'text-ink-mute cursor-not-allowed'
                )
              }
              onClick={(e) => { if (!item.enabled) e.preventDefault(); }}
            >
              <item.icon size={16} strokeWidth={2} />
              <span>{item.label}</span>
              {!item.enabled && (
                <span className="ml-auto text-[10px] uppercase tracking-wide text-ink-mute">soon</span>
              )}
            </NavLink>
          ))}
        </nav>

        <div className="p-3 border-t border-border">
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <div className="text-xs font-medium text-ink truncate">{user.email}</div>
              <div className="text-[11px] text-ink-mute capitalize">{user.role}</div>
            </div>
            <button
              onClick={handleLogout}
              className="p-1.5 text-ink-dim hover:text-ink hover:bg-slate-100 rounded-md"
              title="Sign out"
            >
              <LogOut size={15} />
            </button>
          </div>
        </div>
      </aside>

      <main className="flex-1 overflow-auto">
        <Outlet />
      </main>

      {/* Cmd-K / Ctrl-K palette. Mounted once at the layout level so the
       *  global hotkey is live on every authenticated route. */}
      <CommandPalette />
    </div>
  );
}
