import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useCreateUser,
  useDeleteUser,
  useEffectivePolicy,
  useUpdateUser,
  useUsers,
  type CreateUserInput,
} from '@/hooks/useApi';
import { Dialog } from '@/components/Dialog';
import { GroupsPicker } from '@/components/GroupsPicker';
import { useAuthStore } from '@/stores/authStore';
import type { User } from '@/types';

export function UsersPage() {
  const { t } = useTranslation();
  const users = useUsers();
  const remove = useDeleteUser();
  const me = useAuthStore((s) => s.user);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<User | null>(null);

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
                <th className="px-4 py-3">{t('admin.usersPage.groups')}</th>
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
                  <td className="px-4 py-3">
                    {user.groups && user.groups.length > 0 ? (
                      <span className="flex flex-wrap gap-1">
                        {user.groups.map((g) => (
                          <span
                            key={g}
                            className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-600 dark:bg-slate-700 dark:text-slate-300"
                          >
                            {g}
                          </span>
                        ))}
                      </span>
                    ) : (
                      <span className="text-xs text-slate-400">—</span>
                    )}
                  </td>
                  <td className="px-4 py-3">{user.maxWorkspaces}</td>
                  <td className="px-4 py-3">
                    {user.lastLoginAt
                      ? new Date(user.lastLoginAt).toLocaleString()
                      : t('admin.usersPage.never')}
                  </td>
                  <td className="px-4 py-3">
                    <span className="flex gap-3">
                      <button
                        onClick={() => setEditing(user)}
                        className="text-sm text-blue-600 hover:underline"
                      >
                        {t('app.edit')}
                      </button>
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
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {creating && <CreateUserDialog onClose={() => setCreating(false)} />}
      {editing && <EditUserDialog user={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

/**
 * Edit an account's platform-owned fields. Groups are the WorkspacePolicy
 * matching key: editable here as long as (or in complement of) SSO login,
 * which overwrites them at each login. The dialog shows live which policy
 * the current groups resolve to, via the same evaluator the webhook runs.
 */
function EditUserDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const { t } = useTranslation();
  const update = useUpdateUser();
  const effective = useEffectivePolicy(user.id);
  const [role, setRole] = useState<string>(user.role);
  const [maxWorkspaces, setMaxWorkspaces] = useState(user.maxWorkspaces);
  // Same selector as creation, pre-filled with the account's current
  // groups; submission sends the COMPLETE list (state, not a diff).
  const [groups, setGroups] = useState<string[]>(user.groups ?? []);

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    update.mutate(
      { id: user.id, input: { role, maxWorkspaces, groups } },
      { onSuccess: onClose },
    );
  };

  const field =
    'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white';
  const report = effective.data?.data;

  return (
    <Dialog
      title={t('admin.usersPage.edit', { username: user.username })}
      onClose={onClose}
      onSubmit={onSubmit}
      maxWidth="max-w-lg"
      footer={
        <>
          {update.isError && <p className="mr-auto text-sm text-red-600">{update.error.message}</p>}
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            type="submit"
            disabled={update.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.save')}
          </button>
        </>
      }
    >
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.usersPage.role')}
          </span>
          <select className={field} value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="user">user</option>
            <option value="admin">admin</option>
          </select>
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.usersPage.maxWorkspaces')}
          </span>
          <input
            type="number"
            min={0}
            className={field}
            value={maxWorkspaces}
            onChange={(e) => setMaxWorkspaces(Number(e.target.value))}
          />
        </label>
        <GroupsPicker value={groups} onChange={setGroups} hint={t('admin.usersPage.groupsHint')} />

        {report && (
          <div className="rounded-md bg-slate-50 p-3 text-sm dark:bg-slate-700/50">
            <p className="font-medium text-slate-700 dark:text-slate-200">
              {t('admin.usersPage.effectivePolicy')}:{' '}
              {report.effective ? (
                <span className="text-blue-600 dark:text-blue-400">
                  {report.effective.name} (priority {report.effective.priority})
                </span>
              ) : (
                <span className="text-red-600 dark:text-red-400">
                  {t('admin.usersPage.noPolicy')}
                </span>
              )}
            </p>
            <ul className="mt-1 space-y-0.5 text-xs text-slate-500 dark:text-slate-400">
              {report.evaluated.map((p) => (
                <li key={p.name}>
                  {p.selected ? '▶ ' : p.matched ? '✓ ' : '✗ '}
                  {p.name} (prio {p.priority}
                  {p.via ? `, via ${p.via}` : ''})
                </li>
              ))}
            </ul>
            {report.warnings?.map((warning) => (
              <p key={warning} className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                ⚠ {warning}
              </p>
            ))}
          </div>
        )}

    </Dialog>
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
  const [groups, setGroups] = useState<string[]>([]);

  const set = (patch: Partial<CreateUserInput>) => setInput((prev) => ({ ...prev, ...patch }));

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate({ ...input, groups: groups.length > 0 ? groups : undefined }, { onSuccess: onClose });
  };

  const field =
    'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white';

  return (
    <Dialog
      title={t('admin.usersPage.new')}
      onClose={onClose}
      onSubmit={onSubmit}
      footer={
        <>
          {create.isError && <p className="mr-auto text-sm text-red-600">{create.error.message}</p>}
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
        <GroupsPicker
          value={groups}
          onChange={setGroups}
          hint={t('admin.usersPage.createGroupsHint')}
        />
    </Dialog>
  );
}
