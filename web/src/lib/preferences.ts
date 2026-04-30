// Per-user UI preferences, persisted to localStorage. These are display-only
// knobs that don't need to round-trip through the CP — every browser keeps
// its own copy, and sane defaults are baked in so a fresh session is usable
// without ever opening Settings.
//
// Add a new pref by:
//   1. Extending `Preferences`
//   2. Adding a default in `DEFAULTS`
//   3. Updating the Display settings section to expose it
//
// The hook uses a window-level event so changes from Settings propagate to
// other open tabs/views in the same window (within a single tab — cross-tab
// sync via `storage` events is also supported).

import { useEffect, useState } from 'react';

export interface Preferences {
  liveEvents: {
    /** Rolling window in seconds — events of the same (type, agent) within
     *  this many seconds of the most recent collapse into one row. */
    windowSec: number;
    /** For high-volume types (finding, api_unavailable), keep individual
     *  rows until count exceeds this threshold; collapse only beyond that.
     *  Ignored when `smartGrouping` is on. */
    highVolumeThreshold: number;
    /** Whether grouping also applies to the per-agent EventTimeline (the
     *  Live Events tab on the AgentDetail page). */
    applyToAgentDetail: boolean;
    /** Smart grouping. When on:
     *   - the high-volume-type exception above is dropped (finding /
     *     api_unavailable collapse from the 2nd occurrence in window
     *     just like everything else)
     *   - any (type, agent) key seeing >10 events in the window is
     *     auto-promoted to a coarser (type) key, collapsing across
     *     agents, so a fleet-wide event storm renders as one row.
     *  `error` still never groups regardless of this toggle. */
    smartGrouping: boolean;
  };
  iocDisplay: {
    /** When true, IOC values render in defanged form (e.g.
     *  `1.2.3[.]4`, `example[.]com`, `hxxps://`) so an operator can't
     *  accidentally click or copy a live indicator. Hashes / CVEs /
     *  MITRE IDs pass through unchanged regardless. Default on. */
    defang: boolean;
  };
}

export const DEFAULTS: Preferences = {
  liveEvents: {
    windowSec: 15,
    highVolumeThreshold: 5,
    applyToAgentDetail: true,
    smartGrouping: false,
  },
  iocDisplay: {
    defang: true,
  },
};

const STORAGE_KEY = 'okesu.prefs.v1';

// One synthetic event for same-tab subscribers; storage event handles cross-tab.
const CHANGE_EVENT = 'okesu:prefs-changed';

function readFromStorage(): Preferences {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Partial<Preferences>;
    // Shallow merge each section against defaults so newly-added prefs
    // populate cleanly when an older saved object is loaded.
    return {
      liveEvents: { ...DEFAULTS.liveEvents, ...(parsed.liveEvents ?? {}) },
      iocDisplay: { ...DEFAULTS.iocDisplay, ...(parsed.iocDisplay ?? {}) },
    };
  } catch {
    return DEFAULTS;
  }
}

function writeToStorage(p: Preferences): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(p));
    window.dispatchEvent(new CustomEvent(CHANGE_EVENT));
  } catch {
    /* localStorage may be unavailable (private mode); ignore */
  }
}

/** Reactive hook — re-renders on local or cross-tab change. */
export function usePreferences(): [Preferences, (next: Preferences) => void] {
  const [prefs, setPrefs] = useState<Preferences>(readFromStorage);

  useEffect(() => {
    const onChange = () => setPrefs(readFromStorage());
    const onStorage = (e: StorageEvent) => {
      if (e.key === STORAGE_KEY) setPrefs(readFromStorage());
    };
    window.addEventListener(CHANGE_EVENT, onChange);
    window.addEventListener('storage', onStorage);
    return () => {
      window.removeEventListener(CHANGE_EVENT, onChange);
      window.removeEventListener('storage', onStorage);
    };
  }, []);

  return [
    prefs,
    (next) => {
      writeToStorage(next);
      setPrefs(next);
    },
  ];
}

/** Convenience for consumers that only want one section. */
export function useLiveEventsPrefs() {
  const [prefs, setPrefs] = usePreferences();
  return [
    prefs.liveEvents,
    (next: Partial<Preferences['liveEvents']>) =>
      setPrefs({ ...prefs, liveEvents: { ...prefs.liveEvents, ...next } }),
  ] as const;
}

/** IOC display preferences (Phase 22.2). Mirrors useLiveEventsPrefs:
 *  reactive across same-tab and cross-tab changes via the shared
 *  preferences hook. Default `defang: true` is applied on first read. */
export function useIOCDisplayPrefs() {
  const [prefs, setPrefs] = usePreferences();
  return [
    prefs.iocDisplay,
    (next: Partial<Preferences['iocDisplay']>) =>
      setPrefs({ ...prefs, iocDisplay: { ...prefs.iocDisplay, ...next } }),
  ] as const;
}
