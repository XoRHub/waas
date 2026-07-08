import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { EventsDialog } from '@/components/EventsDialog';
import { useNextTransitionLabel } from '@/components/ScheduleEditor';
import { FolderedGrid, SessionCard } from '@/components/SessionCard';
import { SkeletonGrid } from '@/components/SkeletonGrid';
import { ConnectionSettingsDialog } from '@/dialogs/ConnectionSettingsDialog';
import { DeleteWorkspaceDialog } from '@/dialogs/DeleteWorkspaceDialog';
import { OpenChoiceDialog } from '@/dialogs/OpenChoiceDialog';
import {
  useDeleteWorkspace,
  useUpdateProfile,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { effectivePhase } from '@/lib/lifecycle';
import { openWorkspace } from '@/lib/openWorkspace';
import { targetFromWorkspace } from '@/lib/target';
import { useAuthStore } from '@/stores/authStore';
import type { Workspace } from '@/types';

export function WorkspacesSection({ onCreate }: { onCreate: () => void }) {
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
