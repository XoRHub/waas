import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';

// Which queries each event kind invalidates. The stream carries KINDS
// only — data always comes back through the normal authorized API.
// Partial keys match by prefix (react-query), so parameterized queries
// like ['effective-policy', userId] are covered by their family key.
const INVALIDATIONS: Record<string, string[][]> = {
  workspaces: [['workspaces'], ['quota']],
  'remote-workspaces': [['remote-workspaces'], ['admin-remote-workspaces']],
  templates: [['workspace-templates'], ['catalog']],
  images: [['catalog'], ['admin-images'], ['quota']],
  policies: [['admin-policies'], ['quota'], ['effective-policy']],
  volumes: [['volumes'], ['admin-volumes'], ['quota']],
};

// Reconnection backoff. EventSource's NATIVE auto-reconnect would replay
// the same URL — hence the same short-lived stream token — and loop on 401
// forever once it expires, so reconnection is explicit: close, mint a
// fresh token, reopen. Polling remains the degraded mode, so plateauing
// at 30s costs nothing but immediacy.
const BACKOFF_INITIAL_MS = 1_000;
const BACKOFF_MAX_MS = 30_000;
// A stream must PROVE itself before the backoff resets. `onopen` alone
// also fires for a connection the server drops immediately — a rolling
// update draining connections — which would pin the retry cycle at
// BACKOFF_INITIAL_MS and mint a token, i.e. a DB read, every second,
// exactly while the server is struggling.
const BACKOFF_RESET_AFTER_MS = 10_000;

/**
 * Live updates over SSE: one EventSource per app (mounted by the portal
 * and the admin console — sibling routes, never both at once). The
 * session never touches the URL: each (re)connect first POSTs
 * /auth/stream-token (authenticated by the session cookie) for a
 * short-lived waas-stream token, the only credential /events accepts in
 * the query string — and one that grants nothing else. The existing
 * polling stays untouched as the degraded mode — SSE only makes
 * convergence immediate (cron transitions, kubectl edits, other
 * tabs/devices).
 */
export function useEvents() {
  const queryClient = useQueryClient();
  // Keyed on the signed-in identity, not on a credential: the session is
  // a cookie this code cannot see, so "am I signed in" is only observable
  // through the profile. The id and not the object — a profile update
  // (a theme preference, say) hands back a fresh object, and keying on it
  // would tear down and re-open the stream for nothing.
  const userID = useAuthStore((s) => s.user?.id);

  useEffect(() => {
    if (!userID) return;
    let source: EventSource | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stableTimer: ReturnType<typeof setTimeout> | undefined;
    let backoff = BACKOFF_INITIAL_MS;
    let disposed = false;

    const retry = () => {
      if (disposed) return;
      clearTimeout(timer);
      timer = setTimeout(() => void connect(), backoff);
      backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
    };

    const connect = async () => {
      let streamToken: string;
      try {
        const { data } = await api.post<{ streamToken: string }>('/api/v1/auth/stream-token');
        streamToken = data.streamToken;
      } catch {
        // Includes a rejected session (api.ts then clears the profile and
        // the effect re-runs with no userID); otherwise keep backing off.
        retry();
        return;
      }
      if (disposed) return;
      source = new EventSource(`/api/v1/events?access_token=${encodeURIComponent(streamToken)}`);
      source.onopen = () => {
        clearTimeout(stableTimer);
        stableTimer = setTimeout(() => {
          backoff = BACKOFF_INITIAL_MS;
        }, BACKOFF_RESET_AFTER_MS);
      };
      source.onmessage = (event) => {
        for (const key of INVALIDATIONS[event.data] ?? []) {
          void queryClient.invalidateQueries({ queryKey: key });
        }
      };
      source.onerror = () => {
        clearTimeout(stableTimer);
        source?.close();
        source = null;
        retry();
      };
    };

    void connect();
    return () => {
      disposed = true;
      clearTimeout(timer);
      clearTimeout(stableTimer);
      source?.close();
    };
  }, [userID, queryClient]);
}
