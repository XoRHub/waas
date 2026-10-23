import { api } from '@/lib/api';
import type { ConnectResult } from '@/types';

/**
 * Milliseconds before a connection token expires (its `exp` claim), 0 when
 * unreadable. The TTL is a server setting the portal never receives
 * otherwise; the token itself carries it.
 */
export function tokenTTL(token: string): number {
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const { exp } = JSON.parse(atob(payload)) as { exp?: number };
    return exp ? Math.max(0, exp * 1000 - Date.now()) : 0;
  } catch {
    return 0;
  }
}

/**
 * Re-sets the kasm session cookie from a fresh connection token: the
 * cookie wwt set on the iframe's first request IS the token, and a desktop
 * outlives its TTL — without this KasmVNC's own reconnect and the Files
 * routes start failing with 401 a few minutes in. Returns the new token's
 * remaining life, for the caller's timer.
 */
export async function refreshKasmCookie(workspaceId: string, sessionId: string): Promise<number> {
  const { data } = await api.post<ConnectResult>(
    `/api/v1/workspaces/${workspaceId}/sessions/${sessionId}/token`,
  );
  const r = await fetch(
    `/kasm/${encodeURIComponent(sessionId)}/waas/session?token=${encodeURIComponent(data.connectionToken)}`,
    { credentials: 'same-origin' },
  );
  if (!r.ok) throw r.status;
  return tokenTTL(data.connectionToken);
}
