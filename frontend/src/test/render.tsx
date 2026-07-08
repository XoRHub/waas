import type { ReactElement } from 'react';
import { afterEach } from 'vitest';
import { cleanup, render } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import '@testing-library/jest-dom/vitest';
import i18n from '@/i18n';
import { useAuthStore } from '@/stores/authStore';
import type { User } from '@/types';

// Component tests assert on English strings; the language detector must
// not pick the developer's locale.
void i18n.changeLanguage('en');

afterEach(() => {
  cleanup();
  useAuthStore.setState({ accessToken: null, user: null });
});

/**
 * renderWithProviders wraps a component in the app's real providers
 * (router, TanStack Query with retries off so errors surface
 * immediately). The API layer is NOT provided here: tests mock
 * `@/lib/api` per file — the point where every request funnels through.
 */
export function renderWithProviders(ui: ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </MemoryRouter>,
  );
}

/** signIn seeds the auth store the way a real login does. */
export function signIn(user: Partial<User> & { username: string }) {
  useAuthStore.setState({
    accessToken: 'test-token',
    user: {
      id: 'u1',
      role: 'user',
      ...user,
    } as User,
  });
}
