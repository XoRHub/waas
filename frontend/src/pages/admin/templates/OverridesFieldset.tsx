import { useTranslation } from 'react-i18next';
import type { OverrideFieldMeta, TemplateInput } from '@/hooks/useApi';
import { fieldSm } from './fields';

/**
 * User-override delegation: which fields workspace owners may override,
 * plus the optional owner expression. The checkbox list comes from the
 * server registry (single source shared with the policy editor and the
 * enforcement) — no local copy of the field list.
 */
export function OverridesFieldset({
  overrides,
  fields,
  onChange,
}: {
  overrides: TemplateInput['overrides'];
  fields: OverrideFieldMeta[];
  onChange: (overrides: TemplateInput['overrides']) => void;
}) {
  const { t } = useTranslation();

  return (
    <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.overrides')}
      </legend>
      <p className="mb-2 text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.overridesHint')}
      </p>
      <div className="grid grid-cols-3 gap-1.5">
        {fields.map(({ name: f, description }) => (
          <label
            key={f}
            title={description}
            className="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300"
          >
            <input
              type="checkbox"
              checked={overrides?.allowedFields?.includes(f) ?? false}
              onChange={(e) => {
                const next = new Set(overrides?.allowedFields ?? []);
                if (e.target.checked) next.add(f);
                else next.delete(f);
                onChange({ ...overrides, allowedFields: [...next] });
              }}
            />
            <span className="font-mono text-xs">{f}</span>
          </label>
        ))}
      </div>
      <label className="mt-2 block">
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('admin.templatesPage.overridesOwner')}
        </span>
        <input
          className={fieldSm}
          value={overrides?.owner ?? ''}
          onChange={(e) => onChange({ ...overrides, owner: e.target.value })}
        />
      </label>
    </fieldset>
  );
}
