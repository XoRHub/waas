// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Dialog } from './Dialog';
import { ImagePicker, type ImagePickerOption } from './ImagePicker';

afterEach(cleanup);

const options: ImagePickerOption[] = [
  { id: 'xfce', os: 'linux', title: 'XFCE Desktop', subtitle: 'linux · vnc/rdp' },
  { id: 'kasm', os: 'linux', icon: 'terminal', title: 'Kasm Terminal', subtitle: 'linux · kasmvnc' },
  {
    id: 'blocked',
    os: 'linux',
    title: 'No image',
    subtitle: 'linux',
    disabled: true,
    disabledReason: 'unavailable',
  },
];

function renderPicker(value = '', onChange = vi.fn()) {
  render(
    <ImagePicker
      label="Template"
      placeholder="Select a template…"
      options={options}
      value={value}
      onChange={onChange}
    />,
  );
  return onChange;
}

describe('ImagePicker', () => {
  it('shows the placeholder while nothing is selected, no open list', () => {
    renderPicker();
    expect(screen.getByText('Select a template…')).toBeTruthy();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('opens the listbox on click and lists every option', async () => {
    renderPicker();
    await userEvent.click(screen.getByRole('button', { name: 'Template' }));
    expect(screen.getByRole('listbox')).toBeTruthy();
    expect(screen.getAllByRole('option')).toHaveLength(3);
    // Disabled options stay listed with their reason — never dropped.
    expect(screen.getByRole('option', { name: /No image/ })).toBeDisabled();
  });

  it('selecting an option fires onChange and closes the list', async () => {
    const onChange = renderPicker();
    await userEvent.click(screen.getByRole('button', { name: 'Template' }));
    await userEvent.click(screen.getByRole('option', { name: /Kasm Terminal/ }));
    expect(onChange).toHaveBeenCalledWith('kasm');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('the closed trigger shows the selection with its logo', () => {
    renderPicker('kasm');
    const trigger = screen.getByRole('button', { name: 'Template' });
    expect(trigger.textContent).toContain('Kasm Terminal');
    expect(trigger.querySelector('img')?.getAttribute('src')).toBe('/icons/terminal.svg');
  });

  it('Escape closes the list', async () => {
    renderPicker();
    await userEvent.click(screen.getByRole('button', { name: 'Template' }));
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('inside a Dialog, Escape closes the open list only — the dialog needs a second press', async () => {
    const onCloseDialog = vi.fn();
    render(
      <Dialog title="New workspace" onClose={onCloseDialog} footer={null}>
        <ImagePicker
          label="Template"
          placeholder="Select a template…"
          options={options}
          value=""
          onChange={() => {}}
        />
      </Dialog>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Template' }));
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(onCloseDialog).not.toHaveBeenCalled();
    await userEvent.keyboard('{Escape}');
    expect(onCloseDialog).toHaveBeenCalledOnce();
  });
});
