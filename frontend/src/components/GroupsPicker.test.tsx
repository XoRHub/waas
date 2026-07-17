// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import en from '@/i18n/locales/en.json';
import { GroupsPicker } from './GroupsPicker';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

beforeEach(() => {
  apiMock.route('/api/v1/admin/groups', ['dev', 'ops']);
});

describe('GroupsPicker', () => {
  it('clicking a selection chip removes the group', async () => {
    const onChange = vi.fn();
    renderWithProviders(<GroupsPicker value={['dev', 'qa']} onChange={onChange} />);

    await userEvent.click(screen.getByRole('button', { name: /qa ✕/ }));
    expect(onChange).toHaveBeenCalledWith(['dev']);
  });

  it('suggests only known groups not already selected, click adds', async () => {
    const onChange = vi.fn();
    renderWithProviders(<GroupsPicker value={['dev']} onChange={onChange} />);

    // 'dev' is selected: only 'ops' remains a suggestion.
    const suggestion = await screen.findByRole('button', { name: '+ ops' });
    expect(screen.queryByRole('button', { name: '+ dev' })).toBeNull();

    await userEvent.click(suggestion);
    expect(onChange).toHaveBeenCalledWith(['dev', 'ops']);
  });

  it('adds a free-text group with Enter and clears the input', async () => {
    const onChange = vi.fn();
    renderWithProviders(<GroupsPicker value={[]} onChange={onChange} />);

    const input = screen.getByPlaceholderText(en.admin.usersPage.addGroupPlaceholder);
    await userEvent.type(input, ' newbies {Enter}');
    expect(onChange).toHaveBeenCalledWith(['newbies']);
    expect(input).toHaveValue('');
  });

  it('the add button submits too, and a hint renders when given', async () => {
    const onChange = vi.fn();
    renderWithProviders(<GroupsPicker value={[]} onChange={onChange} hint="who sees this" />);

    await userEvent.type(
      screen.getByPlaceholderText(en.admin.usersPage.addGroupPlaceholder),
      'sre',
    );
    await userEvent.click(screen.getByRole('button', { name: en.admin.usersPage.addGroup }));
    expect(onChange).toHaveBeenCalledWith(['sre']);
    expect(screen.getByText('who sees this')).toBeInTheDocument();
  });
});
