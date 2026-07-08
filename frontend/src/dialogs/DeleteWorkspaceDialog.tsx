import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import type { Workspace } from '@/types';

// DeleteWorkspaceDialog: deletion always asks what happens to the home
// volume. Keeping it is the default; deletion is the explicit opt-in the
// server requires (no volume is ever deleted silently).
export function DeleteWorkspaceDialog({
  workspace,
  pending,
  onConfirm,
  onClose,
}: {
  workspace: Workspace;
  pending: boolean;
  onConfirm: (keepVolume: boolean) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [keepVolume, setKeepVolume] = useState(true);
  const vol = workspace.homeVolume;

  return (
    <Dialog
      title={t('volumes.deleteWorkspaceTitle', {
        name: workspace.displayName || workspace.name,
      })}
      onClose={onClose}
      onSubmit={(e) => {
        e.preventDefault();
        onConfirm(keepVolume);
      }}
      footer={
        <>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            type="submit"
            disabled={pending}
            className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40"
          >
            {t('app.delete')}
          </button>
        </>
      }
    >
      {vol ? (
        <div className="space-y-3 text-sm text-slate-600 dark:text-slate-300">
          <p>
            ⚠{' '}
            {t('volumes.deleteWarning', {
              volume: vol.name,
              size: vol.size ? ` (${vol.size})` : '',
            })}
          </p>
          <label className="flex items-start gap-2 rounded-md border border-slate-200 p-3 dark:border-slate-700">
            <input
              type="radio"
              name="volume-choice"
              checked={keepVolume}
              onChange={() => setKeepVolume(true)}
              className="mt-0.5"
            />
            <span>
              <span className="font-medium text-slate-800 dark:text-slate-100">
                {t('volumes.keepChoice')}
              </span>
              <br />
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {t('volumes.keepChoiceHint')}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2 rounded-md border border-slate-200 p-3 dark:border-slate-700">
            <input
              type="radio"
              name="volume-choice"
              checked={!keepVolume}
              onChange={() => setKeepVolume(false)}
              className="mt-0.5"
            />
            <span>
              <span className="font-medium text-red-700 dark:text-red-400">
                {t('volumes.deleteChoice')}
              </span>
              <br />
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {t('volumes.deleteChoiceHint')}
              </span>
            </span>
          </label>
        </div>
      ) : (
        <p className="text-sm text-slate-600 dark:text-slate-300">{t('portal.deleteConfirm')}</p>
      )}
    </Dialog>
  );
}
