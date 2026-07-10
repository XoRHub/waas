import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { stringify as yamlStringify } from 'yaml';
import {
  useDeleteTemplate,
  useOverrideFields,
  usePlaceholders,
  useProtocolMeta,
  useSaveTemplate,
  useTemplates,
  type TemplateInput,
  type TemplateProtocolInput,
} from '@/hooks/useApi';
import { Dialog } from '@/components/Dialog';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import { ScheduleEditor } from '@/components/ScheduleEditor';
import { categoryDelegated, toggleCategory, toggleName } from '@/lib/userParams';
import { YamlEditor, parseYaml, type YamlIssue } from '@/components/YamlEditor';
import type { TemplateEnvVar, WorkspaceTemplate } from '@/types';

// Semantic validation of the workload YAML: must be a mapping, and kind
// (when present) must be one the CR accepts.
function validateWorkload(value: unknown): YamlIssue[] {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [{ line: 0, message: 'workload must be a YAML mapping' }];
  }
  const kind = (value as Record<string, unknown>).kind;
  if (kind !== undefined && !['Deployment', 'StatefulSet', 'Pod'].includes(String(kind))) {
    return [
      { line: 0, message: `kind: must be Deployment, StatefulSet or Pod (got "${String(kind)}")` },
    ];
  }
  return [];
}

const EMPTY: TemplateInput = {
  name: '',
  displayName: '',
  description: '',
  os: 'linux',
  image: '',
  homeSize: '10Gi',
  protocols: [],
};

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
    kasmvncConfig: tpl.kasmvncConfig ?? '',
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
      // The RAW list (cat: selectors intact) — the editor edits the
      // configuration itself, not the resolved expansion.
      userParams: p.userParams,
      credentialsSecretRef: p.credentialsSecretRef,
      exposeAudioPort: p.exposeAudioPort,
    })),
    overrides:
      tpl.allowedOverrides || tpl.overridesOwner
        ? { allowedFields: tpl.allowedOverrides, owner: tpl.overridesOwner }
        : undefined,
    schedule: tpl.schedule,
    placement: tpl.placement,
  };
}

// Exported for focused dialog tests (the kasmvnc config field's
// conditional rendering and round-trip).
export function TemplateDialog({
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
  const placeholders = usePlaceholders();
  // The overridable fields come from the server registry (single source
  // shared with the policy editor and the enforcement) — this page holds
  // no local copy of the list.
  const overrideFields = useOverrideFields();
  const [input, setInput] = useState(initial);
  // Workload edited as YAML (converted transparently: the API/CR still
  // stores the structured value).
  const [workloadText, setWorkloadText] = useState(
    initial.workload ? yamlStringify(initial.workload) : '',
  );
  const [workloadError, setWorkloadError] = useState('');

  const set = (patch: Partial<TemplateInput>) => setInput((prev) => ({ ...prev, ...patch }));
  const protocols = input.protocols ?? [];
  const [activeProto, setActiveProto] = useState(protocols[0]?.name ?? '');
  const currentProto = protocols.find((p) => p.name === activeProto);
  const patchActive = (patch: Partial<TemplateProtocolInput>) => {
    set({ protocols: protocols.map((p) => (p.name === activeProto ? { ...p, ...patch } : p)) });
  };

  const availableProtocols = (meta.data?.data ?? []).map((m) => m.name);
  // A template declares each protocol at most once (webhook-enforced):
  // the shared "+" menu offers only the registry protocols not
  // configured yet — the admin picks explicitly which one to add.
  const unusedProtocols = availableProtocols.filter((p) => !protocols.some((x) => x.name === p));
  const addProtocol = (name: string) => {
    set({
      protocols: [
        ...protocols,
        { name, port: DEFAULT_PORTS[name] ?? 0, default: protocols.length === 0 },
      ],
    });
    setActiveProto(name);
  };
  const removeProtocol = (name: string) => {
    const next = protocols.filter((p) => p.name !== name);
    // Keep exactly one default among the survivors.
    if (next.length > 0 && !next.some((p) => p.default)) next[0] = { ...next[0], default: true };
    set({ protocols: next });
    setActiveProto(next[0]?.name ?? '');
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    let workload: Record<string, unknown> | undefined;
    if (workloadText.trim() !== '') {
      const { value, issues } = parseYaml(workloadText, validateWorkload);
      if (issues.length > 0 || value === undefined) {
        setWorkloadError(t('admin.templatesPage.workloadInvalid'));
        return;
      }
      workload = value as Record<string, unknown>;
    }
    setWorkloadError('');
    save.mutate({ isNew, input: { ...input, workload } }, { onSuccess: onClose });
  };

  return (
    <Dialog
      title={
        isNew ? t('admin.templatesPage.new') : t('admin.templatesPage.edit', { name: input.name })
      }
      onClose={onClose}
      onSubmit={onSubmit}
      maxWidth="max-w-2xl"
      footer={
        <>
          {save.isError && <p className="mr-auto text-sm text-red-600">{save.error.message}</p>}
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
        </>
      }
    >
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
          <select className={field} value={input.os} onChange={(e) => set({ os: e.target.value })}>
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

      {/* ---------------- protocols (one tab per protocol) ---------------- */}
      <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
          {t('admin.templatesPage.protocols')}
        </legend>
        <p className="text-xs text-slate-400 dark:text-slate-500">
          {t('admin.templatesPage.protocolsHint')}
        </p>
        <ProtocolTabs
          protocols={protocols.map((p) => p.name)}
          active={activeProto}
          onSelect={setActiveProto}
          badge={(p) =>
            protocols.find((x) => x.name === p)?.default ? (
              <span className="text-[10px]" title={t('portal.protocolDefault')}>
                ●
              </span>
            ) : null
          }
          addable={unusedProtocols}
          onAdd={addProtocol}
          onRemove={removeProtocol}
        />
        {protocols.length === 0 && (
          <p className="text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.noProtocolsYet')}
          </p>
        )}
        {currentProto ? (
          <div className="space-y-3">
            <div className="flex items-end gap-3">
              <label className="block w-24">
                <span className="text-xs text-slate-500 dark:text-slate-400">
                  {t('admin.templatesPage.port')}
                </span>
                <input
                  type="number"
                  className={fieldSm}
                  value={currentProto.port || ''}
                  min={1}
                  max={65535}
                  onChange={(e) => patchActive({ port: Number(e.target.value) })}
                  required
                />
              </label>
              <label className="flex items-center gap-1.5 pb-2 text-sm text-slate-600 dark:text-slate-300">
                <input
                  type="radio"
                  name="default-protocol"
                  checked={!!currentProto.default}
                  onChange={() =>
                    set({
                      protocols: protocols.map((p) => ({ ...p, default: p.name === activeProto })),
                    })
                  }
                />
                {t('portal.protocolDefault')}
              </label>
            </div>

            <label className="block">
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {t('admin.templatesPage.credentialsSecret')}
              </span>
              <input
                className={fieldSm}
                value={currentProto.credentialsSecretRef ?? ''}
                onChange={(e) => patchActive({ credentialsSecretRef: e.target.value })}
                placeholder={t('admin.templatesPage.credentialsSecretHint')}
              />
            </label>

            {/* Same registry-driven form as the user connection settings,
                  with the admin extras: a per-param delegation toggle
                  (locked / user) plus a per-section "allow the whole
                  category" toggle that writes a cat:X selector into
                  userParams (individual names of the category are then
                  absorbed). Editor placement stays tier-driven; the raw
                  list (cat: intact) is what gets edited — the api-server
                  resolves it for the connect-time forms. */}
            <ProtocolParamsForm
              key={currentProto.name}
              meta={meta.data?.data}
              protocol={currentProto.name}
              values={currentProto.params ?? {}}
              onChange={(name, value) => {
                const params = { ...currentProto.params };
                if (value === '') delete params[name];
                else params[name] = value;
                patchActive({ params });
              }}
              audioPortExposed={currentProto.exposeAudioPort ?? false}
              onAudioPortChange={(exposed) => patchActive({ exposeAudioPort: exposed })}
              renderSectionExtra={(category) => {
                const full = categoryDelegated(currentProto.userParams, category);
                return (
                  <label
                    className={`flex items-center gap-1 text-[11px] ${
                      full
                        ? 'font-medium text-blue-600 dark:text-blue-400'
                        : 'text-slate-500 dark:text-slate-400'
                    }`}
                    title={t('admin.templatesPage.allowCategoryHint')}
                  >
                    <input
                      type="checkbox"
                      checked={full}
                      onChange={(e) => {
                        const categoryNames = (meta.data?.data ?? [])
                          .find((m) => m.name === currentProto.name)
                          ?.params?.filter((p) => p.category === category)
                          .map((p) => p.name);
                        patchActive({
                          userParams: toggleCategory(
                            currentProto.userParams,
                            category,
                            categoryNames ?? [],
                            e.target.checked,
                          ),
                        });
                      }}
                    />
                    {full
                      ? t('admin.templatesPage.categoryAllowed')
                      : t('admin.templatesPage.allowCategory')}
                  </label>
                );
              }}
              renderParamExtra={(pm) => {
                // A cat:X selector delegates the whole section: the
                // per-param toggle goes inert (visibly delegated, not
                // hidden) until the category toggle is released.
                const viaCategory = categoryDelegated(currentProto.userParams, pm.category);
                const level: 'locked' | 'user' =
                  viaCategory || currentProto.userParams?.includes(pm.name) ? 'user' : 'locked';
                return (
                  <div className="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400">
                    <span
                      className={`inline-flex divide-x divide-slate-300 overflow-hidden rounded border border-slate-300 dark:divide-slate-600 dark:border-slate-600 ${
                        viaCategory ? 'opacity-50' : ''
                      }`}
                      title={viaCategory ? t('admin.templatesPage.categoryAllowed') : undefined}
                    >
                      {(['locked', 'user'] as const).map((lvl) => (
                        <button
                          key={lvl}
                          type="button"
                          aria-pressed={level === lvl}
                          disabled={viaCategory}
                          onClick={() =>
                            patchActive({
                              userParams: toggleName(
                                currentProto.userParams,
                                pm.name,
                                lvl === 'user',
                              ),
                            })
                          }
                          className={`px-1.5 py-0.5 text-[10px] ${
                            level === lvl
                              ? 'bg-blue-600 font-medium text-white'
                              : 'bg-white text-slate-600 hover:bg-slate-100 dark:bg-slate-700 dark:text-slate-300 dark:hover:bg-slate-600'
                          } ${viaCategory ? 'cursor-not-allowed' : ''}`}
                        >
                          {t(`admin.templatesPage.override${lvl[0].toUpperCase()}${lvl.slice(1)}`)}
                        </button>
                      ))}
                    </span>
                    {pm.tier === 'advanced' && (
                      <span className="rounded bg-amber-100 px-1 text-[10px] uppercase text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
                        {t('admin.templatesPage.advanced')}
                      </span>
                    )}
                  </div>
                );
              }}
            />
          </div>
        ) : null}
      </fieldset>

      {/* -------- kasmvnc config (template-level, only with kasmvnc) --------
          Gated on the whole protocol list, not the active tab — same guard
          the webhook enforces ("kasmvncConfig requires a kasmvnc protocol
          entry"). This edits the admin OVERRIDE layer only: KasmVNC merges
          it over the image defaults, and the clipboard DLP keys are
          policy-owned (the webhook rejects them here). */}
      {protocols.some((p) => p.name === 'kasmvnc') && (
        <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
          <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
            {t('admin.templatesPage.kasmvncConfig')}
          </legend>
          <p className="text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.kasmvncConfigHint')}{' '}
            <a
              href="https://kasmweb.com/kasmvnc/docs/latest/configuration.html"
              target="_blank"
              rel="noreferrer"
              className="text-blue-600 underline dark:text-blue-400"
            >
              {t('admin.templatesPage.kasmvncConfigDocLink')}
            </a>
          </p>
          <textarea
            className={`${field} font-mono text-xs`}
            value={input.kasmvncConfig ?? ''}
            onChange={(e) => set({ kasmvncConfig: e.target.value })}
            rows={8}
            spellCheck={false}
            placeholder={t('admin.templatesPage.kasmvncConfigPlaceholder')}
          />
        </fieldset>
      )}

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
            onChange={(next) => set({ env: (input.env ?? []).map((e, j) => (j === i ? next : e)) })}
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
          {(overrideFields.data?.data ?? []).map(({ name: f, description }) => (
            <label
              key={f}
              title={description}
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

      {/* ---------------- placement (workload namespace) ------------ */}
      <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
          {t('admin.templatesPage.placement')}
        </legend>
        <label className="block">
          <span className="text-xs text-slate-500 dark:text-slate-400">
            {t('admin.templatesPage.placementPattern')}
          </span>
          <input
            className={`${fieldSm} font-mono`}
            placeholder="waas-{user}"
            value={input.placement?.namespace ?? ''}
            onChange={(e) =>
              set({
                placement: { ...input.placement, namespace: e.target.value || undefined },
              })
            }
          />
          <span className="mt-0.5 block text-xs text-slate-400 dark:text-slate-500">
            {t('admin.templatesPage.placementHint')}
          </span>
        </label>
        {/* Contextual help straight from the naming engine (GET
              /meta/placeholders) — never a hand-maintained copy. */}
        {(placeholders.data?.data ?? []).length > 0 && (
          <div className="rounded-md bg-slate-50 p-2 text-xs text-slate-500 dark:bg-slate-700/40 dark:text-slate-400">
            <p className="mb-1 font-medium">{t('admin.templatesPage.placeholdersTitle')}</p>
            <ul className="space-y-0.5">
              {(placeholders.data?.data ?? []).map((ph) => (
                <li key={ph.token}>
                  <span className="font-mono text-slate-700 dark:text-slate-200">{ph.token}</span> —{' '}
                  {ph.description} <span className="text-slate-400">({ph.source})</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        <label className="block">
          <span className="text-xs text-slate-500 dark:text-slate-400">
            {t('admin.templatesPage.placementCleanup')}
          </span>
          <select
            className={fieldSm}
            value={input.placement?.cleanup ?? ''}
            onChange={(e) =>
              set({
                placement: { ...input.placement, cleanup: e.target.value || undefined },
              })
            }
          >
            <option value="">Retain ({t('portal.protocolDefault')})</option>
            <option value="Retain">Retain</option>
            <option value="DeleteWhenEmpty">DeleteWhenEmpty</option>
          </select>
        </label>
      </fieldset>

      {/* ---------------- schedule (uptime/downtime) ---------------- */}
      <fieldset className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
          {t('schedule.title')}
        </legend>
        <ScheduleEditor value={input.schedule} onChange={(schedule) => set({ schedule })} />
      </fieldset>

      {/* ---------------- workload (advanced) ---------------- */}
      <details className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <summary className="cursor-pointer text-sm font-medium text-slate-700 dark:text-slate-200">
          {t('admin.templatesPage.workload')}
        </summary>
        <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
          {t('admin.templatesPage.workloadHint')}
        </p>
        <div className="mt-2">
          <YamlEditor
            value={workloadText}
            onChange={setWorkloadText}
            rows={8}
            validate={validateWorkload}
          />
        </div>
        {workloadError && <p className="text-sm text-red-600">{workloadError}</p>}
      </details>
    </Dialog>
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
                    secretKeyRef: {
                      name: e.target.value,
                      key: env.valueFrom?.secretKeyRef?.key ?? '',
                    },
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
                    secretKeyRef: {
                      name: env.valueFrom?.secretKeyRef?.name ?? '',
                      key: e.target.value,
                    },
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
      <button
        type="button"
        onClick={onRemove}
        className="pb-2 text-sm text-red-600 hover:underline"
      >
        ✕
      </button>
    </div>
  );
}
