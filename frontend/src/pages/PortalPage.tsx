import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  useCatalog,
  useCreateWorkspace,
  useDeleteRemoteWorkspace,
  useDeleteWorkspace,
  useProtocolMeta,
  useQuota,
  useRemoteWorkspaces,
  useSaveRemoteWorkspace,
  useWakeRemoteWorkspace,
  useTemplates,
  useUpdateProfile,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { StatusBadge } from '@/components/StatusBadge';
import { UserMenu } from '@/components/UserMenu';
import { ParamField, tieredParams } from '@/components/ParamField';
import { ScheduleEditor, useNextTransitionLabel } from '@/components/ScheduleEditor';
import { useAuthStore } from '@/stores/authStore';
import { templateAvailability } from '@/lib/templates';
import { displayCpu, displayMemory, formatCpu, formatMemory, parseCpu, parseMemory } from '@/lib/quantity';
import type {
  RemoteWorkspace,
  RemoteWorkspaceInput,
  TemplateEnvVar,
  Workspace,
  WorkspaceSchedule,
} from '@/types';

export function PortalPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const quota = useQuota();
  const [creating, setCreating] = useState(false);
  const [tab, setTab] = useState<'workspaces' | 'remote'>('workspaces');

  // Remote Workspaces is policy-gated: the tab only exists when the
  // resolved policy (or the admin role) opts the user in.
  const remoteEnabled = quota.data?.data.features?.remoteWorkspaces ?? false;
  const activeTab = remoteEnabled ? tab : 'workspaces';

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
          {remoteEnabled && (
            <nav className="flex gap-1">
              <button className={tabClass(activeTab === 'workspaces')} onClick={() => setTab('workspaces')}>
                {t('portal.tabWorkspaces')}
              </button>
              <button className={tabClass(activeTab === 'remote')} onClick={() => setTab('remote')}>
                {t('remote.tab')}
              </button>
            </nav>
          )}
        </div>
        <div className="flex items-center gap-4">
          <button
            onClick={() => navigate('/view')}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {t('portal.splitView')}
          </button>
          {activeTab === 'workspaces' && (
            <button
              onClick={() => setCreating(true)}
              className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
            >
              {t('portal.newWorkspace')}
            </button>
          )}
          <UserMenu />
        </div>
      </header>

      <main className="mx-auto max-w-5xl p-6">
        {activeTab === 'workspaces' ? (
          <>
            <QuotaBanner />
            <WorkspacesSection onCreate={() => setCreating(true)} />
          </>
        ) : (
          <RemoteWorkspacesSection />
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
  const user = useAuthStore((s) => s.user);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  // User-defined grouping: folder name → workspaces; '' collects the
  // unfiled ones and is rendered last.
  const folderOf = user?.preferences?.workspaceFolders ?? {};
  const groups = new Map<string, Workspace[]>();
  for (const ws of workspaces.data?.data ?? []) {
    const folder = folderOf[ws.id] ?? '';
    groups.set(folder, [...(groups.get(folder) ?? []), ws]);
  }
  const folderNames = [...groups.keys()].filter((f) => f !== '').sort();

  const renderCards = (items: Workspace[]) => (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {items.map((ws) => (
        <WorkspaceCard key={ws.id} workspace={ws} />
      ))}
    </div>
  );

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
    <div className="space-y-6">
      {folderNames.map((folder) => (
        <section key={folder}>
          <button
            onClick={() => setCollapsed((c) => ({ ...c, [folder]: !c[folder] }))}
            className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-700 dark:text-slate-200"
          >
            <span className="text-xs">{collapsed[folder] ? '▶' : '▼'}</span>
            <span>📁 {folder}</span>
            <span className="font-normal text-slate-400">({groups.get(folder)!.length})</span>
          </button>
          {!collapsed[folder] && renderCards(groups.get(folder)!)}
        </section>
      ))}
      {groups.has('') && (
        <section>
          {folderNames.length > 0 && (
            <h2 className="mb-3 text-sm font-semibold text-slate-500 dark:text-slate-400">
              {t('portal.unfiled')}
            </h2>
          )}
          {renderCards(groups.get('')!)}
        </section>
      )}
    </div>
  );
}

// SkeletonGrid mirrors the card layout while the list loads, so the page
// doesn't jump when real cards replace it.
function SkeletonGrid({ count = 6 }: { count?: number }) {
  return (
    <div className="grid animate-pulse gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-hidden>
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800">
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
        {t('portal.quotaPolicy')} <span className="font-medium text-slate-800 dark:text-slate-100">{q.policy}</span>
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

function WorkspaceCard({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const action = useWorkspaceAction();
  const remove = useDeleteWorkspace();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const nextTransitionLabel = useNextTransitionLabel();
  const [asking, setAsking] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const url = `/workspaces/${workspace.id}/connect`;
  const onOpen = () => {
    const pref = user?.preferences?.openWorkspaceInNewTab;
    if (pref == null) {
      // Never chosen: ask once, optionally remember.
      setAsking(true);
      return;
    }
    openWorkspace(url, pref, navigate);
  };

  const onChoice = (newTab: boolean, remember: boolean) => {
    setAsking(false);
    if (remember) {
      updateProfile.mutate({
        preferences: { ...user?.preferences, openWorkspaceInNewTab: newTab },
      });
    }
    openWorkspace(url, newTab, navigate);
  };

  const folders = user?.preferences?.workspaceFolders ?? {};
  const currentFolder = folders[workspace.id];
  const existingFolders = [...new Set(Object.values(folders))].sort();

  const moveToFolder = (folder: string | null) => {
    setMenuOpen(false);
    const next = { ...folders };
    if (folder) {
      next[workspace.id] = folder;
    } else {
      delete next[workspace.id];
    }
    updateProfile.mutate({
      preferences: { ...user?.preferences, workspaceFolders: next },
    });
  };

  const onNewFolder = () => {
    const name = window.prompt(t('portal.newFolderPrompt'))?.trim();
    if (name) moveToFolder(name);
    else setMenuOpen(false);
  };

  return (
    <div className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800">
      <div className="flex items-start justify-between">
        <div>
          <h2 className="font-medium text-slate-900 dark:text-white">
            {workspace.displayName || workspace.name}
          </h2>
          <p className="text-xs text-slate-500 dark:text-slate-400">
            {workspace.templateRef}
            {currentFolder && <span className="ml-2">📁 {currentFolder}</span>}
          </p>
        </div>
        <div className="flex items-center gap-1">
          <StatusBadge phase={workspace.phase} />
          <div className="relative">
            <button
              onClick={() => setMenuOpen((v) => !v)}
              className="rounded px-1.5 text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-700"
              aria-haspopup="menu"
              aria-expanded={menuOpen}
            >
              ⋯
            </button>
            {menuOpen && (
              <>
                <div className="fixed inset-0 z-10" onClick={() => setMenuOpen(false)} />
                <div
                  role="menu"
                  className="absolute right-0 z-20 mt-1 w-52 overflow-hidden rounded-lg bg-white py-1 text-sm shadow-lg ring-1 ring-slate-200 dark:bg-slate-800 dark:ring-slate-700"
                >
                  <CardMenuItem
                    onClick={() => {
                      setMenuOpen(false);
                      setSettingsOpen(true);
                    }}
                  >
                    {t('portal.connectionSettings')}
                  </CardMenuItem>
                  <CardMenuItem
                    onClick={() => {
                      setMenuOpen(false);
                      navigate(`/view?ws=${workspace.id}`);
                    }}
                  >
                    {t('portal.openInSplitView')}
                  </CardMenuItem>
                  <div className="my-1 border-t border-slate-200 dark:border-slate-700" />
                  <p className="px-4 py-1 text-xs text-slate-400">{t('portal.moveToFolder')}</p>
                  {existingFolders
                    .filter((f) => f !== currentFolder)
                    .map((f) => (
                      <CardMenuItem key={f} onClick={() => moveToFolder(f)}>
                        📁 {f}
                      </CardMenuItem>
                    ))}
                  <CardMenuItem onClick={onNewFolder}>{t('portal.newFolder')}</CardMenuItem>
                  {currentFolder && (
                    <CardMenuItem onClick={() => moveToFolder(null)}>
                      {t('portal.removeFromFolder')}
                    </CardMenuItem>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      </div>
      {workspace.message && (
        <p className="text-xs text-slate-500 dark:text-slate-400">{workspace.message}</p>
      )}
      {nextTransitionLabel(workspace.nextTransition) && (
        <p className="text-xs text-slate-400 dark:text-slate-500">
          ⏰ {nextTransitionLabel(workspace.nextTransition)}
        </p>
      )}
      <div className="mt-auto flex gap-2">
        <button
          onClick={onOpen}
          disabled={workspace.phase === 'Failed' || workspace.phase === 'Terminating'}
          className="flex-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
        >
          {workspace.phase === 'Running'
            ? t('portal.open')
            : workspace.phase === 'Paused' || workspace.phase === 'Stopped'
              ? t('portal.wakeAndOpen')
              : t('portal.starting')}
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
        <button
          onClick={() => {
            if (window.confirm(t('portal.deleteConfirm'))) {
              remove.mutate(workspace.id);
            }
          }}
          disabled={remove.isPending}
          className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 disabled:opacity-40 dark:border-slate-600 dark:hover:bg-slate-700"
        >
          {t('app.delete')}
        </button>
      </div>
      {asking && <OpenChoiceDialog onChoice={onChoice} onClose={() => setAsking(false)} />}
      {settingsOpen && (
        <ConnectionSettingsDialog workspace={workspace} onClose={() => setSettingsOpen(false)} />
      )}
    </div>
  );
}

function CardMenuItem({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      role="menuitem"
      onClick={onClick}
      className="block w-full px-4 py-1.5 text-left text-slate-700 hover:bg-slate-50 dark:text-slate-200 dark:hover:bg-slate-700"
    >
      {children}
    </button>
  );
}

// AdvancedParamsToggle: the simple/advanced switch every guacd parameter
// form shares. Simple mode shows the everyday parameters (registry tier
// "ui"); advanced mode adds the whole "advanced" tier.
function AdvancedParamsToggle({
  value,
  onChange,
}: {
  value: boolean;
  onChange: (v: boolean) => void;
}) {
  const { t } = useTranslation();
  return (
    <label className="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
      <input type="checkbox" checked={value} onChange={(e) => onChange(e.target.checked)} />
      {t('portal.showAdvancedParams')}
    </label>
  );
}

// ConnectionSettingsDialog: pick the protocol among what the template
// declares and tune the guacd parameters the template allow-lists. Saved
// in the profile; the server re-validates at connect time.
function ConnectionSettingsDialog({
  workspace,
  onClose,
}: {
  workspace: Workspace;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const meta = useProtocolMeta();
  const saved = user?.preferences?.workspaceSettings?.[workspace.id];
  const protocols = workspace.protocols ?? [];
  const defaultProtocol = protocols.find((p) => p.default)?.name ?? workspace.protocol ?? '';
  const [protocol, setProtocol] = useState(saved?.protocol || defaultProtocol);
  const [params, setParams] = useState<Record<string, string>>(saved?.params ?? {});
  const [showAdvanced, setShowAdvanced] = useState(false);

  const selected = protocols.find((p) => p.name === protocol);
  const isAdmin = user?.role === 'admin';
  // Admins tune any non-platform param (matches the server's admin
  // bypass); regular users stay inside the template's userParams
  // allow-list. Simple tier is always shown, advanced behind the toggle.
  const { simple, advanced } = tieredParams(
    meta.data?.data,
    protocol,
    isAdmin ? undefined : (selected?.userParams ?? []),
  );
  const tunable = showAdvanced ? [...simple, ...advanced] : simple;

  const onSave = () => {
    const cleaned = Object.fromEntries(Object.entries(params).filter(([, v]) => v !== ''));
    const settings = { ...user?.preferences?.workspaceSettings };
    if (protocol === defaultProtocol && Object.keys(cleaned).length === 0) {
      delete settings[workspace.id];
    } else {
      settings[workspace.id] = {
        protocol: protocol !== defaultProtocol ? protocol : undefined,
        params: Object.keys(cleaned).length > 0 ? cleaned : undefined,
      };
    }
    updateProfile.mutate(
      { preferences: { ...user?.preferences, workspaceSettings: settings } },
      { onSuccess: onClose },
    );
  };

  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-md space-y-4 rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('portal.connectionSettings')}
        </h2>
        {protocols.length > 0 ? (
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">{t('portal.protocol')}</span>
            <select
              className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
              value={protocol}
              onChange={(e) => setProtocol(e.target.value)}
            >
              {protocols.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.name.toUpperCase()}
                  {p.default ? ` (${t('portal.protocolDefault')})` : ''}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <p className="text-sm text-slate-500 dark:text-slate-400">
            {t('portal.protocol')}: {(workspace.protocol || 'vnc').toUpperCase()}
          </p>
        )}
        {tunable.length > 0 ? (
          <fieldset className="space-y-3">
            <legend className="text-sm text-slate-600 dark:text-slate-300">
              {t('portal.protocolParams')}
            </legend>
            {tunable.map((pm) => (
              <ParamField
                key={pm.name}
                meta={pm}
                value={params[pm.name] ?? ''}
                onChange={(value) => setParams((p) => ({ ...p, [pm.name]: value }))}
              />
            ))}
          </fieldset>
        ) : (
          <p className="text-xs text-slate-400 dark:text-slate-500">{t('portal.noTunableParams')}</p>
        )}
        {advanced.length > 0 && (
          <AdvancedParamsToggle value={showAdvanced} onChange={setShowAdvanced} />
        )}
        {updateProfile.isError && (
          <p className="text-sm text-red-600">{updateProfile.error.message}</p>
        )}
        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            onClick={onSave}
            disabled={updateProfile.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.save')}
          </button>
        </div>
      </div>
    </div>
  );
}

// First-open dialog: where should workspaces open? Optionally persisted
// as a profile preference (editable later from the profile page).
function OpenChoiceDialog({
  onChoice,
  onClose,
}: {
  onChoice: (newTab: boolean, remember: boolean) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [remember, setRemember] = useState(true);

  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-sm space-y-4 rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('portal.openWhere')}
        </h2>
        <p className="text-sm text-slate-500 dark:text-slate-400">{t('portal.openWhereHint')}</p>
        <div className="grid grid-cols-2 gap-2">
          <button
            onClick={() => onChoice(false, remember)}
            className="rounded-md border border-slate-300 px-3 py-2 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {t('portal.openSameTab')}
          </button>
          <button
            onClick={() => onChoice(true, remember)}
            className="rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700"
          >
            {t('portal.openNewTab')}
          </button>
        </div>
        <label className="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
          <input
            type="checkbox"
            checked={remember}
            onChange={(e) => setRemember(e.target.checked)}
          />
          {t('portal.rememberChoice')}
        </label>
        <button
          onClick={onClose}
          className="text-sm text-slate-500 hover:underline dark:text-slate-400"
        >
          {t('app.cancel')}
        </button>
      </div>
    </div>
  );
}

// Slider steps: 0.25 vCPU and 256Mi.
const CPU_STEP = 250;
const MEM_STEP = 256 * 1024 * 1024;
// Floors when neither the image nor the policy declares a minimum.
const CPU_FLOOR = 250;
const MEM_FLOOR = 512 * 1024 * 1024;

interface SliderBounds {
  min: number;
  max: number;
  initial: number;
}

// clampRange derives one slider's bounds: min from the image, max from
// min(image.max, policy.perWorkspace, remaining aggregate), initial from
// image.defaults ?? policy defaults ?? template requests (then clamped).
function clampRange(
  candidates: { min?: number; maxes: (number | undefined)[]; defaults: (number | undefined)[] },
  floor: number,
  step: number,
): SliderBounds {
  const min = candidates.min ?? floor;
  const maxes = candidates.maxes.filter((v): v is number => v !== undefined && !Number.isNaN(v));
  const max = maxes.length > 0 ? Math.max(min, Math.min(...maxes)) : min * 16;
  const preferred = candidates.defaults.find((v) => v !== undefined && !Number.isNaN(v)) ?? min;
  const initial = Math.min(Math.max(Math.round(preferred / step) * step, min), max);
  return { min, max, initial };
}

function CreateWorkspaceDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const templates = useTemplates();
  const catalog = useCatalog();
  const quota = useQuota();
  const meta = useProtocolMeta();
  const create = useCreateWorkspace();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const [templateRef, setTemplateRef] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [cpu, setCpu] = useState<number | null>(null);
  const [memory, setMemory] = useState<number | null>(null);
  const [protocol, setProtocol] = useState('');
  const [protoParams, setProtoParams] = useState<Record<string, string>>({});
  const [showAdvancedParams, setShowAdvancedParams] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [envRows, setEnvRows] = useState<{ name: string; value: string }[]>([]);
  const [scheduleOverride, setScheduleOverride] = useState<WorkspaceSchedule | undefined>(undefined);

  // Every template is listed whatever its protocol; the ones the policy
  // excludes are visible but disabled with the reason (never silently
  // dropped — that is how SSH templates used to "disappear").
  const availability = templateAvailability(
    templates.isSuccess ? templates.data.data : [],
    catalog.isSuccess ? catalog.data.data : undefined,
  );
  const template = availability.find(
    (a) => a.template.name === templateRef && a.available,
  )?.template;
  const image = catalog.isSuccess
    ? catalog.data.data.find((img) => img.templates?.includes(templateRef))
    : undefined;
  const q = quota.isSuccess ? quota.data.data : undefined;

  // Remaining aggregate = policy aggregate cap minus current usage.
  const remaining = (kind: 'cpu' | 'memory', parse: (s: string) => number) => {
    const limit = q?.limits?.[kind];
    if (!limit) return undefined;
    return parse(limit) - parse(q?.used?.[kind] ?? '0');
  };

  const cpuBounds = clampRange(
    {
      min: image?.min?.cpu ? parseCpu(image.min.cpu) : undefined,
      maxes: [
        image?.max?.cpu ? parseCpu(image.max.cpu) : undefined,
        q?.perWorkspace?.cpu ? parseCpu(q.perWorkspace.cpu) : undefined,
        remaining('cpu', parseCpu),
      ],
      defaults: [
        image?.defaults?.cpu ? parseCpu(image.defaults.cpu) : undefined,
        q?.defaults?.cpu ? parseCpu(q.defaults.cpu) : undefined,
        template?.requests?.cpu ? parseCpu(template.requests.cpu) : undefined,
      ],
    },
    CPU_FLOOR,
    CPU_STEP,
  );
  const memBounds = clampRange(
    {
      min: image?.min?.memory ? parseMemory(image.min.memory) : undefined,
      maxes: [
        image?.max?.memory ? parseMemory(image.max.memory) : undefined,
        q?.perWorkspace?.memory ? parseMemory(q.perWorkspace.memory) : undefined,
        remaining('memory', parseMemory),
      ],
      defaults: [
        image?.defaults?.memory ? parseMemory(image.defaults.memory) : undefined,
        q?.defaults?.memory ? parseMemory(q.defaults.memory) : undefined,
        template?.requests?.memory ? parseMemory(template.requests.memory) : undefined,
      ],
    },
    MEM_FLOOR,
    MEM_STEP,
  );

  const cpuValue = cpu ?? cpuBounds.initial;
  const memValue = memory ?? memBounds.initial;

  const selectTemplate = (name: string) => {
    setTemplateRef(name);
    // Re-seed sliders, protocol choice and overrides on template change.
    setCpu(null);
    setMemory(null);
    setProtocol('');
    setProtoParams({});
    setEnvRows([]);
    setScheduleOverride(undefined);
  };

  // Protocol section: what the template declares, gated by its override
  // flags. The webhook re-validates server-side — this only mirrors it.
  const tplProtocols = template?.protocols ?? [];
  const defaultProtocol = tplProtocols.find((p) => p.default)?.name ?? tplProtocols[0]?.name ?? '';
  const isAdmin = user?.role === 'admin';
  // A field is overridable when the template allows it AND the policy
  // does not restrict it away (admins bypass both, like the webhook).
  const policyAllows = (field: string) =>
    !q?.allowedOverrides || q.allowedOverrides.includes(field);
  const canOverride = (field: string) =>
    isAdmin || ((template?.allowedOverrides?.includes(field) ?? false) && policyAllows(field));
  const protocolOverridable = canOverride('protocol');
  const effectiveProtocol = protocol || defaultProtocol;
  const selectedProto = tplProtocols.find((p) => p.name === effectiveProtocol);
  // Creation-time params from the registry, simple tier by default; the
  // advanced toggle adds the advanced tier. Non-admins stay inside the
  // template's userParams allow-list; admins may tune any non-platform
  // parameter (mirrors the server's admin bypass).
  const { simple: simpleCreationParams, advanced: advancedCreationParams } = tieredParams(
    meta.data?.data,
    effectiveProtocol,
    isAdmin ? undefined : (selectedProto?.userParams ?? []),
  );
  const creationParams = showAdvancedParams
    ? [...simpleCreationParams, ...advancedCreationParams]
    : simpleCreationParams;

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const chosenProtocol =
      protocolOverridable && effectiveProtocol !== defaultProtocol ? effectiveProtocol : undefined;
    const cleanedParams = Object.fromEntries(
      Object.entries(protoParams).filter(([, v]) => v !== ''),
    );
    const env: TemplateEnvVar[] = envRows
      .filter((row) => row.name.trim() !== '')
      .map((row) => ({ name: row.name.trim(), value: row.value }));
    const scheduleOv = canOverride('schedule') ? scheduleOverride : undefined;
    const overrides =
      chosenProtocol || env.length > 0 || scheduleOv
        ? {
            protocol: chosenProtocol,
            env: env.length > 0 ? env : undefined,
            schedule: scheduleOv,
          }
        : undefined;
    create.mutate(
      {
        templateRef,
        displayName: displayName || undefined,
        resources: { cpu: formatCpu(cpuValue), memory: formatMemory(memValue) },
        overrides,
      },
      {
        onSuccess: ({ data: workspace }) => {
          // Connection tuning lives in the profile (as the post-creation
          // "Connection settings" dialog writes it); server re-validates
          // at connect time.
          if (Object.keys(cleanedParams).length > 0) {
            const settings = { ...user?.preferences?.workspaceSettings };
            settings[workspace.id] = {
              protocol: chosenProtocol,
              params: cleanedParams,
            };
            updateProfile.mutate({
              preferences: { ...user?.preferences, workspaceSettings: settings },
            });
          }
          onClose();
        },
      },
    );
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
      <form
        onSubmit={onSubmit}
        className="max-h-[90vh] w-full max-w-md space-y-4 overflow-y-auto rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800"
      >
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('portal.newWorkspace')}
        </h2>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('portal.template')}</span>
          <select
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={templateRef}
            onChange={(e) => selectTemplate(e.target.value)}
            required
          >
            <option value="" disabled>
              —
            </option>
            {availability.map(({ template: tpl, available }) => (
              <option key={tpl.name} value={tpl.name} disabled={!available}>
                {tpl.displayName} ({tpl.os}
                {tpl.protocols?.length ? ` · ${tpl.protocols.map((p) => p.name).join('/')}` : ''})
                {available ? '' : ` — ${t('portal.templateUnavailable')}`}
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

        {template && (
          <fieldset className="space-y-4">
            <ResourceSlider
              label={t('portal.cpu')}
              value={cpuValue}
              bounds={cpuBounds}
              step={CPU_STEP}
              display={(v) => `${displayCpu(v)} vCPU`}
              onChange={setCpu}
            />
            <ResourceSlider
              label={t('portal.memory')}
              value={memValue}
              bounds={memBounds}
              step={MEM_STEP}
              display={displayMemory}
              onChange={setMemory}
            />
            {q?.limits && (
              <p className="text-xs text-slate-500 dark:text-slate-400">
                {t('portal.quotaRemaining', {
                  cpu: displayCpu(remaining('cpu', parseCpu) ?? 0),
                  memory: displayMemory(remaining('memory', parseMemory) ?? 0),
                })}
              </p>
            )}
          </fieldset>
        )}

        {template && tplProtocols.length > 0 && (
          <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
            <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
              {t('portal.connection')}
            </legend>
            {tplProtocols.length > 1 ? (
              <label className="block">
                <span className="flex items-baseline justify-between text-sm">
                  <span className="text-slate-600 dark:text-slate-300">{t('portal.protocol')}</span>
                  {!protocolOverridable && (
                    <span className="text-xs text-slate-400" title={t('portal.protocolLockedHint')}>
                      🔒 {t('portal.protocolLocked')}
                    </span>
                  )}
                </span>
                <select
                  className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 disabled:opacity-60 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                  value={effectiveProtocol}
                  disabled={!protocolOverridable}
                  onChange={(e) => {
                    setProtocol(e.target.value);
                    setProtoParams({});
                  }}
                >
                  {tplProtocols.map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.name.toUpperCase()}
                      {p.default ? ` (${t('portal.protocolDefault')})` : ''}
                    </option>
                  ))}
                </select>
              </label>
            ) : (
              // Single protocol (typically a legacy template with the
              // OS-derived synthesized entry): show it instead of an
              // empty box — the connection is never a mystery.
              <p className="text-sm text-slate-600 dark:text-slate-300">
                {t('portal.protocol')}:{' '}
                <span className="font-medium">{tplProtocols[0].name.toUpperCase()}</span>
                {tplProtocols[0].port ? (
                  <span className="text-slate-400"> · {t('portal.port')} {tplProtocols[0].port}</span>
                ) : null}{' '}
                <span className="text-xs text-slate-400">({t('portal.protocolDefault')})</span>
              </p>
            )}
            {creationParams.length > 0 ? (
              <div className="grid grid-cols-2 gap-3">
                {creationParams.map((pm) => (
                  <ParamField
                    key={pm.name}
                    meta={{ ...pm, default: selectedProto?.params?.[pm.name] ?? pm.default }}
                    value={protoParams[pm.name] ?? ''}
                    onChange={(value) => setProtoParams((prev) => ({ ...prev, [pm.name]: value }))}
                  />
                ))}
              </div>
            ) : (
              <p className="text-xs text-slate-400 dark:text-slate-500">
                {t('portal.noTunableParams')}
              </p>
            )}
            {advancedCreationParams.length > 0 && (
              <AdvancedParamsToggle value={showAdvancedParams} onChange={setShowAdvancedParams} />
            )}
          </fieldset>
        )}

        {/* Advanced panel (template overrides): only rendered for users
            whose template ∩ policy rights (or admin role) allow at least
            one overridable field — invisible to everyone else. */}
        {template && (canOverride('env') || canOverride('schedule')) && (
          <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
            <legend className="px-1">
              <button
                type="button"
                onClick={() => setAdvancedOpen((v) => !v)}
                className="flex items-center gap-1 text-sm text-slate-600 dark:text-slate-300"
              >
                <span className="text-xs">{advancedOpen ? '▼' : '▶'}</span>
                {t('portal.advancedMode')}
              </button>
            </legend>
            {advancedOpen && (
              <div className="space-y-3">
                <p className="text-xs text-slate-400 dark:text-slate-500">
                  {t('portal.advancedModeHint')}
                </p>
                {canOverride('env') && (
                <div className="space-y-2">
                  <span className="text-sm text-slate-600 dark:text-slate-300">
                    {t('portal.envOverrides')}
                  </span>
                  {envRows.map((row, i) => (
                    <div key={i} className="flex gap-2">
                      <input
                        className="w-2/5 rounded-md border border-slate-300 px-2 py-1.5 font-mono text-xs dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                        placeholder={t('portal.envName')}
                        value={row.name}
                        onChange={(e) =>
                          setEnvRows((rows) =>
                            rows.map((r, j) => (j === i ? { ...r, name: e.target.value } : r)),
                          )
                        }
                      />
                      <input
                        className="flex-1 rounded-md border border-slate-300 px-2 py-1.5 font-mono text-xs dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                        placeholder={t('portal.envValue')}
                        value={row.value}
                        onChange={(e) =>
                          setEnvRows((rows) =>
                            rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)),
                          )
                        }
                      />
                      <button
                        type="button"
                        onClick={() => setEnvRows((rows) => rows.filter((_, j) => j !== i))}
                        className="rounded px-2 text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-700"
                        aria-label={t('app.delete')}
                      >
                        ✕
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    onClick={() => setEnvRows((rows) => [...rows, { name: '', value: '' }])}
                    className="rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
                  >
                    + {t('portal.addEnvVar')}
                  </button>
                </div>
                )}
                {canOverride('schedule') && (
                  <div className="space-y-2">
                    <span className="text-sm text-slate-600 dark:text-slate-300">
                      {t('schedule.title')}
                    </span>
                    <ScheduleEditor
                      value={scheduleOverride ?? template?.schedule}
                      onChange={setScheduleOverride}
                    />
                  </div>
                )}
              </div>
            )}
          </fieldset>
        )}

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

function ResourceSlider({
  label,
  value,
  bounds,
  step,
  display,
  onChange,
}: {
  label: string;
  value: number;
  bounds: SliderBounds;
  step: number;
  display: (v: number) => string;
  onChange: (v: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <label className="block">
      <span className="flex items-baseline justify-between text-sm">
        <span className="text-slate-600 dark:text-slate-300">{label}</span>
        <span className="font-medium text-slate-900 dark:text-white">{display(value)}</span>
      </span>
      <input
        type="range"
        className="mt-1 w-full accent-blue-600"
        min={bounds.min}
        max={bounds.max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      />
      <span className="flex justify-between text-xs text-slate-400 dark:text-slate-500">
        <span>{display(bounds.min)}</span>
        <span>{t('portal.max', { value: display(bounds.max) })}</span>
      </span>
    </label>
  );
}

// ------------------------------------------------------ remote workspaces

// RemoteWorkspacesSection: machines OUTSIDE the cluster reachable through
// guacd. A separate entity with its own lifecycle — nothing here
// provisions or deletes cluster resources.
function RemoteWorkspacesSection() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const remotes = useRemoteWorkspaces(true);
  const remove = useDeleteRemoteWorkspace();
  const wake = useWakeRemoteWorkspace();
  const user = useAuthStore((s) => s.user);
  const [editing, setEditing] = useState<RemoteWorkspace | 'new' | null>(null);

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
  const connect = (rw: RemoteWorkspace) => {
    const url = `/remote/${rw.id}/connect`;
    openWorkspace(url, user?.preferences?.openWorkspaceInNewTab ?? false, navigate);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-slate-500 dark:text-slate-400">{t('remote.hint')}</p>
        <button
          onClick={() => setEditing('new')}
          className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('remote.add')}
        </button>
      </div>
      {items.length === 0 ? (
        <div className="rounded-xl border border-dashed border-slate-300 p-10 text-center dark:border-slate-700">
          <p className="text-slate-500 dark:text-slate-400">{t('remote.empty')}</p>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {items.map((rw) => (
            <div key={rw.id} className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800">
              <div className="flex items-start justify-between">
                <div>
                  <h2 className="font-medium text-slate-900 dark:text-white">{rw.name}</h2>
                  <p className="font-mono text-xs text-slate-500 dark:text-slate-400">
                    {rw.hostname}:{rw.port}
                  </p>
                </div>
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium uppercase text-slate-600 dark:bg-slate-700 dark:text-slate-300">
                  {rw.protocol}
                </span>
              </div>
              <p className="text-xs text-slate-400 dark:text-slate-500">
                {rw.credentialKeys?.length
                  ? t('remote.credentialsStored', { keys: rw.credentialKeys.join(', ') })
                  : t('remote.noCredentials')}
                {rw.macAddress && <span className="ml-2 font-mono">· WoL {rw.macAddress}</span>}
              </p>
              <div className="mt-auto flex gap-2">
                <button
                  onClick={() => connect(rw)}
                  className="flex-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
                >
                  {t('remote.connect')}
                </button>
                {rw.macAddress && (
                  <button
                    onClick={() => wake.mutate(rw.id)}
                    disabled={wake.isPending}
                    title={t('remote.wakeHint')}
                    className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
                  >
                    {t('remote.wake')}
                  </button>
                )}
                <button
                  onClick={() => setEditing(rw)}
                  className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
                >
                  {t('app.edit')}
                </button>
                <button
                  onClick={() => {
                    if (window.confirm(t('remote.deleteConfirm'))) remove.mutate(rw.id);
                  }}
                  disabled={remove.isPending}
                  className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 disabled:opacity-40 dark:border-slate-600 dark:hover:bg-slate-700"
                >
                  {t('app.delete')}
                </button>
              </div>
            </div>
          ))}
        </div>
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

// RemoteWorkspaceDialog: register/edit one external machine. Credentials
// are write-only (stored in a Kubernetes Secret server-side, never echoed
// back); guacd parameters reuse the same registry-driven form as
// provisioned workspaces.
function RemoteWorkspaceDialog({
  remote,
  onClose,
}: {
  remote: RemoteWorkspace | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const meta = useProtocolMeta();
  const save = useSaveRemoteWorkspace();
  const [name, setName] = useState(remote?.name ?? '');
  const [hostname, setHostname] = useState(remote?.hostname ?? '');
  const [port, setPort] = useState(remote ? String(remote.port) : '');
  const [protocol, setProtocol] = useState(remote?.protocol ?? 'ssh');
  const [macAddress, setMacAddress] = useState(remote?.macAddress ?? '');
  const [params, setParams] = useState<Record<string, string>>(remote?.params ?? {});
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [creds, setCreds] = useState({ username: '', password: '', privateKey: '', passphrase: '' });

  const defaultPorts: Record<string, string> = { ssh: '22', vnc: '5900', rdp: '3389' };
  // The user owns this machine: every non-platform parameter is tunable.
  const { simple: simpleFields, advanced: advancedFields } = tieredParams(
    meta.data?.data,
    protocol,
    undefined,
  );
  const fields = showAdvanced ? [...simpleFields, ...advancedFields] : simpleFields;

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const cleanedParams = Object.fromEntries(Object.entries(params).filter(([, v]) => v !== ''));
    // Empty fields are omitted = "keep the stored value" on edit.
    const credentials = Object.fromEntries(
      Object.entries(creds).filter(([, v]) => v !== ''),
    ) as RemoteWorkspaceInput['credentials'];
    const input: RemoteWorkspaceInput = {
      name,
      hostname,
      port: Number(port || defaultPorts[protocol] || 0),
      protocol,
      macAddress: macAddress.trim() || undefined,
      params: Object.keys(cleanedParams).length > 0 ? cleanedParams : undefined,
      credentials: credentials && Object.keys(credentials).length > 0 ? credentials : undefined,
    };
    save.mutate({ id: remote?.id, input }, { onSuccess: onClose });
  };

  const credField = (key: keyof typeof creds, label: string, type = 'text') => (
    <label className="block">
      <span className="text-xs text-slate-500 dark:text-slate-400">{label}</span>
      <input
        type={type}
        autoComplete="off"
        className="mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
        placeholder={remote ? t('remote.keepStored') : ''}
        value={creds[key]}
        onChange={(e) => setCreds((c) => ({ ...c, [key]: e.target.value }))}
      />
    </label>
  );

  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/40 p-4">
      <form
        onSubmit={onSubmit}
        className="max-h-[90vh] w-full max-w-md space-y-4 overflow-y-auto rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800"
      >
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {remote ? t('remote.edit') : t('remote.add')}
        </h2>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('remote.name')}</span>
          <input
            required
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <div className="flex gap-2">
          <label className="block flex-1">
            <span className="text-sm text-slate-600 dark:text-slate-300">{t('remote.hostname')}</span>
            <input
              required
              className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 font-mono text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
              placeholder="203.0.113.10"
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
            />
          </label>
          <label className="block w-24">
            <span className="text-sm text-slate-600 dark:text-slate-300">{t('portal.port')}</span>
            <input
              type="number"
              min={1}
              max={65535}
              className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
              placeholder={defaultPorts[protocol]}
              value={port}
              onChange={(e) => setPort(e.target.value)}
            />
          </label>
        </div>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('portal.protocol')}</span>
          <select
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={protocol}
            onChange={(e) => {
              setProtocol(e.target.value);
              setParams({});
            }}
          >
            {['ssh', 'vnc', 'rdp'].map((p) => (
              <option key={p} value={p}>
                {p.toUpperCase()}
              </option>
            ))}
          </select>
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('remote.mac')}</span>
          <input
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 font-mono text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            placeholder="aa:bb:cc:dd:ee:ff"
            value={macAddress}
            onChange={(e) => setMacAddress(e.target.value)}
          />
          <span className="mt-0.5 block text-xs text-slate-400 dark:text-slate-500">
            {t('remote.macHint')}
          </span>
        </label>

        <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
            {t('remote.credentials')}
          </legend>
          <p className="text-xs text-slate-400 dark:text-slate-500">{t('remote.credentialsHint')}</p>
          {credField('username', t('remote.username'))}
          {credField('password', t('remote.password'), 'password')}
          {protocol === 'ssh' && (
            <>
              <label className="block">
                <span className="text-xs text-slate-500 dark:text-slate-400">
                  {t('remote.privateKey')}
                </span>
                <textarea
                  rows={3}
                  autoComplete="off"
                  spellCheck={false}
                  className="mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 font-mono text-xs dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                  placeholder={remote ? t('remote.keepStored') : '-----BEGIN OPENSSH PRIVATE KEY-----'}
                  value={creds.privateKey}
                  onChange={(e) => setCreds((c) => ({ ...c, privateKey: e.target.value }))}
                />
              </label>
              {credField('passphrase', t('remote.passphrase'), 'password')}
            </>
          )}
        </fieldset>

        <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
            {t('portal.protocolParams')}
          </legend>
          {fields.length > 0 ? (
            <div className="grid grid-cols-2 gap-3">
              {fields.map((pm) => (
                <ParamField
                  key={pm.name}
                  meta={pm}
                  value={params[pm.name] ?? ''}
                  onChange={(value) => setParams((prev) => ({ ...prev, [pm.name]: value }))}
                />
              ))}
            </div>
          ) : (
            <p className="text-xs text-slate-400 dark:text-slate-500">{t('portal.noTunableParams')}</p>
          )}
          {advancedFields.length > 0 && (
            <AdvancedParamsToggle value={showAdvanced} onChange={setShowAdvanced} />
          )}
        </fieldset>

        {save.isError && <p className="text-sm text-red-600">{save.error.message}</p>}
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
            disabled={save.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {remote ? t('app.save') : t('app.create')}
          </button>
        </div>
      </form>
    </div>
  );
}
