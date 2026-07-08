import { useTranslation } from 'react-i18next';
import { useQuota } from '@/hooks/useApi';

export function QuotaBanner() {
  const { t } = useTranslation();
  const quota = useQuota();
  if (!quota.isSuccess || !quota.data.data.policy) return null;
  const q = quota.data.data;

  return (
    <div className="mb-5 flex flex-wrap items-center gap-x-6 gap-y-1 rounded-xl bg-white px-5 py-3 text-sm shadow-sm dark:bg-slate-800">
      <span className="text-slate-500 dark:text-slate-400">
        {t('portal.quotaPolicy')}{' '}
        <span className="font-medium text-slate-800 dark:text-slate-100">{q.policy}</span>
      </span>
      {q.maxWorkspaces != null && (
        <span className="text-slate-500 dark:text-slate-400">
          {t('portal.quotaWorkspaces')}{' '}
          <span className="font-medium text-slate-800 dark:text-slate-100">
            {q.usedWorkspaces}/{q.maxWorkspaces}
          </span>
        </span>
      )}
      {q.limits?.memory && (
        <span className="text-slate-500 dark:text-slate-400">
          RAM{' '}
          <span className="font-medium text-slate-800 dark:text-slate-100">
            {q.used?.memory ?? '0'} / {q.limits.memory}
          </span>
        </span>
      )}
      {q.limits?.cpu && (
        <span className="text-slate-500 dark:text-slate-400">
          CPU{' '}
          <span className="font-medium text-slate-800 dark:text-slate-100">
            {q.used?.cpu ?? '0'} / {q.limits.cpu}
          </span>
        </span>
      )}
      {/* Storage used/limit is the SERVER's number (same computation as
          the admission enforcement), retained volumes included. */}
      {q.limits?.storage && (
        <span className="text-slate-500 dark:text-slate-400">
          {t('portal.quotaStorage')}{' '}
          <span className="font-medium text-slate-800 dark:text-slate-100">
            {q.used?.storage ?? '0'} / {q.limits.storage}
          </span>
          {(q.retainedVolumes ?? 0) > 0 && (
            <span className="ml-1 text-xs">
              (
              {t('portal.quotaStorageRetained', {
                size: q.retainedStorage,
                count: q.retainedVolumes,
              })}
              )
            </span>
          )}
        </span>
      )}
    </div>
  );
}
