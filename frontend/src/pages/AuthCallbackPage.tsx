import { useEffect, useRef } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { api } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';
import type { User } from '@/types';

/**
 * Lands the browser after the OIDC callback. The api-server puts the
 * platform token (or an error) in the URL fragment, which never reaches
 * any server log. The token is stored, the profile fetched, and the
 * fragment scrubbed from history.
 */
export function AuthCallbackPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return; // StrictMode double-invoke guard
    ran.current = true;

    const params = new URLSearchParams(window.location.hash.slice(1));
    window.history.replaceState(null, '', window.location.pathname);

    const error = params.get('error');
    const token = params.get('token');
    if (error || !token) {
      navigate('/login', { replace: true, state: { ssoError: error ?? 'missing token' } });
      return;
    }

    useAuthStore.setState({ accessToken: token });
    api
      .get<User>('/api/v1/auth/me')
      .then(({ data }) => {
        useAuthStore.getState().login(token, data);
        navigate(data.role === 'admin' ? '/admin' : '/', { replace: true });
      })
      .catch(() => {
        useAuthStore.getState().logout();
        navigate('/login', { replace: true, state: { ssoError: 'profile fetch failed' } });
      });
  }, [navigate]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-100 text-slate-500 dark:bg-slate-900 dark:text-slate-400">
      <p>{t('login.ssoCompleting')}</p>
    </div>
  );
}
