import { useTranslation } from 'react-i18next';
import { useAuditLogs } from '@/hooks/useApi';

export function AuditPage() {
  const { t } = useTranslation();
  const audit = useAuditLogs();

  if (audit.isPending) {
    return <p className="text-slate-500">{t('app.loading')}</p>;
  }
  if (audit.isError) {
    return <p className="text-red-600">{t('app.error')}</p>;
  }
  if (audit.data.data.length === 0) {
    return <p className="text-slate-500 dark:text-slate-400">{t('admin.auditPage.empty')}</p>;
  }

  return (
    <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
          <tr>
            <th className="px-4 py-3">{t('admin.auditPage.when')}</th>
            <th className="px-4 py-3">{t('admin.auditPage.actor')}</th>
            <th className="px-4 py-3">{t('admin.auditPage.action')}</th>
            <th className="px-4 py-3">{t('admin.auditPage.resource')}</th>
            <th className="px-4 py-3">{t('admin.auditPage.ip')}</th>
          </tr>
        </thead>
        <tbody className="text-slate-800 dark:text-slate-100">
          {audit.data.data.map((entry) => (
            <tr
              key={entry.id}
              className="border-b border-slate-100 last:border-0 dark:border-slate-700"
            >
              <td className="px-4 py-3 whitespace-nowrap">
                {new Date(entry.occurredAt).toLocaleString()}
              </td>
              <td className="px-4 py-3">{entry.actorUsername || entry.actorId || '—'}</td>
              <td className="px-4 py-3 font-mono text-xs">{entry.action}</td>
              <td className="px-4 py-3">
                {entry.resourceType}
                {entry.detail ? ` · ${entry.detail}` : ''}
              </td>
              <td className="px-4 py-3">{entry.clientIp || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
