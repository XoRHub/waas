// Maps the browser locale to a guacd RDP `server-layout` value, so the
// keyboard layout defaults to "auto" (the client's own layout) without
// the user configuring anything. An explicit template/user server-layout
// still wins — this is only the default the platform sends when none is
// set (see wwt ClientLayout). The values match the server-layout enum in
// operator/pkg/params.

// BCP-47 language(-region) → guacd server-layout. Longest keys first.
const LAYOUT_MAP: Record<string, string> = {
  'fr-CA': 'fr-ca-qwerty',
  'fr-CH': 'fr-ch-qwertz',
  'fr-BE': 'fr-be-azerty',
  'de-CH': 'de-ch-qwertz',
  'en-GB': 'en-gb-qwerty',
  'pt-BR': 'pt-br-qwerty',
  'es-419': 'es-latam-qwerty',
  fr: 'fr-fr-azerty',
  de: 'de-de-qwertz',
  en: 'en-us-qwerty',
  es: 'es-es-qwerty',
  it: 'it-it-qwerty',
  pt: 'pt-pt-qwerty',
  nl: 'nl-nl-qwerty',
  no: 'no-no-qwerty',
  sv: 'sv-se-qwerty',
  da: 'da-dk-qwerty',
  pl: 'pl-pl-qwertz',
  hu: 'hu-hu-qwertz',
  cs: 'cs-cz-qwertz',
  ro: 'ro-ro-qwerty',
  tr: 'tr-tr-qwerty',
  ja: 'ja-jp-qwerty',
};

/** Detects the guacd RDP server-layout from the browser locales. */
export function detectServerLayout(): string {
  const locales = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const raw of locales) {
    if (!raw) continue;
    // Try the full tag (fr-CA), then the language subtag (fr).
    const exact = Object.keys(LAYOUT_MAP).find((k) => k.toLowerCase() === raw.toLowerCase());
    if (exact) return LAYOUT_MAP[exact];
    const lang = raw.split('-')[0].toLowerCase();
    if (LAYOUT_MAP[lang]) return LAYOUT_MAP[lang];
  }
  return 'en-us-qwerty';
}
