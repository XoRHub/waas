import { useTranslation } from 'react-i18next';
import { KeyValueEditor } from '@/components/KeyValueEditor';
import type { TemplateInput } from '@/hooks/useApi';

/**
 * Home PVC metadata (labels/annotations) — typically Longhorn
 * recurring-job enrollment. Size, storage class and mount path stay in
 * IdentityFields; this block only shapes metadata. An entirely empty
 * block leaves the CR (undefined, never a persisted {}).
 */
export function HomeVolumeFieldset({
  homeVolume,
  onChange,
}: {
  homeVolume: TemplateInput['homeVolume'];
  onChange: (homeVolume: TemplateInput['homeVolume']) => void;
}) {
  const { t } = useTranslation();

  const patch = (side: 'labels' | 'annotations', m: Record<string, string>) => {
    const next = {
      ...homeVolume,
      [side]: Object.keys(m).length > 0 ? m : undefined,
    };
    onChange(next.labels || next.annotations ? next : undefined);
  };

  return (
    <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.homeVolume')}
      </legend>
      <div>
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('admin.templatesPage.homeVolumeLabels')}
        </span>
        <div className="mt-1">
          <KeyValueEditor
            value={homeVolume?.labels ?? {}}
            onChange={(m) => patch('labels', m)}
            keyPlaceholder={t('admin.templatesPage.placementMetaKey')}
            valuePlaceholder={t('admin.templatesPage.placementMetaValue')}
            addLabel={t('admin.templatesPage.placementAddLabel')}
          />
        </div>
      </div>
      <div>
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('admin.templatesPage.homeVolumeAnnotations')}
        </span>
        <div className="mt-1">
          <KeyValueEditor
            value={homeVolume?.annotations ?? {}}
            onChange={(m) => patch('annotations', m)}
            keyPlaceholder={t('admin.templatesPage.placementMetaKey')}
            valuePlaceholder={t('admin.templatesPage.placementMetaValue')}
            addLabel={t('admin.templatesPage.placementAddAnnotation')}
          />
        </div>
      </div>
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.homeVolumeHint')}
      </p>
    </fieldset>
  );
}
