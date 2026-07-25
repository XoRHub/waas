import { useEffect, useRef } from 'react';
import { createBrowserRouter, createRoutesFromElements, Route, RouterProvider } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { ApiError, api } from '@/lib/api';
import { applyTheme, storedTheme, watchSystemTheme } from '@/lib/theme';
import { useAuthStore } from '@/stores/authStore';
import type { Theme, User } from '@/types';
import { LoginPage } from '@/pages/LoginPage';
import { AuthCallbackPage } from '@/pages/AuthCallbackPage';
import { PortalPage } from '@/pages/PortalPage';
import { ConnectPage } from '@/pages/ConnectPage';
import { SplitViewPage } from '@/pages/SplitViewPage';
import { ProfilePage } from '@/pages/ProfilePage';
import { AdminLayout } from '@/pages/admin/AdminLayout';
import { FleetPage } from '@/pages/admin/FleetPage';
import { TemplatesPage } from '@/pages/admin/TemplatesPage';
import { UsersPage } from '@/pages/admin/UsersPage';
import { GovernancePage } from '@/pages/admin/GovernancePage';
import { AuditPage } from '@/pages/admin/AuditPage';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
});

// The profile preference wins once signed in; localStorage covers the
// login page and the first paint.
function useTheme() {
  const prefTheme = useAuthStore((s) => s.user?.preferences?.theme);
  const theme: Theme =
    prefTheme === 'light' || prefTheme === 'dark'
      ? prefTheme
      : prefTheme === ''
        ? 'system'
        : storedTheme();
  useEffect(() => {
    applyTheme(theme);
    return watchSystemTheme(() => theme);
  }, [theme]);
}

// The session lives in an httpOnly cookie this code cannot read, so the
// only way to know whether one is active is to ask. One probe at boot,
// before any protected route gets to decide on a redirect — and it is
// also what restores the profile after a page reload, now that nothing
// is persisted client-side.
function useSessionBoot() {
  const probed = useRef(false);
  useEffect(() => {
    if (probed.current) return; // StrictMode double-invoke guard
    probed.current = true;
    // The SSO landing runs this exact request itself and owns what
    // happens next; probing here too would double the lookup and let the
    // two writers decide the sign-in state by whichever settles last.
    if (window.location.pathname === '/auth/callback') return;

    api
      .get<User>('/api/v1/auth/me')
      .then(({ data }) => useAuthStore.getState().setUser(data))
      .catch((error: unknown) => {
        // A sign-in can complete while this is in flight (the form beats
        // a cold server): that session is the current one, and this
        // answer is about the absence that preceded it.
        if (useAuthStore.getState().user) return;
        // Only a 401 means "no session". Anything else — 503 during a
        // database failover, a rolling restart, a proxy blip — means the
        // server could not tell us, and presenting that as signed out
        // throws away the whole point of it answering 503 rather than 401.
        useAuthStore
          .getState()
          .clearLocal(
            error instanceof ApiError && error.problem.status === 401
              ? 'no-session'
              : 'unavailable',
          );
      });
  }, []);
}

// Data router (not declarative <BrowserRouter>): the viewTransition
// navigate option only works through RouterProvider, which is what
// animates opening a workspace. Module scope — created once, never
// per App render.
const router = createBrowserRouter(
  createRoutesFromElements(
    <>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/auth/callback" element={<AuthCallbackPage />} />
      <Route element={<ProtectedRoute />}>
        <Route path="/" element={<PortalPage />} />
        <Route path="/profile" element={<ProfilePage />} />
        <Route path="/workspaces/:id/connect" element={<ConnectPage />} />
        <Route path="/remote/:id/connect" element={<ConnectPage kind="remote" />} />
        <Route path="/view" element={<SplitViewPage />} />
      </Route>
      <Route element={<ProtectedRoute adminOnly />}>
        <Route path="/admin" element={<AdminLayout />}>
          <Route index element={<FleetPage />} />
          <Route path="templates" element={<TemplatesPage />} />
          <Route path="users" element={<UsersPage />} />
          <Route path="governance" element={<GovernancePage />} />
          <Route path="audit" element={<AuditPage />} />
        </Route>
      </Route>
    </>,
  ),
);

export function App() {
  useTheme();
  useSessionBoot();
  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  );
}
