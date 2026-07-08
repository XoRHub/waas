// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { Workspace } from '@/types';
import { ConnectionSettingsDialog } from './ConnectionSettingsDialog';

const apiMock = createApiMock({
  '/api/v1/meta/protocols': [
    { name: 'vnc', params: [] },
    { name: 'ssh', params: [] },
  ],
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const workspace = {
  id: 'w1',
  name: 'marc',
  displayName: 'Marc box',
  templateRef: 'xfce',
  phase: 'Running',
  protocols: [
    { name: 'vnc', port: 5901, default: true },
    { name: 'ssh', port: 22 },
  ],
} as Workspace;

describe('ConnectionSettingsDialog', () => {
  it('choosing a non-default protocol saves it under the workspace id in the profile', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<ConnectionSettingsDialog workspace={workspace} onClose={() => {}} />);

    // Switch to the ssh tab and mark it as the connection choice.
    await userEvent.click(screen.getByRole('button', { name: /ssh/i }));
    await userEvent.click(screen.getByRole('radio', { name: /Connect with this protocol/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(apiMock.api.patch).toHaveBeenCalled());
    const [path, body] = apiMock.api.patch.mock.calls[0] as unknown as [
      string,
      { preferences: { workspaceSettings: Record<string, { protocol?: string }> } },
    ];
    expect(path).toBe('/api/v1/me');
    expect(body.preferences.workspaceSettings.w1.protocol).toBe('ssh');
  });

  it('keeping the default protocol with no params removes the per-workspace entry', async () => {
    signIn({
      username: 'marc',
      preferences: { workspaceSettings: { w1: { protocol: 'ssh' } } },
    });
    renderWithProviders(<ConnectionSettingsDialog workspace={workspace} onClose={() => {}} />);

    // Back to the default protocol: the stored override must be dropped,
    // not overwritten with a no-op entry.
    await userEvent.click(screen.getByRole('button', { name: /vnc/i }));
    await userEvent.click(screen.getByRole('radio', { name: /Connect with this protocol/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(apiMock.api.patch).toHaveBeenCalled());
    const lastCall = apiMock.api.patch.mock.calls.at(-1) as unknown as [
      string,
      { preferences: { workspaceSettings: Record<string, unknown> } },
    ];
    const body = lastCall[1];
    expect(body.preferences.workspaceSettings).not.toHaveProperty('w1');
  });
});
