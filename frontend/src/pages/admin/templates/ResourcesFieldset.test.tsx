// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { ResourcesFieldset } from './ResourcesFieldset';

describe('ResourcesFieldset', () => {
  it('shows current quantities and patches the edited kind only', async () => {
    const onPatch = vi.fn();
    renderWithProviders(
      <ResourcesFieldset
        requests={{ cpu: '500m', memory: '1Gi' }}
        limits={{ cpu: '2' }}
        onPatch={onPatch}
      />,
    );

    expect(screen.getByDisplayValue('500m')).toBeInTheDocument();
    expect(screen.getByDisplayValue('1Gi')).toBeInTheDocument();

    await userEvent.type(screen.getByDisplayValue('2'), '000m');
    // Controlled input without state here: each keystroke patches from
    // the same props — the last call carries the final appended char.
    expect(onPatch).toHaveBeenLastCalledWith({ limits: { cpu: '2m' } });
  });

  it('clearing a field drops the key instead of keeping an empty string', async () => {
    const onPatch = vi.fn();
    renderWithProviders(
      <ResourcesFieldset requests={{ cpu: '500m' }} limits={undefined} onPatch={onPatch} />,
    );

    await userEvent.clear(screen.getByDisplayValue('500m'));
    expect(onPatch).toHaveBeenLastCalledWith({ requests: {} });
  });
});
