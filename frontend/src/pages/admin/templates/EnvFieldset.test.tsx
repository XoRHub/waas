// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import { EnvFieldset } from './EnvFieldset';

describe('EnvFieldset rows', () => {
  it('toggling from Secret swaps the literal value for a secretKeyRef', async () => {
    const onChange = vi.fn();
    renderWithProviders(<EnvFieldset env={[{ name: 'TOKEN', value: 'abc' }]} onChange={onChange} />);

    await userEvent.click(screen.getByRole('checkbox'));
    expect(onChange).toHaveBeenCalledWith([
      { name: 'TOKEN', valueFrom: { secretKeyRef: { name: '', key: '' } } },
    ]);
  });

  it('edits the secret reference name and key', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <EnvFieldset
        env={[{ name: 'TOKEN', valueFrom: { secretKeyRef: { name: 'creds', key: 'token' } } }]}
        onChange={onChange}
      />,
    );

    await userEvent.type(screen.getByDisplayValue('creds'), 'x');
    expect(onChange).toHaveBeenLastCalledWith([
      { name: 'TOKEN', valueFrom: { secretKeyRef: { name: 'credsx', key: 'token' } } },
    ]);

    await userEvent.type(screen.getByDisplayValue('token'), 'x');
    expect(onChange).toHaveBeenLastCalledWith([
      { name: 'TOKEN', valueFrom: { secretKeyRef: { name: 'creds', key: 'tokenx' } } },
    ]);
  });

  it('removes a row with ✕ and appends an empty one with add', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <EnvFieldset
        env={[
          { name: 'A', value: '1' },
          { name: 'B', value: '2' },
        ]}
        onChange={onChange}
      />,
    );

    await userEvent.click(screen.getAllByRole('button', { name: '✕' })[0]);
    expect(onChange).toHaveBeenCalledWith([{ name: 'B', value: '2' }]);

    await userEvent.click(
      screen.getByRole('button', { name: `+ ${en.admin.templatesPage.addEnv}` }),
    );
    expect(onChange).toHaveBeenLastCalledWith([
      { name: 'A', value: '1' },
      { name: 'B', value: '2' },
      { name: '', value: '' },
    ]);
  });
});

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
    const title = suggestion.getAttribute('title') ?? '';
    expect(title).toContain('Where the keys live');
    expect(title).toContain(en.admin.templatesPage.envSuggestionTooltip);
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
