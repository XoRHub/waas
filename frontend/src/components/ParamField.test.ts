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
