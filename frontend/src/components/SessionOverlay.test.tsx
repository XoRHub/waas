// @vitest-environment jsdom
import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { DesktopPaneHandle } from '@/components/DesktopPane';
import type { SessionCapabilities, Workspace } from '@/types';
import { SessionOverlay } from './SessionOverlay';

const apiMock = createApiMock({
  '/api/v1/meta/protocols': [],
  '/api/v1/workspaces/w1/kasmvnc-config': {
    config: 'desktop:\n  resolution:\n    width: 1280\n',
  },
});
vi.mock('@/lib/api', () => ({
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
  capabilities: SessionCapabilities = { clipboardCopy: true, clipboardPaste: true },
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
    // read-only (no live toggle, no manual exchange), reading straight
    // from capabilities.
    await renderOverlay('kasmvnc', { clipboardCopy: true, clipboardPaste: false });

    // Labels shown, but as status — not interactive checkboxes.
    expect(screen.getByText('Copy from workspace')).toBeInTheDocument();
    expect(screen.getByText('Paste to workspace')).toBeInTheDocument();
    expect(screen.queryByText('Manual exchange')).toBeNull();
    expect(screen.queryByRole('checkbox')).toBeNull();

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
    });

    expect(screen.getByTitle(/Disabled by your connection settings/)).toBeInTheDocument();
    expect(screen.getByTitle('Denied by your policy')).toBeInTheDocument();
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
