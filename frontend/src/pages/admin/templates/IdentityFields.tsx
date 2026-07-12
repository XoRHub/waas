import { useTranslation } from 'react-i18next';
import type { TemplateInput } from '@/hooks/useApi';
import { field } from './fields';

/**
 * Identity + description of the template: the flat top-of-form fields
 * (name/displayName/os/image/homeSize/storageClass, then the free-text
 * description). Grouped in one section because they all edit flat
 * TemplateInput fields with no logic of their own.
 */
export function IdentityFields({
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
            {t('admin.templatesPage.image')}
          </span>
          <input
            className={field}
            value={input.image}
            onChange={(e) => onPatch({ image: e.target.value })}
            required
          />
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
