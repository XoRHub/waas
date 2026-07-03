import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  useCreateWorkspace,
  useTemplates,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { StatusBadge } from '@/components/StatusBadge';
import { useAuthStore } from '@/stores/authStore';
import type { Workspace } from '@/types';

export function PortalPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const workspaces = useWorkspaces();
  const [creating, setCreating] = useState(false);

  return (
    <div className="min-h-screen bg-slate-100 dark:bg-slate-900">
      <header className="flex items-center justify-between bg-white px-6 py-4 shadow-sm dark:bg-slate-800">
        <h1 className="text-lg font-semibold text-slate-900 dark:text-white">{t('portal.title')}</h1>
        <div className="flex items-center gap-4">
          {user?.role === 'admin' && (
            <button
              onClick={() => navigate('/admin')}
              className="text-sm text-blue-600 hover:underline dark:text-blue-400"
            >
              {t('admin.title')}
            </button>
          )}
          <button
            onClick={() => setCreating(true)}
            className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
          >
            {t('portal.newWorkspace')}
          </button>
          <button
            onClick={() => {
              logout();
              navigate('/login');
            }}
            className="text-sm text-slate-500 hover:underline dark:text-slate-400"
          >
            {t('app.logout')}
          </button>
        </div>
      </header>

      <main className="mx-auto max-w-5xl p-6">
        {workspaces.isPending && <p className="text-slate-500">{t('app.loading')}</p>}
        {workspaces.isError && <p className="text-red-600">{t('app.error')}</p>}
        {workspaces.isSuccess && workspaces.data.data.length === 0 && (
          <p className="text-slate-500 dark:text-slate-400">{t('portal.empty')}</p>
        )}
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {workspaces.isSuccess &&
            workspaces.data.data.map((ws) => <WorkspaceCard key={ws.id} workspace={ws} />)}
        </div>
      </main>

      {creating && <CreateWorkspaceDialog onClose={() => setCreating(false)} />}
    </div>
  );
}

function WorkspaceCard({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const action = useWorkspaceAction();

  return (
    <div className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800">
      <div className="flex items-start justify-between">
        <div>
          <h2 className="font-medium text-slate-900 dark:text-white">
            {workspace.displayName || workspace.name}
          </h2>
          <p className="text-xs text-slate-500 dark:text-slate-400">{workspace.templateRef}</p>
        </div>
        <StatusBadge phase={workspace.phase} />
      </div>
      {workspace.message && (
        <p className="text-xs text-slate-500 dark:text-slate-400">{workspace.message}</p>
      )}
      <div className="mt-auto flex gap-2">
        <button
          onClick={() => navigate(`/workspaces/${workspace.id}/connect`)}
          disabled={workspace.phase !== 'Running'}
          className="flex-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
        >
          {workspace.phase === 'Running' ? t('portal.open') : t('portal.starting')}
        </button>
        <button
          onClick={() =>
            action.mutate({ id: workspace.id, action: workspace.paused ? 'resume' : 'pause' })
          }
          disabled={action.isPending}
          className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
        >
          {workspace.paused ? t('portal.resume') : t('portal.pause')}
        </button>
      </div>
    </div>
  );
}

function CreateWorkspaceDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const templates = useTemplates();
  const create = useCreateWorkspace();
  const [templateRef, setTemplateRef] = useState('');
  const [displayName, setDisplayName] = useState('');

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(
      { templateRef, displayName: displayName || undefined },
      { onSuccess: onClose },
    );
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-md space-y-4 rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800"
      >
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('portal.newWorkspace')}
        </h2>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('portal.template')}</span>
          <select
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={templateRef}
            onChange={(e) => setTemplateRef(e.target.value)}
            required
          >
            <option value="" disabled>
              —
            </option>
            {templates.isSuccess &&
              templates.data.data.map((tpl) => (
                <option key={tpl.name} value={tpl.name}>
                  {tpl.displayName} ({tpl.os})
                </option>
              ))}
          </select>
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('portal.displayName')}
          </span>
          <input
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </label>
        {create.isError && <p className="text-sm text-red-600">{create.error.message}</p>}
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            type="submit"
            disabled={create.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.create')}
          </button>
        </div>
      </form>
    </div>
  );
}
