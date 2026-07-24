import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { User } from '@/types';

interface AuthState {
  accessToken: string | null;
  user: User | null;
  login: (token: string, user: User) => void;
  setUser: (user: User) => void;
  /** User-initiated sign out: revokes EVERY session of the account. */
  logout: () => void;
  /**
   * Drops this browser's auth state without touching the server. For
   * error paths — a rejected token, a failed profile fetch — where the
   * session is unusable here but must not be torn down on the user's
   * other devices.
   */
  clearLocal: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      accessToken: null,
      user: null,
      login: (accessToken, user) => set({ accessToken, user }),
      setUser: (user) => set({ user }),
      logout: () => {
        const token = get().accessToken;
        // Best-effort server-side revocation (global: every session of
        // the account). Raw fetch on purpose: importing @/lib/api here
        // would be circular (api.ts imports this store), and its 401
        // handler calls logout() — routing this request through it could
        // recurse. Fire and forget: local logout must keep working
        // offline or with the API down.
        if (token) {
          void fetch('/api/v1/auth/logout', {
            method: 'POST',
            headers: { Authorization: `Bearer ${token}` },
          }).catch(() => {
            // Unreachable server or already-dead token: nothing to do —
            // the token either expires or was already revoked.
          });
        }
        set({ accessToken: null, user: null });
      },
      clearLocal: () => set({ accessToken: null, user: null }),
    }),
    { name: 'waas-auth' },
  ),
);
