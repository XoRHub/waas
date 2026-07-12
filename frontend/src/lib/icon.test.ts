import { describe, expect, it } from 'vitest';
import { resolveIcon, VENDORED_ICONS } from './icon';

describe('resolveIcon', () => {
  it('resolves a vendored slug to its own icon', () => {
    expect(resolveIcon('firefox', 'linux')).toBe('/icons/firefox.svg');
  });

  it('falls back to the OS icon for an unknown slug', () => {
    expect(resolveIcon('not-vendored', 'linux')).toBe('/icons/os-linux.svg');
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

describe('VENDORED_ICONS parity with public/icons/', () => {
  // Build-time enumeration of the committed icons (no fs access, works
  // under both vitest and tsc).
  const files = new Set(
    Object.keys(import.meta.glob('../../public/icons/*.svg')).map(
      (p) => p.split('/').pop()!.replace(/\.svg$/, ''),
    ),
  );

  it('every listed slug is vendored', () => {
    for (const slug of VENDORED_ICONS) {
      expect(files, `missing public/icons/${slug}.svg — rerun hack/vendor-icons.sh`).toContain(slug);
    }
  });

  it('every vendored app icon is listed (os-* fallbacks excepted)', () => {
    for (const file of files) {
      if (file.startsWith('os-')) continue;
      expect(VENDORED_ICONS, `unlisted icon ${file}.svg — add it to VENDORED_ICONS`).toContain(file);
    }
  });

  it('both OS fallbacks are vendored', () => {
    expect(files).toContain('os-linux');
    expect(files).toContain('os-windows');
  });
});
