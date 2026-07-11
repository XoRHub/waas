// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { RemoteWorkspace, RemoteWorkspaceInput } from '@/types';
import { RemoteWorkspaceDialog } from './RemoteWorkspaceDialog';

const apiMock = createApiMock({
  '/api/v1/meta/protocols': [
    { name: 'ssh', params: [] },
    { name: 'vnc', params: [] },
    { name: 'rdp', params: [] },
  ],
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

describe('RemoteWorkspaceDialog', () => {
  beforeEach(() => {
    // The module-level mock survives across tests; drop recorded calls
    // (implementations stay).
    vi.clearAllMocks();
  });

  it('registers a machine: ssh:22 default protocol, empty credentials omitted', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<RemoteWorkspaceDialog remote={null} onClose={() => {}} />);

    await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'lab box');
    await userEvent.type(screen.getByRole('textbox', { name: 'Host or IP' }), '10.0.0.5');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const [path, input] = apiMock.api.post.mock.calls[0] as unknown as [
      string,
      RemoteWorkspaceInput,
    ];
    expect(path).toBe('/api/v1/remote-workspaces');
    expect(input.name).toBe('lab box');
    expect(input.hostname).toBe('10.0.0.5');
    expect(input.protocols).toEqual([{ name: 'ssh', port: 22, default: true, params: undefined }]);
    // Write-only credentials: nothing typed = nothing sent (server keeps
    // or stays without credentials — never an empty overwrite).
    expect(input.credentials).toBeUndefined();
    expect(input.macAddress).toBeUndefined();
  });

  it('adding a protocol seeds its well-known port and editing goes through PUT', async () => {
    signIn({ username: 'marc' });
    const remote = {
      id: 'r1',
      name: 'lab',
      hostname: '10.0.0.5',
      protocol: 'ssh',
      port: 22,
      protocols: [{ name: 'ssh', port: 22, default: true }],
    } as RemoteWorkspace;
    renderWithProviders(<RemoteWorkspaceDialog remote={remote} onClose={() => {}} />);

    // The adder proposes the unused protocols; picking vnc must
    // seed 5900, not 0.
    await userEvent.click(screen.getByRole('button', { name: /Add a protocol/ }));
    await userEvent.click(screen.getByRole('button', { name: /vnc/i }));
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(apiMock.api.put).toHaveBeenCalled());
    const [path, input] = apiMock.api.put.mock.calls[0] as unknown as [
      string,
      RemoteWorkspaceInput,
    ];
    expect(path).toBe('/api/v1/remote-workspaces/r1');
    expect(input.protocols).toContainEqual({ name: 'vnc', port: 5900, default: false });
    // ssh keeps the default flag: adding never steals it.
    expect(input.protocols?.find((p) => p.name === 'ssh')?.default).toBe(true);
  });

  it('keeps the last endpoint unremovable (a machine without any is unreachable)', () => {
    signIn({ username: 'marc' });
    renderWithProviders(<RemoteWorkspaceDialog remote={null} onClose={() => {}} />);

    // Unlike the template editor (where zero protocols falls back to
    // the legacy OS-derived entry), a remote machine needs at least
    // one endpoint: the ✕ of the only tab stays disabled.
    const remove = screen.getByTitle('The last protocol cannot be removed');
    expect(remove).toBeDisabled();
  });

  it('typed credentials are sent, blank ones filtered (edit = keep stored)', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<RemoteWorkspaceDialog remote={null} onClose={() => {}} />);

    await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'lab');
    await userEvent.type(screen.getByRole('textbox', { name: 'Host or IP' }), 'h');
    await userEvent.type(screen.getByRole('textbox', { name: 'Username' }), 'root');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const input = (apiMock.api.post.mock.calls[0] as unknown as [string, RemoteWorkspaceInput])[1];
    expect(input.credentials).toEqual({ username: 'root' });
  });
});
