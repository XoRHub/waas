import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { User } from '@/types';

interface AuthState {
  accessToken: string | null;
  user: User | null;
  login: (token: string, user: User) => void;
  setUser: (user: User) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      user: null,
      login: (accessToken, user) => set({ accessToken, user }),
      setUser: (user) => set({ user }),
      logout: () => set({ accessToken: null, user: null }),
    }),
    { name: 'waas-auth' },
  ),
);
