import { useTranslation } from 'react-i18next';
import type { TemplateInput } from '@/hooks/useApi';
import { fieldSm } from './fields';

/** CPU/memory requests and limits (quantity strings, CR passthrough). */
export function ResourcesFieldset({
  requests,
  limits,
  onPatch,
}: {
  requests: TemplateInput['requests'];
  limits: TemplateInput['limits'];
  onPatch: (patch: Partial<TemplateInput>) => void;
}) {
  const { t } = useTranslation();
  const values = { requests, limits };

  return (
    <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.resources')}
      </legend>
      <div className="grid grid-cols-2 gap-3">
        {(['requests', 'limits'] as const).map((kind) => (
          <div key={kind} className="space-y-2">
            <p className="text-xs uppercase text-slate-400">{kind}</p>
            {(['cpu', 'memory'] as const).map((res) => (
              <label key={res} className="block">
                <span className="font-mono text-xs text-slate-500 dark:text-slate-400">{res}</span>
                <input
                  className={fieldSm}
                  value={values[kind]?.[res] ?? ''}
                  placeholder={res === 'cpu' ? '500m' : '1Gi'}
                  onChange={(e) => {
                    const next = { ...values[kind] };
                    if (e.target.value === '') delete next[res];
                    else next[res] = e.target.value;
                    onPatch({ [kind]: next } as Partial<TemplateInput>);
                  }}
                />
              </label>
            ))}
          </div>
        ))}
      </div>
    </fieldset>
  );
}
