import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { nextOccurrence, validateCron, validateTimezone } from '@/lib/cron';
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
 * Textarea for one cron list, keeping ITS OWN text draft: the parsed
 * value trims every line, so a controlled textarea re-rendered from it
 * ate the trailing space of a cron as it was typed — keyboard entry was
 * impossible, only pasting worked. The draft re-seeds only when the
 * parent value diverges from what the draft parses to (an external
 * change, e.g. a template switch) — never mid-typing, where trailing
 * spaces and blank lines must survive in the box.
 */
function CronLinesField({
  label,
  placeholder,
  crons,
  disabled,
  onChange,
  status,
}: {
  label: string;
  placeholder: string;
  crons: string[] | undefined;
  disabled?: boolean;
  onChange: (crons: string[]) => void;
  status: ReactNode;
}) {
  const [draft, setDraft] = useState(() => (crons ?? []).join('\n'));
  if (JSON.stringify(linesToCrons(draft)) !== JSON.stringify(crons ?? [])) {
    setDraft((crons ?? []).join('\n'));
  }
  return (
    <label className="block">
      <span className="text-sm text-slate-600 dark:text-slate-300">{label}</span>
      <textarea
        className={`${fieldSm} font-mono`}
        rows={3}
        spellCheck={false}
        disabled={disabled}
        placeholder={placeholder}
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value);
          onChange(linesToCrons(e.target.value));
        }}
      />
      {status}
    </label>
  );
}

/**
 * Editor for a workspace uptime/downtime schedule: an explicit IANA
 * timezone plus one standard 5-field cron per line for uptime (start)
 * and downtime (stop). Each side is validated live and previews its next
 * occurrence ("next stop: tonight 22:00"); server-side validation
 * (operator/pkg/schedule) stays authoritative.
 */
export function ScheduleEditor({
  value,
  onChange,
  disabled,
}: {
  value: WorkspaceSchedule | undefined;
  onChange: (schedule: WorkspaceSchedule | undefined) => void;
  /** Read-only rendering (the caller shows its own 🔒 marker). */
  disabled?: boolean;
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

  const hasCrons = (schedule.uptime?.length ?? 0) > 0 || (schedule.downtime?.length ?? 0) > 0;
  const tz = schedule.timezone ?? '';
  const tzInvalid = hasCrons && !validateTimezone(tz);

  const sideStatus = (crons: string[] | undefined, up: boolean) => {
    const list = crons ?? [];
    const bad = list.find((c) => !validateCron(c));
    if (bad) {
      return (
        <p className="mt-0.5 text-xs text-red-600">{t('schedule.invalidCron', { cron: bad })}</p>
      );
    }
    if (list.length === 0 || tzInvalid || !tz) return null;
    const next = nextOccurrence(list, tz);
    if (!next) return null;
    const when = next.toLocaleString(undefined, {
      weekday: 'short',
      day: 'numeric',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit',
    });
    return (
      <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
        {up ? t('schedule.nextUp', { when }) : t('schedule.nextDown', { when })}
      </p>
    );
  };

  return (
    <div className="space-y-3">
      <p className="text-xs text-slate-400 dark:text-slate-500">{t('schedule.hint')}</p>
      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">{t('schedule.timezone')}</span>
        <input
          className={fieldSm}
          placeholder="Europe/Paris"
          value={tz}
          disabled={disabled}
          onChange={(e) => patch({ ...schedule, timezone: e.target.value })}
        />
        {tzInvalid && (
          <p className="mt-0.5 text-xs text-red-600">{t('schedule.invalidTimezone')}</p>
        )}
      </label>
      <div className="grid grid-cols-2 gap-3">
        <CronLinesField
          label={t('schedule.uptime')}
          placeholder={'0 8 * * 1-5'}
          crons={schedule.uptime}
          disabled={disabled}
          onChange={(uptime) => patch({ ...schedule, uptime })}
          status={sideStatus(schedule.uptime, true)}
        />
        <CronLinesField
          label={t('schedule.downtime')}
          placeholder={'0 20 * * *'}
          crons={schedule.downtime}
          disabled={disabled}
          onChange={(downtime) => patch({ ...schedule, downtime })}
          status={sideStatus(schedule.downtime, false)}
        />
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
