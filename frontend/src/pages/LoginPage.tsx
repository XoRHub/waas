import { useState, type FormEvent } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useAuthProviders, useLogin } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const login = useAuthStore((s) => s.login);
  const mutation = useLogin();
  const providers = useAuthProviders();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const ssoError = (location.state as { ssoError?: string } | null)?.ssoError;
  const oidc = providers.data?.data.oidc;
  const localEnabled = providers.data?.data.local ?? true;

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    mutation.mutate(
      { username, password },
      {
        onSuccess: ({ data }) => {
          login(data.accessToken, data.user);
          navigate(data.user.role === 'admin' ? '/admin' : '/', { replace: true });
        },
      },
    );
  };

  // Don't flash the local form while the providers request is in flight:
  // if the answer is local: false it would pop in and vanish.
  if (providers.isPending) {
    return <div className="flex min-h-screen items-center justify-center bg-slate-100 dark:bg-slate-900" />;
  }

  // No fallback form here: providers and login are served by the same
  // api-server, so a form rendered blind would fail anyway — and on an
  // OIDC-only deployment it would blame the user's credentials for it.
  if (providers.isError) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-100 dark:bg-slate-900">
        <div className="w-full max-w-sm space-y-4 rounded-xl bg-white p-8 shadow dark:bg-slate-800">
          <h1 className="text-xl font-semibold text-slate-900 dark:text-white">
            {t('login.title')}
          </h1>
          <p className="text-sm text-red-600 dark:text-red-400">{t('app.error')}</p>
          <button
            type="button"
            onClick={() => providers.refetch()}
            className="w-full rounded-md border border-slate-300 px-4 py-2 font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            {t('app.retry')}
          </button>
        </div>
      </div>
    );
  }

  const ssoButton = oidc?.enabled && oidc.startUrl && (
    <button
      type="button"
      onClick={() => {
        window.location.href = oidc.startUrl!;
      }}
      className="w-full rounded-md border border-slate-300 px-4 py-2 font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
    >
      {t('login.sso', { provider: oidc.name ?? 'SSO' })}
    </button>
  );

  if (!localEnabled) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-100 dark:bg-slate-900">
        <div className="w-full max-w-sm space-y-4 rounded-xl bg-white p-8 shadow dark:bg-slate-800">
          <h1 className="text-xl font-semibold text-slate-900 dark:text-white">
            {t('login.title')}
          </h1>
          {ssoError && (
            <p className="text-sm text-red-600 dark:text-red-400">
              {t('login.ssoFailed')}: {ssoError}
            </p>
          )}
          {ssoButton}
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-100 dark:bg-slate-900">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm space-y-4 rounded-xl bg-white p-8 shadow dark:bg-slate-800"
      >
        <h1 className="text-xl font-semibold text-slate-900 dark:text-white">{t('login.title')}</h1>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('login.username')}</span>
          <input
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
          />
        </label>
        <label className="block">
          <span className="text-sm text-slate-600 dark:text-slate-300">{t('login.password')}</span>
          <input
            type="password"
            className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>
        {mutation.isError && (
          <p className="text-sm text-red-600 dark:text-red-400">{t('login.failed')}</p>
        )}
        {ssoError && (
          <p className="text-sm text-red-600 dark:text-red-400">
            {t('login.ssoFailed')}: {ssoError}
          </p>
        )}
        <button
          type="submit"
          disabled={mutation.isPending}
          className="w-full rounded-md bg-blue-600 px-4 py-2 font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {t('login.submit')}
        </button>
        {ssoButton && (
          <>
            <div className="flex items-center gap-3">
              <span className="h-px flex-1 bg-slate-200 dark:bg-slate-600" />
              <span className="text-xs uppercase text-slate-400">{t('login.or')}</span>
              <span className="h-px flex-1 bg-slate-200 dark:bg-slate-600" />
            </div>
            {ssoButton}
          </>
        )}
      </form>
    </div>
  );
}
