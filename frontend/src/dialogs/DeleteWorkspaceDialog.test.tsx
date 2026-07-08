// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import type { Workspace } from '@/types';
import { DeleteWorkspaceDialog } from './DeleteWorkspaceDialog';

const base = {
  id: 'w1',
  name: 'marc',
  displayName: 'Marc box',
  templateRef: 'xfce',
  phase: 'Running',
} as Workspace;

describe('DeleteWorkspaceDialog', () => {
  it('defaults to KEEPING the home volume (deletion is the explicit opt-in)', async () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <DeleteWorkspaceDialog
        workspace={{ ...base, homeVolume: { name: 'home-marc', size: '10Gi' } } as Workspace}
        pending={false}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
    );

    expect(screen.getByRole('radio', { name: /Keep the volume/ })).toBeChecked();
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onConfirm).toHaveBeenCalledWith(true);
  });

  it('passes keepVolume=false only after explicitly choosing deletion', async () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <DeleteWorkspaceDialog
        workspace={{ ...base, homeVolume: { name: 'home-marc', size: '10Gi' } } as Workspace}
        pending={false}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
    );

    await userEvent.click(screen.getByRole('radio', { name: /Also delete the volume/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onConfirm).toHaveBeenCalledWith(false);
  });

  it('shows a plain confirmation when the workspace has no home volume', () => {
    renderWithProviders(
      <DeleteWorkspaceDialog
        workspace={base}
        pending={false}
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    );

    expect(screen.queryByRole('radio')).not.toBeInTheDocument();
    expect(screen.getByText(/Delete this workspace\?/)).toBeInTheDocument();
  });

  it('disables the destructive button while the deletion is pending', () => {
    renderWithProviders(
      <DeleteWorkspaceDialog workspace={base} pending onConfirm={() => {}} onClose={() => {}} />,
    );
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
  });
});
