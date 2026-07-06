import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuditLogs } from '@/hooks/useApi';

const PAGE_SIZE = 25;

interface Filters {
  actor: string;
  action: string;
  from: string;
  to: string;
}

const emptyFilters: Filters = { actor: '', action: '', from: '', to: '' };

export function AuditPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [filters, setFilters] = useState<Filters>(emptyFilters);
  const audit = useAuditLogs({ page, pageSize: PAGE_SIZE, ...filters });

  // Any filter change restarts from page 1; the filters themselves are
  // kept across page moves (they live in the same query).
  const setFilter = (patch: Partial<Filters>) => {
    setFilters((f) => ({ ...f, ...patch }));
    setPage(1);
  };

  if (audit.isPending) {
    return <p className="text-slate-500">{t('app.loading')}</p>;
  }
  if (audit.isError) {
    return <p className="text-red-600">{t('app.error')}</p>;
  }

  const entries = audit.data.data;
  const total = audit.data.meta?.total ?? entries.length;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const hasFilters = Boolean(filters.actor || filters.action || filters.from || filters.to);

  const inputClass =
    'rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm text-slate-800 ' +
    'placeholder:text-slate-400 focus:outline-none focus:ring-2 focus:ring-indigo-500 ' +
    'dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100';

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <input
          className={inputClass}
          placeholder={t('admin.auditPage.filterActor')}
          value={filters.actor}
          onChange={(e) => setFilter({ actor: e.target.value })}
        />
        <input
          className={inputClass}
          placeholder={t('admin.auditPage.filterAction')}
          value={filters.action}
          onChange={(e) => setFilter({ action: e.target.value })}
        />
        <label className="flex items-center gap-1 text-sm text-slate-500 dark:text-slate-400">
          {t('admin.auditPage.filterFrom')}
          <input
            type="date"
            className={inputClass}
            value={filters.from}
            onChange={(e) => setFilter({ from: e.target.value })}
          />
        </label>
        <label className="flex items-center gap-1 text-sm text-slate-500 dark:text-slate-400">
          {t('admin.auditPage.filterTo')}
          <input
            type="date"
            className={inputClass}
            value={filters.to}
            onChange={(e) => setFilter({ to: e.target.value })}
          />
        </label>
        {hasFilters && (
          <button
            type="button"
            className="text-sm text-indigo-600 hover:underline dark:text-indigo-400"
            onClick={() => setFilter(emptyFilters)}
          >
            {t('admin.auditPage.clearFilters')}
          </button>
        )}
      </div>

      {entries.length === 0 ? (
        <p className="text-slate-500 dark:text-slate-400">
          {hasFilters ? t('admin.auditPage.noMatch') : t('admin.auditPage.empty')}
        </p>
      ) : (
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
              {entries.map((entry) => (
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
      )}

      <div className="flex items-center justify-between text-sm text-slate-500 dark:text-slate-400">
        <span>{t('admin.auditPage.pageOf', { page, pages, total })}</span>
        <div className="flex gap-2">
          <button
            type="button"
            disabled={page <= 1}
            className="rounded-lg border border-slate-200 px-3 py-1.5 disabled:opacity-40 dark:border-slate-700"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
          >
            {t('admin.auditPage.prev')}
          </button>
          <button
            type="button"
            disabled={page >= pages}
            className="rounded-lg border border-slate-200 px-3 py-1.5 disabled:opacity-40 dark:border-slate-700"
            onClick={() => setPage((p) => Math.min(pages, p + 1))}
          >
            {t('admin.auditPage.next')}
          </button>
        </div>
      </div>
    </div>
  );
}
