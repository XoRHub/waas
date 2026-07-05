import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Avatar } from '@/components/Avatar';
import { useAuthStore } from '@/stores/authStore';

// Header avatar dropdown: profile, admin console (if admin), logout.
export function UserMenu() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const [open, setOpen] = useState(false);

  if (!user) return null;

  const go = (to: string) => {
    setOpen(false);
    navigate(to);
  };

  return (
    <div className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 rounded-full outline-offset-2"
        aria-haspopup="menu"
        aria-expanded={open}
      >
        <Avatar user={user} size="sm" />
        <span className="hidden text-sm text-slate-700 sm:inline dark:text-slate-200">
          {user.displayName || user.username}
        </span>
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          <div
            role="menu"
            className="absolute right-0 z-20 mt-2 w-48 overflow-hidden rounded-lg bg-white py-1 shadow-lg ring-1 ring-slate-200 dark:bg-slate-800 dark:ring-slate-700"
          >
            <MenuItem onClick={() => go('/profile')}>{t('profile.title')}</MenuItem>
            {user.role === 'admin' && (
              <MenuItem onClick={() => go('/admin')}>{t('admin.title')}</MenuItem>
            )}
            <MenuItem
              onClick={() => {
                logout();
                navigate('/login');
              }}
            >
              {t('app.logout')}
            </MenuItem>
          </div>
        </>
      )}
    </div>
  );
}

function MenuItem({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      role="menuitem"
      onClick={onClick}
      className="block w-full px-4 py-2 text-left text-sm text-slate-700 hover:bg-slate-50 dark:text-slate-200 dark:hover:bg-slate-700"
    >
      {children}
    </button>
  );
}
