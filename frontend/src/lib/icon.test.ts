import { describe, expect, it } from 'vitest';
import { DASHBOARD_ICONS_CDN, osFallbackIcon, resolveIcon, resolveLocalIconPath } from './icon';

describe('resolveIcon', () => {
  it('builds the CDN URL for a valid slug', () => {
    expect(resolveIcon('firefox', 'linux')).toBe(`${DASHBOARD_ICONS_CDN}/firefox.svg`);
    expect(resolveIcon('google-chrome')).toBe(`${DASHBOARD_ICONS_CDN}/google-chrome.svg`);
  });

  it('rejects an invalid slug without building a CDN URL', () => {
    // Icon references are untrusted — anything outside the
    // dashboard-icons charset must go straight to the OS fallback.
    for (const slug of ['../etc/passwd', 'a/b', 'Firefox', 'fire fox', '-leading', '']) {
      const resolved = resolveIcon(slug, 'linux');
      expect(resolved, `slug ${JSON.stringify(slug)}`).toBe('/icons/os-linux.svg');
      expect(resolved).not.toContain(DASHBOARD_ICONS_CDN);
    }
  });

  it('uses an absolute https URL as-is, whatever its path or format', () => {
    // Same CDN but a path/format the slug convention cannot build —
    // the exact use case for absolute URLs.
    const webp = 'https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/webp/longhorn.webp';
    expect(resolveIcon(webp, 'linux')).toBe(webp);
    // No host allow-list: any https host is accepted.
    expect(resolveIcon('https://icons.example.com/logo.png')).toBe(
      'https://icons.example.com/logo.png',
    );
  });

  it('rejects plain http URLs (https only)', () => {
    expect(resolveIcon('http://icons.example.com/logo.png', 'linux')).toBe('/icons/os-linux.svg');
  });

  it('resolves file: references to a same-origin root path', () => {
    expect(resolveIcon('file:custom/longhorn.svg', 'linux')).toBe('/custom/longhorn.svg');
    expect(resolveIcon('file:logo.png')).toBe('/logo.png');
  });

  it('rejects file: escape attempts without producing a dangerous src', () => {
    for (const ref of [
      'file:/etc/passwd', // absolute path
      'file://evil.example/x', // would become a protocol-relative URL
      'file:../../x', // path traversal
      'file:a/../x',
      'file:https://evil.example/x', // embedded scheme
      'file:a\\b', // backslash
      'file:',
    ]) {
      const resolved = resolveIcon(ref, 'linux');
      expect(resolved, `ref ${JSON.stringify(ref)}`).toBe('/icons/os-linux.svg');
    }
  });

  it('falls back to the OS icon when the reference is absent', () => {
    expect(resolveIcon(undefined, 'windows')).toBe('/icons/os-windows.svg');
  });

  it('treats an empty or unknown os as linux', () => {
    expect(resolveIcon()).toBe('/icons/os-linux.svg');
    expect(resolveIcon(undefined, '')).toBe('/icons/os-linux.svg');
    expect(resolveIcon(undefined, 'beos')).toBe('/icons/os-linux.svg');
  });
});

describe('resolveLocalIconPath', () => {
  it('prefixes a safe relative path with /', () => {
    expect(resolveLocalIconPath('custom/longhorn.svg')).toBe('/custom/longhorn.svg');
  });

  it('rejects unsafe paths', () => {
    for (const path of [
      '',
      '/etc/passwd',
      '//evil.example/x',
      '../x',
      'a/../x',
      'a\\b',
      'https://evil.example/x',
    ]) {
      expect(resolveLocalIconPath(path), `path ${JSON.stringify(path)}`).toBeUndefined();
    }
  });
});

describe('osFallbackIcon', () => {
  it('maps windows to its icon and everything else to linux', () => {
    expect(osFallbackIcon('windows')).toBe('/icons/os-windows.svg');
    expect(osFallbackIcon('linux')).toBe('/icons/os-linux.svg');
    expect(osFallbackIcon()).toBe('/icons/os-linux.svg');
  });
});
