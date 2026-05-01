// Settings → Feeds — admin view for managing IOC feeds.
//
// Three sections, top to bottom:
//   1. First-boot consent banner. Shown only when (a) at least one
//      feed exists AND (b) none have ever refreshed (every
//      LastRefreshAt is .Valid=false). Clicking "Allow" calls
//      POST /api/feeds/consent which sets the cp_meta consent flag
//      and flips every InstalledFromRegistry feed to Enabled=true.
//   2. Installed feeds table. Per row: name, kind, parser, last
//      refresh status + relative time + truncated error if any,
//      entry count, "Refresh now" + "Uninstall" buttons.
//      Federated rows are visually muted with a "from parent" chip;
//      override-locally is a future story (currently an inert badge).
//   3. Browse well-known feeds. Cards for each registry entry
//      (Slug/Name/Description/License). Already-installed entries
//      show "Installed" instead of an Install button.
//
// The custom-add form is deferred to a backlog issue — operators
// who need a custom feed can POST /api/feeds directly until then.

import { useEffect, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  RefreshCw,
  Rss,
  Trash2,
} from 'lucide-react';
import {
  api,
  type FeedConfig,
  type FeedRegistryDef,
} from '../../api';
import { cn } from '../../lib/cn';

export default function FeedsSection() {
  const [feeds, setFeeds] = useState<FeedConfig[] | null>(null);
  const [registry, setRegistry] = useState<FeedRegistryDef[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  async function load() {
    setError(null);
    try {
      const [f, r] = await Promise.all([api.feeds(), api.feedsRegistry()]);
      setFeeds(f);
      setRegistry(r);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function run<T>(label: string, fn: () => Promise<T>): Promise<void> {
    setBusy(label);
    setError(null);
    try {
      await fn();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  if (!feeds || !registry) {
    return (
      <div className="p-6 text-ink-dim text-sm flex items-center gap-2">
        <Loader2 size={14} className="animate-spin" /> Loading feeds…
      </div>
    );
  }

  // Consent banner condition: at least one feed exists, none have refreshed yet.
  const consentNeeded =
    feeds.length > 0 && feeds.every((f) => !f.LastRefreshAt.Valid);

  return (
    <div className="p-6 max-w-5xl space-y-6">
      <header>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <Rss size={16} className="text-brand-500" /> Feeds
        </h2>
        <p className="text-xs text-ink-dim mt-1">
          Threat-intel feeds (YARA, Sigma, IOC lists) the CP refreshes on a
          schedule. Default-installed feeds wait for consent before pulling
          from the public internet.
        </p>
      </header>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 text-sm px-4 py-3 rounded-md">
          {error}
        </div>
      )}

      {consentNeeded && (
        <div className="rounded-md border border-amber-200 bg-amber-50 px-4 py-3">
          <div className="font-medium text-amber-900 mb-1 flex items-center gap-2">
            <AlertTriangle size={14} /> Allow scheduled feed refreshes?
          </div>
          <p className="text-amber-800 text-xs mb-3">
            Threat-intel feeds pull from public sources on the internet. Click
            "Allow" once to enable scheduled refreshes; airgapped deployments
            should leave this off.
          </p>
          <button
            disabled={busy !== null}
            onClick={() => run('consent', () => api.feedsConsent())}
            className="px-3 py-1 rounded bg-amber-600 text-white text-xs hover:bg-amber-700 disabled:opacity-50"
          >
            {busy === 'consent' ? 'Allowing…' : 'Allow'}
          </button>
        </div>
      )}

      <section>
        <h3 className="text-sm font-medium mb-2">Installed</h3>
        {feeds.length === 0 ? (
          <div className="text-xs text-ink-dim">
            No feeds installed yet — pick from the registry below or use the
            API to add a custom feed.
          </div>
        ) : (
          <div className="border border-border rounded-md overflow-hidden bg-white">
            <table className="w-full text-sm">
              <thead className="bg-slate-50 text-[11px] uppercase tracking-wide text-ink-mute">
                <tr>
                  <th className="px-3 py-2 text-left">Name</th>
                  <th className="px-3 py-2 text-left">Kind</th>
                  <th className="px-3 py-2 text-left">Parser</th>
                  <th className="px-3 py-2 text-left">Last refresh</th>
                  <th className="px-3 py-2 text-right">Entries</th>
                  <th className="px-3 py-2 text-right w-20"></th>
                </tr>
              </thead>
              <tbody>
                {feeds.map((f) => (
                  <FeedRow
                    key={f.ID}
                    feed={f}
                    busy={busy}
                    onRefresh={() => run('refresh-' + f.ID, () => api.feedRefresh(f.ID))}
                    onUninstall={() => {
                      if (
                        confirm(
                          `Uninstall ${f.Name}? Rows it produced will be removed; observation history is preserved.`,
                        )
                      ) {
                        run('uninstall-' + f.ID, () => api.feedUninstall(f.ID));
                      }
                    }}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section>
        <h3 className="text-sm font-medium mb-2">Browse well-known feeds</h3>
        <div className="grid sm:grid-cols-2 gap-3">
          {registry.map((d) => {
            const installed = feeds.some((f) => f.Slug === d.Slug);
            return (
              <div
                key={d.Slug}
                className="border border-border rounded-md p-3 bg-white"
              >
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <div className="font-medium text-sm">{d.Name}</div>
                    <div className="text-[11px] text-ink-mute font-mono">
                      {d.Slug}
                    </div>
                  </div>
                  <div className="flex gap-1 flex-shrink-0">
                    <Chip>{d.Kind}</Chip>
                    <Chip>{d.Parser}</Chip>
                  </div>
                </div>
                <p className="text-xs text-ink-dim mt-2">{d.Description}</p>
                <div className="text-[10px] text-ink-mute mt-1">
                  License: {d.License}
                </div>
                <div className="mt-2">
                  {installed ? (
                    <span className="text-[11px] text-green-700 inline-flex items-center gap-1">
                      <CheckCircle2 size={12} /> Installed
                    </span>
                  ) : (
                    <button
                      disabled={busy !== null}
                      onClick={() =>
                        run('install-' + d.Slug, () =>
                          api.feedInstall({ from_registry_slug: d.Slug }),
                        )
                      }
                      className="px-2 py-1 text-xs rounded bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
                    >
                      {busy === 'install-' + d.Slug ? 'Installing…' : 'Install'}
                    </button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </section>
    </div>
  );
}

function FeedRow({
  feed: f,
  busy,
  onRefresh,
  onUninstall,
}: {
  feed: FeedConfig;
  busy: string | null;
  onRefresh: () => void;
  onUninstall: () => void;
}) {
  const isFederated = f.Source === 'federated_from_parent';
  return (
    <tr className={cn('border-t border-border', isFederated && 'opacity-60')}>
      <td className="px-3 py-2">
        <div className="font-medium text-ink flex items-center gap-2">
          {f.Name}
          {isFederated && (
            <span className="px-1.5 py-0.5 text-[10px] rounded bg-slate-100 text-ink-dim ring-1 ring-border">
              from parent
            </span>
          )}
        </div>
        <div className="text-[11px] text-ink-mute font-mono">{f.Slug}</div>
      </td>
      <td className="px-3 py-2">
        <Chip>{f.Kind}</Chip>
      </td>
      <td className="px-3 py-2">
        <Chip>{f.Parser}</Chip>
      </td>
      <td className="px-3 py-2 text-xs text-ink-dim">
        {f.LastRefreshAt.Valid ? (
          <span className="inline-flex items-center gap-1">
            {f.LastRefreshStatus === 'ok' ? (
              <CheckCircle2 size={12} className="text-green-600" />
            ) : (
              <AlertTriangle size={12} className="text-red-600" />
            )}
            {new Date(f.LastRefreshAt.Time).toLocaleString()}
          </span>
        ) : (
          <span className="text-ink-mute">—</span>
        )}
        {f.LastRefreshError && (
          <div
            className="text-red-700 text-[11px] mt-0.5 truncate max-w-xs"
            title={f.LastRefreshError}
          >
            {f.LastRefreshError}
          </div>
        )}
      </td>
      <td className="px-3 py-2 text-right text-ink">
        {f.LastRefreshEntryCount || 0}
      </td>
      <td className="px-3 py-2 text-right">
        <div className="inline-flex gap-1">
          <button
            disabled={busy !== null || isFederated}
            onClick={onRefresh}
            className="px-2 py-1 text-xs rounded border border-border hover:bg-slate-50 disabled:opacity-40"
            title={isFederated ? 'Federated feeds refresh on a schedule' : 'Refresh now'}
          >
            <RefreshCw size={12} />
          </button>
          <button
            disabled={busy !== null || isFederated}
            onClick={onUninstall}
            className="px-2 py-1 text-xs rounded border border-border hover:bg-red-50 text-red-700 disabled:opacity-40"
            title={isFederated ? 'Federated feeds cannot be uninstalled here' : 'Uninstall'}
          >
            <Trash2 size={12} />
          </button>
        </div>
      </td>
    </tr>
  );
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="px-1.5 py-0.5 text-[10px] rounded bg-slate-100 text-ink-dim ring-1 ring-border">
      {children}
    </span>
  );
}
