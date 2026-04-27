import { useEffect, useState } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { api, ApiError, type User } from './api';
import LoginPage from './pages/Login';
import EventsPage from './pages/Events';
import DaimonsPage from './pages/Daimons';
import DaimonDetailPage from './pages/DaimonDetail';
import AgentsPage from './pages/Agents';
import DashboardPage from './pages/Dashboard';
import FindingsPage from './pages/Findings';
import NodesPage from './pages/Nodes';
import NodeDetailPage from './pages/NodeDetail';
import RunsPage from './pages/Runs';
import SettingsPage from './pages/Settings';
import Layout from './components/Layout';

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined);

  useEffect(() => {
    api.me()
      .then(setUser)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) {
          setUser(null);
        } else {
          setUser(null);
        }
      });
  }, []);

  if (user === undefined) {
    return (
      <div className="flex h-screen items-center justify-center text-ink-dim">
        Loading…
      </div>
    );
  }

  return (
    <Routes>
      <Route
        path="/login"
        element={user ? <Navigate to="/" replace /> : <LoginPage onLogin={setUser} />}
      />
      <Route
        element={user ? <Layout user={user} onLogout={() => setUser(null)} /> : <Navigate to="/login" replace />}
      >
        <Route index element={<DashboardPage />} />
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/findings" element={<FindingsPage />} />
        <Route path="/events" element={<EventsPage />} />
        <Route path="/daimons" element={<DaimonsPage />} />
        <Route path="/daimons/:name" element={<DaimonDetailPage />} />
        <Route path="/agents" element={<AgentsPage />} />
        <Route path="/nodes" element={<NodesPage />} />
        <Route path="/nodes/:id" element={<NodeDetailPage />} />
        <Route path="/runs" element={<RunsPage />} />
        <Route path="/settings/*" element={<SettingsPage user={user!} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
