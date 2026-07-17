// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { OverridesFieldset } from './OverridesFieldset';

const fields = [
  { name: 'env', description: 'environment variables' },
  { name: 'resources', description: 'cpu/memory' },
];

describe('OverridesFieldset', () => {
  it('checking a field adds it to allowedFields, unchecking removes it', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <OverridesFieldset
        overrides={{ allowedFields: ['env'] }}
        fields={fields}
        onChange={onChange}
      />,
    );

    expect(screen.getByRole('checkbox', { name: 'env' })).toBeChecked();
    await userEvent.click(screen.getByRole('checkbox', { name: 'resources' }));
    expect(onChange).toHaveBeenLastCalledWith({ allowedFields: ['env', 'resources'] });

    await userEvent.click(screen.getByRole('checkbox', { name: 'env' }));
    expect(onChange).toHaveBeenLastCalledWith({ allowedFields: [] });
  });

  it('edits the owner expression preserving the field selection', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <OverridesFieldset
        overrides={{ allowedFields: ['env'], owner: '' }}
        fields={fields}
        onChange={onChange}
      />,
    );

    await userEvent.type(screen.getByRole('textbox'), 'a');
    expect(onChange).toHaveBeenLastCalledWith({ allowedFields: ['env'], owner: 'a' });
  });
});
