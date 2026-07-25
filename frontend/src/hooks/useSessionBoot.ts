import { useEffect, useRef } from 'react';
import { ApiError, api } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';
import type { User } from '@/types';

// The session lives in an httpOnly cookie this code cannot read, so the
// only way to know whether one is active is to ask. One probe at boot,
// before any protected route gets to decide on a redirect — and it is
// also what restores the profile after a page reload, now that nothing
// is persisted client-side.
export function useSessionBoot() {
  const probed = useRef(false);
  useEffect(() => {
    if (probed.current) return; // StrictMode double-invoke guard
    probed.current = true;
    // The SSO landing runs this exact request itself and owns what
    // happens next; probing here too would double the lookup and let the
    // two writers decide the sign-in state by whichever settles last.
    if (window.location.pathname === '/auth/callback') return;

    api
      .get<User>('/api/v1/auth/me')
      .then(({ data }) => useAuthStore.getState().setUser(data))
      .catch((error: unknown) => {
        // A sign-in can complete while this is in flight (the form beats
        // a cold server): that session is the current one, and this
        // answer is about the absence that preceded it.
        if (useAuthStore.getState().user) return;
        // Only a 401 means "no session". Anything else — 503 during a
        // database failover, a rolling restart, a proxy blip — means the
        // server could not tell us, and presenting that as signed out
        // throws away the whole point of it answering 503 rather than 401.
        useAuthStore
          .getState()
          .clearLocal(
            error instanceof ApiError && error.problem.status === 401
              ? 'no-session'
              : 'unavailable',
          );
      });
  }, []);
}
