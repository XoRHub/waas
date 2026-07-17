// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import type { User } from '@/types';
import { useAuthStore } from './authStore';

const user = { id: 'u1', username: 'marc', role: 'user' } as User;

beforeEach(() => {
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
});
