import { NavLink, Outlet, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';

const NAV_CLASS = ({ isActive }: { isActive: boolean }) =>
  `block rounded-md px-3 py-2 text-sm ${
    isActive
      ? 'bg-blue-600 text-white'
      : 'text-slate-700 hover:bg-slate-200 dark:text-slate-200 dark:hover:bg-slate-700'
  }`;

export function AdminLayout() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);

  return (
    <div className="flex min-h-screen bg-slate-100 dark:bg-slate-900">
      <aside className="flex w-56 flex-col gap-1 border-r border-slate-200 bg-white p-4 dark:border-slate-700 dark:bg-slate-800">
        <h1 className="mb-4 px-3 text-lg font-semibold text-slate-900 dark:text-white">
          {t('admin.title')}
        </h1>
        <NavLink to="/admin" end className={NAV_CLASS}>
          {t('admin.fleet')}
        </NavLink>
        <NavLink to="/admin/templates" className={NAV_CLASS}>
          {t('admin.templates')}
        </NavLink>
        <NavLink to="/admin/users" className={NAV_CLASS}>
          {t('admin.users')}
        </NavLink>
        <NavLink to="/admin/governance" className={NAV_CLASS}>
          {t('admin.governance')}
        </NavLink>
        <NavLink to="/admin/audit" className={NAV_CLASS}>
          {t('admin.audit')}
        </NavLink>
        <div className="mt-auto space-y-1">
          <button
            onClick={() => navigate('/')}
            className="block w-full rounded-md px-3 py-2 text-left text-sm text-slate-700 hover:bg-slate-200 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {t('admin.backToPortal')}
          </button>
          <button
            onClick={() => {
              logout();
              navigate('/login');
            }}
            className="block w-full rounded-md px-3 py-2 text-left text-sm text-slate-500 hover:bg-slate-200 dark:text-slate-400 dark:hover:bg-slate-700"
          >
            {t('app.logout')}
          </button>
        </div>
      </aside>
      <main className="flex-1 p-6">
        <Outlet />
      </main>
    </div>
  );
}
