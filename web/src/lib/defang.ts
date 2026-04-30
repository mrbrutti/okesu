// IOC defanging — render-time helper. Operators viewing IOC values in
// the dashboard expect them in *defanged* form (e.g. `1.2.3[.]4`,
// `example[.]com`, `hxxps://example[.]com/path`) so a stray click or
// copy-paste doesn't accidentally exfil to a live indicator.
//
// This mirrors the inverse of the Go-side `normalize.Refang` in
// `controlplane/ioc/normalize`: refang takes a defanged catalog entry
// and reconstitutes the live form for matching; we go the other way
// for display only. The DB always stores normalized (refanged) values.
//
// Kinds covered: `ipv4`, `ipv6`, `domain`, `url`. Hashes (sha256, md5,
// …), CVEs, and MITRE IDs pass through unchanged — they're already
// non-clickable and have no canonical defanged form.
//
// The toggle that decides whether to invoke this lives in
// `useIOCDisplayPrefs()` (lib/preferences.ts) — default on, persisted
// to localStorage, and reactively shared across tabs.

export function defangValue(kind: string, value: string): string {
  if (!value) return value;
  switch (kind) {
    case 'ipv4':
    case 'ipv6':
    case 'domain':
      return defangDots(value);
    case 'url':
      // Replace the scheme's `t`s with `x`s (http→hxxp, https→hxxps),
      // then defang every dot. Anchored at start so a literal `http`
      // somewhere in the path doesn't get rewritten.
      return defangDots(
        value
          .replace(/^https/i, (m) => (m[0] === 'H' ? 'HXXPS' : 'hxxps'))
          .replace(/^http/i, (m) => (m[0] === 'H' ? 'HXXP' : 'hxxp')),
      );
    default:
      return value;
  }
}

// Split/join is the lib-target-independent way to do a global string
// replace; `String.prototype.replaceAll` is ES2021+ and the build
// targets ES2020.
function defangDots(s: string): string {
  return s.split('.').join('[.]');
}
