// Icon resolution for catalog entries and templates. App icons are
// loaded live from the dashboard-icons CDN; only the two OS fallbacks
// (os-linux.svg, os-windows.svg) are vendored under public/icons/
// (hack/vendor-icons.sh, ATTRIBUTION.md). The fallback is used when
// the slug is absent or invalid, and by AppIcon's onError when the
// CDN load fails (unknown slug, offline).

/** Same CDN as hack/vendor-icons.sh — keep the two in sync. */
export const DASHBOARD_ICONS_CDN =
  'https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg';

// Catalog content (DiscoveredImage.icon) is not trusted: it can come
// from a spec.catalog.from.url fetch or a third-party-edited
// ConfigMap/Secret. Only slugs matching the dashboard-icons naming
// charset are ever interpolated into a CDN URL; anything else goes
// straight to the OS fallback without a fetch attempt.
const SLUG_RE = /^[a-z0-9][a-z0-9-]*$/;

/**
 * Local OS-fallback icon path (an empty or unknown os renders as
 * linux — same tolerance as the catalog wire-format).
 */
export function osFallbackIcon(os?: string): string {
  return os === 'windows' ? '/icons/os-windows.svg' : '/icons/os-linux.svg';
}

/**
 * URL of the logo to show for a catalog entry or template: the
 * dashboard-icons CDN when a valid slug is present, else the vendored
 * OS fallback. A CDN load failure is handled by the <img> onError
 * (AppIcon), not here.
 */
export function resolveIcon(slug?: string, os?: string): string {
  if (slug && SLUG_RE.test(slug)) return `${DASHBOARD_ICONS_CDN}/${slug}.svg`;
  return osFallbackIcon(os);
}
