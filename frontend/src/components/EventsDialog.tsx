import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import { useWorkspaceEvents } from '@/hooks/useApi';
import type { WorkspaceEvent } from '@/types';

/** Compact relative age ("3m", "2h", "5d") for the events table. */
export function eventAge(lastSeen: string, now: Date = new Date()): string {
  const seconds = Math.max(0, Math.floor((now.getTime() - new Date(lastSeen).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

/**
 * The ArgoCD-style events panel of one workspace: the CR's lifecycle
 * milestones (operator) interleaved with the children's Kubernetes
 * events (scheduling, image pulls, probes…), newest first. The list is
 * aggregated and authorized server-side; the refresh cadence is
 * server-driven too (pollIntervalSeconds, WAAS_EVENTS_POLL_INTERVAL).
 */
export function EventsDialog({
  workspaceId,
  title,
  onClose,
}: {
  workspaceId: string;
  title: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const query = useWorkspaceEvents(workspaceId);
  const events: WorkspaceEvent[] = query.data?.data.events ?? [];

  return (
    <Dialog
      title={t('portal.events.title', { name: title })}
      onClose={onClose}
      maxWidth="max-w-3xl"
      footer={
        <button
          type="button"
          onClick={onClose}
          className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
        >
          {t('app.close')}
        </button>
      }
    >
      {query.isLoading && (
        <p className="py-6 text-center text-sm text-slate-400">{t('app.loading')}</p>
      )}
      {query.isError && (
        <p className="py-6 text-center text-sm text-red-600">{query.error.message}</p>
      )}
      {!query.isLoading && !query.isError && events.length === 0 && (
        <p className="py-6 text-center text-sm text-slate-400">{t('portal.events.empty')}</p>
      )}
      {events.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="text-xs uppercase text-slate-400">
              <tr>
                <th className="py-2 pr-3">{t('portal.events.type')}</th>
                <th className="py-2 pr-3">{t('portal.events.reason')}</th>
                <th className="py-2 pr-3">{t('portal.events.message')}</th>
                <th className="py-2 pr-3">{t('portal.events.object')}</th>
                <th className="py-2 pr-3 text-right">{t('portal.events.age')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-700">
              {events.map((ev, i) => (
                <tr key={`${ev.objectKind}-${ev.objectName}-${ev.reason}-${i}`} className="align-top">
                  <td className="py-2 pr-3">
                    <span
                      className={
                        ev.type === 'Warning'
                          ? 'rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/50 dark:text-amber-200'
                          : 'rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600 dark:bg-slate-700 dark:text-slate-300'
                      }
                    >
                      {ev.type}
                    </span>
                  </td>
                  <td className="whitespace-nowrap py-2 pr-3 font-medium text-slate-700 dark:text-slate-200">
                    {ev.reason}
                  </td>
                  <td className="py-2 pr-3 text-slate-600 dark:text-slate-300">{ev.message}</td>
                  <td className="whitespace-nowrap py-2 pr-3 text-xs text-slate-400">
                    {ev.objectKind}/{ev.objectName}
                  </td>
                  <td className="whitespace-nowrap py-2 pr-3 text-right text-xs text-slate-400">
                    {eventAge(ev.lastSeen)}
                    {ev.count > 1 && ` ×${ev.count}`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Dialog>
  );
}
