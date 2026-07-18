import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import type { Workspace } from '@/types';

// RunningLimitDialog: the running-workspace quota is full but creating
// remains possible — paused (no slot needed) or after freeing a slot by
// pausing a sibling. Convenience only: the admission webhook stays the
// judge, a race with another tab surfaces as the usual server denial.
export function RunningLimitDialog({
  running,
  max,
  workspaces,
  pending,
  error,
  onConfirm,
  onClose,
}: {
  running: number;
  max: number;
  /** The user's currently running workspaces (candidates to pause). */
  workspaces: Workspace[];
  pending: boolean;
  error?: string;
  onConfirm: (choice: { paused: true } | { paused: false; pauseId: string }) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<'paused' | 'pauseFirst'>('paused');
  const [pauseId, setPauseId] = useState(workspaces[0]?.id ?? '');

  return (
    <Dialog
      title={t('portal.runningLimit.title')}
      onClose={onClose}
      onSubmit={(e) => {
        e.preventDefault();
        onConfirm(mode === 'paused' ? { paused: true } : { paused: false, pauseId });
      }}
      footer={
        <>
          {error && <p className="mr-auto text-sm text-red-600">{error}</p>}
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            type="submit"
            disabled={pending || (mode === 'pauseFirst' && !pauseId)}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('portal.runningLimit.confirm')}
          </button>
        </>
      }
    >
      <div className="space-y-3 text-sm text-slate-600 dark:text-slate-300">
        <p>{t('portal.runningLimit.explain', { running, max })}</p>
        <label className="flex items-start gap-2 rounded-md border border-slate-200 p-3 dark:border-slate-700">
          <input
            type="radio"
            name="running-limit-choice"
            checked={mode === 'paused'}
            onChange={() => setMode('paused')}
            className="mt-0.5"
          />
          <span>
            <span className="font-medium text-slate-800 dark:text-slate-100">
              {t('portal.runningLimit.createPaused')}
            </span>
            <br />
            <span className="text-xs text-slate-500 dark:text-slate-400">
              {t('portal.runningLimit.createPausedHint')}
            </span>
          </span>
        </label>
        <label className="flex items-start gap-2 rounded-md border border-slate-200 p-3 dark:border-slate-700">
          <input
            type="radio"
            name="running-limit-choice"
            checked={mode === 'pauseFirst'}
            onChange={() => setMode('pauseFirst')}
            className="mt-0.5"
            disabled={workspaces.length === 0}
          />
          <span className="flex-1">
            <span className="font-medium text-slate-800 dark:text-slate-100">
              {t('portal.runningLimit.pauseFirst')}
            </span>
            <br />
            <span className="text-xs text-slate-500 dark:text-slate-400">
              {t('portal.runningLimit.pauseFirstHint')}
            </span>
            {mode === 'pauseFirst' && workspaces.length > 0 && (
              <select
                value={pauseId}
                onChange={(e) => setPauseId(e.target.value)}
                className="mt-2 block w-full rounded-md border border-slate-300 bg-white px-2 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-800 dark:text-slate-200"
              >
                {workspaces.map((ws) => (
                  <option key={ws.id} value={ws.id}>
                    {ws.displayName || ws.name}
                  </option>
                ))}
              </select>
            )}
          </span>
        </label>
      </div>
    </Dialog>
  );
}
