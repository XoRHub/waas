import { Navigate, Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';

// The boot probe could not reach a verdict, so we do not know whether the
// cookie is valid. Say so and offer a retry instead of sending the user to
// the login page: a database failover or a rolling restart would otherwise
// look exactly like being signed out, and re-authenticating is not what
// fixes it.
function SessionUnavailable() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-screen items-center justify-center app-background">
      <div className="w-full max-w-sm space-y-4 rounded-xl bg-white p-8 text-center shadow dark:bg-slate-800">
        <p className="text-sm text-slate-600 dark:text-slate-300">{t('app.unreachable')}</p>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="w-full rounded-md border border-slate-300 px-4 py-2 font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
        >
          {t('app.retry')}
        </button>
      </div>
    </div>
  );
}

export function ProtectedRoute({ adminOnly = false }: { adminOnly?: boolean }) {
  const user = useAuthStore((s) => s.user);
  const ready = useAuthStore((s) => s.ready);
  const signedOutReason = useAuthStore((s) => s.signedOutReason);

  // The session is an httpOnly cookie this code cannot read, so "signed
  // in?" is only answerable once the boot /auth/me probe has come back.
  // Redirecting before that would bounce a perfectly valid session to the
  // login page on every page load.
  if (!ready) {
    return <div className="flex min-h-screen items-center justify-center app-background" />;
  }
  if (!user) {
    if (signedOutReason === 'unavailable') {
      return <SessionUnavailable />;
    }
    return <Navigate to="/login" replace />;
  }
  if (adminOnly && user.role !== 'admin') {
    return <Navigate to="/" replace />;
  }
  return <Outlet />;
}
