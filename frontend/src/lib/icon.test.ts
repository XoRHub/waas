import { describe, expect, it } from 'vitest';
import { DASHBOARD_ICONS_CDN, osFallbackIcon, resolveIcon } from './icon';

describe('resolveIcon', () => {
  it('builds the CDN URL for a valid slug', () => {
    expect(resolveIcon('firefox', 'linux')).toBe(`${DASHBOARD_ICONS_CDN}/firefox.svg`);
    expect(resolveIcon('google-chrome')).toBe(`${DASHBOARD_ICONS_CDN}/google-chrome.svg`);
  });

  it('rejects an invalid slug without building a CDN URL', () => {
    // Catalog content is untrusted — anything outside the
    // dashboard-icons charset must go straight to the OS fallback.
    for (const slug of ['../etc/passwd', 'a/b', 'Firefox', 'fire fox', '-leading', '']) {
      const resolved = resolveIcon(slug, 'linux');
      expect(resolved, `slug ${JSON.stringify(slug)}`).toBe('/icons/os-linux.svg');
      expect(resolved).not.toContain(DASHBOARD_ICONS_CDN);
    }
  });

  it('falls back to the OS icon when the slug is absent', () => {
    expect(resolveIcon(undefined, 'windows')).toBe('/icons/os-windows.svg');
  });

  it('treats an empty or unknown os as linux', () => {
    expect(resolveIcon()).toBe('/icons/os-linux.svg');
    expect(resolveIcon(undefined, '')).toBe('/icons/os-linux.svg');
    expect(resolveIcon(undefined, 'beos')).toBe('/icons/os-linux.svg');
  });
});

describe('osFallbackIcon', () => {
  it('maps windows to its icon and everything else to linux', () => {
    expect(osFallbackIcon('windows')).toBe('/icons/os-windows.svg');
    expect(osFallbackIcon('linux')).toBe('/icons/os-linux.svg');
    expect(osFallbackIcon()).toBe('/icons/os-linux.svg');
  });
});
