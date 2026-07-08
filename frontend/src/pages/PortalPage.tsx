import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  useCatalog,
  useCreateWorkspace,
  useNamespacePreview,
  useDeleteRemoteWorkspace,
  useDeleteVolume,
  useDeleteWorkspace,
  useProtocolMeta,
  useQuota,
  useVolumes,
  useRemoteWorkspaces,
  useWakeRemoteWorkspace,
  useTemplates,
  useUpdateProfile,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { UserMenu } from '@/components/UserMenu';
import { Dialog } from '@/components/Dialog';
import { EventsDialog } from '@/components/EventsDialog';
import { ConnectionSettingsDialog } from '@/dialogs/ConnectionSettingsDialog';
import { DeleteWorkspaceDialog } from '@/dialogs/DeleteWorkspaceDialog';
import { RemoteWorkspaceDialog } from '@/dialogs/RemoteWorkspaceDialog';
import { OpenChoiceDialog } from '@/dialogs/OpenChoiceDialog';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import { ScheduleEditor, useNextTransitionLabel } from '@/components/ScheduleEditor';
import { FolderedGrid, SessionCard } from '@/components/SessionCard';
import { useAuthStore } from '@/stores/authStore';
import { useEvents } from '@/hooks/useEvents';
import { effectivePhase } from '@/lib/lifecycle';
import { targetFromRemote, targetFromWorkspace } from '@/lib/target';
import { templateAvailability } from '@/lib/templates';
import {
  displayCpu,
  displayMemory,
  formatCpu,
  formatMemory,
  parseCpu,
  parseMemory,
} from '@/lib/quantity';
import type { RemoteWorkspace, TemplateEnvVar, Workspace, WorkspaceSchedule } from '@/types';

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
  // Protocol section state: the visited tab, the chosen connection
  // protocol ('' = template default) and per-protocol param drafts —
  // switching tabs never loses edits (same model as connection settings).
  const [chosen, setChosen] = useState('');
  const [protoTab, setProtoTab] = useState('');
  const [protoParamsByProto, setProtoParamsByProto] = useState<
    Record<string, Record<string, string>>
  >({});
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [envRows, setEnvRows] = useState<{ name: string; value: string }[]>([]);
  const [scheduleOverride, setScheduleOverride] = useState<WorkspaceSchedule | undefined>(
    undefined,
  );
  const [homeVolumeName, setHomeVolumeName] = useState('');
  const nsPreview = useNamespacePreview(templateRef, displayName);
  const volumes = useVolumes();
  // "Start from an existing volume": only retained volumes living in the
  // RESOLVED destination namespace are attachable (PVCs are namespaced;
  // the webhook enforces the same rule server-side).
  const attachableVolumes = (volumes.data?.data ?? []).filter(
    (v) => v.namespace === nsPreview.data?.data.namespace,
  );

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
    setChosen('');
    setProtoTab('');
    setProtoParamsByProto({});
    setEnvRows([]);
    setScheduleOverride(undefined);
  };

  // Protocol section: what the template declares, gated by its override
  // flags. The webhook re-validates server-side — this only mirrors it.
  const tplProtocols = template?.protocols ?? [];
  const protoNames = tplProtocols.map((p) => p.name);
  const defaultProtocol = tplProtocols.find((p) => p.default)?.name ?? tplProtocols[0]?.name ?? '';
  const isAdmin = user?.role === 'admin';
  // A field is overridable when the template allows it AND the policy
  // does not restrict it away (admins bypass both, like the webhook).
  const policyAllows = (field: string) =>
    !q?.allowedOverrides || q.allowedOverrides.includes(field);
  const canOverride = (field: string) =>
    isAdmin || ((template?.allowedOverrides?.includes(field) ?? false) && policyAllows(field));
  const protocolOverridable = canOverride('protocol');
  const effectiveProtocol = (protocolOverridable && chosen) || defaultProtocol;
  const tab = protoNames.includes(protoTab) ? protoTab : effectiveProtocol || protoNames[0] || '';
  const tabProto = tplProtocols.find((p) => p.name === tab);

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const chosenProtocol =
      protocolOverridable && effectiveProtocol !== defaultProtocol ? effectiveProtocol : undefined;
    const cleanedByProto: Record<string, Record<string, string>> = {};
    for (const [proto, draft] of Object.entries(protoParamsByProto)) {
      const cleaned = Object.fromEntries(Object.entries(draft).filter(([, v]) => v !== ''));
      if (Object.keys(cleaned).length > 0) cleanedByProto[proto] = cleaned;
    }
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
        // spec.resources present = override (webhook contract): omit it
        // entirely when the right is absent, the template sizing applies.
        resources: canOverride('resources')
          ? { cpu: formatCpu(cpuValue), memory: formatMemory(memValue) }
          : undefined,
        overrides,
        homeVolumeName: homeVolumeName || undefined,
      },
      {
        onSuccess: ({ data: workspace }) => {
          // Connection tuning lives in the profile (as the post-creation
          // "Connection settings" dialog writes it); server re-validates
          // at connect time.
          if (Object.keys(cleanedByProto).length > 0 || chosenProtocol) {
            const settings = { ...user?.preferences?.workspaceSettings };
            settings[workspace.id] = {
              protocol: chosenProtocol,
              params: cleanedByProto[effectiveProtocol],
              paramsByProtocol: Object.keys(cleanedByProto).length > 0 ? cleanedByProto : undefined,
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
    <Dialog
      title={t('portal.newWorkspace')}
      onClose={onClose}
      onSubmit={onSubmit}
      footer={
        <>
          {create.isError && <p className="mr-auto text-sm text-red-600">{create.error.message}</p>}
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
        </>
      }
    >
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
      {/* Resolved server-side: what the precedence chain (template >
            global > built-in) actually yields for THIS user — never
            computed by the UI. */}
      {template && nsPreview.data?.data.namespace && (
        <p className="text-xs text-slate-500 dark:text-slate-400">
          {t('portal.namespacePreview')}{' '}
          <span className="font-mono text-slate-700 dark:text-slate-200">
            {nsPreview.data.data.namespace}
          </span>
        </p>
      )}
      {/* Reattach a retained volume as the home of this workspace. */}
      {template && attachableVolumes.length > 0 && (
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('volumes.attachExisting')}
          </span>
          <select
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={homeVolumeName}
            onChange={(e) => setHomeVolumeName(e.target.value)}
          >
            <option value="">{t('volumes.attachNone')}</option>
            {attachableVolumes.map((v) => (
              <option key={v.name} value={v.name}>
                {v.name} — {v.size}
                {v.originWorkspace ? ` (${v.originWorkspace})` : ''}
              </option>
            ))}
          </select>
          <span className="text-xs text-slate-400 dark:text-slate-500">
            {t('volumes.attachHint')}
          </span>
        </label>
      )}

      {template && !canOverride('resources') && (
        // No "resources" right: the sizing is the template's and the
        // payload OMITS spec.resources entirely — sending it (even with
        // identical values) counts as an override and the webhook
        // rejects it. Display only.
        <p className="text-sm text-slate-500 dark:text-slate-400">
          {t('portal.fixedSizing', {
            cpu: displayCpu(cpuBounds.initial),
            memory: displayMemory(memBounds.initial),
          })}
        </p>
      )}
      {template && canOverride('resources') && (
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
            <>
              <ProtocolTabs
                protocols={protoNames}
                active={tab}
                onSelect={setProtoTab}
                badge={(p) =>
                  p === effectiveProtocol ? <span className="text-[10px]">●</span> : null
                }
              />
              <label className="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
                <input
                  type="radio"
                  name="create-chosen-protocol"
                  checked={effectiveProtocol === tab}
                  disabled={!protocolOverridable}
                  onChange={() => setChosen(tab)}
                />
                {t('portal.useThisProtocol')}
                {tab === defaultProtocol && (
                  <span className="text-xs text-slate-400">({t('portal.protocolDefault')})</span>
                )}
                {!protocolOverridable && (
                  <span className="text-xs text-slate-400" title={t('portal.protocolLockedHint')}>
                    🔒 {t('portal.protocolLocked')}
                  </span>
                )}
              </label>
            </>
          ) : (
            // Single protocol (typically a legacy template with the
            // OS-derived synthesized entry): show it instead of an
            // empty box — the connection is never a mystery.
            <p className="text-sm text-slate-600 dark:text-slate-300">
              {t('portal.protocol')}:{' '}
              <span className="font-medium">{tplProtocols[0].name.toUpperCase()}</span>
              {tplProtocols[0].port ? (
                <span className="text-slate-400">
                  {' '}
                  · {t('portal.port')} {tplProtocols[0].port}
                </span>
              ) : null}{' '}
              <span className="text-xs text-slate-400">({t('portal.protocolDefault')})</span>
            </p>
          )}
          {/* The SAME shared per-protocol form as connection settings
                and the admin template editor: template-locked values as
                placeholders, userParams allow-list (admins bypass). */}
          {tabProto && (
            <ProtocolParamsForm
              meta={meta.data?.data}
              protocol={tabProto.name}
              values={protoParamsByProto[tabProto.name] ?? {}}
              onChange={(name, value) =>
                setProtoParamsByProto((prev) => ({
                  ...prev,
                  [tabProto.name]: { ...prev[tabProto.name], [name]: value },
                }))
              }
              allowList={isAdmin ? undefined : (tabProto.userParams ?? [])}
              placeholders={tabProto.params}
            />
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
    </Dialog>
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
