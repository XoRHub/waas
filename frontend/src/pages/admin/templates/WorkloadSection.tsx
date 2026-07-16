import { useTranslation } from 'react-i18next';
import { YamlEditor } from '@/components/YamlEditor';
import { validateWorkload } from './validate';

/**
 * Advanced workload editor (collapsed by default). Edited as YAML text;
 * TemplateDialog owns the text state and converts it back to the
 * structured value at submit time.
 *
 * `open` is a controlled prop (default collapsed) so an explicit
 * "apply catalog recommendation" action can expand the section to show
 * what it just injected — `onToggle` keeps it in sync when the admin
 * collapses/expands it manually afterward.
 */
export function WorkloadSection({
  text,
  onChange,
  error,
  open = false,
  onToggle,
}: {
  text: string;
  onChange: (text: string) => void;
  error: string;
  open?: boolean;
  onToggle?: (open: boolean) => void;
}) {
  const { t } = useTranslation();

  return (
    <details
      open={open}
      onToggle={(e) => onToggle?.(e.currentTarget.open)}
      className="rounded-lg border border-slate-200 p-3 dark:border-slate-700"
    >
      <summary className="cursor-pointer text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.workload')}
      </summary>
      <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.workloadHint')}
      </p>
      <div className="mt-2">
        <YamlEditor value={text} onChange={onChange} rows={8} validate={validateWorkload} />
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
    </details>
  );
}
