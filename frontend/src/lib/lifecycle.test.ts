import { describe, expect, it } from 'vitest';
import { effectivePhase, isTransient } from './lifecycle';
import type { Workspace } from '@/types';

const ws = (phase: Workspace['phase'], paused: boolean): Workspace =>
  ({ phase, paused }) as Workspace;

describe('effectivePhase', () => {
  it('labels the gap between intent and status', () => {
    expect(effectivePhase(ws('Running', true))).toBe('Pausing');
    expect(effectivePhase(ws('Provisioning', true))).toBe('Pausing');
    expect(effectivePhase(ws('Paused', false))).toBe('Resuming');
  });

  it('keeps steady states steady', () => {
    expect(effectivePhase(ws('Running', false))).toBe('Running');
    expect(effectivePhase(ws('Paused', true))).toBe('Paused');
    // Cron-stopped with paused=false is the schedule holding it down,
    // NOT a pending resume.
    expect(effectivePhase(ws('Stopped', false))).toBe('Stopped');
    expect(effectivePhase(ws('Failed', true))).toBe('Failed');
    expect(effectivePhase(ws('Terminating', false))).toBe('Terminating');
  });
});

describe('isTransient', () => {
  it('covers converging states, not steady ones', () => {
    expect(isTransient(ws('Running', true))).toBe(true); // pausing
    expect(isTransient(ws('Paused', false))).toBe(true); // resuming
    expect(isTransient(ws('Provisioning', false))).toBe(true);
    expect(isTransient(ws('Terminating', false))).toBe(true);
    expect(isTransient(ws('Running', false))).toBe(false);
    expect(isTransient(ws('Stopped', false))).toBe(false);
  });
});
