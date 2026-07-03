import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useDeleteTemplate,
  useSaveTemplate,
  useTemplates,
  type TemplateInput,
} from '@/hooks/useApi';

const EMPTY: TemplateInput = {
  name: '',
  displayName: '',
  description: '',
  os: 'linux',
  image: '',
  homeSize: '10Gi',
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
                  <td className="px-4 py-3 font-mono text-xs">{tpl.image}</td>
                  <td className="px-4 py-3">{tpl.homeSize}</td>
                  <td className="px-4 py-3">
                    <div className="flex gap-3">
                      <button
                        onClick={() =>
                          setEditing({
                            isNew: false,
                            input: {
                              name: tpl.name,
                              displayName: tpl.displayName,
                              description: tpl.description ?? '',
                              os: tpl.os,
                              image: tpl.image,
                              homeSize: tpl.homeSize ?? '',
                            },
                          })
                        }
                        className="text-sm text-blue-600 hover:underline dark:text-blue-400"
                      >
                        {t('app.save')}
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
  const [input, setInput] = useState(initial);

  const set = (patch: Partial<TemplateInput>) => setInput((prev) => ({ ...prev, ...patch }));

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    save.mutate({ isNew, input }, { onSuccess: onClose });
  };

  const field =
    'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white';

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-lg space-y-3 rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800"
      >
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('admin.templatesPage.new')}
        </h2>
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
            {t('admin.templatesPage.description')}
          </span>
          <textarea
            className={field}
            value={input.description}
            onChange={(e) => set({ description: e.target.value })}
            rows={2}
          />
        </label>
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
