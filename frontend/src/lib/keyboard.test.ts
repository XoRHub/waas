import { afterEach, describe, expect, it, vi } from 'vitest';
import { detectServerLayout } from './keyboard';

function mockLanguages(langs: string[]) {
  vi.stubGlobal('navigator', { languages: langs, language: langs[0] });
}

afterEach(() => vi.unstubAllGlobals());

describe('detectServerLayout', () => {
  it('maps a region-specific locale', () => {
    mockLanguages(['fr-CA']);
    expect(detectServerLayout()).toBe('fr-ca-qwerty');
  });

  it('falls back to the language subtag', () => {
    mockLanguages(['fr-FR']);
    expect(detectServerLayout()).toBe('fr-fr-azerty');
  });

  it('uses the first known locale in the preference list', () => {
    mockLanguages(['xx-YY', 'de-DE']);
    expect(detectServerLayout()).toBe('de-de-qwertz');
  });

  it('defaults to us qwerty for unknown locales', () => {
    mockLanguages(['zz']);
    expect(detectServerLayout()).toBe('en-us-qwerty');
  });
});
