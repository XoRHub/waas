// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { User } from '@/types';
import { useAuthStore } from '@/stores/authStore';
import { ApiError, api } from './api';

const user = { id: 'u1', username: 'marc', role: 'user' } as User;

const fetchMock = vi.fn();
vi.stubGlobal('fetch', fetchMock);

const unauthorized = () =>
  new Response(JSON.stringify({ type: 'about:blank', title: 'Unauthorized', status: 401 }), {
    status: 401,
    headers: { 'Content-Type': 'application/json' },
  });

beforeEach(() => {
  fetchMock.mockReset();
  useAuthStore.setState({ user: null, ready: false, signedOutReason: null });
});

describe('api 401 handling', () => {
  it('drops the local session when the server rejects the one we hold', async () => {
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(unauthorized());

    await expect(api.get('/api/v1/workspaces')).rejects.toBeInstanceOf(ApiError);

    expect(useAuthStore.getState().user).toBeNull();
    expect(useAuthStore.getState().signedOutReason).toBe('rejected');
  });

  it('ignores a 401 about a session that has since been replaced', async () => {
    // The boot probe leaves before anyone is signed in, so it is bound to
    // 401. On a cold server it can land after the user has signed in — and
    // acting on it would sign them straight back out of a valid session.
    let release: (r: Response) => void = () => {};
    fetchMock.mockReturnValue(
      new Promise<Response>((resolve) => {
        release = resolve;
      }),
    );

    const probe = api.get<User>('/api/v1/auth/me');
    useAuthStore.getState().setUser(user); // the login wins the race
    release(unauthorized());

    await expect(probe).rejects.toBeInstanceOf(ApiError);

    expect(useAuthStore.getState().user).toEqual(user);
    expect(useAuthStore.getState().signedOutReason).toBeNull();
  });

  it('leaves a signed-out browser alone', async () => {
    fetchMock.mockResolvedValue(unauthorized());
    await expect(api.get('/api/v1/auth/me')).rejects.toBeInstanceOf(ApiError);
    expect(useAuthStore.getState().signedOutReason).toBeNull();
  });

  it('never touches the session on a server failure', async () => {
    // A 503 says the server could not answer, not that the session died.
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(new Response('nope', { status: 503 }));

    await expect(api.get('/api/v1/workspaces')).rejects.toBeInstanceOf(ApiError);

    expect(useAuthStore.getState().user).toEqual(user);
  });
});
