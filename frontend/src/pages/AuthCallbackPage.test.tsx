// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { ApiError } from '@/lib/api';
import { createApiMock } from '@/test/apiMock';
import { renderWithProviders } from '@/test/render';
import { useAuthStore } from '@/stores/authStore';
import { AuthCallbackPage } from './AuthCallbackPage';
import type { User } from '@/types';

const apiMock = createApiMock();
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  get api() {
    return apiMock.api;
  },
}));

const navigate = vi.fn();
vi.mock('react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react-router')>()),
  useNavigate: () => navigate,
}));

const user = { id: 'u-1', username: 'alice', role: 'user' } as User;
const admin = { id: 'u-2', username: 'root', role: 'admin' } as User;

const problem = (status: number) => new ApiError({ type: 'about:blank', title: 'x', status });

const landOn = (hash: string) => window.history.replaceState(null, '', '/auth/callback' + hash);

beforeEach(() => {
  useAuthStore.setState({ user: null, ready: false, signedOutReason: null });
  landOn('');
});
afterEach(() => {
  navigate.mockReset();
  apiMock.api.get.mockReset();
});

it('signs the user in and sends them to their landing page', async () => {
  apiMock.api.get.mockResolvedValue({ data: user });
  renderWithProviders(<AuthCallbackPage />);

  await waitFor(() => expect(useAuthStore.getState().user).toEqual(user));
  expect(navigate).toHaveBeenCalledWith('/', { replace: true });
  // The session cookie is already set; this is the only thing the page
  // fetches, and a wrong route would send every SSO sign-in to /login.
  expect(apiMock.api.get).toHaveBeenCalledWith('/api/v1/auth/me');
});

it('sends an admin to the console', async () => {
  apiMock.api.get.mockResolvedValue({ data: admin });
  renderWithProviders(<AuthCallbackPage />);

  await waitFor(() => expect(navigate).toHaveBeenCalledWith('/admin', { replace: true }));
});

// The api-server hands the error back in the fragment; there is no
// session to fetch and asking for one would only mask the real cause.
it('carries an IdP error to the login page without probing', async () => {
  landOn('#error=access_denied');
  renderWithProviders(<AuthCallbackPage />);

  await waitFor(() =>
    expect(navigate).toHaveBeenCalledWith('/login', {
      replace: true,
      state: { ssoError: 'access_denied' },
    }),
  );
  expect(apiMock.api.get).not.toHaveBeenCalled();
  // And the fragment does not stay in history for the next reload.
  expect(window.location.hash).toBe('');
});

// A 401 here can only mean the cookie the callback set was never stored:
// the sign-in itself succeeded. Told apart because "try again" sends the
// user into a loop that cannot end.
it('names a blocked cookie instead of blaming the sign-in', async () => {
  apiMock.api.get.mockRejectedValue(problem(401));
  renderWithProviders(<AuthCallbackPage />);

  await waitFor(() => expect(navigate).toHaveBeenCalled());
  const [, options] = navigate.mock.calls[0] as [string, { state: { ssoError: string } }];
  expect(options.state.ssoError).toMatch(/did not keep the session cookie/);
});

// Anything else says nothing about the freshly minted session, so it must
// drop this browser's view only. logout() would revoke the account
// everywhere because one profile fetch hiccuped.
it('clears locally without revoking when the server could not answer', async () => {
  apiMock.api.get.mockRejectedValue(problem(503));
  renderWithProviders(<AuthCallbackPage />);

  await waitFor(() => expect(useAuthStore.getState().signedOutReason).toBe('no-session'));
  const [, options] = navigate.mock.calls[0] as [string, { state: { ssoError: string } }];
  expect(options.state.ssoError).toMatch(/Can't reach the server/);
  expect(useAuthStore.getState().user).toBeNull();
});
