// @vitest-environment jsdom
import { createRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import { DesktopPane, type DesktopPaneHandle } from '@/components/DesktopPane';
import type { SessionCapabilities, Workspace } from '@/types';
import { SessionOverlay } from './SessionOverlay';

const apiMock = createApiMock({
  '/api/v1/meta/protocols': [],
  '/api/v1/workspaces/w1/kasmvnc-config': {
    config: 'desktop:\n  resolution:\n    width: 1280\n',
  },
});
vi.mock('@/lib/api', async (importOriginal) => ({
  // Keep the real exports (DesktopPane does `e instanceof ApiError`) and
  // only substitute the api client.
  ...(await importOriginal<typeof import('@/lib/api')>()),
  get api() {
    return apiMock.api;
  },
}));

const workspace = (protocol: string): Workspace => ({
  id: 'w1',
  name: 'w1',
  templateRef: 'xfce',
  ownerId: 'u1',
  phase: 'Running',
  paused: false,
  createdAt: '2026-07-10T00:00:00Z',
  protocol,
  protocols: [{ name: protocol, default: true }],
});

const renderOverlay = async (
  protocol: string,
  capabilities: SessionCapabilities = {
    clipboardCopy: true,
    clipboardPaste: true,
    fileUpload: false,
    fileDownload: false,
  },
) => {
  signIn({ username: 'marc' });
  renderWithProviders(
    <SessionOverlay
      workspace={workspace(protocol)}
      capabilities={capabilities}
      pane={createRef<DesktopPaneHandle>()}
    />,
  );
  await userEvent.click(screen.getByTitle(/session menu/i));
};

describe('SessionOverlay clipboard section by protocol', () => {
  it.each(['vnc', 'rdp', 'ssh'])('renders the clipboard controls on %s', async (protocol) => {
    await renderOverlay(protocol);

    expect(screen.getByText('Copy from workspace')).toBeInTheDocument();
    expect(screen.getByText('Paste to workspace')).toBeInTheDocument();
    expect(screen.getByText('Manual exchange')).toBeInTheDocument();
  });

  it('shows the enforced clipboard state read-only on kasmvnc', async () => {
    // The kasm path bypasses the guac tunnel: the clipboard is enforced
    // in the container from the policy, so the overlay reflects that state
    // read-only (no live toggle), reading straight from capabilities. The
    // manual exchange works there too (through the client's messages),
    // gated by the same capabilities.
    await renderOverlay('kasmvnc', {
      clipboardCopy: true,
      clipboardPaste: false,
      fileUpload: false,
      fileDownload: false,
    });

    // Labels shown, but as status — not interactive checkboxes.
    expect(screen.getByText('Copy from workspace')).toBeInTheDocument();
    expect(screen.getByText('Paste to workspace')).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(screen.getByText('Manual exchange')).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/Text to send/)).toBeDisabled();

    // Truthful per-direction status from capabilities.
    expect(screen.getByText('Allowed')).toBeInTheDocument();
    expect(screen.getByText('Denied by your policy')).toBeInTheDocument();
    expect(screen.getByText(/Native KasmVNC clipboard/i)).toBeInTheDocument();
  });

  it('names the denying gate per direction: own setting vs policy', async () => {
    // Copy is blocked by the user's own disable-copy connection setting
    // (lock=params, undoable), paste by the admin policy (lock=policy).
    // Labeling both "denied by your policy" was the bug: the user's own
    // setting must not read as an admin restriction.
    await renderOverlay('vnc', {
      clipboardCopy: false,
      clipboardPaste: false,
      clipboardCopyLock: 'params',
      clipboardPasteLock: 'policy',
      fileUpload: false,
      fileDownload: false,
    });

    expect(screen.getByTitle(/Disabled by your connection settings/)).toBeInTheDocument();
    expect(screen.getByTitle('Denied by your policy')).toBeInTheDocument();
  });
});

describe('SessionOverlay protocol quick switch', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    // mockReset restores the vi.fn defaults set in createApiMock.
    apiMock.api.post.mockReset();
    apiMock.api.patch.mockReset();
  });

  // Full chain behind the overlay's protocol buttons, with the REAL
  // DesktopPane: click VNC → window.confirm gate → the choice is PATCHed
  // to the profile (workspaceSettings[id].protocol) → setUser re-runs the
  // pane's connection effect → the reconnect POST carries the protocol
  // the user clicked. A field report claimed the button reconnected with
  // the OLD protocol; live reproduction showed the automation had
  // auto-dismissed the confirm dialog — this pins the real behavior.
  it('clicking VNC stores the preference and reconnects with protocol vnc', async () => {
    // Stop the pane at the connect POST (the assertion target) so the
    // test never reaches the Guacamole tunnel.
    apiMock.api.post.mockImplementation(() => Promise.reject(new Error('halt at connect')));
    // PATCH /me answers like the server: the updated user, preferences
    // included — what useUpdateProfile feeds back into the auth store.
    apiMock.api.patch.mockImplementation((_path, input) =>
      Promise.resolve({
        data: {
          id: 'u1',
          username: 'marc',
          role: 'user',
          preferences: (input as { preferences?: unknown }).preferences,
        },
      }),
    );
    vi.spyOn(window, 'confirm').mockReturnValue(true);

    signIn({ username: 'marc' });
    const ws: Workspace = {
      ...workspace('ssh'),
      protocols: [{ name: 'ssh', default: true }, { name: 'vnc' }],
    };
    const pane = createRef<DesktopPaneHandle>();
    renderWithProviders(
      <>
        <DesktopPane ref={pane} workspaceId="w1" />
        <SessionOverlay
          workspace={ws}
          capabilities={{
            clipboardCopy: true,
            clipboardPaste: true,
            fileUpload: false,
            fileDownload: false,
          }}
          pane={pane}
        />
      </>,
    );

    // Initial connect: no stored preference — no body, server default.
    await waitFor(() =>
      expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/connect', undefined),
    );

    await userEvent.click(screen.getByTitle(/session menu/i));
    await userEvent.click(screen.getByRole('button', { name: /^vnc$/i }));

    // The switch is confirm-gated (an unhandled dialog in automation
    // cancels it — the false-alarm bug report).
    expect(window.confirm).toHaveBeenCalledOnce();
    await waitFor(() =>
      expect(apiMock.api.patch).toHaveBeenCalledWith('/api/v1/me', {
        preferences: expect.objectContaining({
          workspaceSettings: { w1: expect.objectContaining({ protocol: 'vnc' }) },
        }),
      }),
    );
    // The reconnect carries the protocol that was clicked.
    await waitFor(() =>
      expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/connect', {
        protocol: 'vnc',
      }),
    );
    // And the overlay now marks VNC active (bg-blue-600 = selected).
    expect(screen.getByRole('button', { name: /^vnc$/i }).className).toContain('bg-blue-600');
  });

  it('a dismissed confirm switches nothing', async () => {
    apiMock.api.post.mockImplementation(() => Promise.reject(new Error('halt at connect')));
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    signIn({ username: 'marc' });
    const ws: Workspace = {
      ...workspace('ssh'),
      protocols: [{ name: 'ssh', default: true }, { name: 'vnc' }],
    };
    const pane = createRef<DesktopPaneHandle>();
    renderWithProviders(
      <>
        <DesktopPane ref={pane} workspaceId="w1" />
        <SessionOverlay
          workspace={ws}
          capabilities={{
            clipboardCopy: true,
            clipboardPaste: true,
            fileUpload: false,
            fileDownload: false,
          }}
          pane={pane}
        />
      </>,
    );
    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalledOnce());

    await userEvent.click(screen.getByTitle(/session menu/i));
    await userEvent.click(screen.getByRole('button', { name: /^vnc$/i }));

    expect(window.confirm).toHaveBeenCalledOnce();
    expect(apiMock.api.patch).not.toHaveBeenCalled();
    // No preference change — the pane never reconnects.
    expect(apiMock.api.post).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: /^ssh$/i }).className).toContain('bg-blue-600');
  });
});

describe('SessionOverlay reconnect params', () => {
  afterEach(() => {
    apiMock.route('/api/v1/meta/protocols', []);
  });

  // The overlay and the connection-settings dialog read the same profile
  // values, but the dialog also surfaces the template-locked value as the
  // "Default (X)" segment (ProtocolParamsForm placeholders). The overlay
  // used to render the raw registry default instead, so a template with
  // enable-audio=true read "Default (true)" in the dialog and
  // "Default (false)" in the session menu — the same param, two stories.
  it('shows a template-locked value as the inherited default, like the dialog', async () => {
    apiMock.route('/api/v1/meta/protocols', [
      {
        name: 'vnc',
        params: [
          {
            name: 'enable-audio',
            protocols: ['vnc'],
            kind: 'bool',
            default: 'false',
            tier: 'ui',
            category: 'audio',
            live: false,
            description: 'Stream audio from the workspace.',
          },
        ],
      },
    ]);
    signIn({ username: 'marc' });
    const ws: Workspace = {
      ...workspace('vnc'),
      protocols: [
        {
          name: 'vnc',
          default: true,
          params: { 'enable-audio': 'true' },
          resolvedUserParams: ['enable-audio'],
        },
      ],
    };
    renderWithProviders(
      <SessionOverlay
        workspace={ws}
        capabilities={{
          clipboardCopy: true,
          clipboardPaste: true,
          fileUpload: false,
          fileDownload: false,
        }}
        pane={createRef<DesktopPaneHandle>()}
      />,
    );
    await userEvent.click(screen.getByTitle(/session menu/i));

    // No profile override ("" = inherit): the selected segment must carry
    // the template's value, not the registry's.
    const inherit = await screen.findByRole('button', { name: 'Default (true)' });
    expect(inherit).toHaveAttribute('aria-pressed', 'true');
    expect(screen.queryByRole('button', { name: 'Default (false)' })).toBeNull();
  });
});

describe('SessionOverlay kasmvnc effective config', () => {
  it('shows the operator-materialized config read-only on kasmvnc', async () => {
    // The effective kasmvnc.yaml (template + policy layer) fetched from
    // the workspace's kasmvnc-config endpoint — informational display,
    // nothing editable.
    await renderOverlay('kasmvnc');

    expect(
      screen.getByText('KasmVNC configuration (managed by the administrator)'),
    ).toBeInTheDocument();
    expect(await screen.findByText(/width: 1280/)).toBeInTheDocument();
    expect(screen.getByText(/actually applied to this workspace/)).toBeInTheDocument();
  });

  it('never fetches nor renders the config section on guacd protocols', async () => {
    // Call history accumulates across tests (module-scope mock): reset it
    // so the negative assertion only sees this render's requests.
    apiMock.api.get.mockClear();
    await renderOverlay('vnc');

    expect(screen.queryByText('KasmVNC configuration (managed by the administrator)')).toBeNull();
    expect(apiMock.api.get).not.toHaveBeenCalledWith('/api/v1/workspaces/w1/kasmvnc-config');
  });
});

describe('SessionOverlay kasmvnc image quality', () => {
  it('shows the pane quality and applies a preset live', async () => {
    const setQuality = vi.fn();
    const pane = {
      current: {
        readQuality: () => 2,
        setQuality,
        readRemoteClipboard: () => '',
        readSessionId: () => 's1',
      },
    } as unknown as React.RefObject<DesktopPaneHandle>;
    signIn({ username: 'marc' });
    renderWithProviders(
      <SessionOverlay
        workspace={workspace('kasmvnc')}
        capabilities={{
          clipboardCopy: true,
          clipboardPaste: true,
          fileUpload: false,
          fileDownload: false,
        }}
        pane={pane}
      />,
    );
    await userEvent.click(screen.getByTitle(/session menu/i));

    expect(screen.getByRole('button', { name: 'Medium' }).className).toContain('bg-blue-600');
    await userEvent.click(screen.getByRole('button', { name: 'High' }));

    expect(setQuality).toHaveBeenCalledWith(3);
    expect(screen.getByRole('button', { name: 'High' }).className).toContain('bg-blue-600');
  });

  it('has no quality control on guacd protocols', async () => {
    await renderOverlay('vnc');

    expect(screen.queryByText('Image quality')).toBeNull();
  });
});

describe('SessionOverlay kasmvnc files', () => {
  const kasmPane = {
    current: {
      readQuality: () => 2,
      setQuality: vi.fn(),
      readRemoteClipboard: () => '',
      readSessionId: () => 's1',
    },
  } as unknown as React.RefObject<DesktopPaneHandle>;

  afterEach(() => vi.unstubAllGlobals());

  it('shows the Files section on kasmvnc when the policy grants a direction', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))),
    );
    signIn({ username: 'marc' });
    renderWithProviders(
      <SessionOverlay
        workspace={workspace('kasmvnc')}
        capabilities={{
          clipboardCopy: true,
          clipboardPaste: true,
          fileUpload: false,
          fileDownload: true,
        }}
        pane={kasmPane}
      />,
    );
    await userEvent.click(screen.getByTitle(/session menu/i));

    expect(screen.getByText('Files')).toBeInTheDocument();
    expect(await screen.findByText('Empty folder')).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith('/kasm/s1/waas/files?path=', expect.anything());
  });

  it('hides it entirely without any grant', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    signIn({ username: 'marc' });
    renderWithProviders(
      <SessionOverlay
        workspace={workspace('kasmvnc')}
        capabilities={{
          clipboardCopy: true,
          clipboardPaste: true,
          fileUpload: false,
          fileDownload: false,
        }}
        pane={kasmPane}
      />,
    );
    await userEvent.click(screen.getByTitle(/session menu/i));
    expect(screen.queryByText('Files')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('never shows it on guacd protocols, grant or not', async () => {
    await renderOverlay('vnc', {
      clipboardCopy: true,
      clipboardPaste: true,
      fileUpload: true,
      fileDownload: true,
    });
    expect(screen.queryByText('Files')).toBeNull();
  });
});
