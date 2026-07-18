// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { KeyValueEditor } from './KeyValueEditor';

const setup = (value: Record<string, string>, disabled = false) => {
  const onChange = vi.fn();
  renderWithProviders(
    <KeyValueEditor
      value={value}
      onChange={onChange}
      disabled={disabled}
      keyPlaceholder="key"
      valuePlaceholder="value"
      addLabel="Add entry"
    />,
  );
  return onChange;
};

describe('KeyValueEditor', () => {
  it('edits an existing value', async () => {
    const onChange = setup({ zone: 'a' });
    await userEvent.type(screen.getByDisplayValue('a'), 'b');
    expect(onChange).toHaveBeenLastCalledWith({ zone: 'ab' });
  });

  it('adds a row and exports it once the key is typed, trimmed', async () => {
    const onChange = setup({});
    await userEvent.click(screen.getByRole('button', { name: '+ Add entry' }));
    // The empty in-progress row is not exported.
    expect(onChange).toHaveBeenLastCalledWith({});

    await userEvent.type(screen.getByPlaceholderText('key'), ' team ');
    await userEvent.type(screen.getByPlaceholderText('value'), 'blue');
    expect(onChange).toHaveBeenLastCalledWith({ team: 'blue' });
  });

  it('removes a row', async () => {
    const onChange = setup({ zone: 'a', team: 'blue' });
    const remove = screen.getAllByRole('button', { name: 'Delete' })[0];
    await userEvent.click(remove);
    expect(onChange).toHaveBeenLastCalledWith({ team: 'blue' });
  });

  it('keeps a row whose key is cleared visible but out of the export', async () => {
    const onChange = setup({ zone: 'a' });
    await userEvent.clear(screen.getByDisplayValue('zone'));
    expect(onChange).toHaveBeenLastCalledWith({});
    // The value input is still there for the user to finish the rename.
    expect(screen.getByDisplayValue('a')).toBeInTheDocument();
  });

  it('disabled: inputs read-only, no add/remove buttons', () => {
    setup({ zone: 'a' }, true);
    expect(screen.getByDisplayValue('zone')).toBeDisabled();
    expect(screen.getByDisplayValue('a')).toBeDisabled();
    expect(screen.queryByRole('button')).toBeNull();
  });
});
