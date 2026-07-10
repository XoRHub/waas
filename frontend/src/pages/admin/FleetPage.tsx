import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useAdminDeleteVolume,
  useAdminRemoteWorkspaces,
  useAdminVolumes,
  useDeleteWorkspace,
  useWorkspaces,
} from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';
import { StatusBadge } from '@/components/StatusBadge';
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
    <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
          <tr>
            <th className="px-4 py-3">{t('volumes.name')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.namespace')}</th>
            <th className="px-4 py-3">{t('volumes.size')}</th>
            <th className="px-4 py-3">{t('volumes.owner')}</th>
            <th className="px-4 py-3">{t('volumes.origin')}</th>
            <th className="px-4 py-3">{t('volumes.retainedAt')}</th>
            <th className="px-4 py-3">{t('app.actions')}</th>
          </tr>
        </thead>
        <tbody className="text-slate-800 dark:text-slate-100">
          {volumes.data.data.map((v) => (
            <tr
              key={`${v.namespace}/${v.name}`}
              className="border-b border-slate-100 last:border-0 dark:border-slate-700"
            >
              <td className="px-4 py-3 font-medium">{v.name}</td>
              <td className="px-4 py-3 font-mono text-xs">{v.namespace}</td>
              <td className="px-4 py-3">{v.size}</td>
              <td className="px-4 py-3 font-mono text-xs">{v.ownerId}</td>
              <td className="px-4 py-3">{v.originWorkspace || '—'}</td>
              <td className="px-4 py-3">
                {v.retainedAt ? new Date(v.retainedAt).toLocaleString() : '—'}
              </td>
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
  );
}

function WorkspacesFleet() {
  const { t } = useTranslation();
  const workspaces = useWorkspaces();
  const remove = useDeleteWorkspace();
  const user = useAuthStore((s) => s.user);
  const [view, setView] = useState<'mine' | 'byUser'>('mine');

  const viewClass = (active: boolean) =>
    `rounded-md px-3 py-1 text-sm ${
      active
        ? 'bg-slate-200 font-medium text-slate-800 dark:bg-slate-700 dark:text-slate-100'
        : 'text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-700/50'
    }`;

  if (workspaces.isPending) {
    return <p className="text-slate-500">{t('app.loading')}</p>;
  }
  if (workspaces.isError) {
    return <p className="text-red-600">{t('app.error')}</p>;
  }
  if (workspaces.data.data.length === 0) {
    return <p className="text-slate-500 dark:text-slate-400">{t('admin.fleetPage.empty')}</p>;
  }

  const mine = workspaces.data.data.filter((ws) => ws.ownerId === user?.id);
  const others = workspaces.data.data.filter((ws) => ws.ownerId !== user?.id);

  // One group per owner, labelled by username (id fallback, same rule as
  // RemoteFleet), sorted alphabetically on that label.
  const byOwner = new Map<string, { label: string; items: Workspace[] }>();
  for (const ws of others) {
    const group = byOwner.get(ws.ownerId) ?? {
      label: ws.ownerUsername || ws.ownerId,
      items: [],
    };
    group.items.push(ws);
    byOwner.set(ws.ownerId, group);
  }
  const groups = [...byOwner.entries()].sort((a, b) => a[1].label.localeCompare(b[1].label));

  return (
    <div className="space-y-3">
      <nav className="flex gap-1">
        <button className={viewClass(view === 'mine')} onClick={() => setView('mine')}>
          {t('admin.fleetPage.myWorkspaces')}
        </button>
        <button className={viewClass(view === 'byUser')} onClick={() => setView('byUser')}>
          {t('admin.fleetPage.byUser')}
        </button>
      </nav>
      {view === 'mine' &&
        (mine.length === 0 ? (
          <p className="text-slate-500 dark:text-slate-400">{t('admin.fleetPage.myEmpty')}</p>
        ) : (
          <WorkspaceTable items={mine} remove={remove} />
        ))}
      {view === 'byUser' &&
        (groups.length === 0 ? (
          <p className="text-slate-500 dark:text-slate-400">{t('admin.fleetPage.byUserEmpty')}</p>
        ) : (
          groups.map(([ownerId, group]) => (
            <details
              key={ownerId}
              open
              className="rounded-xl bg-white shadow-sm dark:bg-slate-800"
            >
              <summary className="cursor-pointer px-4 py-3 text-sm font-medium text-slate-700 dark:text-slate-200">
                {group.label}
                <span className="ml-2 text-xs font-normal text-slate-400 dark:text-slate-500">
                  {t('admin.fleetPage.workspaceCount', { count: group.items.length })}
                </span>
              </summary>
              <WorkspaceTable items={group.items} remove={remove} />
            </details>
          ))
        ))}
    </div>
  );
}

// Shared table for both fleet views. No owner column: in "mine" every row
// belongs to the signed-in admin, in "by user" the group header already
// names the owner — repeating it per row is pure noise.
function WorkspaceTable({
  items,
  remove,
}: {
  items: Workspace[];
  remove: ReturnType<typeof useDeleteWorkspace>;
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
              <td className="px-4 py-3">{new Date(ws.createdAt).toLocaleString()}</td>
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
    <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
          <tr>
            <th className="px-4 py-3">{t('admin.fleetPage.remoteName')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.owner')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.target')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.remoteProtocol')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.wol')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.state')}</th>
            <th className="px-4 py-3">{t('admin.fleetPage.lastConnection')}</th>
          </tr>
        </thead>
        <tbody className="text-slate-800 dark:text-slate-100">
          {remotes.data.data.map((rw) => (
            <tr
              key={rw.id}
              className="border-b border-slate-100 last:border-0 dark:border-slate-700"
            >
              <td className="px-4 py-3 font-medium">{rw.name}</td>
              <td className="px-4 py-3">{rw.ownerUsername || rw.ownerId}</td>
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
                  new Date(rw.lastConnectedAt).toLocaleString()
                ) : (
                  <span className="text-slate-400">{t('admin.fleetPage.never')}</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
