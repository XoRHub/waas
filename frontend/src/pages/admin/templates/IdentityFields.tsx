import { useTranslation } from 'react-i18next';
import type { TemplateInput } from '@/hooks/useApi';
import type { DeploymentRecommendation } from '@/types';
import { CatalogImageField } from './CatalogImageField';
import { field } from './fields';

/**
 * Name + display name: the always-visible identity header ABOVE the
 * dialog's section tabs. Both are required — kept out of any tab so
 * native validation always reaches a visible control.
 */
export function IdentityHeader({
  input,
  isNew,
  onPatch,
}: {
  input: TemplateInput;
  isNew: boolean;
  onPatch: (patch: Partial<TemplateInput>) => void;
}) {
  const { t } = useTranslation();

  return (
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
    </div>
  );
}

/**
 * The General section of the template editor: OS/storage in a
 * two-column grid, then the catalog-assisted image field full-width —
 * its picker and search list don't fit a half column — and the
 * free-text description. Grouped in one section because they all edit
 * flat TemplateInput fields with no logic of their own.
 */
export function IdentityFields({
  input,
  onPatch,
  onApplyRecommendation,
  onArchitectures,
}: {
  input: TemplateInput;
  onPatch: (patch: Partial<TemplateInput>) => void;
  /** See CatalogImageField.onApplyRecommendation. */
  onApplyRecommendation?: (recommended: DeploymentRecommendation, imageProtocols: string[]) => void;
  /** See CatalogImageField.onArchitectures. */
  onArchitectures?: (architectures: string[]) => void;
}) {
  const { t } = useTranslation();

  return (
    <>
      <div className="grid grid-cols-2 gap-3">
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
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.homeMountPath')}
          </span>
          <input
            className={`${field} font-mono`}
            value={input.homeMountPath ?? ''}
            onChange={(e) => onPatch({ homeMountPath: e.target.value || undefined })}
            placeholder="/home/waas_user"
          />
          <span className="mt-0.5 block text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.homeMountPathHint')}
          </span>
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
