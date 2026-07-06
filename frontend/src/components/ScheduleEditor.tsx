import { useTranslation } from 'react-i18next';
import type { WorkspaceSchedule } from '@/types';

const fieldSm =
  'mt-0.5 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white';

/** One cron per line ↔ string[]; blank lines are dropped. */
function linesToCrons(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '');
}

/**
 * Editor for a workspace uptime/downtime schedule: an explicit IANA
 * timezone plus one standard 5-field cron per line for uptime (start)
 * and downtime (stop). Server-side validation (operator/pkg/schedule) is
 * authoritative; this only shapes the payload.
 */
export function ScheduleEditor({
  value,
  onChange,
}: {
  value: WorkspaceSchedule | undefined;
  onChange: (schedule: WorkspaceSchedule | undefined) => void;
}) {
  const { t } = useTranslation();
  const schedule = value ?? {};

  const patch = (next: WorkspaceSchedule) => {
    // Empty in every field ⇒ no schedule at all.
    if (!next.timezone && (next.uptime?.length ?? 0) === 0 && (next.downtime?.length ?? 0) === 0) {
      onChange(undefined);
    } else {
      onChange(next);
    }
  };

  return (
    <div className="space-y-3">
      <p className="text-xs text-slate-400 dark:text-slate-500">{t('schedule.hint')}</p>
      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">{t('schedule.timezone')}</span>
        <input
          className={fieldSm}
          placeholder="Europe/Paris"
          value={schedule.timezone ?? ''}
          onChange={(e) => patch({ ...schedule, timezone: e.target.value })}
        />
      </label>
      <div className="grid grid-cols-2 gap-3">
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('schedule.uptime')}</span>
          <textarea
            className={`${fieldSm} font-mono`}
            rows={3}
            spellCheck={false}
            placeholder={'0 8 * * 1-5'}
            value={(schedule.uptime ?? []).join('\n')}
            onChange={(e) => patch({ ...schedule, uptime: linesToCrons(e.target.value) })}
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('schedule.downtime')}</span>
          <textarea
            className={`${fieldSm} font-mono`}
            rows={3}
            spellCheck={false}
            placeholder={'0 20 * * *'}
            value={(schedule.downtime ?? []).join('\n')}
            onChange={(e) => patch({ ...schedule, downtime: linesToCrons(e.target.value) })}
          />
        </label>
      </div>
    </div>
  );
}

/** Human label for the next scheduled transition, in the viewer's locale. */
export function useNextTransitionLabel() {
  const { t } = useTranslation();
  return (next: { time: string; up: boolean } | undefined): string | null => {
    if (!next) return null;
    const when = new Date(next.time).toLocaleString(undefined, {
      weekday: 'short',
      hour: '2-digit',
      minute: '2-digit',
    });
    return next.up ? t('schedule.nextUp', { when }) : t('schedule.nextDown', { when });
  };
}
