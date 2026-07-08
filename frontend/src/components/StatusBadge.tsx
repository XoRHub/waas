import { useTranslation } from 'react-i18next';
import type { EffectivePhase } from '@/lib/lifecycle';

// Traffic-light colors for the fleet dashboard and the portal cards.
// Pausing/Resuming are DERIVED transitional states (spec intent vs CR
// status, see lib/lifecycle) — amber like the other converging phases.
const PHASE_STYLES: Record<EffectivePhase, string> = {
  Running: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200',
  Provisioning: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  Pending: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  Pausing: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  Resuming: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  // Paused = manual scale-to-0 (blue, user-driven); Stopped = scheduled
  // downtime (grey, system-driven).
  Paused: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200',
  Stopped: 'bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200',
  Failed: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200',
  Terminating: 'bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200',
};

export function StatusBadge({ phase }: { phase: EffectivePhase }) {
  const { t } = useTranslation();
  const style = PHASE_STYLES[phase] ?? PHASE_STYLES.Stopped;
  return (
    <span
      className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${style}`}
    >
      {t(`portal.phase.${phase}`)}
    </span>
  );
}
