import { describe, expect, it } from 'vitest';
import { templateAvailability } from './templates';
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
