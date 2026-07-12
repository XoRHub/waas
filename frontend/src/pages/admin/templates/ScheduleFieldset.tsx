import { useTranslation } from 'react-i18next';
import { ScheduleEditor } from '@/components/ScheduleEditor';
import type { TemplateInput } from '@/hooks/useApi';

/** Uptime/downtime schedule — a thin fieldset around ScheduleEditor. */
export function ScheduleFieldset({
  value,
  onChange,
}: {
  value: TemplateInput['schedule'];
  onChange: (schedule: TemplateInput['schedule']) => void;
}) {
  const { t } = useTranslation();

  return (
    <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('schedule.title')}
      </legend>
      <ScheduleEditor value={value} onChange={onChange} />
    </fieldset>
  );
}
