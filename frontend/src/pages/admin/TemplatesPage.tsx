import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useDeleteTemplate, useTemplates, type TemplateInput } from '@/hooks/useApi';
import { DEFAULT_PORTS, TemplateDialog } from './templates/TemplateDialog';
import type { WorkspaceTemplate } from '@/types';

// Re-exported for focused dialog tests (the kasmvnc config field's
// conditional rendering and round-trip). The form itself lives in
// ./templates/ — one file per section, TemplateDialog composing them.
export { TemplateDialog };

const EMPTY: TemplateInput = {
  name: '',
  displayName: '',
  description: '',
  os: 'linux',
  image: '',
  homeSize: '10Gi',
  protocols: [],
};

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
    logo: tpl.logo ?? '',
    os: tpl.os,
    image: tpl.image,
    homeSize: tpl.homeSize ?? '',
    homeMountPath: tpl.homeMountPath,
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
