import { useTranslation } from 'react-i18next';
import { SkeletonGrid } from '@/components/SkeletonGrid';
import { useDeleteVolume, useVolumes } from '@/hooks/useApi';

// VolumesSection: the user's retained volumes — origin, size, date, and
// deletion (server checks ownership, every deletion is audited).
export function VolumesSection() {
  const { t } = useTranslation();
  const volumes = useVolumes();
  const removeVolume = useDeleteVolume();

  if (volumes.isPending) return <SkeletonGrid count={3} />;
  if (volumes.isError) {
    return <p className="text-sm text-red-600">{t('portal.loadError')}</p>;
  }
  const items = volumes.data.data;
  if (items.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-slate-300 p-10 text-center dark:border-slate-700">
        <p className="text-slate-500 dark:text-slate-400">{t('volumes.empty')}</p>
      </div>
    );
  }
  return (
    <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-200 text-xs uppercase text-slate-500 dark:border-slate-700 dark:text-slate-400">
          <tr>
            <th className="px-4 py-3">{t('volumes.name')}</th>
            <th className="px-4 py-3">{t('volumes.size')}</th>
            <th className="px-4 py-3">{t('volumes.origin')}</th>
            <th className="px-4 py-3">{t('volumes.retainedAt')}</th>
            <th className="px-4 py-3" />
          </tr>
        </thead>
        <tbody>
          {items.map((v) => (
            <tr
              key={`${v.namespace}/${v.name}`}
              className="border-b border-slate-100 last:border-0 dark:border-slate-700/60"
            >
              <td className="px-4 py-3 font-medium text-slate-800 dark:text-slate-100">
                {v.name}
                <span className="ml-2 text-xs font-normal text-slate-400">{v.namespace}</span>
              </td>
              <td className="px-4 py-3 text-slate-600 dark:text-slate-300">{v.size}</td>
              <td className="px-4 py-3 text-slate-600 dark:text-slate-300">
                {v.originWorkspace || '—'}
              </td>
              <td className="px-4 py-3 text-slate-600 dark:text-slate-300">
                {v.retainedAt ? new Date(v.retainedAt).toLocaleString() : '—'}
              </td>
              <td className="px-4 py-3 text-right">
                <button
                  onClick={() => {
                    if (window.confirm(t('volumes.deleteVolumeConfirm', { name: v.name }))) {
                      removeVolume.mutate({ namespace: v.namespace, name: v.name });
                    }
                  }}
                  disabled={removeVolume.isPending}
                  className="rounded-md border border-slate-300 px-3 py-1 text-sm text-red-600 hover:bg-red-50 disabled:opacity-40 dark:border-slate-600 dark:hover:bg-slate-700"
                >
                  {t('app.delete')}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="px-4 py-3 text-xs text-slate-400 dark:text-slate-500">
        {t('volumes.quotaNote')}
      </p>
    </div>
  );
}
