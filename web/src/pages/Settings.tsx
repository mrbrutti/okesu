import { Outlet, NavLink, Navigate, Routes, Route } from 'react-router-dom';
import {
  Bell,
  Brain,
  ClipboardList,
  Cloud,
  Database,
  Eye,
  HardDrive,
  Info,
  KeyRound,
  Plug,
  ShieldCheck,
  Sparkles,
  User as UserIcon,
  Users as UsersIcon,
} from 'lucide-react';
import { cn } from '../lib/cn';
import { type User } from '../api';
import { useEffect, useState } from 'react';
import { api, type AboutInfo } from '../api';

import ProfileSection from './settings/Profile';
import UsersSection from './settings/Users';
import AuditLogSection from './settings/AuditLog';
import AuthenticationSection from './settings/Authentication';
import AboutSection from './settings/About';
import NotificationsSection from './settings/Notifications';
import IntegrationsSection from './settings/Integrations';
import DatabaseSection from './settings/Database';
import DeploySection from './settings/Deploy';
import DisplaySection from './settings/Display';
import CloudSection from './settings/Cloud';
import LLMKeysSection from './settings/LLMKeys';
import InvestigationsSection from './settings/Investigations';
import GroupsSection from './settings/Groups';
import CredentialsSection from './settings/Credentials';

interface Props {
  user: User;
}

interface NavItem {
  to: string;
  label: string;
  icon: typeof Info;
  adminOnly?: boolean;
}

const NAV: NavItem[] = [
  { to: 'profile',         label: 'Profile',        icon: UserIcon },
  { to: 'display',         label: 'Display',        icon: Eye },
  { to: 'users',           label: 'Users',          icon: UsersIcon,    adminOnly: true },
  { to: 'groups',          label: 'Groups',         icon: UsersIcon,    adminOnly: true },
  { to: 'credentials',     label: 'Credentials',    icon: KeyRound,     adminOnly: true },
  { to: 'investigations',  label: 'Investigations', icon: Sparkles,     adminOnly: true },
  { to: 'notifications',   label: 'Notifications',  icon: Bell,         adminOnly: true },
  { to: 'integrations',    label: 'Integrations',   icon: Plug,         adminOnly: true },
  { to: 'audit',           label: 'Audit log',      icon: ClipboardList, adminOnly: true },
  { to: 'authentication',  label: 'Authentication', icon: ShieldCheck,  adminOnly: true },
  { to: 'deploy',          label: 'Deploy',         icon: HardDrive,    adminOnly: true },
  { to: 'cloud',           label: 'Cloud',          icon: Cloud,        adminOnly: true },
  { to: 'llm-keys',        label: 'LLM keys',       icon: Brain,        adminOnly: true },
  { to: 'database',        label: 'Database',       icon: Database,     adminOnly: true },
  { to: 'about',           label: 'About',          icon: Info },
];

export default function SettingsPage({ user }: Props) {
  const [about, setAbout] = useState<AboutInfo | null>(null);
  useEffect(() => {
    api.about().then(setAbout).catch(() => { /* ignore */ });
  }, []);

  const visible = NAV.filter((n) => !n.adminOnly || user.role === 'admin');

  return (
    <div className="h-full flex">
      <aside className="w-56 shrink-0 border-r border-border bg-panel">
        <header className="px-5 py-4 border-b border-border">
          <h1 className="text-sm font-semibold flex items-center gap-2">
            <KeyRound size={14} className="text-brand-500" />
            Settings
          </h1>
        </header>
        <nav className="p-2">
          {visible.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors',
                  isActive
                    ? 'bg-brand-50 text-brand-700 font-medium'
                    : 'text-ink hover:bg-slate-100',
                )
              }
            >
              <item.icon size={14} />
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>

      <main className="flex-1 overflow-auto">
        <Routes>
          <Route index element={<Navigate to="profile" replace />} />
          <Route path="profile"        element={<ProfileSection user={user} />} />
          <Route path="display"        element={<DisplaySection />} />
          <Route path="users"          element={user.role === 'admin' ? <UsersSection /> : <Forbidden />} />
          <Route path="groups"         element={user.role === 'admin' ? <GroupsSection /> : <Forbidden />} />
          <Route path="credentials"    element={user.role === 'admin' ? <CredentialsSection /> : <Forbidden />} />
          <Route path="investigations" element={user.role === 'admin' ? <InvestigationsSection /> : <Forbidden />} />
          <Route path="notifications"  element={user.role === 'admin' ? <NotificationsSection /> : <Forbidden />} />
          <Route path="integrations"   element={user.role === 'admin' ? <IntegrationsSection /> : <Forbidden />} />
          <Route path="audit"          element={user.role === 'admin' ? <AuditLogSection /> : <Forbidden />} />
          <Route path="authentication" element={user.role === 'admin' ? <AuthenticationSection about={about} /> : <Forbidden />} />
          <Route path="deploy"         element={user.role === 'admin' ? <DeploySection /> : <Forbidden />} />
          <Route path="cloud"          element={user.role === 'admin' ? <CloudSection /> : <Forbidden />} />
          <Route path="llm-keys"       element={user.role === 'admin' ? <LLMKeysSection /> : <Forbidden />} />
          <Route path="database"       element={user.role === 'admin' ? <DatabaseSection /> : <Forbidden />} />
          <Route path="about"          element={<AboutSection about={about} />} />
          <Route path="*"              element={<Navigate to="profile" replace />} />
        </Routes>
        <Outlet />
      </main>
    </div>
  );
}

function Forbidden() {
  return (
    <div className="p-6">
      <div className="bg-red-50 border border-red-200 text-red-700 text-sm px-4 py-3 rounded-md">
        This section requires the <strong>admin</strong> role.
      </div>
    </div>
  );
}
