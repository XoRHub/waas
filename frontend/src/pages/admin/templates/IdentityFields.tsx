import { useTranslation } from 'react-i18next';
import type { TemplateInput } from '@/hooks/useApi';
import type { DeploymentRecommendation } from '@/types';
import { CatalogImageField } from './CatalogImageField';
import { field } from './fields';

/**
 * Identity + description of the template: the flat top-of-form fields
 * (name/displayName/os/homeSize/storageClass in a two-column grid,
 * then the catalog-assisted image field full-width — its picker and
 * search list don't fit a half column — and the free-text
 * description). Grouped in one section because they all edit flat
 * TemplateInput fields with no logic of their own.
 */
export function IdentityFields({
  input,
  isNew,
  onPatch,
  onApplyRecommendation,
  onArchitectures,
}: {
  input: TemplateInput;
  isNew: boolean;
  onPatch: (patch: Partial<TemplateInput>) => void;
  onApplyRecommendation?: (recommended: DeploymentRecommendation) => void;
  /** See CatalogImageField.onArchitectures. */
  onArchitectures?: (architectures: string[]) => void;
}) {
  const { t } = useTranslation();

  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.name')}
          </span>
          <input
            className={field}
            value={input.name}
            onChange={(e) => onPatch({ name: e.target.value })}
            disabled={!isNew}
            pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?"
            required
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.displayName')}
          </span>
          <input
            className={field}
            value={input.displayName}
            onChange={(e) => onPatch({ displayName: e.target.value })}
            required
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.os')}
          </span>
          <select
            className={field}
            value={input.os}
            onChange={(e) => onPatch({ os: e.target.value })}
          >
            <option value="linux">Linux</option>
            <option value="windows">Windows</option>
          </select>
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.homeSize')}
          </span>
          <input
            className={field}
            value={input.homeSize}
            onChange={(e) => onPatch({ homeSize: e.target.value })}
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.storageClass')}
          </span>
          <input
            className={field}
            value={input.storageClassName ?? ''}
            onChange={(e) => onPatch({ storageClassName: e.target.value })}
            placeholder={t('admin.templatesPage.storageClassDefault')}
          />
        </label>
      </div>

      <CatalogImageField
        image={input.image}
        onChange={(image) => onPatch({ image })}
        onApplyRecommendation={onApplyRecommendation}
        onArchitectures={onArchitectures}
      />

      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">
          {t('admin.templatesPage.logo')}
        </span>
        <input
          className={field}
          value={input.logo ?? ''}
          onChange={(e) => onPatch({ logo: e.target.value })}
          placeholder={t('admin.templatesPage.logoHint')}
        />
      </label>

      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">
          {t('admin.templatesPage.description')}
        </span>
        <textarea
          className={field}
          value={input.description}
          onChange={(e) => onPatch({ description: e.target.value })}
          rows={2}
        />
      </label>
    </>
  );
}
