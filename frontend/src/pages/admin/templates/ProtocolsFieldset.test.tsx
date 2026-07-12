// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import type { ProtocolMeta } from '@/types';
import type { TemplateProtocolInput } from '@/hooks/useApi';
import { ProtocolsFieldset } from './ProtocolsFieldset';

// Two ui-tier params in one category: enough to exercise the per-param
// delegation toggle AND the whole-category selector absorption.
const meta: ProtocolMeta[] = [
  {
    name: 'vnc',
    params: [
      {
        name: 'color-depth',
        protocols: ['vnc'],
        kind: 'enum',
        enum: ['16', '24'],
        tier: 'ui',
        category: 'display',
        live: false,
        description: 'depth',
      },
      {
        name: 'swap-red-blue',
        protocols: ['vnc'],
        kind: 'bool',
        tier: 'ui',
        category: 'display',
        live: false,
        description: 'swap',
      },
    ],
  },
];

const vnc: TemplateProtocolInput = { name: 'vnc', port: 5901, default: true };

function renderFieldset(over: Partial<Parameters<typeof ProtocolsFieldset>[0]> = {}) {
  const onPatchActive = vi.fn();
  const onMakeDefault = vi.fn();
  renderWithProviders(
    <ProtocolsFieldset
      protocols={[vnc]}
      meta={meta}
      active="vnc"
      onSelect={() => {}}
      addable={[]}
      onAdd={() => {}}
      onRemove={() => {}}
      onPatchActive={onPatchActive}
      onMakeDefault={onMakeDefault}
      {...over}
    />,
  );
  return { onPatchActive, onMakeDefault };
}

describe('ProtocolsFieldset', () => {
  it('shows the empty-state hint when no protocol is configured', () => {
    renderFieldset({ protocols: [], active: '' });
    expect(screen.getByText(en.admin.templatesPage.noProtocolsYet)).toBeInTheDocument();
  });

  it('patches the active protocol port (state stays in the dialog)', () => {
    const { onPatchActive } = renderFieldset();
    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '5902' } });
    expect(onPatchActive).toHaveBeenCalledWith({ port: 5902 });
  });

  it('reports the default radio through onMakeDefault', async () => {
    const { onMakeDefault } = renderFieldset({
      protocols: [vnc, { name: 'rdp', port: 3389 }],
      active: 'rdp',
    });
    await userEvent.click(screen.getByRole('radio'));
    expect(onMakeDefault).toHaveBeenCalled();
  });

  it('delegates one param by name via the locked/user toggle', async () => {
    const { onPatchActive } = renderFieldset();
    // One user button per rendered param, in registry order.
    const userButtons = screen.getAllByRole('button', {
      name: en.admin.templatesPage.overrideUser,
    });
    await userEvent.click(userButtons[0]);
    expect(onPatchActive).toHaveBeenCalledWith({ userParams: ['color-depth'] });
  });

  it('delegates a whole category with a cat: selector absorbing the names', async () => {
    const { onPatchActive } = renderFieldset({
      protocols: [{ ...vnc, userParams: ['color-depth'] }],
    });
    await userEvent.click(
      screen.getByRole('checkbox', { name: en.admin.templatesPage.allowCategory }),
    );
    // The individual name is absorbed by the selector, not kept alongside.
    expect(onPatchActive).toHaveBeenCalledWith({ userParams: ['cat:display'] });
  });

  it('inerts the per-param toggles while the category is delegated', () => {
    renderFieldset({ protocols: [{ ...vnc, userParams: ['cat:display'] }] });
    for (const b of screen.getAllByRole('button', {
      name: en.admin.templatesPage.overrideUser,
    })) {
      expect(b).toBeDisabled();
      expect(b).toHaveAttribute('aria-pressed', 'true');
    }
  });
});
