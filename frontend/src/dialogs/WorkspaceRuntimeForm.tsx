import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { KeyValueEditor, addButton, removeButton, rowInput } from '@/components/KeyValueEditor';
import { ScheduleEditor } from '@/components/ScheduleEditor';
import { TabbedPanels, type PanelTab } from '@/components/TabbedPanels';
import {
  CPU_FLOOR,
  CPU_STEP,
  MEM_FLOOR,
  MEM_STEP,
  ResourceSlider,
  clampRange,
} from '@/dialogs/CreateWorkspaceDialog';
import { useCatalog, useQuota, useTemplates, type UpdateOverridesInput } from '@/hooks/useApi';
import { canOverrideField } from '@/lib/overrides';
import {
  displayCpu,
  displayMemory,
  formatCpu,
  formatMemory,
  parseCpu,
  parseMemory,
} from '@/lib/quantity';
import { useAuthStore } from '@/stores/authStore';
import type { EnvVar, Toleration, Workspace, WorkspaceSchedule } from '@/types';

const TOLERATION_OPERATORS = ['Equal', 'Exists'];
const TOLERATION_EFFECTS = ['', 'NoSchedule', 'PreferNoSchedule', 'NoExecute'];

interface EnvRow {
  name: string;
  value: string;
  /** Reference entries (secret/configmap) round-trip untouched: the
   *  editor never sees, edits or drops the referenced value. */
  valueFrom?: EnvVar['valueFrom'];
}

const toEnvRows = (env: EnvVar[]): EnvRow[] =>
  env.map((e) => ({ name: e.name, value: e.value ?? '', valueFrom: e.valueFrom }));

const fromEnvRows = (rows: EnvRow[]): EnvVar[] =>
  rows
    .filter((r) => r.name.trim() !== '')
    .map((r) =>
      r.valueFrom
        ? { name: r.name.trim(), valueFrom: r.valueFrom }
        : { name: r.name.trim(), value: r.value },
    );

/**
 * The "Workspace" tab of the connection-settings dialog: runtime
 * reconfiguration of an INSTANTIATED workspace — env, node placement
 * (nodeSelector + tolerations) and sizing. Each group is editable only
 * when the template ∩ policy rights allow it (admins bypass), exactly
 * like the creation dialog; the webhook stays the enforcement point.
 * Submitting sends ONLY the changed fields (a provided field replaces
 * the stored override wholesale); the change reaches the desktop at the
 * next stop/start boundary — or immediately via the drift badge reload.
 */
export function WorkspaceRuntimeForm({
  workspace,
  formId,
  onApply,
}: {
  workspace: Workspace;
  /** The dialog footer's Apply button targets this form id. */
  formId: string;
  /** Called on submit with the changed fields, or null when nothing
   *  editable changed (the caller just closes). */
  onApply: (input: UpdateOverridesInput | null) => void;
}) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const templates = useTemplates();
  const catalog = useCatalog();
  const quota = useQuota();

  const template = templates.data?.data.find((tpl) => tpl.name === workspace.templateRef);
  const image = catalog.data?.data.find((img) => img.templates?.includes(workspace.templateRef));
  const q = quota.data?.data;
  const isAdmin = user?.role === 'admin';
  const canOverride = (field: string) =>
    canOverrideField(field, {
      isAdmin,
      templateAllows: template?.allowedOverrides,
      policyAllows: q?.allowedOverrides,
    });

  const runtime = workspace.runtime;
  const [envRows, setEnvRows] = useState<EnvRow[]>(() => toEnvRows(runtime?.env ?? []));
  const [nodeSel, setNodeSel] = useState<Record<string, string>>(() => runtime?.nodeSelector ?? {});
  const [tolRows, setTolRows] = useState<Toleration[]>(() => runtime?.tolerations ?? []);
  const [labels, setLabels] = useState<Record<string, string>>(() => runtime?.labels ?? {});
  const [annotations, setAnnotations] = useState<Record<string, string>>(
    () => runtime?.annotations ?? {},
  );
  // Displayed schedule: the stored override, else the EFFECTIVE schedule
  // the model already resolved (workspace.schedule = override ?? template)
  // — synchronous, unlike the templates query.
  const [schedule, setSchedule] = useState<WorkspaceSchedule | undefined>(
    () => runtime?.schedule ?? workspace.schedule,
  );
  const hadResources = Boolean(runtime?.resources);
  const [customSizing, setCustomSizing] = useState(hadResources);
  const [cpu, setCpu] = useState<number | null>(null);
  const [memory, setMemory] = useState<number | null>(null);

  // Same bounds as creation, with two differences: the INITIAL value is
  // the workspace's current sizing, and the remaining aggregate gets the
  // current sizing back (the quota's `used` already counts this
  // workspace — without the correction the slider could not even keep
  // the current value). The webhook re-checks the real limits anyway.
  const currentCpu = runtime?.resources?.cpu ?? template?.requests?.cpu;
  const currentMem = runtime?.resources?.memory ?? template?.requests?.memory;
  const remaining = (kind: 'cpu' | 'memory', parse: (s: string) => number, current?: string) => {
    const limit = q?.limits?.[kind];
    if (!limit) return undefined;
    return parse(limit) - parse(q?.used?.[kind] ?? '0') + (current ? parse(current) : 0);
  };
  const cpuBounds = clampRange(
    {
      min: image?.min?.cpu ? parseCpu(image.min.cpu) : undefined,
      maxes: [
        image?.max?.cpu ? parseCpu(image.max.cpu) : undefined,
        q?.perWorkspace?.cpu ? parseCpu(q.perWorkspace.cpu) : undefined,
        remaining('cpu', parseCpu, currentCpu),
      ],
      defaults: [
        currentCpu ? parseCpu(currentCpu) : undefined,
        image?.defaults?.cpu ? parseCpu(image.defaults.cpu) : undefined,
        q?.defaults?.cpu ? parseCpu(q.defaults.cpu) : undefined,
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
        remaining('memory', parseMemory, currentMem),
      ],
      defaults: [
        currentMem ? parseMemory(currentMem) : undefined,
        image?.defaults?.memory ? parseMemory(image.defaults.memory) : undefined,
        q?.defaults?.memory ? parseMemory(q.defaults.memory) : undefined,
      ],
    },
    MEM_FLOOR,
    MEM_STEP,
  );
  const cpuValue = cpu ?? cpuBounds.initial;
  const memValue = memory ?? memBounds.initial;

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    // Only CHANGED fields travel: a provided field replaces the stored
    // override wholesale, so an untouched group must stay absent.
    const input: UpdateOverridesInput = {};
    if (canOverride('env')) {
      const env = fromEnvRows(envRows);
      if (JSON.stringify(env) !== JSON.stringify(fromEnvRows(toEnvRows(runtime?.env ?? [])))) {
        input.env = env;
      }
    }
    if (canOverride('nodeSelector')) {
      if (JSON.stringify(nodeSel) !== JSON.stringify(runtime?.nodeSelector ?? {})) {
        input.nodeSelector = nodeSel;
      }
    }
    if (canOverride('tolerations')) {
      const tols = tolRows.filter((tol) =>
        Boolean(tol.key || tol.operator || tol.value || tol.effect),
      );
      if (JSON.stringify(tols) !== JSON.stringify(runtime?.tolerations ?? [])) {
        input.tolerations = tols;
      }
    }
    if (canOverride('metadata')) {
      if (JSON.stringify(labels) !== JSON.stringify(runtime?.labels ?? {})) {
        input.labels = labels;
      }
      if (JSON.stringify(annotations) !== JSON.stringify(runtime?.annotations ?? {})) {
        input.annotations = annotations;
      }
    }
    if (canOverride('schedule')) {
      // Compared against the DISPLAYED initial: untouched sends nothing;
      // edited sends the full schedule; cleared sends {} — back to the
      // template's schedule (presence = override).
      const initial = runtime?.schedule ?? workspace.schedule;
      if (JSON.stringify(schedule ?? null) !== JSON.stringify(initial ?? null)) {
        input.schedule = schedule ?? {};
      }
    }
    if (canOverride('resources')) {
      if (customSizing) {
        const res = { cpu: formatCpu(cpuValue), memory: formatMemory(memValue) };
        if (JSON.stringify(res) !== JSON.stringify(runtime?.resources ?? null)) {
          input.resources = res;
        }
      } else if (hadResources) {
        // Presence = override: the empty map reverts to template sizing.
        input.resources = {};
      }
    }
    onApply(Object.keys(input).length > 0 ? input : null);
  };

  const locked = (
    <span className="text-xs text-slate-400 dark:text-slate-500">
      🔒 {t('portal.runtime.locked')}
    </span>
  );
  const rowSelect =
    'rounded-md border border-slate-300 px-1 py-1.5 text-xs disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-white';

  const envEditable = canOverride('env');
  const selEditable = canOverride('nodeSelector');
  const tolEditable = canOverride('tolerations');
  const resEditable = canOverride('resources');
  const metaEditable = canOverride('metadata');
  const schedEditable = canOverride('schedule');
  const placementEditable = selEditable || tolEditable;

  // One tab per group. A group gets its tab when the right is delegated
  // OR a value is already stored — a stored override with the right
  // revoked shows read-only (🔒): hiding it would keep it silently
  // applying with no UI trace. No right and nothing stored = no tab.
  const stored = {
    env: (runtime?.env?.length ?? 0) > 0,
    placement:
      Object.keys(runtime?.nodeSelector ?? {}).length > 0 ||
      (runtime?.tolerations?.length ?? 0) > 0,
    metadata:
      Object.keys(runtime?.labels ?? {}).length > 0 ||
      Object.keys(runtime?.annotations ?? {}).length > 0,
    schedule: Boolean(runtime?.schedule),
    resources: hadResources,
  };

  const envContent = (
    <>
      {envRows.map((row, i) => (
        <div key={i} className="flex gap-2">
          <input
            className={`w-2/5 ${rowInput}`}
            placeholder={t('portal.envName')}
            value={row.name}
            disabled={!envEditable}
            onChange={(e) =>
              setEnvRows((rows) =>
                rows.map((r, j) => (j === i ? { ...r, name: e.target.value } : r)),
              )
            }
          />
          <input
            className={`flex-1 ${rowInput}`}
            placeholder={row.valueFrom ? t('portal.runtime.valueFromRef') : t('portal.envValue')}
            value={row.valueFrom ? '' : row.value}
            disabled={!envEditable || Boolean(row.valueFrom)}
            onChange={(e) =>
              setEnvRows((rows) =>
                rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)),
              )
            }
          />
          {envEditable && (
            <button
              type="button"
              onClick={() => setEnvRows((rows) => rows.filter((_, j) => j !== i))}
              className={removeButton}
              aria-label={t('app.delete')}
            >
              ✕
            </button>
          )}
        </div>
      ))}
      {envEditable && (
        <button
          type="button"
          onClick={() => setEnvRows((rows) => [...rows, { name: '', value: '' }])}
          className={addButton}
        >
          + {t('portal.addEnvVar')}
        </button>
      )}
    </>
  );

  const placementContent = (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('portal.runtime.nodeSelector')}
        </span>
        {!selEditable && placementEditable && locked}
      </div>
      <KeyValueEditor
        value={nodeSel}
        onChange={setNodeSel}
        disabled={!selEditable}
        keyPlaceholder={t('portal.runtime.selectorKey')}
        valuePlaceholder={t('portal.runtime.selectorValue')}
        addLabel={t('portal.runtime.addSelector')}
      />

      <div className="flex items-center gap-2 pt-1">
        <span className="text-xs text-slate-500 dark:text-slate-400">
          {t('portal.runtime.tolerations')}
        </span>
        {!tolEditable && placementEditable && locked}
      </div>
      {tolRows.map((tol, i) => (
        <div key={i} className="flex gap-1">
          <input
            className={`w-1/4 ${rowInput}`}
            placeholder={t('portal.runtime.tolKey')}
            value={tol.key ?? ''}
            disabled={!tolEditable}
            onChange={(e) =>
              setTolRows((rows) =>
                rows.map((r, j) => (j === i ? { ...r, key: e.target.value || undefined } : r)),
              )
            }
          />
          <select
            className={rowSelect}
            aria-label={t('portal.runtime.tolOperator')}
            value={tol.operator ?? 'Equal'}
            disabled={!tolEditable}
            onChange={(e) =>
              setTolRows((rows) =>
                rows.map((r, j) => (j === i ? { ...r, operator: e.target.value } : r)),
              )
            }
          >
            {TOLERATION_OPERATORS.map((op) => (
              <option key={op} value={op}>
                {op}
              </option>
            ))}
          </select>
          <input
            className={`w-1/5 ${rowInput}`}
            placeholder={t('portal.runtime.tolValue')}
            value={tol.value ?? ''}
            disabled={!tolEditable || tol.operator === 'Exists'}
            onChange={(e) =>
              setTolRows((rows) =>
                rows.map((r, j) => (j === i ? { ...r, value: e.target.value || undefined } : r)),
              )
            }
          />
          <select
            className={rowSelect}
            aria-label={t('portal.runtime.tolEffect')}
            value={tol.effect ?? ''}
            disabled={!tolEditable}
            onChange={(e) =>
              setTolRows((rows) =>
                rows.map((r, j) => (j === i ? { ...r, effect: e.target.value || undefined } : r)),
              )
            }
          >
            {TOLERATION_EFFECTS.map((effect) => (
              <option key={effect} value={effect}>
                {effect === '' ? t('portal.runtime.tolAnyEffect') : effect}
              </option>
            ))}
          </select>
          {tolEditable && (
            <button
              type="button"
              onClick={() => setTolRows((rows) => rows.filter((_, j) => j !== i))}
              className={removeButton}
              aria-label={t('app.delete')}
            >
              ✕
            </button>
          )}
        </div>
      ))}
      {tolEditable && (
        <button
          type="button"
          onClick={() => setTolRows((rows) => [...rows, { operator: 'Equal' }])}
          className={addButton}
        >
          + {t('portal.runtime.addToleration')}
        </button>
      )}
    </div>
  );

  const metadataContent = (
    <div className="space-y-2">
      <span className="text-xs text-slate-500 dark:text-slate-400">
        {t('portal.runtime.labels')}
      </span>
      <KeyValueEditor
        value={labels}
        onChange={setLabels}
        disabled={!metaEditable}
        keyPlaceholder={t('portal.runtime.metaKey')}
        valuePlaceholder={t('portal.runtime.metaValue')}
        addLabel={t('portal.runtime.addLabel')}
      />
      <span className="block pt-1 text-xs text-slate-500 dark:text-slate-400">
        {t('portal.runtime.annotations')}
      </span>
      <KeyValueEditor
        value={annotations}
        onChange={setAnnotations}
        disabled={!metaEditable}
        keyPlaceholder={t('portal.runtime.metaKey')}
        valuePlaceholder={t('portal.runtime.metaValue')}
        addLabel={t('portal.runtime.addAnnotation')}
      />
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('portal.runtime.metadataHint')}
      </p>
    </div>
  );

  const resourcesContent = (
    <div className="space-y-3">
      {resEditable ? (
        <>
          <label className="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
            <input
              type="checkbox"
              checked={customSizing}
              onChange={(e) => setCustomSizing(e.target.checked)}
            />
            {t('portal.runtime.customSizing')}
          </label>
          {customSizing ? (
            <>
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
            </>
          ) : (
            <p className="text-xs text-slate-400 dark:text-slate-500">
              {t('portal.runtime.templateSizing')}
            </p>
          )}
        </>
      ) : (
        // Same story as portal.fixedSizing at creation: display only,
        // never a payload — presence would count as an override.
        <p className="text-sm text-slate-500 dark:text-slate-400">
          {t('portal.fixedSizing', {
            cpu: displayCpu(cpuBounds.initial),
            memory: displayMemory(memBounds.initial),
          })}
        </p>
      )}
    </div>
  );

  const tabs: PanelTab[] = [
    ...(envEditable || stored.env
      ? [
          {
            id: 'env',
            label: t('portal.runtime.environment'),
            locked: !envEditable,
            content: envContent,
          },
        ]
      : []),
    ...(placementEditable || stored.placement
      ? [
          {
            id: 'placement',
            label: t('portal.runtime.placement'),
            locked: !placementEditable,
            content: placementContent,
          },
        ]
      : []),
    ...(metaEditable || stored.metadata
      ? [
          {
            id: 'metadata',
            label: t('portal.runtime.metadata'),
            locked: !metaEditable,
            content: metadataContent,
          },
        ]
      : []),
    ...(schedEditable || stored.schedule
      ? [
          {
            id: 'schedule',
            label: t('portal.runtime.schedule'),
            locked: !schedEditable,
            content: (
              <ScheduleEditor value={schedule} onChange={setSchedule} disabled={!schedEditable} />
            ),
          },
        ]
      : []),
    ...(resEditable || stored.resources
      ? [
          {
            id: 'resources',
            label: t('portal.runtime.resources'),
            locked: !resEditable,
            content: resourcesContent,
          },
        ]
      : []),
  ];

  return (
    <form id={formId} onSubmit={onSubmit} className="space-y-4">
      <p className="text-xs text-slate-400 dark:text-slate-500">{t('portal.runtime.hint')}</p>
      {tabs.length > 0 ? (
        <TabbedPanels tabs={tabs} />
      ) : (
        <p className="text-sm text-slate-500 dark:text-slate-400">
          {t('portal.runtime.nothingEditable')}
        </p>
      )}
    </form>
  );
}
