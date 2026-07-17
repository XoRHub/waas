// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import { EnvFieldset } from './EnvFieldset';

describe('EnvFieldset suggestions', () => {
  it('renders nothing suggestion-related when the props are omitted', () => {
    renderWithProviders(<EnvFieldset env={[{ name: 'A', value: '1' }]} onChange={() => {}} />);
    expect(screen.queryByText(en.admin.templatesPage.envSuggestionHint)).toBeNull();
    expect(screen.queryByTitle(en.admin.templatesPage.envSuggestionTooltip)).toBeNull();
  });

  it('renders greyed suggestions with name, description, tooltip and hint line', () => {
    renderWithProviders(
      <EnvFieldset
        env={[]}
        onChange={() => {}}
        suggestions={[{ name: 'SSH_KEYS_FILE', description: 'Where the keys live' }]}
        onAdopt={() => {}}
      />,
    );
    const suggestion = screen.getByRole('button', { name: /SSH_KEYS_FILE/ });
    expect(suggestion).toHaveAttribute('title', en.admin.templatesPage.envSuggestionTooltip);
    expect(within(suggestion).getByText('Where the keys live')).toBeInTheDocument();
    expect(screen.getByText(en.admin.templatesPage.envSuggestionHint)).toBeInTheDocument();
  });

  it('reports adoption with the suggestion name', async () => {
    const onAdopt = vi.fn();
    renderWithProviders(
      <EnvFieldset
        env={[]}
        onChange={() => {}}
        suggestions={[{ name: 'SSH_KEYS_FILE' }]}
        onAdopt={onAdopt}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: /SSH_KEYS_FILE/ }));
    expect(onAdopt).toHaveBeenCalledWith('SSH_KEYS_FILE');
  });

  it('feeds valuePlaceholders into the matching row value input', () => {
    renderWithProviders(
      <EnvFieldset
        env={[
          { name: 'SSH_KEYS_FILE', value: '' },
          { name: 'OTHER', value: '' },
        ]}
        onChange={() => {}}
        valuePlaceholders={{ SSH_KEYS_FILE: 'Where the keys live' }}
      />,
    );
    const row = screen.getByDisplayValue('SSH_KEYS_FILE').closest('div.flex') as HTMLElement;
    expect(within(row).getByLabelText('value')).toHaveAttribute(
      'placeholder',
      'Where the keys live',
    );
    const other = screen.getByDisplayValue('OTHER').closest('div.flex') as HTMLElement;
    expect(within(other).getByLabelText('value')).not.toHaveAttribute('placeholder');
  });
});
