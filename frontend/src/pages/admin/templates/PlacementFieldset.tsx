import { useTranslation } from 'react-i18next';
import type { NamespacePlaceholder, TemplateInput } from '@/hooks/useApi';
import { fieldSm } from './fields';

/** Workload placement: namespace pattern + cleanup policy. */
export function PlacementFieldset({
  placement,
  placeholders,
  onChange,
}: {
  placement: TemplateInput['placement'];
  placeholders: NamespacePlaceholder[];
  onChange: (placement: TemplateInput['placement']) => void;
}) {
  const { t } = useTranslation();

  return (
    <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.placement')}
      </legend>
      <label className="block">
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('admin.templatesPage.placementPattern')}
        </span>
        <input
          className={`${fieldSm} font-mono`}
          placeholder="waas-{user}"
          value={placement?.namespace ?? ''}
          onChange={(e) => onChange({ ...placement, namespace: e.target.value || undefined })}
        />
        <span className="mt-0.5 block text-xs text-slate-400 dark:text-slate-500">
          {t('admin.templatesPage.placementHint')}
        </span>
      </label>
      {/* Contextual help straight from the naming engine (GET
            /meta/placeholders) — never a hand-maintained copy. */}
      {placeholders.length > 0 && (
        <div className="rounded-md bg-slate-50 p-2 text-xs text-slate-500 dark:bg-slate-700/40 dark:text-slate-400">
          <p className="mb-1 font-medium">{t('admin.templatesPage.placeholdersTitle')}</p>
          <ul className="space-y-0.5">
            {placeholders.map((ph) => (
              <li key={ph.token}>
                <span className="font-mono text-slate-700 dark:text-slate-200">{ph.token}</span> —{' '}
                {ph.description} <span className="text-slate-400">({ph.source})</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <label className="block">
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('admin.templatesPage.placementCleanup')}
        </span>
        <select
          className={fieldSm}
          value={placement?.cleanup ?? ''}
          onChange={(e) => onChange({ ...placement, cleanup: e.target.value || undefined })}
        >
          <option value="">Retain ({t('portal.protocolDefault')})</option>
          <option value="Retain">Retain</option>
          <option value="DeleteWhenEmpty">DeleteWhenEmpty</option>
        </select>
      </label>
    </fieldset>
  );
}
