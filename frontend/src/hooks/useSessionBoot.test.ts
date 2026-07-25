// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { ApiError } from '@/lib/api';
import { createApiMock } from '@/test/apiMock';
import { useAuthStore } from '@/stores/authStore';
import { useSessionBoot } from './useSessionBoot';
import type { User } from '@/types';

const apiMock = createApiMock();
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  get api() {
    return apiMock.api;
  },
}));

const user = { id: 'u-1', username: 'alice', role: 'user' } as User;

const problem = (status: number) => new ApiError({ type: 'about:blank', title: 'x', status });

/** The boot probe runs once per mount; the ref guard is per hook instance. */
const boot = () => renderHook(() => useSessionBoot());

beforeEach(() => {
  useAuthStore.setState({ user: null, ready: false, signedOutReason: null });
  window.history.replaceState({}, '', '/');
});
afterEach(() => {
  cleanup();
  apiMock.api.get.mockReset();
});

it('restores the profile when the cookie is still good', async () => {
  apiMock.api.get.mockResolvedValue({ data: user });
  boot();

  await waitFor(() => expect(useAuthStore.getState().user).toEqual(user));
  expect(useAuthStore.getState().ready).toBe(true);
});

// The whole point of the api-server answering 503 rather than 401 on a
// database hiccup is that an outage must not sign the fleet out. Reading
// anything-but-401 as "signed out" here would throw that away — and the
// two land on different screens: 'unavailable' renders a "try again",
// 'no-session' redirects to the login page.
it('tells "no session" apart from "could not tell"', async () => {
  for (const [status, want] of [
    [401, 'no-session'],
    [503, 'unavailable'],
    [500, 'unavailable'],
  ] as const) {
    useAuthStore.setState({ user: null, ready: false, signedOutReason: null });
    apiMock.api.get.mockRejectedValue(problem(status));
    const view = boot();

    await waitFor(() => expect(useAuthStore.getState().ready).toBe(true));
    expect(useAuthStore.getState().signedOutReason).toBe(want);
    view.unmount();
  }
});

it('treats a network failure as "could not tell", not as a sign-out', async () => {
  apiMock.api.get.mockRejectedValue(new TypeError('Failed to fetch'));
  boot();

  await waitFor(() => expect(useAuthStore.getState().ready).toBe(true));
  expect(useAuthStore.getState().signedOutReason).toBe('unavailable');
});

// A cold server can make the probe slower than a sign-in typed right
// after it. The session that exists now is the current one; this answer
// is about the absence that preceded it.
it('does not sign out a session that arrived while it was in flight', async () => {
  let reject: (e: unknown) => void = () => {};
  apiMock.api.get.mockReturnValue(
    new Promise((_resolve, r) => {
      reject = r;
    }),
  );
  boot();

  useAuthStore.getState().setUser(user); // the login wins the race
  reject(problem(401));

  await waitFor(() => expect(useAuthStore.getState().user).toEqual(user));
  expect(useAuthStore.getState().signedOutReason).toBeNull();
});

// The SSO landing issues this exact request itself and owns what happens
// next. Probing here too would double the lookup and let whichever
// settles last decide the sign-in state.
it('stays out of the way on the SSO callback route', () => {
  window.history.replaceState({}, '', '/auth/callback');
  boot();

  expect(apiMock.api.get).not.toHaveBeenCalled();
});
