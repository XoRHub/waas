import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  useCatalog,
  useCreateWorkspace,
  useDeleteWorkspace,
  useProtocolMeta,
  useQuota,
  useTemplates,
  useUpdateProfile,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { StatusBadge } from '@/components/StatusBadge';
import { UserMenu } from '@/components/UserMenu';
import { ParamField, paramsFor } from '@/components/ParamField';
import { useAuthStore } from '@/stores/authStore';
import { displayCpu, displayMemory, formatCpu, formatMemory, parseCpu, parseMemory } from '@/lib/quantity';
import type { Workspace } from '@/types';

export function PortalPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const workspaces = useWorkspaces();
  const user = useAuthStore((s) => s.user);
  const [creating, setCreating] = useState(false);
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

  return (
    <div className="min-h-screen bg-slate-100 dark:bg-slate-900">
      <header className="flex items-center justify-between bg-white px-6 py-4 shadow-sm dark:bg-slate-800">
        <h1 className="text-lg font-semibold text-slate-900 dark:text-white">{t('portal.title')}</h1>
        <div className="flex items-center gap-4">
          <button
            onClick={() => navigate('/view')}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {t('portal.splitView')}
          </button>
          <button
            onClick={() => setCreating(true)}
            className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
          >
            {t('portal.newWorkspace')}
          </button>
          <UserMenu />
        </div>
      </header>

      <main className="mx-auto max-w-5xl p-6">
        <QuotaBanner />
        {workspaces.isPending && <p className="text-slate-500">{t('app.loading')}</p>}
        {workspaces.isError && <p className="text-red-600">{t('app.error')}</p>}
        {workspaces.isSuccess && workspaces.data.data.length === 0 && (
          <p className="text-slate-500 dark:text-slate-400">{t('portal.empty')}</p>
        )}
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
      </main>

      {creating && <CreateWorkspaceDialog onClose={() => setCreating(false)} />}
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
function openWorkspace(id: string, newTab: boolean, navigate: (to: string) => void) {
  const url = `/workspaces/${id}/connect`;
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
  const [asking, setAsking] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const onOpen = () => {
    const pref = user?.preferences?.openWorkspaceInNewTab;
    if (pref == null) {
      // Never chosen: ask once, optionally remember.
      setAsking(true);
      return;
    }
    openWorkspace(workspace.id, pref, navigate);
  };

  const onChoice = (newTab: boolean, remember: boolean) => {
    setAsking(false);
    if (remember) {
      updateProfile.mutate({
        preferences: { ...user?.preferences, openWorkspaceInNewTab: newTab },
      });
    }
    openWorkspace(workspace.id, newTab, navigate);
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
      <div className="mt-auto flex gap-2">
        <button
          onClick={onOpen}
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

  const selected = protocols.find((p) => p.name === protocol);
  // Typed fields from the registry; post-creation settings may tune the
  // advanced-tier params too when the template delegates them.
  const tunable = paramsFor(
    meta.data?.data,
    protocol,
    ['ui', 'advanced'],
    selected?.userParams ?? [],
  );

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

  const availableTemplates = templates.isSuccess
    ? templates.data.data.filter(
        (tpl) =>
          !catalog.isSuccess || catalog.data.data.some((img) => img.templates?.includes(tpl.name)),
      )
    : [];
  const template = availableTemplates.find((tpl) => tpl.name === templateRef);
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
    // Re-seed sliders and protocol choice on template change.
    setCpu(null);
    setMemory(null);
    setProtocol('');
    setProtoParams({});
  };

  // Protocol section: what the template declares, gated by its override
  // flags. The webhook re-validates server-side — this only mirrors it.
  const tplProtocols = template?.protocols ?? [];
  const defaultProtocol = tplProtocols.find((p) => p.default)?.name ?? tplProtocols[0]?.name ?? '';
  const protocolOverridable = template?.allowedOverrides?.includes('protocol') ?? false;
  const effectiveProtocol = protocol || defaultProtocol;
  const selectedProto = tplProtocols.find((p) => p.name === effectiveProtocol);
  // Creation-time params: UI-tier registry entries the template delegates,
  // pre-filled with the template's locked values as placeholders.
  const creationParams = paramsFor(
    meta.data?.data,
    effectiveProtocol,
    ['ui'],
    selectedProto?.userParams ?? [],
  );

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const chosenProtocol =
      protocolOverridable && effectiveProtocol !== defaultProtocol ? effectiveProtocol : undefined;
    const cleanedParams = Object.fromEntries(
      Object.entries(protoParams).filter(([, v]) => v !== ''),
    );
    create.mutate(
      {
        templateRef,
        displayName: displayName || undefined,
        resources: { cpu: formatCpu(cpuValue), memory: formatMemory(memValue) },
        overrides: chosenProtocol ? { protocol: chosenProtocol } : undefined,
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
            onChange={(e) => selectTemplate(e.target.value)}
            required
          >
            <option value="" disabled>
              —
            </option>
            {availableTemplates.map((tpl) => (
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

        {templateRef && (
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

        {templateRef && tplProtocols.length > 0 && (
          <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
            <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
              {t('portal.connection')}
            </legend>
            {tplProtocols.length > 1 && (
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
            )}
            {creationParams.length > 0 && (
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
