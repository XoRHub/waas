import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { stringify as yamlStringify } from 'yaml';
import {
  useAdminImages,
  useAdminPolicies,
  useAdminUsage,
  useScaffold,
  useToggleImage,
  useUpsertImage,
  useUpsertPolicy,
} from '@/hooks/useApi';
import { Dialog } from '@/components/Dialog';
import { YamlEditor, parseYaml, type YamlIssue } from '@/components/YamlEditor';
import type { CatalogImage, PolicyModel } from '@/types';

type Kind = 'workspacepolicy' | 'workspaceimage';

// deepMerge fills missing keys of `over` from `base` (the schema
// scaffold), so an edited object shows EVERY field — populated values win,
// absent ones fall back to the scaffold placeholder. Arrays and scalars
// are replaced wholesale by `over`.
function deepMerge(base: unknown, over: unknown): unknown {
  if (
    base &&
    over &&
    typeof base === 'object' &&
    typeof over === 'object' &&
    !Array.isArray(base) &&
    !Array.isArray(over)
  ) {
    const out: Record<string, unknown> = { ...(base as Record<string, unknown>) };
    for (const [k, v] of Object.entries(over as Record<string, unknown>)) {
      out[k] = k in (base as Record<string, unknown>) ? deepMerge((base as Record<string, unknown>)[k], v) : v;
    }
    return out;
  }
  return over === undefined ? base : over;
}

// Governance console: catalog kill-switches, policy editing and the
// per-user consumption view. Policies are edited as YAML — the shape is
// exactly the PUT /admin/policies payload — which keeps this page honest
// with the API instead of hiding fields behind a partial form.
export function GovernancePage() {
  const { t } = useTranslation();

  return (
    <div className="space-y-10">
      <CatalogSection />
      <PoliciesSection />
      <UsageSection />
      <p className="text-xs text-slate-400 dark:text-slate-500">{t('governance.gitopsNote')}</p>
    </div>
  );
}

function CatalogSection() {
  const { t } = useTranslation();
  const images = useAdminImages();
  const toggle = useToggleImage();
  const upsert = useUpsertImage();
  // null = closed; 'new' = create; a CatalogImage = edit.
  const [editing, setEditing] = useState<CatalogImage | 'new' | null>(null);

  return (
    <section>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('governance.catalog')}
        </h2>
        <button
          onClick={() => setEditing('new')}
          className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('governance.newImage')}
        </button>
      </div>
      <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
            <tr>
              <th className="px-4 py-3">{t('governance.image')}</th>
              <th className="px-4 py-3">{t('governance.reference')}</th>
              <th className="px-4 py-3">{t('governance.protocols')}</th>
              <th className="px-4 py-3">{t('governance.groups')}</th>
              <th className="px-4 py-3">{t('governance.status')}</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-700">
            {images.isSuccess &&
              images.data.data.map((img) => (
                <tr key={img.name} className="text-slate-700 dark:text-slate-200">
                  <td className="px-4 py-3 font-medium">{img.displayName}</td>
                  <td className="max-w-xs truncate px-4 py-3 font-mono text-xs">{img.image}</td>
                  <td className="px-4 py-3">{img.protocols.join(', ')}</td>
                  <td className="px-4 py-3">{img.allowedGroups?.join(', ') || t('governance.everyone')}</td>
                  <td className="px-4 py-3">
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                        img.enabled
                          ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
                          : 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
                      }`}
                    >
                      {img.enabled ? t('governance.enabled') : t('governance.disabled')}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => setEditing(img)}
                      className="mr-2 rounded-md border border-slate-300 px-3 py-1 text-xs text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
                    >
                      {t('app.edit')}
                    </button>
                    <button
                      onClick={() => toggle.mutate({ name: img.name, enabled: !img.enabled })}
                      disabled={toggle.isPending}
                      className="rounded-md border border-slate-300 px-3 py-1 text-xs text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
                    >
                      {img.enabled ? t('governance.disable') : t('governance.enable')}
                    </button>
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
        {images.isSuccess && images.data.data.length === 0 && (
          <p className="p-4 text-sm text-slate-500">{t('governance.noImages')}</p>
        )}
      </div>
      {editing && (
        <GovernanceEditor
          kind="workspaceimage"
          title={t('governance.editImage')}
          name={editing === 'new' ? '' : editing.name}
          isNew={editing === 'new'}
          body={editing === 'new' ? undefined : imageBody(editing)}
          onSave={(name, body) => upsert.mutate({ name, body }, { onSuccess: () => setEditing(null) })}
          pending={upsert.isPending}
          error={upsert.error?.message}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}

// imageBody strips read-only projection fields, leaving the PUT payload.
function imageBody(img: CatalogImage): Record<string, unknown> {
  const { name: _n, templates: _t, ...body } = img;
  void _n;
  void _t;
  return body;
}

function PoliciesSection() {
  const { t } = useTranslation();
  const policies = useAdminPolicies();
  const upsert = useUpsertPolicy();
  const [editing, setEditing] = useState<PolicyModel | 'new' | null>(null);

  return (
    <section>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('governance.policies')}
        </h2>
        <button
          onClick={() => setEditing('new')}
          className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('governance.newPolicy')}
        </button>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        {policies.isSuccess &&
          policies.data.data
            .slice()
            .sort((a, b) => b.priority - a.priority)
            .map((pol) => (
              <div key={pol.name} className="rounded-xl bg-white p-4 shadow-sm dark:bg-slate-800">
                <div className="flex items-center justify-between">
                  <h3 className="font-medium text-slate-900 dark:text-white">{pol.name}</h3>
                  <span className="text-xs text-slate-500">
                    {t('governance.priority')} {pol.priority}
                  </span>
                </div>
                <dl className="mt-2 space-y-1 text-xs text-slate-600 dark:text-slate-300">
                  <div>
                    <dt className="inline font-medium">{t('governance.subjects')}: </dt>
                    <dd className="inline">
                      {pol.subjects?.map((s) => `${s.kind}:${s.name}`).join(', ') ||
                        t('governance.everyone')}
                    </dd>
                  </div>
                  <div>
                    <dt className="inline font-medium">{t('governance.maxWorkspaces')}: </dt>
                    <dd className="inline">{pol.limits.maxWorkspaces ?? '∞'}</dd>
                  </div>
                  <div>
                    <dt className="inline font-medium">{t('governance.images')}: </dt>
                    <dd className="inline">{pol.images?.join(', ') || t('governance.allCatalog')}</dd>
                  </div>
                  {pol.limits.defaults && (
                    <div>
                      <dt className="inline font-medium">{t('governance.defaults')}: </dt>
                      <dd className="inline">
                        {Object.entries(pol.limits.defaults)
                          .map(([k, v]) => `${k}=${v}`)
                          .join(', ')}
                      </dd>
                    </div>
                  )}
                </dl>
                <button
                  onClick={() => setEditing(pol)}
                  className="mt-3 rounded-md border border-slate-300 px-3 py-1 text-xs text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
                >
                  {t('app.edit')}
                </button>
              </div>
            ))}
      </div>
      {editing && (
        <GovernanceEditor
          kind="workspacepolicy"
          title={t('governance.editPolicy')}
          name={editing === 'new' ? '' : editing.name}
          isNew={editing === 'new'}
          body={editing === 'new' ? undefined : policyBody(editing)}
          validate={validatePolicy}
          onSave={(name, body) => upsert.mutate({ name, body }, { onSuccess: () => setEditing(null) })}
          pending={upsert.isPending}
          error={upsert.error?.message}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}

function policyBody(pol: PolicyModel): Record<string, unknown> {
  const { name: _n, ...body } = pol;
  void _n;
  return body;
}

// Semantic checks over the parsed YAML: field-level messages the API
// would reject anyway, surfaced before submit with readable wording.
const KNOWN_OVERRIDE_FIELDS = [
  'env',
  'securityContext',
  'podSecurityContext',
  'volumes',
  'nodeSelector',
  'tolerations',
  'resources',
  'protocol',
  'protocolParams',
];

function validatePolicy(value: unknown): YamlIssue[] {
  const issues: YamlIssue[] = [];
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [{ line: 0, message: 'the policy must be a YAML mapping' }];
  }
  const pol = value as Record<string, unknown>;
  if (pol.priority !== undefined && typeof pol.priority !== 'number') {
    issues.push({ line: 0, message: 'priority: must be a number' });
  }
  if (pol.subjects !== undefined) {
    if (!Array.isArray(pol.subjects)) {
      issues.push({ line: 0, message: 'subjects: must be a list of {kind, name}' });
    } else {
      for (const s of pol.subjects as Record<string, unknown>[]) {
        if (s?.kind !== 'User' && s?.kind !== 'Group') {
          issues.push({ line: 0, message: `subjects: kind must be User or Group (got "${String(s?.kind)}")` });
        }
      }
    }
  }
  const overrides = pol.overrides as Record<string, unknown> | undefined;
  if (overrides?.allowedFields !== undefined) {
    const fields = overrides.allowedFields;
    if (!Array.isArray(fields)) {
      issues.push({ line: 0, message: 'overrides.allowedFields: must be a list' });
    } else {
      for (const f of fields) {
        if (!KNOWN_OVERRIDE_FIELDS.includes(String(f))) {
          issues.push({
            line: 0,
            message: `overrides.allowedFields: unknown field "${String(f)}" (known: ${KNOWN_OVERRIDE_FIELDS.join(', ')})`,
          });
        }
      }
    }
  }
  if (pol.remoteWorkspaces !== undefined && typeof pol.remoteWorkspaces !== 'boolean') {
    issues.push({ line: 0, message: 'remoteWorkspaces: must be true or false' });
  }
  return issues;
}

// GovernanceEditor is the shared YAML editor for policies and images.
// The editor is seeded from the server-generated schema scaffold (every
// field present, even empty), deep-merged with the object being edited —
// so admins never go back to the docs to find a field. On create it asks
// for the object name; the API server re-validates and writes the CR
// directly (no GitOps round-trip; if ArgoCD manages these objects, Git
// wins on the next sync).
function GovernanceEditor({
  kind,
  title,
  name: initialName,
  isNew,
  body,
  validate,
  onSave,
  pending,
  error,
  onClose,
}: {
  kind: Kind;
  title: string;
  name: string;
  isNew: boolean;
  body: Record<string, unknown> | undefined;
  validate?: (value: unknown) => YamlIssue[];
  onSave: (name: string, body: unknown) => void;
  pending: boolean;
  error?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const scaffold = useScaffold(kind);
  const [name, setName] = useState(initialName);
  const [text, setText] = useState('');
  const [touched, setTouched] = useState(false);
  const [parseError, setParseError] = useState('');

  // Seed once the scaffold is loaded: scaffold for create, scaffold
  // deep-merged with the object for edit (all fields, real values win).
  const seed = useMemo(() => {
    const base = scaffold.data?.data.scaffold;
    if (base === undefined) return undefined;
    const scaffoldObj = parseYaml(base).value;
    const merged = body ? deepMerge(scaffoldObj, body) : scaffoldObj;
    return yamlStringify(merged);
  }, [scaffold.data, body]);

  if (seed !== undefined && !touched && text === '') {
    setText(seed);
  }

  const onSaveClick = () => {
    if (isNew && !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)) {
      setParseError(t('governance.invalidName'));
      return;
    }
    const { value, issues } = parseYaml(text, validate);
    if (issues.length > 0 || value === undefined) {
      setParseError(t('governance.invalidYaml'));
      return;
    }
    setParseError('');
    onSave(name, value);
  };

  return (
    <Dialog
      title={`${title}${!isNew && name ? ` — ${name}` : ''}`}
      onClose={onClose}
      maxWidth="max-w-2xl"
      footer={
        <>
          {(parseError || error) && (
            <p className="mr-auto text-sm text-red-600">{parseError || error}</p>
          )}
          <button
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            onClick={onSaveClick}
            disabled={pending || seed === undefined}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.save')}
          </button>
        </>
      }
    >
      {isNew && (
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('governance.name')}</span>
          <input
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={name}
            onChange={(e) => setName(e.target.value)}
            pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?"
            placeholder="my-policy"
          />
        </label>
      )}
      {seed === undefined ? (
        <p className="text-sm text-slate-500">{t('app.loading')}</p>
      ) : (
        <YamlEditor
          value={text}
          onChange={(v) => {
            setTouched(true);
            setText(v);
          }}
          rows={18}
          validate={validate}
        />
      )}
    </Dialog>
  );
}

function UsageSection() {
  const { t } = useTranslation();
  const usage = useAdminUsage();

  return (
    <section>
      <h2 className="mb-3 text-lg font-semibold text-slate-900 dark:text-white">
        {t('governance.usage')}
      </h2>
      <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
            <tr>
              <th className="px-4 py-3">{t('governance.user')}</th>
              <th className="px-4 py-3">{t('governance.groups')}</th>
              <th className="px-4 py-3">{t('governance.policy')}</th>
              <th className="px-4 py-3">{t('governance.workspaces')}</th>
              <th className="px-4 py-3">CPU</th>
              <th className="px-4 py-3">RAM</th>
              <th className="px-4 py-3">{t('governance.storage')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-700">
            {usage.isSuccess &&
              usage.data.data.map((row) => (
                <tr key={row.userId} className="text-slate-700 dark:text-slate-200">
                  <td className="px-4 py-3">{row.username || row.userId}</td>
                  <td className="px-4 py-3 text-xs">{row.groups?.join(', ') || '—'}</td>
                  <td className="px-4 py-3">{row.policy || '—'}</td>
                  <td className="px-4 py-3">{row.workspaces}</td>
                  <td className="px-4 py-3">{row.used?.cpu ?? '—'}</td>
                  <td className="px-4 py-3">{row.used?.memory ?? '—'}</td>
                  <td className="px-4 py-3">{row.used?.storage ?? '—'}</td>
                </tr>
              ))}
          </tbody>
        </table>
        {usage.isSuccess && usage.data.data.length === 0 && (
          <p className="p-4 text-sm text-slate-500">{t('governance.noUsage')}</p>
        )}
      </div>
    </section>
  );
}
