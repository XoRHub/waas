// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import { PlacementFieldset } from './PlacementFieldset';

describe('PlacementFieldset', () => {
  it('edits the namespace pattern, empty input clearing it to undefined', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <PlacementFieldset
        placement={{ namespace: 'waas-{user}' }}
        placeholders={[]}
        onChange={onChange}
      />,
    );

    const input = screen.getByDisplayValue('waas-{user}');
    await userEvent.type(input, 'x');
    expect(onChange).toHaveBeenLastCalledWith({ namespace: 'waas-{user}x' });

    await userEvent.clear(input);
    expect(onChange).toHaveBeenLastCalledWith({ namespace: undefined });
  });

  it('selects the cleanup policy', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <PlacementFieldset placement={undefined} placeholders={[]} onChange={onChange} />,
    );

    await userEvent.selectOptions(screen.getByRole('combobox'), 'DeleteWhenEmpty');
    expect(onChange).toHaveBeenLastCalledWith({ cleanup: 'DeleteWhenEmpty' });
  });

  it('shows existing namespace metadata and reports the full placement on edit', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <PlacementFieldset
        placement={{ namespace: 'waas-{user}', namespaceLabels: { team: 'blue' } }}
        placeholders={[]}
        onChange={onChange}
      />,
    );

    const value = screen.getByDisplayValue('blue');
    await userEvent.type(value, 's');
    // The whole placement travels: pattern preserved, labels updated.
    expect(onChange).toHaveBeenLastCalledWith({
      namespace: 'waas-{user}',
      namespaceLabels: { team: 'blues' },
    });
  });

  it('an emptied metadata map leaves the payload (undefined, never {})', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <PlacementFieldset
        placement={{ namespaceAnnotations: { 'backup/policy': 'daily' } }}
        placeholders={[]}
        onChange={onChange}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: en.app.delete }));
    expect(onChange).toHaveBeenLastCalledWith({ namespaceAnnotations: undefined });
  });

  it('lists the server-provided placeholders, hidden when none', () => {
    const { unmount } = renderWithProviders(
      <PlacementFieldset
        placement={undefined}
        placeholders={[{ token: '{user}', description: 'the owner', source: 'JWT' }]}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText('{user}')).toBeInTheDocument();
    expect(screen.getByText(/the owner/)).toBeInTheDocument();
    unmount();

    renderWithProviders(
      <PlacementFieldset placement={undefined} placeholders={[]} onChange={() => {}} />,
    );
    expect(screen.queryByText(en.admin.templatesPage.placeholdersTitle)).toBeNull();
  });
});
