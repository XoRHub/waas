import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
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
import type { EnvVar, Toleration, Workspace } from '@/types';

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
  const [selRows, setSelRows] = useState<{ key: string; value: string }[]>(() =>
    Object.entries(runtime?.nodeSelector ?? {}).map(([key, value]) => ({ key, value })),
  );
  const [tolRows, setTolRows] = useState<Toleration[]>(() => runtime?.tolerations ?? []);
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
      const sel: Record<string, string> = {};
      for (const row of selRows) {
        if (row.key.trim() !== '') sel[row.key.trim()] = row.value;
      }
      if (JSON.stringify(sel) !== JSON.stringify(runtime?.nodeSelector ?? {})) {
        input.nodeSelector = sel;
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
  const rowInput =
    'rounded-md border border-slate-300 px-2 py-1.5 font-mono text-xs disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-white';
  const rowSelect =
    'rounded-md border border-slate-300 px-1 py-1.5 text-xs disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-white';
  const addButton =
    'rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700';
  const removeButton = 'rounded px-2 text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-700';

  const envEditable = canOverride('env');
  const selEditable = canOverride('nodeSelector');
  const tolEditable = canOverride('tolerations');
  const resEditable = canOverride('resources');

  return (
    <form id={formId} onSubmit={onSubmit} className="space-y-4">
      <p className="text-xs text-slate-400 dark:text-slate-500">{t('portal.runtime.hint')}</p>

      <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="flex items-center gap-2 px-1 text-sm text-slate-600 dark:text-slate-300">
          {t('portal.envOverrides')}
          {!envEditable && locked}
        </legend>
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
      </fieldset>

      <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
          {t('portal.runtime.placement')}
        </legend>
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-500 dark:text-slate-400">
            {t('portal.runtime.nodeSelector')}
          </span>
          {!selEditable && locked}
        </div>
        {selRows.map((row, i) => (
          <div key={i} className="flex gap-2">
            <input
              className={`w-2/5 ${rowInput}`}
              placeholder={t('portal.runtime.selectorKey')}
              value={row.key}
              disabled={!selEditable}
              onChange={(e) =>
                setSelRows((rows) =>
                  rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)),
                )
              }
            />
            <input
              className={`flex-1 ${rowInput}`}
              placeholder={t('portal.runtime.selectorValue')}
              value={row.value}
              disabled={!selEditable}
              onChange={(e) =>
                setSelRows((rows) =>
                  rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)),
                )
              }
            />
            {selEditable && (
              <button
                type="button"
                onClick={() => setSelRows((rows) => rows.filter((_, j) => j !== i))}
                className={removeButton}
                aria-label={t('app.delete')}
              >
                ✕
              </button>
            )}
          </div>
        ))}
        {selEditable && (
          <button
            type="button"
            onClick={() => setSelRows((rows) => [...rows, { key: '', value: '' }])}
            className={addButton}
          >
            + {t('portal.runtime.addSelector')}
          </button>
        )}

        <div className="flex items-center gap-2 pt-1">
          <span className="text-xs text-slate-500 dark:text-slate-400">
            {t('portal.runtime.tolerations')}
          </span>
          {!tolEditable && locked}
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
      </fieldset>

      <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="flex items-center gap-2 px-1 text-sm text-slate-600 dark:text-slate-300">
          {t('portal.runtime.resources')}
          {!resEditable && locked}
        </legend>
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
      </fieldset>
    </form>
  );
}
