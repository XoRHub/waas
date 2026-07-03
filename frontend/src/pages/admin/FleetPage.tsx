import { useTranslation } from 'react-i18next';
import { useDeleteWorkspace, useWorkspaces } from '@/hooks/useApi';
import { StatusBadge } from '@/components/StatusBadge';

export function FleetPage() {
  const { t } = useTranslation();
  const workspaces = useWorkspaces();
  const remove = useDeleteWorkspace();

  if (workspaces.isPending) {
    return <p className="text-slate-500">{t('app.loading')}</p>;
  }
  if (workspaces.isError) {
    return <p className="text-red-600">{t('app.error')}</p>;
  }
  if (workspaces.data.data.length === 0) {
    return <p className="text-slate-500 dark:text-slate-400">{t('admin.fleetPage.empty')}</p>;
  }

  return (
    <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
          <tr>
            <th className="px-4 py-3">{t('admin.fleetPage.workspace')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.owner')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.template')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.phase')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.created')}</th>
            <th className="px-4 py-3">{t('app.actions')}</th>
          </tr>
        </thead>
        <tbody className="text-slate-800 dark:text-slate-100">
          {workspaces.data.data.map((ws) => (
            <tr key={ws.id} className="border-b border-slate-100 last:border-0 dark:border-slate-700">
              <td className="px-4 py-3 font-medium">{ws.displayName || ws.name}</td>
              <td className="px-4 py-3 font-mono text-xs">{ws.ownerId}</td>
              <td className="px-4 py-3">{ws.templateRef}</td>
              <td className="px-4 py-3">
                <StatusBadge phase={ws.phase} />
              </td>
              <td className="px-4 py-3">{new Date(ws.createdAt).toLocaleString()}</td>
              <td className="px-4 py-3">
                <button
                  onClick={() => remove.mutate(ws.id)}
                  disabled={remove.isPending}
                  className="text-sm text-red-600 hover:underline disabled:opacity-40"
                >
                  {t('app.delete')}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
