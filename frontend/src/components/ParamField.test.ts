import { describe, expect, it } from 'vitest';
import { tieredParams } from './ParamField';
import type { ParamMeta, ProtocolMeta } from '@/types';

const p = (name: string, tier: ParamMeta['tier']): ParamMeta => ({
  name,
  protocols: ['rdp'],
  kind: 'string',
  tier,
  live: false,
  description: name,
});

const meta: ProtocolMeta[] = [
  {
    name: 'rdp',
    params: [
      p('color-depth', 'ui'),
      p('server-layout', 'ui'),
      p('normalize-clipboard', 'advanced'),
      p('console', 'advanced'),
      p('password', 'platform'),
    ],
  },
];

describe('tieredParams', () => {
  it('splits ui and advanced tiers, never platform', () => {
    const { simple, advanced } = tieredParams(meta, 'rdp', undefined);
    expect(simple.map((x) => x.name)).toEqual(['color-depth', 'server-layout']);
    expect(advanced.map((x) => x.name)).toEqual(['normalize-clipboard', 'console']);
    // platform-owned never surfaces in either tier.
    expect([...simple, ...advanced].some((x) => x.name === 'password')).toBe(false);
  });

  // The "advanced toggle does nothing" bug: an allow-list without any
  // advanced name yields an empty advanced tier — callers must then hide
  // the toggle rather than render an inert checkbox.
  it('yields no advanced params when the allow-list omits them', () => {
    const { simple, advanced } = tieredParams(meta, 'rdp', ['color-depth', 'server-layout']);
    expect(simple.map((x) => x.name)).toEqual(['color-depth', 'server-layout']);
    expect(advanced).toEqual([]);
  });

  it('reveals an advanced param the allow-list delegates', () => {
    const { advanced } = tieredParams(meta, 'rdp', ['console']);
    expect(advanced.map((x) => x.name)).toEqual(['console']);
  });
});

describe('paramsFor null-safety (kasmvnc/terminal regression)', () => {
  // The backend contract says params is always an array, but a nil Go
  // slice once leaked as null and crashed every param form on kasmvnc
  // template selection. Every template protocol shape must be safe.
  const shapes: { label: string; entry: ProtocolMeta }[] = [
    { label: 'kasmvnc with null params', entry: { name: 'kasmvnc', params: null as unknown as ParamMeta[] } },
    { label: 'kasmvnc with empty params', entry: { name: 'kasmvnc', params: [] } },
    { label: 'vnc with params', entry: { name: 'vnc', params: [p('color-depth', 'ui')] } },
  ];
  for (const { label, entry } of shapes) {
    it(`does not throw for ${label}`, () => {
      const { simple, advanced } = tieredParams([entry], entry.name, undefined);
      expect(Array.isArray(simple)).toBe(true);
      expect(Array.isArray(advanced)).toBe(true);
    });
  }
  it('returns [] for a protocol absent from the meta (ssh/rdp fallback)', () => {
    expect(tieredParams(meta, 'ssh', undefined).simple).toEqual([]);
  });
});
