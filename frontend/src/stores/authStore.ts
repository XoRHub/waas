import { create } from 'zustand';
import type { User } from '@/types';

/**
 * The session credential is NOT here, and must never come back: it lives
 * in the httpOnly `waas_session` cookie the api-server sets, out of
 * JavaScript's reach (security audit 2026-07-20, finding #13). This store
 * holds only the profile — who is signed in, for rendering and route
 * gating — which is why nothing here is persisted either: `user` is
 * re-hydrated from /auth/me at boot, and a page reload keeps the session
 * because the cookie survives it.
 */
/**
 * One-time eviction of the pre-cookie store. Releases before the session
 * cookie persisted `{accessToken, user}` under this key; dropping the
 * persist middleware only stops WRITING it, so every browser already
 * signed in when the upgrade lands keeps a readable bearer — valid until
 * it expires, and accepted by the Authorization transport from anywhere —
 * sitting in localStorage, which is precisely the exposure finding #13
 * closes. Removable once no deployed browser predates that release.
 */
try {
  localStorage.removeItem('waas-auth');
} catch {
  // No storage at all (blocked by policy, non-DOM test env): nothing to evict.
}

/**
 * Why this browser holds no session, when that is actually known:
 *
 * - `no-session` — asked, got a clean 401. The ordinary signed-out state;
 *   nothing to tell the user.
 * - `unavailable` — could not get a verdict at all (5xx, network, proxy).
 *   The cookie may well be perfectly valid, so this must NOT be rendered
 *   as "signed out": the api-server answers 503 rather than 401 on a
 *   database hiccup precisely so an outage does not sign the fleet out.
 * - `rejected` — the server refused a session we believed in: it expired
 *   or was revoked, or the browser never stored the cookie to begin with.
 * - `password-changed` / `rights-changed` — the user ended their own
 *   session by what they just did: changing their password, or demoting
 *   or deactivating their own account through the admin API. Both revoke
 *   every session of the account, this one included. Told apart from
 *   `rejected` because they are the expected outcome of a deliberate
 *   action, not a failure — and both are announced by the server in the
 *   response itself (see SESSION_ENDED_HEADER), never guessed here.
 */
export type SignedOutReason =
  'no-session' | 'unavailable' | 'rejected' | 'password-changed' | 'rights-changed';

interface AuthState {
  user: User | null;
  /** True until the boot /auth/me probe has settled, either way. */
  ready: boolean;
  /**
   * Bumped on every change of session identity. A request captures it
   * before it leaves; a 401 answer that comes back under a different
   * epoch is about a session that no longer exists and must be ignored,
   * or the boot probe racing a sign-in on a cold server would tear down
   * the session that just replaced it.
   */
  epoch: number;
  signedOutReason: SignedOutReason | null;
  setUser: (user: User) => void;
  /** User-initiated sign out: revokes EVERY session of the account. */
  logout: () => void;
  /**
   * Drops this browser's view of the session without revoking anything.
   * For error paths — a rejected cookie, a failed profile fetch — where
   * the session is unusable here but must not be torn down on the user's
   * other devices.
   */
  clearLocal: (reason: SignedOutReason) => void;
}

export const useAuthStore = create<AuthState>()((set) => ({
  user: null,
  ready: false,
  epoch: 0,
  signedOutReason: null,
  setUser: (user) => set((s) => ({ user, ready: true, epoch: s.epoch + 1, signedOutReason: null })),
  logout: () => {
    // Best-effort server-side revocation (global: every session of the
    // account) — it is also what expires the cookie. Raw fetch on
    // purpose: importing @/lib/api here would be circular (api.ts imports
    // this store), and its 401 handler calls clearLocal(). Fire and
    // forget: signing out locally must work with the API down.
    void fetch('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
    }).catch(() => {
      // Unreachable server or already-dead session: nothing to do.
    });
    // No reason: the user asked for this, so the login page has nothing
    // to explain to them.
    set((s) => ({ user: null, ready: true, epoch: s.epoch + 1, signedOutReason: null }));
  },
  clearLocal: (reason) =>
    set((s) => ({ user: null, ready: true, epoch: s.epoch + 1, signedOutReason: reason })),
}));
