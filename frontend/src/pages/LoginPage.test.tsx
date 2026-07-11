// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { AuthProviders } from '@/types';
import { LoginPage } from './LoginPage';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

function providers(overrides: Partial<AuthProviders>): AuthProviders {
  return {
    local: true,
    oidc: { enabled: true, name: 'Authentik', startUrl: '/api/v1/auth/oidc/start' },
    ...overrides,
  };
}

describe('LoginPage local-login toggle', () => {
  it('hides the whole username/password form when local is disabled', async () => {
    apiMock.route('/api/v1/auth/providers', providers({ local: false }));
    const { container } = renderWithProviders(<LoginPage />);

    await screen.findByRole('button', { name: 'Sign in with Authentik' });
    expect(container.querySelector('form')).toBeNull();
    expect(screen.queryByLabelText('Username')).toBeNull();
    expect(screen.queryByLabelText('Password')).toBeNull();
    // The "or" separator makes no sense without a form next to it.
    expect(screen.queryByText('or')).toBeNull();
  });

  it('keeps the current behavior when local login is enabled', async () => {
    apiMock.route('/api/v1/auth/providers', providers({ local: true }));
    const { container } = renderWithProviders(<LoginPage />);

    await screen.findByRole('button', { name: 'Sign in with Authentik' });
    expect(container.querySelector('form')).not.toBeNull();
    expect(screen.getByLabelText('Username')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByText('or')).toBeInTheDocument();
  });

  it('renders no form while the providers request is in flight (no flash)', () => {
    apiMock.api.get.mockReturnValueOnce(new Promise(() => {}));
    const { container } = renderWithProviders(<LoginPage />);

    expect(container.querySelector('form')).toBeNull();
    expect(screen.queryByLabelText('Username')).toBeNull();
  });

  it('shows an error state with retry when the providers request fails', async () => {
    // Never fall back to the local form: providers and login share the
    // same api-server, so a blind form would fail too — and on an
    // OIDC-only deployment its 404 would read as "wrong credentials".
    apiMock.api.get.mockRejectedValueOnce(new Error('providers down'));
    const { container } = renderWithProviders(<LoginPage />);

    expect(await screen.findByRole('button', { name: 'Retry' })).toBeInTheDocument();
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    expect(container.querySelector('form')).toBeNull();
    expect(screen.queryByLabelText('Username')).toBeNull();
  });
});
