import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import { ImagePicker } from '@/components/ImagePicker';
import { KeyValueEditor } from '@/components/KeyValueEditor';
import { TabbedPanels, type PanelTab } from '@/components/TabbedPanels';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import { ScheduleEditor } from '@/components/ScheduleEditor';
import {
  useCatalog,
  useCreateWorkspace,
  useNamespacePreview,
  useProtocolMeta,
  useQuota,
  useTemplates,
  useUpdateProfile,
  useVolumes,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import { RunningLimitDialog } from '@/dialogs/RunningLimitDialog';
import { useAuthStore } from '@/stores/authStore';
import { canOverrideField } from '@/lib/overrides';
import { templateAvailability, templateIcon } from '@/lib/templates';
import {
  displayCpu,
  displayMemory,
  formatCpu,
  formatMemory,
  parseCpu,
  parseMemory,
} from '@/lib/quantity';
import type { TemplateEnvVar, WorkspaceSchedule } from '@/types';

// Slider steps: 0.25 vCPU and 256Mi. Exported with the floors so the
// runtime settings tab sizes its sliders identically.
export const CPU_STEP = 250;
export const MEM_STEP = 256 * 1024 * 1024;
// Floors when neither the image nor the policy declares a minimum.
export const CPU_FLOOR = 250;
export const MEM_FLOOR = 512 * 1024 * 1024;

export interface SliderBounds {
  min: number;
  max: number;
  initial: number;
}

// clampRange derives one slider's bounds: min from the image, max from
// min(image.max, policy.perWorkspace, remaining aggregate), initial from
// image.defaults ?? policy defaults ?? template requests (then clamped).
// Exported for its unit tests: this is where quota UX bugs would live.
export function clampRange(
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

export function CreateWorkspaceDialog({ onClose }: { onClose: () => void }) {
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
  const [envRows, setEnvRows] = useState<{ name: string; value: string }[]>([]);
  const [scheduleOverride, setScheduleOverride] = useState<WorkspaceSchedule | undefined>(
    undefined,
  );
  const [labelsOv, setLabelsOv] = useState<Record<string, string>>({});
  const [annotationsOv, setAnnotationsOv] = useState<Record<string, string>>({});
  const [homeVolumeName, setHomeVolumeName] = useState('');
  // Running-quota flow: when every running slot is taken, submission
  // detours through a choice dialog (create paused / pause one first).
  const [slotDialogOpen, setSlotDialogOpen] = useState(false);
  const workspaces = useWorkspaces();
  const pauseAction = useWorkspaceAction();
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
    setLabelsOv({});
    setAnnotationsOv({});
  };

  // Protocol section: what the template declares, gated by its override
  // flags. The webhook re-validates server-side — this only mirrors it.
  const tplProtocols = template?.protocols ?? [];
  const protoNames = tplProtocols.map((p) => p.name);
  const defaultProtocol = tplProtocols.find((p) => p.default)?.name ?? tplProtocols[0]?.name ?? '';
  const isAdmin = user?.role === 'admin';
  // Template ∩ policy gate, shared with the runtime settings tab
  // (lib/overrides mirrors the webhook; enforcement stays server-side).
  const canOverride = (field: string) =>
    canOverrideField(field, {
      isAdmin,
      templateAllows: template?.allowedOverrides,
      policyAllows: q?.allowedOverrides,
    });
  const protocolOverridable = canOverride('protocol');
  const effectiveProtocol = (protocolOverridable && chosen) || defaultProtocol;
  const tab = protoNames.includes(protoTab) ? protoTab : effectiveProtocol || protoNames[0] || '';
  const tabProto = tplProtocols.find((p) => p.name === tab);

  const runningLimitReached =
    q?.maxRunningWorkspaces != null && (q.runningWorkspaces ?? 0) >= q.maxRunningWorkspaces;
  const runningWorkspaces = (workspaces.data?.data ?? []).filter((ws) => !ws.paused);

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    // The card grid has no native `required`: an empty selection just
    // refuses to submit (cards are the only way to pick).
    if (!templateRef) return;
    if (runningLimitReached) {
      setSlotDialogOpen(true);
      return;
    }
    submitCreate(false);
  };

  const submitCreate = (paused: boolean) => {
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
    const labels =
      canOverride('metadata') && Object.keys(labelsOv).length > 0 ? labelsOv : undefined;
    const annotations =
      canOverride('metadata') && Object.keys(annotationsOv).length > 0 ? annotationsOv : undefined;
    const overrides =
      chosenProtocol || env.length > 0 || scheduleOv || labels || annotations
        ? {
            protocol: chosenProtocol,
            env: env.length > 0 ? env : undefined,
            schedule: scheduleOv,
            labels,
            annotations,
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
        paused: paused || undefined,
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

  // Sections of a selected template, mirroring the connection-settings
  // dialog: Connection | Workspace, the latter one tab per override
  // group. At creation nothing is stored yet, so the tabs are purely
  // right-filtered — never locked. Plain JSX constants, NOT a nested
  // component: a component recreated each render would remount and lose
  // the editors' internal drafts on every keystroke.
  const connectionContent = (
    <>
      {tplProtocols.length > 1 ? (
        <>
          <ProtocolTabs
            protocols={protoNames}
            active={tab}
            onSelect={setProtoTab}
            badge={(p) => (p === effectiveProtocol ? <span className="text-[10px]">●</span> : null)}
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
      ) : tplProtocols.length === 1 ? (
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
      ) : null}
      {/* The SAME shared per-protocol form as connection settings
            and the admin template editor: template-locked values as
            placeholders, the server-resolved userParams allow-list
            (cat: expanded; admins bypass). */}
      {tabProto && (
        <ProtocolParamsForm
          key={tabProto.name}
          meta={meta.data?.data}
          protocol={tabProto.name}
          values={protoParamsByProto[tabProto.name] ?? {}}
          onChange={(name, value) =>
            setProtoParamsByProto((prev) => ({
              ...prev,
              [tabProto.name]: { ...prev[tabProto.name], [name]: value },
            }))
          }
          allowList={isAdmin ? undefined : (tabProto.resolvedUserParams ?? [])}
          placeholders={tabProto.params}
          audioPortExposed={tabProto.exposeAudioPort ?? false}
          // kasmvnc: no userParams by design, but the admin's config
          // exists and will apply — surface it read-only (the raw
          // template text: the workspace is not born yet, the
          // policy-merged effective content does not exist).
          kasmvncConfig={
            tabProto.name === 'kasmvnc'
              ? { content: template?.kasmvncConfig ?? '', variant: 'template' }
              : undefined
          }
        />
      )}
    </>
  );

  const workspaceTabs: PanelTab[] = [
    ...(canOverride('resources')
      ? [
          {
            id: 'resources',
            label: t('portal.runtime.resources'),
            content: (
              <div className="space-y-4">
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
              </div>
            ),
          },
        ]
      : []),
    ...(canOverride('env')
      ? [
          {
            id: 'env',
            label: t('portal.runtime.environment'),
            content: (
              <div className="space-y-2">
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
            ),
          },
        ]
      : []),
    ...(canOverride('metadata')
      ? [
          {
            id: 'metadata',
            label: t('portal.runtime.metadata'),
            content: (
              <div className="space-y-2">
                <span className="block text-xs text-slate-500 dark:text-slate-400">
                  {t('portal.runtime.labels')}
                </span>
                {/* Keyed on the template: switching templates re-seeds
                    the editors like the other override drafts. */}
                <KeyValueEditor
                  key={`labels-${templateRef}`}
                  value={labelsOv}
                  onChange={setLabelsOv}
                  keyPlaceholder={t('portal.runtime.metaKey')}
                  valuePlaceholder={t('portal.runtime.metaValue')}
                  addLabel={t('portal.runtime.addLabel')}
                />
                <span className="block text-xs text-slate-500 dark:text-slate-400">
                  {t('portal.runtime.annotations')}
                </span>
                <KeyValueEditor
                  key={`annotations-${templateRef}`}
                  value={annotationsOv}
                  onChange={setAnnotationsOv}
                  keyPlaceholder={t('portal.runtime.metaKey')}
                  valuePlaceholder={t('portal.runtime.metaValue')}
                  addLabel={t('portal.runtime.addAnnotation')}
                />
                <p className="text-xs text-slate-400 dark:text-slate-500">
                  {t('portal.runtime.metadataHint')}
                </p>
              </div>
            ),
          },
        ]
      : []),
    ...(canOverride('schedule')
      ? [
          {
            id: 'schedule',
            label: t('portal.runtime.schedule'),
            content: (
              <ScheduleEditor
                value={scheduleOverride ?? template?.schedule}
                onChange={setScheduleOverride}
              />
            ),
          },
        ]
      : []),
  ];

  const sections: PanelTab[] = [
    ...(tplProtocols.length > 0
      ? [
          {
            id: 'connection',
            label: t('portal.settingsTabConnection'),
            content: connectionContent,
          },
        ]
      : []),
    ...(workspaceTabs.length > 0
      ? [
          {
            id: 'workspace',
            label: t('portal.settingsTabWorkspace'),
            content: (
              <>
                <p className="text-xs text-slate-400 dark:text-slate-500">
                  {t('portal.advancedModeHint')}
                </p>
                <TabbedPanels tabs={workspaceTabs} />
              </>
            ),
          },
        ]
      : []),
  ];

  return (
    <>
      <Dialog
        title={t('portal.newWorkspace')}
        onClose={onClose}
        onSubmit={onSubmit}
        footer={
          <>
            {create.isError && (
              <p className="mr-auto text-sm text-red-600">{create.error.message}</p>
            )}
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
        <div>
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('portal.template')}</span>
          <div className="mt-1">
            <ImagePicker
              label={t('portal.template')}
              placeholder={t('portal.templatePlaceholder')}
              value={templateRef}
              onChange={selectTemplate}
              options={availability.map(({ template: tpl, available }) => ({
                id: tpl.name,
                icon: templateIcon(tpl, catalog.data?.data),
                os: tpl.os,
                title: tpl.displayName,
                subtitle: `${tpl.os}${
                  tpl.protocols?.length ? ` · ${tpl.protocols.map((p) => p.name).join('/')}` : ''
                }`,
                disabled: !available,
                disabledReason: t('portal.templateUnavailable'),
              }))}
            />
          </div>
        </div>
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
        {template && sections.length > 0 && <TabbedPanels tabs={sections} />}
      </Dialog>
      {/* Sibling, never a child: the base Dialog is a <form> and forms
          cannot nest. */}
      {slotDialogOpen && q?.maxRunningWorkspaces != null && (
        <RunningLimitDialog
          running={q.runningWorkspaces ?? 0}
          max={q.maxRunningWorkspaces}
          workspaces={runningWorkspaces}
          pending={pauseAction.isPending || create.isPending}
          error={pauseAction.error?.message}
          onConfirm={(choice) => {
            if (choice.paused) {
              setSlotDialogOpen(false);
              submitCreate(true);
            } else {
              // Chain: pause the chosen sibling, then create. A lost race
              // (another tab grabbed the slot) surfaces as the usual
              // server denial in the create dialog footer.
              pauseAction.mutate(
                { id: choice.pauseId, action: 'pause' },
                {
                  onSuccess: () => {
                    setSlotDialogOpen(false);
                    submitCreate(false);
                  },
                },
              );
            }
          }}
          onClose={() => setSlotDialogOpen(false)}
        />
      )}
    </>
  );
}

export function ResourceSlider({
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
