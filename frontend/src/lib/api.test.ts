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

// A browser cannot observe the server expiring its session: the cookie is
// HttpOnly and `Set-Cookie` is unreadable from JavaScript. The server
// therefore announces it in a header, which this layer is the single
// reader of — every endpoint that revokes its own caller is covered at
// once, and none of them has to duplicate the rule client-side.
describe('api session-ended announcements', () => {
  const announcing = (reason: string) =>
    new Response(JSON.stringify({ data: user }), {
      status: 200,
      headers: { 'Content-Type': 'application/json', 'X-Waas-Session-Ended': reason },
    });

  it('signs out with the reason the server named', async () => {
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(announcing('password-changed'));

    const res = await api.patch<User>('/api/v1/me', { newPassword: 'x' });

    expect(useAuthStore.getState().user).toBeNull();
    expect(useAuthStore.getState().signedOutReason).toBe('password-changed');
    // Surfaced on the envelope too, so a caller about to store the body
    // can tell it describes an account it may no longer act as.
    expect(res.sessionEnded).toBe('password-changed');
  });

  it('carries the admin path reason as well', async () => {
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(announcing('rights-changed'));

    await api.patch<User>('/api/v1/users/u1', { role: 'user' });

    expect(useAuthStore.getState().signedOutReason).toBe('rights-changed');
  });

  it('ignores a reason it does not know', async () => {
    // Never widen the union from a string off the wire.
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(announcing('something-else'));

    const res = await api.patch<User>('/api/v1/me', {});

    expect(useAuthStore.getState().user).toEqual(user);
    expect(res.sessionEnded).toBeUndefined();
  });

  it('leaves an ordinary response alone', async () => {
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ data: user }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    const res = await api.patch<User>('/api/v1/me', { displayName: 'Marc' });

    expect(useAuthStore.getState().user).toEqual(user);
    expect(res.sessionEnded).toBeUndefined();
  });
});

// 204 has no body to parse, and it is the shape logout answers with —
// the one response that both ends a session and carries nothing.
describe('api empty responses', () => {
  it('returns no data and still reports a session the server ended', async () => {
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(
      new Response(null, { status: 204, headers: { 'X-Waas-Session-Ended': 'rights-changed' } }),
    );

    const res = await api.delete('/api/v1/remote-workspaces/rw1');

    expect(res.data).toBeUndefined();
    expect(res.sessionEnded).toBe('rights-changed');
    expect(useAuthStore.getState().signedOutReason).toBe('rights-changed');
  });

  it('returns no data on a plain 204', async () => {
    useAuthStore.getState().setUser(user);
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

    const res = await api.delete('/api/v1/remote-workspaces/rw1');

    expect(res.data).toBeUndefined();
    expect(useAuthStore.getState().user).toEqual(user);
  });
});
