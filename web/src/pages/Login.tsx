import { FormEvent, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api, ApiError, type AuthConfig, type User } from '../api';

interface Props {
  onLogin: (user: User) => void;
}

export default function LoginPage({ onLogin }: Props) {
  const navigate = useNavigate();
  const [email, setEmail] = useState('admin@local');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null);

  useEffect(() => {
    api.authConfig().then(setAuthConfig).catch(() => setAuthConfig({ oidc_enabled: false }));
  }, []);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const user = await api.login(email, password);
      onLogin(user);
      navigate('/', { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setError('Invalid email or password.');
      } else {
        setError('Something went wrong. Please try again.');
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-gradient-to-br from-brand-50 via-bg to-bg">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-brand-500 mb-4 shadow-card">
            <div className="w-5 h-5 rounded-full bg-white"/>
          </div>
          <h1 className="text-xl font-semibold">Okesu Control Plane</h1>
          <p className="text-sm text-ink-dim mt-1">Sign in to continue</p>
        </div>

        <div className="bg-panel border border-border rounded-xl p-6 shadow-card space-y-4">
          {authConfig?.oidc_enabled && (
            <>
              <a
                href="/auth/oidc/login"
                className="w-full inline-flex items-center justify-center gap-2 bg-ink hover:bg-ink/90 text-white text-sm font-medium py-2 rounded-md transition-colors"
              >
                {authConfig.oidc_label || 'Sign in with SSO'}
              </a>
              <div className="flex items-center gap-3 text-xs text-ink-mute">
                <div className="flex-1 h-px bg-border"/>
                <span>or</span>
                <div className="flex-1 h-px bg-border"/>
              </div>
            </>
          )}

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-ink-dim mb-1.5">Email</label>
              <input
                type="email"
                autoComplete="username"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                className="w-full px-3 py-2 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 focus:border-brand-500"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-ink-dim mb-1.5">Password</label>
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                className="w-full px-3 py-2 text-sm border border-border rounded-md focus:outline-none focus:ring-2 focus:ring-brand-500/30 focus:border-brand-500"
              />
            </div>

            {error && (
              <div className="text-xs text-red-600 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="w-full bg-brand-500 hover:bg-brand-600 disabled:bg-brand-500/60 text-white text-sm font-medium py-2 rounded-md transition-colors"
            >
              {submitting ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
