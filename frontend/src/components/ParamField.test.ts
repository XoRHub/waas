import { describe, expect, it } from 'vitest';
import { sectionedParams } from './ParamField';
import type { ParamMeta, ProtocolMeta } from '@/types';

const p = (name: string, tier: ParamMeta['tier'], category: ParamMeta['category']): ParamMeta => ({
  name,
  protocols: ['rdp'],
  kind: 'string',
  tier,
  category,
  live: false,
  description: name,
});

const meta: ProtocolMeta[] = [
  {
    name: 'rdp',
    params: [
      // Payload order is the backend's canonical category order; the
      // sections must come out in this order, not re-sorted client-side.
      p('color-depth', 'ui', 'display'),
      p('enable-wallpaper', 'advanced', 'display'),
      p('server-layout', 'ui', 'input'),
      p('normalize-clipboard', 'advanced', 'clipboard'),
      p('read-only', 'ui', 'security'),
      p('password', 'platform', 'connection'),
    ],
  },
];

describe('sectionedParams', () => {
  it('groups by category in payload order, ui as simple and advanced behind, never platform', () => {
    const sections = sectionedParams(meta, 'rdp', undefined);
    expect(sections.map((s) => s.category)).toEqual(['display', 'input', 'clipboard', 'security']);
    const display = sections[0];
    expect(display.simple.map((x) => x.name)).toEqual(['color-depth']);
    expect(display.advanced.map((x) => x.name)).toEqual(['enable-wallpaper']);
    // platform-owned never surfaces anywhere (its section is dropped).
    expect(sections.some((s) => s.category === 'connection')).toBe(false);
  });

  it('drops sections emptied by the allow-list', () => {
    const sections = sectionedParams(meta, 'rdp', ['color-depth', 'read-only']);
    expect(sections.map((s) => s.category)).toEqual(['display', 'security']);
    // The "advanced toggle does nothing" bug, per section now: no advanced
    // name delegated → empty advanced bucket → callers hide the disclosure.
    expect(sections[0].advanced).toEqual([]);
  });

  it('reveals an advanced param the allow-list delegates, in its section', () => {
    const sections = sectionedParams(meta, 'rdp', ['normalize-clipboard']);
    expect(sections).toHaveLength(1);
    expect(sections[0].category).toBe('clipboard');
    expect(sections[0].simple).toEqual([]);
    expect(sections[0].advanced.map((x) => x.name)).toEqual(['normalize-clipboard']);
  });

  // The allow-list a connect form receives is the server-RESOLVED flat
  // list (resolvedUserParams). The frontend never expands cat: syntax:
  // a raw selector leaking in matches no parameter name and delegates
  // nothing (fail-closed), it does not open the whole category.
  it('never expands a raw cat: selector client-side', () => {
    expect(sectionedParams(meta, 'rdp', ['cat:display'])).toEqual([]);
    const mixed = sectionedParams(meta, 'rdp', ['cat:display', 'read-only']);
    expect(mixed).toHaveLength(1);
    expect(mixed[0].category).toBe('security');
    expect(mixed[0].simple.map((x) => x.name)).toEqual(['read-only']);
  });
});

describe('sectionedParams null-safety (kasmvnc/terminal regression)', () => {
  // The backend contract says params is always an array, but a nil Go
  // slice once leaked as null and crashed every param form on kasmvnc
  // template selection. Every template protocol shape must be safe.
  const shapes: { label: string; entry: ProtocolMeta }[] = [
    {
      label: 'kasmvnc with null params',
      entry: { name: 'kasmvnc', params: null as unknown as ParamMeta[] },
    },
    { label: 'kasmvnc with empty params', entry: { name: 'kasmvnc', params: [] } },
    {
      label: 'vnc with params',
      entry: { name: 'vnc', params: [p('color-depth', 'ui', 'display')] },
    },
  ];
  for (const { label, entry } of shapes) {
    it(`does not throw for ${label}`, () => {
      expect(Array.isArray(sectionedParams([entry], entry.name, undefined))).toBe(true);
    });
  }
  it('returns [] for a protocol absent from the meta (ssh/rdp fallback)', () => {
    expect(sectionedParams(meta, 'ssh', undefined)).toEqual([]);
  });
});
