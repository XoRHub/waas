// Icon resolution for catalog entries and templates. Icons are a
// vendored dashboard-icons subset committed under public/icons/
// (hack/vendor-icons.sh, ATTRIBUTION.md) — resolveIcon only builds
// local paths, it NEVER fetches from the network.

/** Vendored app slugs — keep in sync with public/icons/ (a test
 * enforces parity). The os-* fallbacks are deliberately not listed:
 * they are not valid catalog icon slugs. */
export const VENDORED_ICONS: ReadonlySet<string> = new Set([
  'firefox',
  'google-chrome',
  'chromium',
  'kasm',
  'terminal',
  'code',
  'ubuntu-linux',
]);

/**
 * Path of the logo to show for a catalog entry or template: the
 * vendored icon when the slug is known, else the OS fallback (an empty
 * or unknown os renders as linux — same tolerance as the catalog
 * wire-format).
 */
export function resolveIcon(slug?: string, os?: string): string {
  if (slug && VENDORED_ICONS.has(slug)) return `/icons/${slug}.svg`;
  return os === 'windows' ? '/icons/os-windows.svg' : '/icons/os-linux.svg';
}
