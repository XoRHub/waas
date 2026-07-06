import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  useCatalog,
  useCreateWorkspace,
  useDeleteWorkspace,
  useQuota,
  useTemplates,
  useUpdateProfile,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { StatusBadge } from '@/components/StatusBadge';
import { UserMenu } from '@/components/UserMenu';
import { useAuthStore } from '@/stores/authStore';
import { displayCpu, displayMemory, formatCpu, formatMemory, parseCpu, parseMemory } from '@/lib/quantity';
import type { Workspace } from '@/types';

export function PortalPage() {
  const { t } = useTranslation();
  const workspaces = useWorkspaces();
  const [creating, setCreating] = useState(false);

  return (
    <div className="min-h-screen bg-slate-100 dark:bg-slate-900">
      <header className="flex items-center justify-between bg-white px-6 py-4 shadow-sm dark:bg-slate-800">
        <h1 className="text-lg font-semibold text-slate-900 dark:text-white">{t('portal.title')}</h1>
        <div className="flex items-center gap-4">
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
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {workspaces.isSuccess &&
            workspaces.data.data.map((ws) => <WorkspaceCard key={ws.id} workspace={ws} />)}
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
  const create = useCreateWorkspace();
  const [templateRef, setTemplateRef] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [cpu, setCpu] = useState<number | null>(null);
  const [memory, setMemory] = useState<number | null>(null);

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
    // Re-seed the sliders on template change: bounds and defaults differ.
    setCpu(null);
    setMemory(null);
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(
      {
        templateRef,
        displayName: displayName || undefined,
        resources: { cpu: formatCpu(cpuValue), memory: formatMemory(memValue) },
      },
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
