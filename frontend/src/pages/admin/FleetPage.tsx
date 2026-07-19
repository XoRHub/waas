import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useAdminDeleteVolume,
  useAdminDeleteWorkspace,
  useAdminRemoteWorkspaces,
  useAdminVolumes,
  useAdminWorkspaces,
} from '@/hooks/useApi';
import { StatusBadge } from '@/components/StatusBadge';
import { formatDateTime } from '@/lib/datetime';
import type { Workspace } from '@/types';

export function FleetPage() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<'workspaces' | 'remote' | 'volumes'>('workspaces');

  const tabClass = (active: boolean) =>
    `rounded-md px-3 py-1.5 text-sm font-medium ${
      active
        ? 'bg-blue-600 text-white'
        : 'text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-700'
    }`;

  return (
    <div className="space-y-4">
      <nav className="flex gap-1">
        <button className={tabClass(tab === 'workspaces')} onClick={() => setTab('workspaces')}>
          {t('admin.fleetPage.tabWorkspaces')}
        </button>
        <button className={tabClass(tab === 'remote')} onClick={() => setTab('remote')}>
          {t('admin.fleetPage.tabRemote')}
        </button>
        <button className={tabClass(tab === 'volumes')} onClick={() => setTab('volumes')}>
          {t('volumes.tab')}
        </button>
      </nav>
      {tab === 'workspaces' && <WorkspacesFleet />}
      {tab === 'remote' && <RemoteFleet />}
      {tab === 'volumes' && <VolumesFleet />}
    </div>
  );
}

// Every fleet tab shows the same thing: rows owned by users. Group them
// per owner, labelled by username (id fallback), sorted alphabetically on
// that label. The admin's own rows form an ordinary group like any other.
function groupByOwner<T extends { ownerId: string; ownerUsername?: string }>(
  items: T[],
): [string, { label: string; items: T[] }][] {
  const byOwner = new Map<string, { label: string; items: T[] }>();
  for (const item of items) {
    const group = byOwner.get(item.ownerId) ?? {
      label: item.ownerUsername || item.ownerId,
      items: [],
    };
    group.items.push(item);
    byOwner.set(item.ownerId, group);
  }
  return [...byOwner.entries()].sort((a, b) => a[1].label.localeCompare(b[1].label));
}

// OwnerGroup is one collapsible per-owner section: username header with a
// row count, table inside. The tables carry no owner column — the header
// already names the owner.
function OwnerGroup({
  label,
  count,
  countKey,
  children,
}: {
  label: string;
  count: number;
  countKey: string;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <details open className="rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <summary className="cursor-pointer px-4 py-3 text-sm font-medium text-slate-700 dark:text-slate-200">
        {label}
        <span className="ml-2 text-xs font-normal text-slate-400 dark:text-slate-500">
          {t(countKey, { count })}
        </span>
      </summary>
      {children}
    </details>
  );
}

// WorkspacesFleet reads the ADMIN route: /api/v1/workspaces is now
// strictly the caller's own rows, whatever the role — only
// /api/v1/admin/workspaces carries the whole fleet (with ownerUsername).
function WorkspacesFleet() {
  const { t } = useTranslation();
  const workspaces = useAdminWorkspaces();
  const remove = useAdminDeleteWorkspace();

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
    <div className="space-y-3">
      {groupByOwner(workspaces.data.data).map(([ownerId, group]) => (
        <OwnerGroup
          key={ownerId}
          label={group.label}
          count={group.items.length}
          countKey="admin.fleetPage.workspaceCount"
        >
          <WorkspaceTable items={group.items} remove={remove} />
        </OwnerGroup>
      ))}
    </div>
  );
}

function WorkspaceTable({
  items,
  remove,
}: {
  items: Workspace[];
  remove: ReturnType<typeof useAdminDeleteWorkspace>;
}) {
  const { t } = useTranslation();
  return (
    <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
          <tr>
            <th className="px-4 py-3">{t('admin.fleetPage.workspace')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.template')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.namespace')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.phase')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.created')}</th>
            <th className="px-4 py-3">{t('app.actions')}</th>
          </tr>
        </thead>
        <tbody className="text-slate-800 dark:text-slate-100">
          {items.map((ws) => (
            <tr
              key={ws.id}
              className="border-b border-slate-100 last:border-0 dark:border-slate-700"
            >
              <td className="px-4 py-3 font-medium">{ws.displayName || ws.name}</td>
              <td className="px-4 py-3">{ws.templateRef}</td>
              {/* Resolved (frozen) workload namespace; empty = platform ns (legacy). */}
              <td className="px-4 py-3 font-mono text-xs">{ws.namespace || '—'}</td>
              <td className="px-4 py-3">
                <StatusBadge phase={ws.phase} />
              </td>
              <td className="px-4 py-3">{formatDateTime(ws.createdAt)}</td>
              <td className="px-4 py-3">
                {/* Admin fleet delete always RETAINS the user's volume:
                    destroying user data needs the volumes tab (audited). */}
                <button
                  onClick={() => remove.mutate({ id: ws.id, keepVolume: true })}
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

function RemoteFleet() {
  const { t } = useTranslation();
  const remotes = useAdminRemoteWorkspaces();

  if (remotes.isPending) {
    return <p className="text-slate-500">{t('app.loading')}</p>;
  }
  if (remotes.isError) {
    return <p className="text-red-600">{t('app.error')}</p>;
  }
  if (remotes.data.data.length === 0) {
    return <p className="text-slate-500 dark:text-slate-400">{t('admin.fleetPage.remoteEmpty')}</p>;
  }

  return (
    <div className="space-y-3">
      {groupByOwner(remotes.data.data).map(([ownerId, group]) => (
        <OwnerGroup
          key={ownerId}
          label={group.label}
          count={group.items.length}
          countKey="admin.fleetPage.workspaceCount"
        >
          <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
                <tr>
                  <th className="px-4 py-3">{t('admin.fleetPage.remoteName')}</th>
                  <th className="px-4 py-3">{t('admin.fleetPage.target')}</th>
                  <th className="px-4 py-3">{t('admin.fleetPage.remoteProtocol')}</th>
                  <th className="px-4 py-3">{t('admin.fleetPage.wol')}</th>
                  <th className="px-4 py-3">{t('admin.fleetPage.state')}</th>
                  <th className="px-4 py-3">{t('admin.fleetPage.lastConnection')}</th>
                </tr>
              </thead>
              <tbody className="text-slate-800 dark:text-slate-100">
                {group.items.map((rw) => (
                  <tr
                    key={rw.id}
                    className="border-b border-slate-100 last:border-0 dark:border-slate-700"
                  >
                    <td className="px-4 py-3 font-medium">{rw.name}</td>
                    <td className="px-4 py-3 font-mono text-xs">
                      {rw.hostname}:{rw.port}
                    </td>
                    <td className="px-4 py-3 uppercase">{rw.protocol}</td>
                    <td className="px-4 py-3 font-mono text-xs">
                      {rw.macAddress || <span className="text-slate-400">—</span>}
                    </td>
                    <td className="px-4 py-3">
                      {rw.activeNow ? (
                        <span className="rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/40 dark:text-green-300">
                          {t('admin.fleetPage.connected')}
                        </span>
                      ) : (
                        <span className="rounded-full bg-slate-200 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-700 dark:text-slate-300">
                          {t('admin.fleetPage.idle')}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {rw.lastConnectedAt ? (
                        formatDateTime(rw.lastConnectedAt)
                      ) : (
                        <span className="text-slate-400">{t('admin.fleetPage.never')}</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </OwnerGroup>
      ))}
    </div>
  );
}

// VolumesFleet: every user's retained volumes; deletion from here is a
// destructive admin action and lands in the audit trail (via=admin).
function VolumesFleet() {
  const { t } = useTranslation();
  const volumes = useAdminVolumes();
  const remove = useAdminDeleteVolume();

  if (volumes.isPending) {
    return <p className="text-slate-500">{t('app.loading')}</p>;
  }
  if (volumes.isError) {
    return <p className="text-red-600">{t('app.error')}</p>;
  }
  if (volumes.data.data.length === 0) {
    return <p className="text-slate-500 dark:text-slate-400">{t('volumes.empty')}</p>;
  }
  return (
    <div className="space-y-3">
      {groupByOwner(volumes.data.data).map(([ownerId, group]) => (
        <OwnerGroup
          key={ownerId}
          label={group.label}
          count={group.items.length}
          countKey="admin.fleetPage.volumeCount"
        >
          <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
                <tr>
                  <th className="px-4 py-3">{t('volumes.name')}</th>
                  <th className="px-4 py-3">{t('admin.fleetPage.namespace')}</th>
                  <th className="px-4 py-3">{t('volumes.size')}</th>
                  <th className="px-4 py-3">{t('volumes.origin')}</th>
                  <th className="px-4 py-3">{t('volumes.retainedAt')}</th>
                  <th className="px-4 py-3">{t('app.actions')}</th>
                </tr>
              </thead>
              <tbody className="text-slate-800 dark:text-slate-100">
                {group.items.map((v) => (
                  <tr
                    key={`${v.namespace}/${v.name}`}
                    className="border-b border-slate-100 last:border-0 dark:border-slate-700"
                  >
                    <td className="px-4 py-3 font-medium">{v.name}</td>
                    <td className="px-4 py-3 font-mono text-xs">{v.namespace}</td>
                    <td className="px-4 py-3">{v.size}</td>
                    <td className="px-4 py-3">{v.originWorkspace || '—'}</td>
                    <td className="px-4 py-3">{formatDateTime(v.retainedAt)}</td>
                    <td className="px-4 py-3">
                      <button
                        onClick={() => {
                          if (window.confirm(t('volumes.deleteVolumeConfirm', { name: v.name }))) {
                            remove.mutate({ namespace: v.namespace, name: v.name });
                          }
                        }}
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
        </OwnerGroup>
      ))}
    </div>
  );
}
