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

// The sign-out itself belongs to the api layer, which reads the server's
// announcement (see lib/api.test.ts). What this hook must not do is put
// the user back from a body describing an account they just lost — the
// response still carries the updated profile, and storing it would
// resurrect a session the server has ended (audit 3, F9).
it('does not resurrect the user when the server ended the session', async () => {
  apiMock.api.patch.mockImplementation(async () => {
    // What the real api layer does before this hook's onSuccess runs.
    useAuthStore.getState().clearLocal('password-changed');
    return { data: user, sessionEnded: 'password-changed' };
  });
  const { result } = renderHook(() => useUpdateProfile(), { wrapper });

  await act(async () => {
    result.current.mutate({ currentPassword: 'old-one', newPassword: 'a-new-one' });
  });

  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(useAuthStore.getState().user).toBeNull();
  expect(useAuthStore.getState().signedOutReason).toBe('password-changed');
});

it('keeps the session and refreshes the user on an ordinary profile edit', async () => {
  const { result } = renderHook(() => useUpdateProfile(), { wrapper });

  await act(async () => {
    result.current.mutate({ displayName: 'Alice' });
  });

  await waitFor(() => expect(useAuthStore.getState().user?.displayName).toBe('Alice'));
  expect(useAuthStore.getState().signedOutReason).toBeNull();
});
