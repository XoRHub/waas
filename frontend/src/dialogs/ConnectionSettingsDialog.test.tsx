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
  // The Workspace tab resolves the template (rights + sizing) exactly
  // like the creation dialog: template ∩ policy, webhook-mirrored.
  '/api/v1/workspace-templates': [
    { name: 'xfce', displayName: 'XFCE', os: 'linux', allowedOverrides: ['env'] },
  ],
  '/api/v1/catalog': [],
  '/api/v1/me/quota': {},
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
  runtime: { env: [{ name: 'HTTP_PROXY', value: 'http://proxy:3128' }] },
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

describe('ConnectionSettingsDialog — Workspace tab', () => {
  it('gates each group on the template ∩ policy rights and PATCHes only the changed fields', async () => {
    apiMock.api.patch.mockClear();
    signIn({ username: 'marc' });
    renderWithProviders(<ConnectionSettingsDialog workspace={workspace} onClose={() => {}} />);

    await userEvent.click(screen.getByRole('button', { name: 'Workspace' }));

    // env is allowed by the template: its tab exists and is editable,
    // seeded with the current override. The right-less groups with
    // nothing stored get NO tab at all.
    const addVar = await screen.findByRole('button', { name: /Add variable/ });
    expect(screen.getByDisplayValue('HTTP_PROXY')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Node placement' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Metadata' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Schedule' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Resources' })).toBeNull();

    await userEvent.click(addVar);
    const names = screen.getAllByPlaceholderText('NAME');
    await userEvent.type(names[names.length - 1], 'FOO');
    const values = screen.getAllByPlaceholderText('value');
    await userEvent.type(values[values.length - 1], 'bar');
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));

    await waitFor(() => expect(apiMock.api.patch).toHaveBeenCalled());
    const [path, body] = apiMock.api.patch.mock.calls.at(-1) as unknown as [
      string,
      { env?: { name: string; value: string }[]; nodeSelector?: unknown; resources?: unknown },
    ];
    expect(path).toBe('/api/v1/workspaces/w1/overrides');
    // The PROVIDED field replaces the override wholesale (current rows +
    // the new one); untouched groups must stay absent from the payload.
    expect(body.env).toEqual([
      { name: 'HTTP_PROXY', value: 'http://proxy:3128' },
      { name: 'FOO', value: 'bar' },
    ]);
    expect(body).not.toHaveProperty('nodeSelector');
    expect(body).not.toHaveProperty('tolerations');
    expect(body).not.toHaveProperty('resources');
  });

  it('closes without a request when nothing changed', async () => {
    apiMock.api.patch.mockClear();
    signIn({ username: 'marc' });
    const onClose = vi.fn();
    renderWithProviders(<ConnectionSettingsDialog workspace={workspace} onClose={onClose} />);

    await userEvent.click(screen.getByRole('button', { name: 'Workspace' }));
    await screen.findByRole('button', { name: /Add variable/ });
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));

    expect(onClose).toHaveBeenCalled();
    expect(apiMock.api.patch).not.toHaveBeenCalled();
  });
});
