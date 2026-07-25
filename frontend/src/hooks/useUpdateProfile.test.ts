// @vitest-environment jsdom
import { createElement, type ReactNode } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createApiMock } from '@/test/apiMock';
import { useAuthStore } from '@/stores/authStore';
import { useUpdateProfile } from './useApi';
import type { User } from '@/types';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const user: User = {
  id: 'u-1',
  username: 'alice',
  role: 'user',
  active: true,
  maxWorkspaces: 3,
} as User;

const queryClient = new QueryClient();
const wrapper = ({ children }: { children: ReactNode }) =>
  createElement(QueryClientProvider, { client: queryClient }, children);

beforeEach(() => {
  useAuthStore.setState({ user, ready: true, signedOutReason: null });
  apiMock.api.patch.mockResolvedValue({ data: { ...user, displayName: 'Alice' } });
});
afterEach(() => {
  cleanup();
  apiMock.api.patch.mockReset();
});

// The password change revoked every token of the account server-side and
// the response already expired the cookie. Staying on screen as if signed
// in only defers the discovery to the next request's 401 — and to a
// notice that reads like a failure rather than like what the user just
// asked for (audit 3, F9).
it('signs the browser out with its own reason after a password change', async () => {
  const { result } = renderHook(() => useUpdateProfile(), { wrapper });

  await act(async () => {
    result.current.mutate({ currentPassword: 'old-one', newPassword: 'a-new-one' });
  });

  await waitFor(() => expect(useAuthStore.getState().user).toBeNull());
  expect(useAuthStore.getState().signedOutReason).toBe('password-changed');
  // ready stays true: this is a settled verdict, not a boot probe in
  // flight, so ProtectedRoute must redirect instead of showing a spinner.
  expect(useAuthStore.getState().ready).toBe(true);
});

it('keeps the session and refreshes the user on an ordinary profile edit', async () => {
  const { result } = renderHook(() => useUpdateProfile(), { wrapper });

  await act(async () => {
    result.current.mutate({ displayName: 'Alice' });
  });

  await waitFor(() => expect(useAuthStore.getState().user?.displayName).toBe('Alice'));
  expect(useAuthStore.getState().signedOutReason).toBeNull();
});
