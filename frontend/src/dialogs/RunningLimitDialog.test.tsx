// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import type { Workspace } from '@/types';
import { RunningLimitDialog } from './RunningLimitDialog';

const running = [
  { id: 'w1', name: 'one', displayName: 'One', phase: 'Running' },
  { id: 'w2', name: 'two', displayName: 'Two', phase: 'Running' },
] as Workspace[];

describe('RunningLimitDialog', () => {
  it('defaults to creating paused (no slot consumed)', async () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <RunningLimitDialog
        running={2}
        max={2}
        workspaces={running}
        pending={false}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
    );

    expect(screen.getByRole('radio', { name: /Create it paused/ })).toBeChecked();
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(onConfirm).toHaveBeenCalledWith({ paused: true });
  });

  it('pause-first passes the selected sibling id', async () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <RunningLimitDialog
        running={2}
        max={2}
        workspaces={running}
        pending={false}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
    );

    await userEvent.click(screen.getByRole('radio', { name: /Pause another workspace first/ }));
    await userEvent.selectOptions(screen.getByRole('combobox'), 'w2');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(onConfirm).toHaveBeenCalledWith({ paused: false, pauseId: 'w2' });
  });

  it('disables pause-first when nothing is running', () => {
    renderWithProviders(
      <RunningLimitDialog
        running={1}
        max={1}
        workspaces={[]}
        pending={false}
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getByRole('radio', { name: /Pause another workspace first/ })).toBeDisabled();
  });

  it('disables Continue while the pause/create chain is pending', () => {
    renderWithProviders(
      <RunningLimitDialog
        running={2}
        max={2}
        workspaces={running}
        pending
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDisabled();
  });
});
