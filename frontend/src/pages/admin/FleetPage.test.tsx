// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { RemoteWorkspaceAdmin, RetainedVolume, Workspace } from '@/types';
import { FleetPage } from './FleetPage';

const ws = (over: Partial<Workspace> & { id: string; name: string; ownerId: string }): Workspace =>
  ({
    templateRef: 'xfce',
    phase: 'Running',
    paused: false,
    createdAt: '2026-07-10T00:00:00Z',
    ...over,
  }) as Workspace;

const fleet = [
  ws({ id: 'w-mine', name: 'admin-desk', ownerId: 'admin1', ownerUsername: 'boss' }),
  ws({ id: 'w-a1', name: 'alice-one', ownerId: 'u-alice', ownerUsername: 'alice' }),
  ws({ id: 'w-a2', name: 'alice-two', ownerId: 'u-alice', ownerUsername: 'alice' }),
  // Owner deleted since: no ownerUsername — the group header falls back
  // to the raw id.
  ws({ id: 'w-ghost', name: 'orphan', ownerId: 'ghost-uuid' }),
];

const remotes: RemoteWorkspaceAdmin[] = [
  {
    id: 'r1',
    ownerId: 'u-alice',
    ownerUsername: 'alice',
    name: 'alice-lab',
    hostname: '10.0.0.5',
    port: 22,
    protocol: 'ssh',
    hasCredentials: false,
    activeNow: false,
    createdAt: '2026-07-10T00:00:00Z',
  },
  {
    id: 'r2',
    ownerId: 'ghost-uuid',
    name: 'orphan-remote',
    hostname: '10.0.0.6',
    port: 3389,
    protocol: 'rdp',
    hasCredentials: true,
    activeNow: false,
    createdAt: '2026-07-10T00:00:00Z',
  },
];

const volumes: RetainedVolume[] = [
  {
    name: 'home-alice',
    namespace: 'waas-alice',
    size: '10Gi',
    ownerId: 'u-alice',
    ownerUsername: 'alice',
  },
  { name: 'home-orphan', namespace: 'waas-ghost', size: '5Gi', ownerId: 'ghost-uuid' },
];

const apiMock = createApiMock({
  // The fleet reads the ADMIN route: /api/v1/workspaces only ever
  // returns the caller's own rows now, whatever the role.
  '/api/v1/admin/workspaces': fleet,
  '/api/v1/admin/remote-workspaces': remotes,
  '/api/v1/admin/volumes': volumes,
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const signInAdmin = () => signIn({ id: 'admin1', username: 'boss', role: 'admin' });

describe('admin fleet grouped by owner', () => {
  it('groups every workspace by username (admin included), id as fallback', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    // The admin's own workspaces form an ordinary group like any other.
    const bossGroup = (await screen.findByText('boss')).closest('details')!;
    expect(within(bossGroup).getByText('admin-desk')).toBeInTheDocument();

    const aliceGroup = screen.getByText('alice').closest('details')!;
    expect(within(aliceGroup).getByText('alice-one')).toBeInTheDocument();
    expect(within(aliceGroup).getByText('alice-two')).toBeInTheDocument();

    // Deleted owner: raw id as the group label.
    const ghostGroup = screen.getByText('ghost-uuid').closest('details')!;
    expect(within(ghostGroup).getByText('orphan')).toBeInTheDocument();
  });

  it('deletes (volume retained) from inside a group', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    const row = (await screen.findByText('alice-two')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: 'Delete' }));

    expect(apiMock.api.delete).toHaveBeenCalledWith(
      '/api/v1/admin/workspaces/w-a2?keepVolume=true',
    );
  });

  it('groups remote workspaces by owner too', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    await userEvent.click(screen.getByRole('button', { name: 'Remote workspaces' }));

    const aliceGroup = (await screen.findByText('alice')).closest('details')!;
    expect(within(aliceGroup).getByText('alice-lab')).toBeInTheDocument();

    const ghostGroup = screen.getByText('ghost-uuid').closest('details')!;
    expect(within(ghostGroup).getByText('orphan-remote')).toBeInTheDocument();
  });

  it('groups volumes by owner and deletes after confirmation', async () => {
    signInAdmin();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderWithProviders(<FleetPage />);

    await userEvent.click(screen.getByRole('button', { name: 'Volumes' }));

    const aliceGroup = (await screen.findByText('alice')).closest('details')!;
    const row = within(aliceGroup).getByText('home-alice').closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: 'Delete' }));

    expect(apiMock.api.delete).toHaveBeenCalledWith('/api/v1/admin/volumes/waas-alice/home-alice');

    expect(screen.getByText('ghost-uuid').closest('details')).not.toBeNull();
    expect(screen.getByText('home-orphan')).toBeInTheDocument();
  });
});
