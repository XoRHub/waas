import { describe, expect, it } from 'vitest';
import { templateAvailability, templateIcon } from './templates';
import type { CatalogImage, WorkspaceTemplate } from '@/types';

const tpl = (name: string, protocol: string): WorkspaceTemplate => ({
  id: name,
  name,
  displayName: name,
  os: 'linux',
  image: `reg/${name}:1`,
  createdAt: '2026-01-01T00:00:00Z',
  protocols: [{ name: protocol, port: 1, default: true }],
});

const img = (name: string, templates: string[]): CatalogImage => ({
  name,
  displayName: name,
  image: `reg/${name}:1`,
  protocols: [],
  enabled: true,
  templates,
});

describe('templateAvailability', () => {
  // Non-regression for "SSH templates invisible at creation": templates
  // of EVERY protocol must be listed, never silently dropped.
  it('lists ssh, vnc and rdp templates when their images are allowed', () => {
    const templates = [tpl('t-ssh', 'ssh'), tpl('t-vnc', 'vnc'), tpl('t-rdp', 'rdp')];
    const catalog = [img('t-ssh', ['t-ssh']), img('t-vnc', ['t-vnc']), img('t-rdp', ['t-rdp'])];
    const out = templateAvailability(templates, catalog);
    expect(out).toHaveLength(3);
    expect(out.every((a) => a.available)).toBe(true);
  });

  it('keeps policy-excluded templates visible but flags them unavailable', () => {
    const templates = [tpl('t-ssh', 'ssh'), tpl('t-vnc', 'vnc')];
    // Catalog without the ssh image (policy restriction).
    const catalog = [img('t-vnc', ['t-vnc'])];
    const out = templateAvailability(templates, catalog);
    expect(out).toHaveLength(2);
    expect(out.find((a) => a.template.name === 't-ssh')?.available).toBe(false);
    expect(out.find((a) => a.template.name === 't-vnc')?.available).toBe(true);
  });

  it('treats a missing catalog (loading/error) as all-available', () => {
    const out = templateAvailability([tpl('t-ssh', 'ssh')], undefined);
    expect(out[0].available).toBe(true);
  });
});

describe('templateIcon', () => {
  const catalog = [
    {
      ...img('cat-vnc', ['t-vnc']),
      discovered: [
        { image: 'reg/other:1', icon: 'other-icon' },
        { image: 'reg/t-vnc:1', icon: 'firefox' },
      ],
    },
  ];

  it('prefers the explicit spec.logo over the catalog icon', () => {
    expect(templateIcon({ ...tpl('t-vnc', 'vnc'), logo: 'https://x/logo.png' }, catalog)).toBe(
      'https://x/logo.png',
    );
  });

  it('falls back to the discovered entry matching the template image', () => {
    expect(templateIcon(tpl('t-vnc', 'vnc'), catalog)).toBe('firefox');
  });

  it('finds the icon on another entry than the one listing the template', () => {
    // Real k3d-dev topology: the template is attributed to a
    // single-image CatalogImage (no discovered list) while the icons
    // live on the registry-mode entry's discovered list.
    const split = [
      img('single', ['t-vnc']),
      { ...img('registry', []), discovered: [{ image: 'reg/t-vnc:1', icon: 'firefox' }] },
    ];
    expect(templateIcon(tpl('t-vnc', 'vnc'), split)).toBe('firefox');
  });

  it('resolves to undefined (OS fallback) when both are absent or the template is gone', () => {
    expect(templateIcon(tpl('t-vnc', 'vnc'), [img('cat-vnc', ['t-vnc'])])).toBeUndefined();
    expect(templateIcon(tpl('t-vnc', 'vnc'), undefined)).toBeUndefined();
    expect(templateIcon(undefined, catalog)).toBeUndefined();
  });
});
