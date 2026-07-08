import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  useDeleteRemoteWorkspace,
  useDeleteVolume,
  useDeleteWorkspace,
  useQuota,
  useVolumes,
  useRemoteWorkspaces,
  useWakeRemoteWorkspace,
  useUpdateProfile,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { UserMenu } from '@/components/UserMenu';
import { EventsDialog } from '@/components/EventsDialog';
import { ConnectionSettingsDialog } from '@/dialogs/ConnectionSettingsDialog';
import { CreateWorkspaceDialog } from '@/dialogs/CreateWorkspaceDialog';
import { DeleteWorkspaceDialog } from '@/dialogs/DeleteWorkspaceDialog';
import { RemoteWorkspaceDialog } from '@/dialogs/RemoteWorkspaceDialog';
import { OpenChoiceDialog } from '@/dialogs/OpenChoiceDialog';
import { useNextTransitionLabel } from '@/components/ScheduleEditor';
import { FolderedGrid, SessionCard } from '@/components/SessionCard';
import { useAuthStore } from '@/stores/authStore';
import { useEvents } from '@/hooks/useEvents';
import { effectivePhase } from '@/lib/lifecycle';
import { targetFromRemote, targetFromWorkspace } from '@/lib/target';
import type { RemoteWorkspace, Workspace } from '@/types';

export function PortalPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const quota = useQuota();
  useEvents(); // live card updates (SSE), polling as fallback
  const [creating, setCreating] = useState(false);
  // Remote create/edit state lives here so the primary action button
  // stays in the same header slot whatever the active tab.
  const [remoteEditing, setRemoteEditing] = useState<RemoteWorkspace | 'new' | null>(null);
  const [tab, setTab] = useState<'workspaces' | 'remote' | 'volumes'>('workspaces');

  // Remote Workspaces is policy-gated: the tab only exists when the
  // resolved policy (or the admin role) opts the user in.
  const remoteEnabled = quota.data?.data.features?.remoteWorkspaces ?? false;
  const activeTab = tab === 'remote' && !remoteEnabled ? 'workspaces' : tab;

  const tabClass = (active: boolean) =>
    `rounded-md px-3 py-1.5 text-sm font-medium ${
      active
        ? 'bg-blue-600 text-white'
        : 'text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-700'
    }`;

  return (
    <div className="min-h-screen bg-slate-100 dark:bg-slate-900">
      <header className="flex items-center justify-between bg-white px-6 py-4 shadow-sm dark:bg-slate-800">
        <div className="flex items-center gap-6">
          <h1 className="text-lg font-semibold text-slate-900 dark:text-white">
            {t('portal.title')}
          </h1>
          <nav className="flex gap-1">
            <button
              className={tabClass(activeTab === 'workspaces')}
              onClick={() => setTab('workspaces')}
            >
              {t('portal.tabWorkspaces')}
            </button>
            {remoteEnabled && (
              <button className={tabClass(activeTab === 'remote')} onClick={() => setTab('remote')}>
                {t('remote.tab')}
              </button>
            )}
            <button className={tabClass(activeTab === 'volumes')} onClick={() => setTab('volumes')}>
              {t('volumes.tab')}
            </button>
          </nav>
        </div>
        <div className="flex items-center gap-4">
          <button
            onClick={() => navigate('/view')}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {t('portal.splitView')}
          </button>
          {/* Single primary action, same slot on every tab. */}
          <button
            onClick={() => (activeTab === 'remote' ? setRemoteEditing('new') : setCreating(true))}
            className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
          >
            {activeTab === 'remote' ? t('remote.add') : t('portal.newWorkspace')}
          </button>
          <UserMenu />
        </div>
      </header>

      <main className="mx-auto max-w-5xl p-6">
        {activeTab === 'workspaces' && (
          <>
            <QuotaBanner />
            <WorkspacesSection onCreate={() => setCreating(true)} />
          </>
        )}
        {activeTab === 'remote' && (
          <RemoteWorkspacesSection editing={remoteEditing} setEditing={setRemoteEditing} />
        )}
        {activeTab === 'volumes' && (
          <>
            <QuotaBanner />
            <VolumesSection />
          </>
        )}
      </main>

      {creating && <CreateWorkspaceDialog onClose={() => setCreating(false)} />}
    </div>
  );
}

// ---------------------------------------------------------------- list

function WorkspacesSection({ onCreate }: { onCreate: () => void }) {
  const { t } = useTranslation();
  const workspaces = useWorkspaces();

  // Three distinct states: skeletons while fetching, an explicit error
  // with a retry, and a first-run empty state with a call to action.
  if (workspaces.isPending) {
    return <SkeletonGrid />;
  }
  if (workspaces.isError) {
    return (
      <div className="rounded-xl border border-red-200 bg-red-50 p-6 text-center dark:border-red-900/50 dark:bg-red-950/30">
        <p className="text-sm text-red-700 dark:text-red-300">{t('portal.loadError')}</p>
        <button
          onClick={() => void workspaces.refetch()}
          className="mt-3 rounded-md border border-red-300 px-4 py-1.5 text-sm text-red-700 hover:bg-red-100 dark:border-red-800 dark:text-red-300 dark:hover:bg-red-900/40"
        >
          {t('app.retry')}
        </button>
      </div>
    );
  }
  if (workspaces.data.data.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-slate-300 p-10 text-center dark:border-slate-700">
        <p className="text-slate-500 dark:text-slate-400">{t('portal.empty')}</p>
        <button
          onClick={onCreate}
          className="mt-4 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('portal.newWorkspace')}
        </button>
      </div>
    );
  }

  return (
    <FolderedGrid
      items={workspaces.data.data}
      renderCard={(ws) => <WorkspaceCard key={ws.id} workspace={ws} />}
    />
  );
}

// SkeletonGrid mirrors the card layout while the list loads, so the page
// doesn't jump when real cards replace it.
function SkeletonGrid({ count = 6 }: { count?: number }) {
  return (
    <div className="grid animate-pulse gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-hidden>
      {Array.from({ length: count }, (_, i) => (
        <div
          key={i}
          className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800"
        >
          <div className="flex items-start justify-between">
            <div className="space-y-2">
              <div className="h-4 w-32 rounded bg-slate-200 dark:bg-slate-700" />
              <div className="h-3 w-24 rounded bg-slate-100 dark:bg-slate-700/60" />
            </div>
            <div className="h-5 w-16 rounded-full bg-slate-100 dark:bg-slate-700/60" />
          </div>
          <div className="mt-auto flex gap-2">
            <div className="h-8 flex-1 rounded-md bg-slate-200 dark:bg-slate-700" />
            <div className="h-8 w-16 rounded-md bg-slate-100 dark:bg-slate-700/60" />
            <div className="h-8 w-16 rounded-md bg-slate-100 dark:bg-slate-700/60" />
          </div>
        </div>
      ))}
    </div>
  );
}

function QuotaBanner() {
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

// VolumesSection: the user's retained volumes — origin, size, date, and
// deletion (server checks ownership, every deletion is audited).
function VolumesSection() {
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

// openWorkspace applies the user's tab preference for one workspace.
function openWorkspace(url: string, newTab: boolean, navigate: (to: string) => void) {
  if (newTab) {
    window.open(url, '_blank');
  } else {
    navigate(url);
  }
}

// WorkspaceCard: the in-cluster wrapper around the shared SessionCard —
// it only contributes what is specific to provisioned workspaces
// (lifecycle actions, connection settings, split view, next transition).
function WorkspaceCard({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const action = useWorkspaceAction();
  const remove = useDeleteWorkspace();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const nextTransitionLabel = useNextTransitionLabel();
  const [asking, setAsking] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [eventsOpen, setEventsOpen] = useState(false);

  const target = targetFromWorkspace(workspace);
  // Badge and buttons follow the DERIVED phase: between a lifecycle
  // action and the operator's reconcile, intent and status disagree and
  // the card shows the transition (Pausing…/Resuming…) instead of a
  // stale steady state. The fast poll converges it to the CR status.
  const phase = effectivePhase(workspace);
  const settling = phase === 'Pausing' || phase === 'Resuming' || phase === 'Terminating';
  const [deleting, setDeleting] = useState(false);

  const onOpen = () => {
    const pref = user?.preferences?.openWorkspaceInNewTab;
    if (pref == null) {
      // Never chosen: ask once, optionally remember.
      setAsking(true);
      return;
    }
    openWorkspace(target.connectUrl, pref, navigate);
  };

  const onChoice = (newTab: boolean, remember: boolean) => {
    setAsking(false);
    if (remember) {
      updateProfile.mutate({
        preferences: { ...user?.preferences, openWorkspaceInNewTab: newTab },
      });
    }
    openWorkspace(target.connectUrl, newTab, navigate);
  };

  return (
    <>
      <SessionCard
        target={target}
        phase={phase}
        message={workspace.message}
        footerNote={
          nextTransitionLabel(workspace.nextTransition) ? (
            <p className="text-xs text-slate-400 dark:text-slate-500">
              ⏰ {nextTransitionLabel(workspace.nextTransition)}
            </p>
          ) : undefined
        }
        menuItems={[
          { label: t('portal.connectionSettings'), onClick: () => setSettingsOpen(true) },
          {
            label: t('portal.openInSplitView'),
            onClick: () => navigate(`/view?ws=${workspace.id}`),
          },
          { label: t('portal.events.menu'), onClick: () => setEventsOpen(true) },
        ]}
        buttons={
          <>
            <button
              onClick={onOpen}
              disabled={phase === 'Failed' || phase === 'Terminating'}
              className="flex-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
            >
              {phase === 'Running'
                ? t('portal.open')
                : phase === 'Paused' || phase === 'Stopped'
                  ? t('portal.wakeAndOpen')
                  : t('portal.starting')}
            </button>
            <button
              onClick={() =>
                action.mutate({ id: workspace.id, action: workspace.paused ? 'resume' : 'pause' })
              }
              disabled={action.isPending || settling}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
            >
              {workspace.paused ? t('portal.resume') : t('portal.pause')}
            </button>
          </>
        }
        onDelete={() => setDeleting(true)}
        deletePending={remove.isPending}
      />
      {deleting && (
        <DeleteWorkspaceDialog
          workspace={workspace}
          pending={remove.isPending}
          onConfirm={(keepVolume) =>
            remove.mutate({ id: workspace.id, keepVolume }, { onSuccess: () => setDeleting(false) })
          }
          onClose={() => setDeleting(false)}
        />
      )}
      {asking && <OpenChoiceDialog onChoice={onChoice} onClose={() => setAsking(false)} />}
      {eventsOpen && (
        <EventsDialog
          workspaceId={workspace.id}
          title={workspace.displayName ?? workspace.name}
          onClose={() => setEventsOpen(false)}
        />
      )}
      {settingsOpen && (
        <ConnectionSettingsDialog workspace={workspace} onClose={() => setSettingsOpen(false)} />
      )}
    </>
  );
}

// ------------------------------------------------------ remote workspaces

// RemoteWorkspacesSection: machines OUTSIDE the cluster reachable through
// guacd. A separate entity with its own lifecycle — nothing here
// provisions or deletes cluster resources.
function RemoteWorkspacesSection({
  editing,
  setEditing,
}: {
  editing: RemoteWorkspace | 'new' | null;
  setEditing: (v: RemoteWorkspace | 'new' | null) => void;
}) {
  const { t } = useTranslation();
  const remotes = useRemoteWorkspaces(true);

  if (remotes.isPending) return <SkeletonGrid count={3} />;
  if (remotes.isError) {
    return (
      <div className="rounded-xl border border-red-200 bg-red-50 p-6 text-center dark:border-red-900/50 dark:bg-red-950/30">
        <p className="text-sm text-red-700 dark:text-red-300">{t('remote.loadError')}</p>
        <button
          onClick={() => void remotes.refetch()}
          className="mt-3 rounded-md border border-red-300 px-4 py-1.5 text-sm text-red-700 hover:bg-red-100 dark:border-red-800 dark:text-red-300 dark:hover:bg-red-900/40"
        >
          {t('app.retry')}
        </button>
      </div>
    );
  }

  const items = remotes.data.data;

  return (
    <div className="space-y-4">
      <p className="text-sm text-slate-500 dark:text-slate-400">{t('remote.hint')}</p>
      {items.length === 0 ? (
        <div className="rounded-xl border border-dashed border-slate-300 p-10 text-center dark:border-slate-700">
          <p className="mb-4 text-slate-500 dark:text-slate-400">{t('remote.empty')}</p>
          <button
            onClick={() => setEditing('new')}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
          >
            {t('remote.add')}
          </button>
        </div>
      ) : (
        <FolderedGrid
          items={items}
          renderCard={(rw) => <RemoteCard key={rw.id} remote={rw} onEdit={() => setEditing(rw)} />}
        />
      )}
      {editing && (
        <RemoteWorkspaceDialog
          remote={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}

// RemoteCard: the remote wrapper around the shared SessionCard — it only
// contributes what is specific to registered machines (connect, WoL,
// endpoint/credentials editing). Chips, folders, menu and delete come
// from the shared card.
function RemoteCard({ remote, onEdit }: { remote: RemoteWorkspace; onEdit: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const remove = useDeleteRemoteWorkspace();
  const wake = useWakeRemoteWorkspace();
  const user = useAuthStore((s) => s.user);

  const target = targetFromRemote(remote);

  return (
    <SessionCard
      target={target}
      footerNote={
        <p className="text-xs text-slate-400 dark:text-slate-500">
          {remote.credentialKeys?.length
            ? t('remote.credentialsStored', { keys: remote.credentialKeys.join(', ') })
            : t('remote.noCredentials')}
          {remote.macAddress && <span className="ml-2 font-mono">· WoL {remote.macAddress}</span>}
        </p>
      }
      menuItems={[{ label: t('app.edit'), onClick: onEdit }]}
      buttons={
        <>
          <button
            onClick={() =>
              openWorkspace(
                target.connectUrl,
                user?.preferences?.openWorkspaceInNewTab ?? false,
                navigate,
              )
            }
            className="flex-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
          >
            {t('remote.connect')}
          </button>
          {target.capabilities.wake && (
            <button
              onClick={() => wake.mutate(remote.id)}
              disabled={wake.isPending}
              title={t('remote.wakeHint')}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
            >
              {t('remote.wake')}
            </button>
          )}
        </>
      }
      onDelete={() => remove.mutate(remote.id)}
      deletePending={remove.isPending}
      deleteConfirm={t('remote.deleteConfirm')}
    />
  );
}
