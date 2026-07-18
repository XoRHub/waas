// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { HomeVolumeFieldset } from './HomeVolumeFieldset';

describe('HomeVolumeFieldset', () => {
  it('shows existing metadata and reports the full block on edit', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <HomeVolumeFieldset
        homeVolume={{ labels: { 'recurring-job.longhorn.io/source': 'enabled' } }}
        onChange={onChange}
      />,
    );

    const value = screen.getByDisplayValue('enabled');
    await userEvent.clear(value);
    await userEvent.type(value, 'x');
    expect(onChange).toHaveBeenLastCalledWith({
      labels: { 'recurring-job.longhorn.io/source': 'x' },
    });
  });

  it('an emptied block leaves the payload (undefined, never {})', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <HomeVolumeFieldset homeVolume={{ labels: { team: 'blue' } }} onChange={onChange} />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });

  it('adding an annotation keeps the labels side intact', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <HomeVolumeFieldset homeVolume={{ labels: { team: 'blue' } }} onChange={onChange} />,
    );

    await userEvent.click(screen.getByRole('button', { name: '+ Add annotation' }));
    await userEvent.type(screen.getAllByPlaceholderText('key')[1], 'backup.example.com/tier');
    await userEvent.type(screen.getAllByPlaceholderText('value')[1], 'gold');
    expect(onChange).toHaveBeenLastCalledWith({
      labels: { team: 'blue' },
      annotations: { 'backup.example.com/tier': 'gold' },
    });
  });
});
