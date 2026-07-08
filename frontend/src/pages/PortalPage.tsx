import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { UserMenu } from '@/components/UserMenu';
import { CreateWorkspaceDialog } from '@/dialogs/CreateWorkspaceDialog';
import { useQuota } from '@/hooks/useApi';
import { useEvents } from '@/hooks/useEvents';
import { QuotaBanner } from '@/sections/QuotaBanner';
import { RemoteWorkspacesSection } from '@/sections/RemoteWorkspacesSection';
import { VolumesSection } from '@/sections/VolumesSection';
import { WorkspacesSection } from '@/sections/WorkspacesSection';
import type { RemoteWorkspace } from '@/types';

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
