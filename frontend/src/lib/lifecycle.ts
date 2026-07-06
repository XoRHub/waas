import type { Workspace } from '@/types';

/**
 * The card badge must show what is HAPPENING, not just the last observed
 * status: between a lifecycle action and the operator's reconcile, the
 * CR's spec (intent) and status (reality) disagree. This helper derives
 * the transitional display state from that disagreement — the UI never
 * invents state, it only labels the gap until the poll converges.
 *
 * 'Pausing'  = spec.paused, status still up.
 * 'Resuming' = manual Paused status, spec.paused already cleared.
 * A cron-Stopped workspace with paused=false is a STEADY state (the
 * schedule holds it down), not "Resuming" — it stays 'Stopped'.
 */
export type EffectivePhase = Workspace['phase'] | 'Pausing' | 'Resuming';

export function effectivePhase(ws: Workspace): EffectivePhase {
  if (ws.phase === 'Terminating' || ws.phase === 'Failed' || ws.phase === 'Pending') {
    return ws.phase;
  }
  const down = ws.phase === 'Paused' || ws.phase === 'Stopped';
  if (ws.paused && !down) return 'Pausing';
  if (!ws.paused && ws.phase === 'Paused') return 'Resuming';
  return ws.phase;
}

/** Phases during which the portal polls fast (state converging). */
const TRANSIENT = new Set<EffectivePhase>([
  'Pending',
  'Provisioning',
  'Terminating',
  'Pausing',
  'Resuming',
]);

export function isTransient(ws: Workspace): boolean {
  return TRANSIENT.has(effectivePhase(ws));
}
