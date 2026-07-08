import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
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

/**
 * Live updates over SSE: one EventSource per app (mounted by the portal
 * and the admin console), auto-reconnecting (native EventSource
 * behavior). The existing polling stays untouched as the degraded mode —
 * SSE only makes convergence immediate (cron transitions, kubectl edits,
 * other tabs/devices).
 */
export function useEvents() {
  const queryClient = useQueryClient();
  const token = useAuthStore((s) => s.accessToken);

  useEffect(() => {
    if (!token) return;
    // EventSource cannot set headers: the SAME access token travels as a
    // query parameter, verified by the same middleware.
    const source = new EventSource(`/api/v1/events?access_token=${encodeURIComponent(token)}`);
    source.onmessage = (event) => {
      for (const key of INVALIDATIONS[event.data] ?? []) {
        void queryClient.invalidateQueries({ queryKey: key });
      }
    };
    return () => source.close();
  }, [token, queryClient]);
}
