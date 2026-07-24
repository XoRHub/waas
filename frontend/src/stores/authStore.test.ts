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
  useAuthStore.setState({ accessToken: null, user: null });
});

describe('authStore', () => {
  it('login stores the token and the user together', () => {
    useAuthStore.getState().login('tok', user);
    expect(useAuthStore.getState().accessToken).toBe('tok');
    expect(useAuthStore.getState().user).toEqual(user);
  });

  it('setUser refreshes the profile without touching the token', () => {
    useAuthStore.getState().login('tok', user);
    useAuthStore.getState().setUser({ ...user, username: 'renamed' });
    expect(useAuthStore.getState().user?.username).toBe('renamed');
    expect(useAuthStore.getState().accessToken).toBe('tok');
  });

  it('logout clears both', () => {
    useAuthStore.getState().login('tok', user);
    useAuthStore.getState().logout();
    expect(useAuthStore.getState().accessToken).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('logout revokes the session server-side with the bearer', () => {
    useAuthStore.getState().login('tok', user);
    useAuthStore.getState().logout();
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/logout', {
      method: 'POST',
      headers: { Authorization: 'Bearer tok' },
    });
  });

  it('logout clears local state even when the server call fails', () => {
    // Offline / API down: the user must still be able to log out locally.
    fetchMock.mockImplementation(() => Promise.reject(new Error('offline')));
    useAuthStore.getState().login('tok', user);
    useAuthStore.getState().logout();
    expect(fetchMock).toHaveBeenCalled();
    expect(useAuthStore.getState().accessToken).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('logout without a token never calls the server', () => {
    // Nothing to revoke, so no request to send.
    useAuthStore.getState().logout();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('clearLocal drops this browser without revoking the account', () => {
    // The error paths (a 401 from the api funnel, a failed profile fetch
    // after SSO) must not sign the user out on their other devices.
    useAuthStore.getState().login('token', user);
    useAuthStore.getState().clearLocal();
    expect(useAuthStore.getState()).toMatchObject({ accessToken: null, user: null });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
