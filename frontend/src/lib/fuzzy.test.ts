import { describe, expect, it } from 'vitest';
import { fuzzyFilter, fuzzyScore } from './fuzzy';

describe('fuzzyScore', () => {
  it('matches a case-insensitive subsequence', () => {
    expect(fuzzyScore('Firefox 128', 'ffx')).not.toBeNull();
    expect(fuzzyScore('firefox', 'FIRE')).not.toBeNull();
  });

  it('returns null when characters are missing or out of order', () => {
    expect(fuzzyScore('firefox', 'xz')).toBeNull();
    expect(fuzzyScore('firefox', 'xf')).toBeNull();
  });
});

describe('fuzzyFilter', () => {
  const items = ['chromium', 'firefox-esr', 'my-firefox', 'libreoffice'];

  it('keeps only matching items', () => {
    expect(fuzzyFilter(items, 'fox', (s) => s)).toEqual(['firefox-esr', 'my-firefox']);
  });

  it('returns the list untouched on an empty query', () => {
    expect(fuzzyFilter(items, '', (s) => s)).toEqual(items);
    expect(fuzzyFilter(items, '   ', (s) => s)).toEqual(items);
  });

  it('returns nothing when nothing matches', () => {
    expect(fuzzyFilter(items, 'windows', (s) => s)).toEqual([]);
  });

  it('ranks a start-of-string match before the same match mid-string', () => {
    // 'firefox-esr' starts with the query; 'my-firefox' contains it
    // later — the former must come out first despite catalog order.
    expect(fuzzyFilter(['my-firefox', 'firefox-esr'], 'firefox', (s) => s)).toEqual([
      'firefox-esr',
      'my-firefox',
    ]);
  });

  it('ranks contiguous matches above scattered ones', () => {
    // Both contain f…o…x as a subsequence, only one as a solid run.
    expect(fuzzyFilter(['freebox-unix', 'foxglove'], 'fox', (s) => s)).toEqual([
      'foxglove',
      'freebox-unix',
    ]);
  });
});
