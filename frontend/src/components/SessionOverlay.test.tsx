// @vitest-environment jsdom
import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { DesktopPaneHandle } from '@/components/DesktopPane';
import type { Workspace } from '@/types';
import { SessionOverlay } from './SessionOverlay';

const apiMock = createApiMock({ '/api/v1/meta/protocols': [] });
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
  capabilities = { clipboardCopy: true, clipboardPaste: true },
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
});
