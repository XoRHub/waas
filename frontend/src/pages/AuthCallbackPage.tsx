import { useEffect, useRef } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ApiError, api } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';
import type { User } from '@/types';

/**
 * Lands the browser after the OIDC callback. The session itself arrived
 * as an httpOnly cookie on that callback response — nothing here ever
 * handles a credential. The fragment is now only used to carry an error
 * back from the api-server, and is scrubbed from history either way.
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
    if (error) {
      navigate('/login', { replace: true, state: { ssoError: error } });
      return;
    }

    // No token to pick up: the api-server already set the session cookie
    // on the callback response. Fetching the profile is both how we learn
    // who signed in and how we confirm the cookie took.
    api
      .get<User>('/api/v1/auth/me')
      .then(({ data }) => {
        useAuthStore.getState().setUser(data);
        navigate(data.role === 'admin' ? '/admin' : '/', { replace: true });
      })
      .catch((error: unknown) => {
        // clearLocal, not logout: the token here is freshly minted and may
        // well be valid — a 500 or a 503 from /auth/me says nothing about
        // it. Revoking would sign the account out on every other device
        // because one profile fetch hiccuped.
        //
        // 'no-session' rather than 'rejected' because the message below is
        // already more precise than anything the login page could add.
        useAuthStore.getState().clearLocal('no-session');
        // A 401 here can only mean the cookie the callback set was never
        // stored — the sign-in itself succeeded. Without that distinction
        // the user reads "try again", retries, and loops forever.
        const cookieBlocked = error instanceof ApiError && error.problem.status === 401;
        navigate('/login', {
          replace: true,
          state: { ssoError: t(cookieBlocked ? 'login.ssoCookieBlocked' : 'app.unreachable') },
        });
      });
  }, [navigate, t]);

  return (
    <div className="app-background flex min-h-screen items-center justify-center text-slate-400">
      <p>{t('login.ssoCompleting')}</p>
    </div>
  );
}
