import { describe, expect, it } from 'vitest';
import { targetFromRemote, targetFromWorkspace } from './target';
import type { RemoteWorkspace, Workspace } from '@/types';

const ws = {
  id: 'w1',
  name: 'cr-name',
  displayName: 'CAD Station',
  templateRef: 'xfce',
  phase: 'Running',
  paused: false,
  protocol: 'vnc',
  protocols: [
    { name: 'vnc', port: 5901, default: true, userParams: ['color-depth'] },
    { name: 'rdp', port: 3389 },
  ],
} as unknown as Workspace;

const rwMulti = {
  id: 'r1',
  name: 'lab-box',
  hostname: '203.0.113.10',
  port: 22,
  protocol: 'ssh',
  protocols: [
    { name: 'ssh', port: 22, default: true },
    { name: 'vnc', port: 5900, params: { 'color-depth': '16' } },
  ],
  macAddress: 'aa:bb:cc:dd:ee:ff',
} as unknown as RemoteWorkspace;

const rwLegacy = {
  id: 'r2',
  name: 'old-box',
  hostname: '203.0.113.11',
  port: 3389,
  protocol: 'rdp',
  params: { security: 'any' },
} as unknown as RemoteWorkspace;

describe('targetFromWorkspace', () => {
  it('maps protocols and declares in-cluster capabilities', () => {
    const t = targetFromWorkspace(ws);
    expect(t.kind).toBe('workspace');
    expect(t.displayName).toBe('CAD Station');
    expect(t.protocols.map((p) => p.name)).toEqual(['vnc', 'rdp']);
    expect(t.defaultProtocol).toBe('vnc');
    expect(t.capabilities).toMatchObject({
      pause: true,
      wake: false,
      splitView: true,
      connectionSettings: true,
      editEndpoint: false,
      hasPhase: true,
    });
  });
});

describe('targetFromRemote', () => {
  it('maps the endpoint list and remote capabilities', () => {
    const t = targetFromRemote(rwMulti);
    expect(t.kind).toBe('remote');
    expect(t.protocols.map((p) => p.name)).toEqual(['ssh', 'vnc']);
    expect(t.defaultProtocol).toBe('ssh');
    expect(t.capabilities).toMatchObject({
      pause: false,
      wake: true, // has a MAC
      splitView: false,
      editEndpoint: true,
      hasPhase: false,
    });
  });

  it('synthesizes the single legacy endpoint (pre multi-protocol rows)', () => {
    const t = targetFromRemote(rwLegacy);
    expect(t.protocols).toEqual([
      { name: 'rdp', port: 3389, default: true, params: { security: 'any' } },
    ]);
    expect(t.defaultProtocol).toBe('rdp');
    expect(t.capabilities.wake).toBe(false); // no MAC
  });
});
