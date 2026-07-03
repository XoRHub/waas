import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useCreateUser, useDeleteUser, useUsers, type CreateUserInput } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';

export function UsersPage() {
  const { t } = useTranslation();
  const users = useUsers();
  const remove = useDeleteUser();
  const me = useAuthStore((s) => s.user);
  const [creating, setCreating] = useState(false);

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <button
          onClick={() => setCreating(true)}
          className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
        >
          {t('admin.usersPage.new')}
        </button>
      </div>

      {users.isPending && <p className="text-slate-500">{t('app.loading')}</p>}
      {users.isError && <p className="text-red-600">{t('app.error')}</p>}

      {users.isSuccess && (
        <div className="overflow-x-auto rounded-xl bg-white shadow-sm dark:bg-slate-800">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700 dark:text-slate-400">
              <tr>
                <th className="px-4 py-3">{t('admin.usersPage.username')}</th>
                <th className="px-4 py-3">{t('admin.usersPage.role')}</th>
                <th className="px-4 py-3">{t('admin.usersPage.maxWorkspaces')}</th>
                <th className="px-4 py-3">{t('admin.usersPage.lastLogin')}</th>
                <th className="px-4 py-3">{t('app.actions')}</th>
              </tr>
            </thead>
            <tbody className="text-slate-800 dark:text-slate-100">
              {users.data.data.map((user) => (
                <tr
                  key={user.id}
                  className="border-b border-slate-100 last:border-0 dark:border-slate-700"
                >
                  <td className="px-4 py-3 font-medium">{user.username}</td>
                  <td className="px-4 py-3">{user.role}</td>
                  <td className="px-4 py-3">{user.maxWorkspaces}</td>
                  <td className="px-4 py-3">
                    {user.lastLoginAt
                      ? new Date(user.lastLoginAt).toLocaleString()
                      : t('admin.usersPage.never')}
                  </td>
                  <td className="px-4 py-3">
                    {user.id !== me?.id && (
                      <button
                        onClick={() => {
                          if (window.confirm(t('admin.usersPage.deleteConfirm'))) {
                            remove.mutate(user.id);
                          }
                        }}
                        className="text-sm text-red-600 hover:underline"
                      >
                        {t('app.delete')}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {creating && <CreateUserDialog onClose={() => setCreating(false)} />}
    </div>
  );
}

function CreateUserDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const create = useCreateUser();
  const [input, setInput] = useState<CreateUserInput>({
    username: '',
    email: '',
    password: '',
    role: 'user',
  });

  const set = (patch: Partial<CreateUserInput>) => setInput((prev) => ({ ...prev, ...patch }));

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(input, { onSuccess: onClose });
  };

  const field =
    'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white';

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-md space-y-3 rounded-xl bg-white p-6 shadow-lg dark:bg-slate-800"
      >
        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('admin.usersPage.new')}
        </h2>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.usersPage.username')}
          </span>
          <input
            className={field}
            value={input.username}
            onChange={(e) => set({ username: e.target.value })}
            required
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.usersPage.email')}
          </span>
          <input
            type="email"
            className={field}
            value={input.email}
            onChange={(e) => set({ email: e.target.value })}
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.usersPage.password')}
          </span>
          <input
            type="password"
            className={field}
            value={input.password}
            onChange={(e) => set({ password: e.target.value })}
            required
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.usersPage.role')}
          </span>
          <select
            className={field}
            value={input.role}
            onChange={(e) => set({ role: e.target.value })}
          >
            <option value="user">user</option>
            <option value="admin">admin</option>
          </select>
        </label>
        {create.isError && <p className="text-sm text-red-600">{create.error.message}</p>}
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
            disabled={create.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.create')}
          </button>
        </div>
      </form>
    </div>
  );
}
