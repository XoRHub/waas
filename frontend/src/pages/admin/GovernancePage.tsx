import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useAdminImages,
  useAdminPolicies,
  useAdminUsage,
  useToggleImage,
  useUpsertPolicy,
} from '@/hooks/useApi';
import type { PolicyModel } from '@/types';

// Governance console: catalog kill-switches, policy editing and the
// per-user consumption view. Policies are edited as JSON — the shape is
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

  return (
    <section>
      <h2 className="mb-3 text-lg font-semibold text-slate-900 dark:text-white">
        {t('governance.catalog')}
      </h2>
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
    </section>
  );
}

function PoliciesSection() {
  const { t } = useTranslation();
  const policies = useAdminPolicies();
  const [editing, setEditing] = useState<PolicyModel | null>(null);

  return (
    <section>
      <h2 className="mb-3 text-lg font-semibold text-slate-900 dark:text-white">
        {t('governance.policies')}
      </h2>
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
      {editing && <PolicyEditor policy={editing} onClose={() => setEditing(null)} />}
    </section>
  );
}

function PolicyEditor({ policy, onClose }: { policy: PolicyModel; onClose: () => void }) {
  const { t } = useTranslation();
  const upsert = useUpsertPolicy();
  const { name, ...body } = policy;
  const [text, setText] = useState(JSON.stringify(body, null, 2));
  const [parseError, setParseError] = useState('');

  const onSave = () => {
    try {
      const parsed: unknown = JSON.parse(text);
      setParseError('');
      upsert.mutate({ name, body: parsed }, { onSuccess: onClose });
    } catch {
      setParseError(t('governance.invalidJson'));
    }
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-2xl space-y-3 rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800">
        <h3 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('governance.editPolicy')} — {name}
        </h3>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={16}
          spellCheck={false}
          className="w-full rounded-md border border-slate-300 p-3 font-mono text-xs dark:border-slate-600 dark:bg-slate-900 dark:text-slate-100"
        />
        {(parseError || upsert.isError) && (
          <p className="text-sm text-red-600">{parseError || upsert.error?.message}</p>
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
            disabled={upsert.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.save')}
          </button>
        </div>
      </div>
    </div>
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
