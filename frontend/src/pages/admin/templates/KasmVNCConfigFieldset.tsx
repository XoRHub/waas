import { useTranslation } from 'react-i18next';
import { YamlEditor } from '@/components/YamlEditor';
import { validateKasmVNCConfig } from './validate';

/**
 * Template-level kasmvnc.yaml override editor. The caller gates this on
 * the whole protocol list, not the active tab — same guard the webhook
 * enforces ("kasmvncConfig requires a kasmvnc protocol entry"). This
 * edits the admin OVERRIDE layer only: KasmVNC merges it over the image
 * defaults, and the clipboard DLP keys are policy-owned (the webhook
 * rejects them here).
 */
export function KasmVNCConfigFieldset({
  value,
  onChange,
}: {
  value: string;
  onChange: (text: string) => void;
}) {
  const { t } = useTranslation();

  return (
    <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.kasmvncConfig')}
      </legend>
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.kasmvncConfigHint')}{' '}
        <a
          href="https://kasmweb.com/kasmvnc/docs/latest/configuration.html"
          target="_blank"
          rel="noreferrer"
          className="text-blue-600 underline dark:text-blue-400"
        >
          {t('admin.templatesPage.kasmvncConfigDocLink')}
        </a>
      </p>
      <YamlEditor
        value={value}
        onChange={onChange}
        rows={8}
        validate={validateKasmVNCConfig}
        placeholder={t('admin.templatesPage.kasmvncConfigPlaceholder')}
      />
    </fieldset>
  );
}
