import { useTranslation } from 'react-i18next';
import { YamlEditor } from '@/components/YamlEditor';
import { validateWorkload } from './validate';

/**
 * Advanced workload editor. Edited as YAML text; TemplateDialog owns
 * the text state and converts it back to the structured value at
 * submit time. Lives in its own Workspace › Workload tab — the tab IS
 * the disclosure, so the editor renders directly.
 */
export function WorkloadSection({
  text,
  onChange,
  error,
}: {
  text: string;
  onChange: (text: string) => void;
  error: string;
}) {
  const { t } = useTranslation();

  return (
    <div className="space-y-2">
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.workloadHint')}
      </p>
      <YamlEditor value={text} onChange={onChange} rows={8} validate={validateWorkload} />
      {error && <p className="text-sm text-red-600">{error}</p>}
    </div>
  );
}
