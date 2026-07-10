// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { vi } from 'vitest';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { Workspace } from '@/types';
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

const apiMock = createApiMock({ '/api/v1/workspaces': fleet });
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const signInAdmin = () => signIn({ id: 'admin1', username: 'boss', role: 'admin' });

describe('admin fleet workspaces grouping', () => {
  it('shows only the admin’s own workspaces, flat, in the default view', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    expect(await screen.findByText('admin-desk')).toBeInTheDocument();
    expect(screen.queryByText('alice-one')).not.toBeInTheDocument();
    expect(screen.queryByText('orphan')).not.toBeInTheDocument();
  });

  it('groups other users’ workspaces by username, id as fallback', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    await screen.findByText('admin-desk');
    await userEvent.click(screen.getByRole('button', { name: 'By user' }));

    // Alice's group header and both of her rows.
    const aliceGroup = screen.getByText('alice').closest('details')!;
    expect(within(aliceGroup).getByText('alice-one')).toBeInTheDocument();
    expect(within(aliceGroup).getByText('alice-two')).toBeInTheDocument();

    // Deleted owner: raw id as the group label.
    const ghostGroup = screen.getByText('ghost-uuid').closest('details')!;
    expect(within(ghostGroup).getByText('orphan')).toBeInTheDocument();

    // The admin's own workspaces never leak into the per-user view.
    expect(screen.queryByText('admin-desk')).not.toBeInTheDocument();
  });

  it('deletes (volume retained) from the flat view', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    const row = (await screen.findByText('admin-desk')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: 'Delete' }));

    expect(apiMock.api.delete).toHaveBeenCalledWith('/api/v1/workspaces/w-mine?keepVolume=true');
  });

  it('deletes (volume retained) from inside a user group', async () => {
    signInAdmin();
    renderWithProviders(<FleetPage />);

    await screen.findByText('admin-desk');
    await userEvent.click(screen.getByRole('button', { name: 'By user' }));

    const row = screen.getByText('alice-two').closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: 'Delete' }));

    expect(apiMock.api.delete).toHaveBeenCalledWith('/api/v1/workspaces/w-a2?keepVolume=true');
  });
});
