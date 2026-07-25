// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { User } from '@/types';
import { useAuthStore } from './authStore';

const user = { id: 'u1', username: 'marc', role: 'user' } as User;

const fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
vi.stubGlobal('fetch', fetchMock);

beforeEach(() => {
  fetchMock.mockClear();
  fetchMock.mockImplementation(() => Promise.resolve(new Response(null, { status: 204 })));
  useAuthStore.setState({ user: null, ready: false, signedOutReason: null });
});

describe('authStore', () => {
  it('holds no credential at all — the session is an httpOnly cookie', () => {
    useAuthStore.getState().setUser(user);
    // Guards finding #13: anything token-shaped landing back in this
    // store is reachable by an XSS again.
    expect(Object.keys(useAuthStore.getState())).not.toContain('accessToken');
    expect(JSON.stringify(useAuthStore.getState())).not.toMatch(/token/i);
  });

  it('setUser stores the profile and marks the boot probe settled', () => {
    expect(useAuthStore.getState().ready).toBe(false);
    useAuthStore.getState().setUser(user);
    expect(useAuthStore.getState().user).toEqual(user);
    expect(useAuthStore.getState().ready).toBe(true);
  });

  it('logout clears the profile', () => {
    useAuthStore.getState().setUser(user);
    useAuthStore.getState().logout();
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('logout revokes the session server-side, authenticated by the cookie', () => {
    useAuthStore.getState().setUser(user);
    useAuthStore.getState().logout();
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
    });
  });

  it('logout clears local state even when the server call fails', () => {
    // Offline / API down: the user must still be able to log out locally.
    fetchMock.mockImplementation(() => Promise.reject(new Error('offline')));
    useAuthStore.getState().setUser(user);
    useAuthStore.getState().logout();
    expect(fetchMock).toHaveBeenCalled();
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('logout stays ready so the app lands on the login page, not the spinner', () => {
    useAuthStore.getState().setUser(user);
    useAuthStore.getState().logout();
    expect(useAuthStore.getState().ready).toBe(true);
  });

  it('clearLocal drops this browser without revoking the account', () => {
    // The error paths (a 401 from the api funnel, a failed profile fetch
    // after SSO) must not sign the user out on their other devices.
    useAuthStore.getState().setUser(user);
    useAuthStore.getState().clearLocal('rejected');
    expect(useAuthStore.getState()).toMatchObject({ user: null, ready: true });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('clearLocal keeps the reason, so the UI can stop guessing', () => {
    // 'unavailable' in particular must never be rendered as "signed out":
    // the server answers 503 rather than 401 on a database hiccup so that
    // an outage does not sign the fleet out.
    useAuthStore.getState().clearLocal('unavailable');
    expect(useAuthStore.getState().signedOutReason).toBe('unavailable');
    useAuthStore.getState().setUser(user);
    expect(useAuthStore.getState().signedOutReason).toBeNull();
  });

  it('logout leaves nothing for the login page to explain', () => {
    useAuthStore.getState().clearLocal('rejected');
    useAuthStore.getState().setUser(user);
    useAuthStore.getState().logout();
    expect(useAuthStore.getState().signedOutReason).toBeNull();
  });

  it('every change of session identity bumps the epoch', () => {
    // What lets a late 401 be told apart from one about the session we
    // hold now — see the guard in lib/api.ts.
    const start = useAuthStore.getState().epoch;
    useAuthStore.getState().setUser(user);
    expect(useAuthStore.getState().epoch).toBe(start + 1);
    useAuthStore.getState().clearLocal('rejected');
    expect(useAuthStore.getState().epoch).toBe(start + 2);
  });

  it('persists nothing: a reload rebuilds from the cookie, never from storage', () => {
    useAuthStore.getState().setUser(user);
    expect(window.localStorage.getItem('waas-auth')).toBeNull();
  });

  it('evicts the entry an older release left behind', async () => {
    // A browser signed in before the cookie release still holds a live
    // bearer + profile PII under the old persist key. Not writing it any
    // more is not the same as deleting it: importing the store must.
    window.localStorage.setItem(
      'waas-auth',
      JSON.stringify({ state: { accessToken: 'stale.but.still.valid', user } }),
    );
    vi.resetModules();
    await import('./authStore');
    expect(window.localStorage.getItem('waas-auth')).toBeNull();
  });
});
