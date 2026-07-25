// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import type { User } from '@/types';
import { useAuthStore } from '@/stores/authStore';
import { ProtectedRoute } from './ProtectedRoute';

const marc = { id: 'u1', username: 'marc', role: 'user' } as User;
const root = { id: 'u2', username: 'root', role: 'admin' } as User;

afterEach(() => {
  cleanup();
  useAuthStore.setState({ user: null, ready: false, signedOutReason: null });
});

function renderAt(path: string, adminOnly = false) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route element={<ProtectedRoute adminOnly={adminOnly} />}>
          <Route path="/" element={<p>portal</p>} />
          <Route path="/admin" element={<p>admin</p>} />
        </Route>
        <Route path="/login" element={<p>login</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ProtectedRoute', () => {
  // The session is an httpOnly cookie the app cannot read, so a page load
  // starts out not knowing whether it is signed in. Redirecting during
  // that window would bounce every valid session to the login page on
  // every reload — the one regression this whole change could introduce.
  it('waits for the boot probe instead of redirecting', () => {
    renderAt('/');
    expect(screen.queryByText('login')).toBeNull();
    expect(screen.queryByText('portal')).toBeNull();
  });

  it('renders the route once the probe found a session', () => {
    useAuthStore.setState({ user: marc, ready: true });
    renderAt('/');
    expect(screen.getByText('portal')).toBeTruthy();
  });

  it('redirects to the login page once the probe came back empty', () => {
    useAuthStore.setState({ user: null, ready: true, signedOutReason: 'no-session' });
    renderAt('/');
    expect(screen.getByText('login')).toBeTruthy();
  });

  // A probe that could not get a verdict is not a signed-out user. Sending
  // them to the login page during a database failover or a rolling restart
  // discards a cookie that is probably still valid, and asks them to fix it
  // by re-authenticating — which is not what is broken.
  it('offers a retry instead of the login page when the server could not answer', () => {
    useAuthStore.setState({ user: null, ready: true, signedOutReason: 'unavailable' });
    renderAt('/');
    expect(screen.queryByText('login')).toBeNull();
    expect(screen.getByRole('button')).toBeTruthy();
  });

  it('keeps a non-admin out of the admin console', () => {
    useAuthStore.setState({ user: marc, ready: true });
    renderAt('/admin', true);
    expect(screen.queryByText('admin')).toBeNull();
  });

  it('lets an admin through', () => {
    useAuthStore.setState({ user: root, ready: true });
    renderAt('/admin', true);
    expect(screen.getByText('admin')).toBeTruthy();
  });
});
