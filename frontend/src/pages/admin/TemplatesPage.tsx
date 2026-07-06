import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useDeleteTemplate,
  useProtocolMeta,
  useSaveTemplate,
  useTemplates,
  type TemplateInput,
  type TemplateProtocolInput,
} from '@/hooks/useApi';
import { ParamField, paramsFor } from '@/components/ParamField';
import type { TemplateEnvVar, WorkspaceTemplate } from '@/types';

const EMPTY: TemplateInput = {
  name: '',
  displayName: '',
  description: '',
  os: 'linux',
  image: '',
  homeSize: '10Gi',
  protocols: [],
};

// Every OverridableField the CR knows; 'protocol'/'protocolParams' drive
// the workspace-creation form, the rest gate spec.overrides facets.
const OVERRIDABLE_FIELDS = [
  'protocol',
  'protocolParams',
  'resources',
  'env',
  'securityContext',
  'podSecurityContext',
  'volumes',
  'nodeSelector',
  'tolerations',
] as const;

const DEFAULT_PORTS: Record<string, number> = { vnc: 5901, rdp: 3389, ssh: 2222 };

const field =
  'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white';
const fieldSm =
  'mt-0.5 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white';

export function TemplatesPage() {
  const { t } = useTranslation();
  const templates = useTemplates();
  const remove = useDeleteTemplate();
  const [editing, setEditing] = useState<{ isNew: boolean; input: TemplateInput } | null>(null);

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <button
          onClick={() => setEditing({ isNew: true, input: EMPTY })}
          className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('admin.templatesPage.new')}
        </button>
      </div>

      {templates.isPending && <p className="text-slate-500">{t('app.loading')}</p>}
      {templates.isError && <p className="text-red-600">{t('app.error')}</p>}
      {templates.isSuccess && templates.data.data.length === 0 && (
        <p className="text-slate-500 dark:text-slate-400">{t('admin.templatesPage.empty')}</p>
      )}

      {templates.isSuccess && templates.data.data.length > 0 && (
        <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
              <tr>
                <th className="px-4 py-3">{t('admin.templatesPage.name')}</th>
                <th className="px-4 py-3">{t('admin.templatesPage.displayName')}</th>
                <th className="px-4 py-3">{t('admin.templatesPage.os')}</th>
                <th className="px-4 py-3">{t('admin.templatesPage.protocols')}</th>
                <th className="px-4 py-3">{t('admin.templatesPage.image')}</th>
                <th className="px-4 py-3">{t('admin.templatesPage.homeSize')}</th>
                <th className="px-4 py-3">{t('app.actions')}</th>
              </tr>
            </thead>
            <tbody className="text-slate-800 dark:text-slate-100">
              {templates.data.data.map((tpl) => (
                <tr
                  key={tpl.name}
                  className="border-b border-slate-100 last:border-0 dark:border-slate-700"
                >
                  <td className="px-4 py-3 font-mono text-xs">{tpl.name}</td>
                  <td className="px-4 py-3 font-medium">{tpl.displayName}</td>
                  <td className="px-4 py-3">{tpl.os}</td>
                  <td className="px-4 py-3">
                    <span className="flex gap-1">
                      {(tpl.protocols ?? []).map((p) => (
                        <span
                          key={p.name}
                          className={`rounded px-1.5 py-0.5 text-xs uppercase ${
                            p.default
                              ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300'
                              : 'bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300'
                          }`}
                        >
                          {p.name}
                        </span>
                      ))}
                    </span>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs">{tpl.image}</td>
                  <td className="px-4 py-3">{tpl.homeSize}</td>
                  <td className="px-4 py-3">
                    <div className="flex gap-3">
                      <button
                        onClick={() => setEditing({ isNew: false, input: toInput(tpl) })}
                        className="text-sm text-blue-600 hover:underline dark:text-blue-400"
                      >
                        {t('app.edit')}
                      </button>
                      <button
                        onClick={() => {
                          if (window.confirm(t('admin.templatesPage.deleteConfirm'))) {
                            remove.mutate(tpl.name);
                          }
                        }}
                        className="text-sm text-red-600 hover:underline"
                      >
                        {t('app.delete')}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {editing && (
        <TemplateDialog
          isNew={editing.isNew}
          initial={editing.input}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}

// toInput rebuilds the full edit payload from the API projection, so
// editing round-trips every CR facet the form exposes.
function toInput(tpl: WorkspaceTemplate): TemplateInput {
  return {
    name: tpl.name,
    displayName: tpl.displayName,
    description: tpl.description ?? '',
    os: tpl.os,
    image: tpl.image,
    homeSize: tpl.homeSize ?? '',
    storageClassName: tpl.storageClassName ?? '',
    requests: tpl.requests,
    limits: tpl.limits,
    env: tpl.env,
    workload: tpl.workloadSpec,
    protocols: (tpl.protocols ?? []).map((p) => ({
      name: p.name,
      port: p.port ?? DEFAULT_PORTS[p.name] ?? 0,
      default: p.default,
      params: p.params,
      userParams: p.userParams,
      credentialsSecretRef: p.credentialsSecretRef,
    })),
    overrides:
      tpl.allowedOverrides || tpl.overridesOwner
        ? { allowedFields: tpl.allowedOverrides, owner: tpl.overridesOwner }
        : undefined,
  };
}

function TemplateDialog({
  isNew,
  initial,
  onClose,
}: {
  isNew: boolean;
  initial: TemplateInput;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const save = useSaveTemplate();
  const meta = useProtocolMeta();
  const [input, setInput] = useState(initial);
  const [workloadText, setWorkloadText] = useState(
    initial.workload ? JSON.stringify(initial.workload, null, 2) : '',
  );
  const [workloadError, setWorkloadError] = useState('');

  const set = (patch: Partial<TemplateInput>) => setInput((prev) => ({ ...prev, ...patch }));
  const protocols = input.protocols ?? [];
  const setProtocol = (index: number, patch: Partial<TemplateProtocolInput>) => {
    const next = protocols.map((p, i) => (i === index ? { ...p, ...patch } : p));
    set({ protocols: next });
  };

  const availableProtocols = (meta.data?.data ?? []).map((m) => m.name);

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    let workload: Record<string, unknown> | undefined;
    if (workloadText.trim() !== '') {
      try {
        workload = JSON.parse(workloadText) as Record<string, unknown>;
      } catch {
        setWorkloadError(t('admin.templatesPage.workloadInvalid'));
        return;
      }
    }
    setWorkloadError('');
    save.mutate({ isNew, input: { ...input, workload } }, { onSuccess: onClose });
  };

  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/40 p-4">
      <form
        onSubmit={onSubmit}
        className="max-h-[90vh] w-full max-w-2xl space-y-4 overflow-y-auto rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800"
      >
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {isNew ? t('admin.templatesPage.new') : t('admin.templatesPage.edit', { name: input.name })}
        </h2>

        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">
              {t('admin.templatesPage.name')}
            </span>
            <input
              className={field}
              value={input.name}
              onChange={(e) => set({ name: e.target.value })}
              disabled={!isNew}
              pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?"
              required
            />
          </label>
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">
              {t('admin.templatesPage.displayName')}
            </span>
            <input
              className={field}
              value={input.displayName}
              onChange={(e) => set({ displayName: e.target.value })}
              required
            />
          </label>
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">
              {t('admin.templatesPage.os')}
            </span>
            <select
              className={field}
              value={input.os}
              onChange={(e) => set({ os: e.target.value })}
            >
              <option value="linux">Linux</option>
              <option value="windows">Windows</option>
            </select>
          </label>
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">
              {t('admin.templatesPage.image')}
            </span>
            <input
              className={field}
              value={input.image}
              onChange={(e) => set({ image: e.target.value })}
              required
            />
          </label>
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">
              {t('admin.templatesPage.homeSize')}
            </span>
            <input
              className={field}
              value={input.homeSize}
              onChange={(e) => set({ homeSize: e.target.value })}
            />
          </label>
          <label className="block">
            <span className="text-sm text-slate-600 dark:text-slate-300">
              {t('admin.templatesPage.storageClass')}
            </span>
            <input
              className={field}
              value={input.storageClassName ?? ''}
              onChange={(e) => set({ storageClassName: e.target.value })}
              placeholder={t('admin.templatesPage.storageClassDefault')}
            />
          </label>
        </div>

        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.description')}
          </span>
          <textarea
            className={field}
            value={input.description}
            onChange={(e) => set({ description: e.target.value })}
            rows={2}
          />
        </label>

        {/* ---------------- resources ---------------- */}
        <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
            {t('admin.templatesPage.resources')}
          </legend>
          <div className="grid grid-cols-2 gap-3">
            {(['requests', 'limits'] as const).map((kind) => (
              <div key={kind} className="space-y-2">
                <p className="text-xs uppercase text-slate-400">{kind}</p>
                {(['cpu', 'memory'] as const).map((res) => (
                  <label key={res} className="block">
                    <span className="font-mono text-xs text-slate-500 dark:text-slate-400">
                      {res}
                    </span>
                    <input
                      className={fieldSm}
                      value={input[kind]?.[res] ?? ''}
                      placeholder={res === 'cpu' ? '500m' : '1Gi'}
                      onChange={(e) => {
                        const next = { ...input[kind] };
                        if (e.target.value === '') delete next[res];
                        else next[res] = e.target.value;
                        set({ [kind]: next } as Partial<TemplateInput>);
                      }}
                    />
                  </label>
                ))}
              </div>
            ))}
          </div>
        </fieldset>

        {/* ---------------- protocols ---------------- */}
        <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
            {t('admin.templatesPage.protocols')}
          </legend>
          <p className="text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.protocolsHint')}
          </p>
          {protocols.map((proto, i) => (
            <div
              key={i}
              className="space-y-3 rounded-md border border-slate-200 p-3 dark:border-slate-600"
            >
              <div className="flex items-end gap-3">
                <label className="block">
                  <span className="text-xs text-slate-500 dark:text-slate-400">
                    {t('portal.protocol')}
                  </span>
                  <select
                    className={fieldSm}
                    value={proto.name}
                    onChange={(e) =>
                      setProtocol(i, {
                        name: e.target.value,
                        port: DEFAULT_PORTS[e.target.value] ?? proto.port,
                        params: {},
                        userParams: [],
                      })
                    }
                  >
                    {availableProtocols.map((p) => (
                      <option key={p} value={p}>
                        {p.toUpperCase()}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="block w-24">
                  <span className="text-xs text-slate-500 dark:text-slate-400">
                    {t('admin.templatesPage.port')}
                  </span>
                  <input
                    type="number"
                    className={fieldSm}
                    value={proto.port || ''}
                    min={1}
                    max={65535}
                    onChange={(e) => setProtocol(i, { port: Number(e.target.value) })}
                    required
                  />
                </label>
                <label className="flex items-center gap-1.5 pb-2 text-sm text-slate-600 dark:text-slate-300">
                  <input
                    type="radio"
                    name="default-protocol"
                    checked={!!proto.default}
                    onChange={() =>
                      set({
                        protocols: protocols.map((p, j) => ({ ...p, default: j === i })),
                      })
                    }
                  />
                  {t('portal.protocolDefault')}
                </label>
                <button
                  type="button"
                  onClick={() => set({ protocols: protocols.filter((_, j) => j !== i) })}
                  className="ml-auto pb-2 text-sm text-red-600 hover:underline"
                >
                  {t('app.delete')}
                </button>
              </div>

              <label className="block">
                <span className="text-xs text-slate-500 dark:text-slate-400">
                  {t('admin.templatesPage.credentialsSecret')}
                </span>
                <input
                  className={fieldSm}
                  value={proto.credentialsSecretRef ?? ''}
                  onChange={(e) => setProtocol(i, { credentialsSecretRef: e.target.value })}
                  placeholder={t('admin.templatesPage.credentialsSecretHint')}
                />
              </label>

              {/* Registry-driven params: value + per-param overridable flag. */}
              <div className="grid grid-cols-2 gap-3">
                {paramsFor(meta.data?.data, proto.name, ['ui', 'advanced']).map((pm) => (
                  <div key={pm.name} className="space-y-1">
                    <ParamField
                      meta={pm}
                      value={proto.params?.[pm.name] ?? ''}
                      onChange={(value) => {
                        const params = { ...proto.params };
                        if (value === '') delete params[pm.name];
                        else params[pm.name] = value;
                        setProtocol(i, { params });
                      }}
                    />
                    <label className="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400">
                      <input
                        type="checkbox"
                        checked={proto.userParams?.includes(pm.name) ?? false}
                        onChange={(e) => {
                          const setNames = new Set(proto.userParams ?? []);
                          if (e.target.checked) setNames.add(pm.name);
                          else setNames.delete(pm.name);
                          setProtocol(i, { userParams: [...setNames] });
                        }}
                      />
                      {t('admin.templatesPage.userOverridable')}
                      {pm.tier === 'advanced' && (
                        <span className="rounded bg-amber-100 px-1 text-[10px] uppercase text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
                          {t('admin.templatesPage.advanced')}
                        </span>
                      )}
                    </label>
                  </div>
                ))}
              </div>
            </div>
          ))}
          <button
            type="button"
            onClick={() =>
              set({
                protocols: [
                  ...protocols,
                  {
                    name: 'vnc',
                    port: DEFAULT_PORTS.vnc,
                    default: protocols.length === 0,
                  },
                ],
              })
            }
            className="text-sm text-blue-600 hover:underline dark:text-blue-400"
          >
            + {t('admin.templatesPage.addProtocol')}
          </button>
        </fieldset>

        {/* ---------------- env ---------------- */}
        <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
            {t('admin.templatesPage.env')}
          </legend>
          <p className="text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.envHint')}
          </p>
          {(input.env ?? []).map((env, i) => (
            <EnvRow
              key={i}
              env={env}
              onChange={(next) =>
                set({ env: (input.env ?? []).map((e, j) => (j === i ? next : e)) })
              }
              onRemove={() => set({ env: (input.env ?? []).filter((_, j) => j !== i) })}
            />
          ))}
          <button
            type="button"
            onClick={() => set({ env: [...(input.env ?? []), { name: '', value: '' }] })}
            className="text-sm text-blue-600 hover:underline dark:text-blue-400"
          >
            + {t('admin.templatesPage.addEnv')}
          </button>
        </fieldset>

        {/* ---------------- user overrides ---------------- */}
        <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
            {t('admin.templatesPage.overrides')}
          </legend>
          <p className="mb-2 text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.overridesHint')}
          </p>
          <div className="grid grid-cols-3 gap-1.5">
            {OVERRIDABLE_FIELDS.map((f) => (
              <label
                key={f}
                className="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300"
              >
                <input
                  type="checkbox"
                  checked={input.overrides?.allowedFields?.includes(f) ?? false}
                  onChange={(e) => {
                    const fields = new Set(input.overrides?.allowedFields ?? []);
                    if (e.target.checked) fields.add(f);
                    else fields.delete(f);
                    set({ overrides: { ...input.overrides, allowedFields: [...fields] } });
                  }}
                />
                <span className="font-mono text-xs">{f}</span>
              </label>
            ))}
          </div>
          <label className="mt-2 block">
            <span className="text-xs text-slate-500 dark:text-slate-400">
              {t('admin.templatesPage.overridesOwner')}
            </span>
            <input
              className={fieldSm}
              value={input.overrides?.owner ?? ''}
              onChange={(e) => set({ overrides: { ...input.overrides, owner: e.target.value } })}
            />
          </label>
        </fieldset>

        {/* ---------------- workload (advanced) ---------------- */}
        <details className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <summary className="cursor-pointer text-sm font-medium text-slate-700 dark:text-slate-200">
            {t('admin.templatesPage.workload')}
          </summary>
          <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.workloadHint')}
          </p>
          <textarea
            className={`${field} font-mono text-xs`}
            rows={8}
            value={workloadText}
            onChange={(e) => setWorkloadText(e.target.value)}
            placeholder='{"kind": "Deployment", "podSecurityContext": {"runAsNonRoot": true}}'
          />
          {workloadError && <p className="text-sm text-red-600">{workloadError}</p>}
        </details>

        {save.isError && <p className="text-sm text-red-600">{save.error.message}</p>}
        <div className="flex justify-end gap-2 pt-2">
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
            {t('app.save')}
          </button>
        </div>
      </form>
    </div>
  );
}

// One env row: literal value or Secret reference — matching corev1.EnvVar,
// so what the form writes is exactly what the CR stores.
function EnvRow({
  env,
  onChange,
  onRemove,
}: {
  env: TemplateEnvVar;
  onChange: (env: TemplateEnvVar) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const fromSecret = !!env.valueFrom?.secretKeyRef;

  return (
    <div className="flex items-end gap-2">
      <label className="block flex-1">
        <span className="text-xs text-slate-500 dark:text-slate-400">name</span>
        <input
          className={fieldSm}
          value={env.name}
          onChange={(e) => onChange({ ...env, name: e.target.value })}
          required
        />
      </label>
      <label className="flex items-center gap-1 pb-2 text-xs text-slate-500 dark:text-slate-400">
        <input
          type="checkbox"
          checked={fromSecret}
          onChange={(e) =>
            onChange(
              e.target.checked
                ? { name: env.name, valueFrom: { secretKeyRef: { name: '', key: '' } } }
                : { name: env.name, value: '' },
            )
          }
        />
        {t('admin.templatesPage.fromSecret')}
      </label>
      {fromSecret ? (
        <>
          <label className="block flex-1">
            <span className="text-xs text-slate-500 dark:text-slate-400">secret</span>
            <input
              className={fieldSm}
              value={env.valueFrom?.secretKeyRef?.name ?? ''}
              onChange={(e) =>
                onChange({
                  ...env,
                  valueFrom: {
                    secretKeyRef: { name: e.target.value, key: env.valueFrom?.secretKeyRef?.key ?? '' },
                  },
                })
              }
              required
            />
          </label>
          <label className="block flex-1">
            <span className="text-xs text-slate-500 dark:text-slate-400">key</span>
            <input
              className={fieldSm}
              value={env.valueFrom?.secretKeyRef?.key ?? ''}
              onChange={(e) =>
                onChange({
                  ...env,
                  valueFrom: {
                    secretKeyRef: { name: env.valueFrom?.secretKeyRef?.name ?? '', key: e.target.value },
                  },
                })
              }
              required
            />
          </label>
        </>
      ) : (
        <label className="block flex-1">
          <span className="text-xs text-slate-500 dark:text-slate-400">value</span>
          <input
            className={fieldSm}
            value={env.value ?? ''}
            onChange={(e) => onChange({ ...env, value: e.target.value })}
          />
        </label>
      )}
      <button type="button" onClick={onRemove} className="pb-2 text-sm text-red-600 hover:underline">
        ✕
      </button>
    </div>
  );
}
