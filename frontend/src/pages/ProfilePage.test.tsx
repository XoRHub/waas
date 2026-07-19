// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import { useAuthStore } from '@/stores/authStore';
import { ProfilePage } from './ProfilePage';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

// theme.ts calls window.matchMedia at module load; jsdom has no matchMedia.
vi.mock('@/lib/theme', () => ({
  applyTheme: vi.fn(),
  storedTheme: vi.fn(() => 'system'),
  watchSystemTheme: vi.fn(() => () => {}),
}));

describe('ProfilePage SSO account lockdown', () => {
  beforeEach(() => {
    apiMock.api.patch.mockClear();
    // The hook writes the PATCH response back into the auth store — echo
    // the signed-in user so downstream renders keep a real one.
    apiMock.api.patch.mockImplementation(() =>
      Promise.resolve({ data: useAuthStore.getState().user }),
    );
  });

  it('disables identity and password editing for an SSO account', () => {
    signIn({ username: 'alice', sso: true, preferences: {} });
    renderWithProviders(<ProfilePage />);

    expect(screen.getByLabelText('Display name')).toBeDisabled();
    expect(screen.getByLabelText('Email')).toBeDisabled();
    expect(screen.getByLabelText('Current password')).toBeDisabled();
    expect(screen.getByLabelText('New password')).toBeDisabled();
    expect(screen.getByLabelText('Confirm new password')).toBeDisabled();
    for (const save of screen.getAllByRole('button', { name: 'Save' })) {
      expect(save).toBeDisabled();
    }
    expect(
      screen.getByText(
        'Your identity is managed by your identity provider and cannot be edited here.',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        'You sign in through your identity provider — this account has no local password.',
      ),
    ).toBeInTheDocument();
  });

  it('keeps preferences editable for an SSO account', async () => {
    signIn({ username: 'alice', sso: true, preferences: {} });
    renderWithProviders(<ProfilePage />);

    const theme = screen.getByLabelText('Theme');
    expect(theme).toBeEnabled();
    fireEvent.change(theme, { target: { value: 'dark' } });
    await waitFor(() =>
      expect(apiMock.api.patch).toHaveBeenCalledWith('/api/v1/me', {
        preferences: { theme: 'dark' },
      }),
    );
  });

  it('keeps everything editable for a local account', async () => {
    signIn({ username: 'bob', sso: false, preferences: {} });
    renderWithProviders(<ProfilePage />);

    const displayName = screen.getByLabelText('Display name');
    expect(displayName).toBeEnabled();
    expect(screen.getByLabelText('Email')).toBeEnabled();
    expect(screen.getByLabelText('Current password')).toBeEnabled();
    expect(
      screen.getByText(
        'These fields will be managed by your identity provider once SSO is enabled.',
      ),
    ).toBeInTheDocument();

    fireEvent.change(displayName, { target: { value: 'Bob' } });
    fireEvent.submit(displayName.closest('form')!);
    await waitFor(() =>
      expect(apiMock.api.patch).toHaveBeenCalledWith('/api/v1/me', {
        displayName: 'Bob',
        email: '',
      }),
    );
  });
});
