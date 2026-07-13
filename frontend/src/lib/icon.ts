// Icon resolution for catalog entries and templates. An icon
// reference (DiscoveredImage.icon or WorkspaceTemplate.spec.logo) is
// one of, detected by prefix in this order:
//
//   1. absent/empty          → vendored OS fallback
//   2. https://...           → absolute URL, used as-is (https only —
//                              no host allow-list, but never plain http)
//   3. file:<path>           → same-origin path under the frontend's
//                              web root, for assets mounted into the
//                              nginx container or baked into a custom
//                              frontend image (repo convention, NOT the
//                              browser's file:// scheme)
//   4. anything else         → dashboard-icons slug, loaded live from
//                              the CDN
//
// Only the two OS fallbacks (os-linux.svg, os-windows.svg) are
// vendored under public/icons/ (hack/vendor-icons.sh,
// ATTRIBUTION.md). The fallback is used when the reference is absent
// or invalid, and by AppIcon's onError when the load fails (unknown
// slug, offline).

/** Same CDN as hack/vendor-icons.sh — keep the two in sync. */
export const DASHBOARD_ICONS_CDN = 'https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg';

// Icon references are not trusted: they can come from a
// spec.catalog.from.url fetch, a third-party-edited ConfigMap/Secret,
// or any template author. Only slugs matching the dashboard-icons
// naming charset are ever interpolated into a CDN URL; anything else
// goes straight to the OS fallback without a fetch attempt.
const SLUG_RE = /^[a-z0-9][a-z0-9-]*$/;

/**
 * Local OS-fallback icon path (an empty or unknown os renders as
 * linux — same tolerance as the catalog wire-format).
 */
export function osFallbackIcon(os?: string): string {
  return os === 'windows' ? '/icons/os-windows.svg' : '/icons/os-linux.svg';
}

/**
 * Resolve the path part of a `file:<path>` reference to a root-relative
 * same-origin URL (`file:custom/longhorn.svg` → `/custom/longhorn.svg`),
 * or undefined when the path is unsafe. The path is untrusted: reject
 * anything that could escape the origin or the web root — a leading `/`
 * (would yield `//host/...`, a protocol-relative URL), `..` traversal,
 * an embedded scheme, or backslashes.
 */
export function resolveLocalIconPath(path: string): string | undefined {
  if (!path) return undefined;
  if (path.startsWith('/')) return undefined;
  if (path.includes('..') || path.includes('://') || path.includes('\\')) {
    return undefined;
  }
  return `/${path}`;
}

/**
 * URL of the logo to show for a catalog entry or template, from an
 * icon reference in any of the forms documented at the top of this
 * file; an absent or invalid reference resolves to the vendored OS
 * fallback without any load attempt. A load failure of the resolved
 * URL is handled by the <img> onError (AppIcon), not here.
 */
export function resolveIcon(iconRef?: string, os?: string): string {
  if (iconRef) {
    if (iconRef.startsWith('https://')) return iconRef;
    if (iconRef.startsWith('file:')) {
      const path = resolveLocalIconPath(iconRef.slice('file:'.length));
      if (path) return path;
    } else if (SLUG_RE.test(iconRef)) {
      return `${DASHBOARD_ICONS_CDN}/${iconRef}.svg`;
    }
  }
  return osFallbackIcon(os);
}
