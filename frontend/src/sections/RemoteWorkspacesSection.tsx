import { useRef } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { FolderedGrid, SessionCard } from '@/components/SessionCard';
import { SkeletonGrid } from '@/components/SkeletonGrid';
import { RemoteWorkspaceDialog } from '@/dialogs/RemoteWorkspaceDialog';
import {
  useDeleteRemoteWorkspace,
  useRemoteWorkspaces,
  useWakeRemoteWorkspace,
} from '@/hooks/useApi';
import { openWorkspace } from '@/lib/openWorkspace';
import { targetFromRemote } from '@/lib/target';
import { useAuthStore } from '@/stores/authStore';
import type { RemoteWorkspace } from '@/types';

// RemoteWorkspacesSection: machines OUTSIDE the cluster reachable through
// guacd. A separate entity with its own lifecycle — nothing here
// provisions or deletes cluster resources.
export function RemoteWorkspacesSection({
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

  const cardRef = useRef<HTMLDivElement>(null);
  // Same card→connect morph as WorkspaceCard: tag only the clicked
  // card right before a same-tab navigation (see WorkspacesSection).
  const connect = () => {
    const newTab = user?.preferences?.openWorkspaceInNewTab ?? false;
    if (!newTab) cardRef.current?.style.setProperty('view-transition-name', 'workspace-open');
    openWorkspace(target.connectUrl, newTab, navigate);
  };

  return (
    <SessionCard
      ref={cardRef}
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
            onClick={connect}
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
