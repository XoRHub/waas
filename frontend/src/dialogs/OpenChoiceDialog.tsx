import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';

// First-open dialog: where should workspaces open? Optionally persisted
// as a profile preference (editable later from the profile page).
export function OpenChoiceDialog({
  onChoice,
  onClose,
}: {
  onChoice: (newTab: boolean, remember: boolean) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [remember, setRemember] = useState(true);

  return (
    <Dialog
      title={t('portal.openWhere')}
      onClose={onClose}
      maxWidth="max-w-sm"
      footer={
        <button
          onClick={onClose}
          className="text-sm text-slate-500 hover:underline dark:text-slate-400"
        >
          {t('app.cancel')}
        </button>
      }
    >
      <p className="text-sm text-slate-500 dark:text-slate-400">{t('portal.openWhereHint')}</p>
      <div className="grid grid-cols-2 gap-2">
        <button
          onClick={() => onChoice(false, remember)}
          className="rounded-md border border-slate-300 px-3 py-2 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
        >
          {t('portal.openSameTab')}
        </button>
        <button
          onClick={() => onChoice(true, remember)}
          className="rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('portal.openNewTab')}
        </button>
      </div>
      <label className="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
        <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
        {t('portal.rememberChoice')}
      </label>
    </Dialog>
  );
}
