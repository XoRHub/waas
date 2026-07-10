// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import type { ParamMeta } from '@/types';
import { ParamField } from './ParamField';

const meta = (over: Partial<ParamMeta> & { name: string; kind: ParamMeta['kind'] }): ParamMeta => ({
  protocols: ['ssh'],
  tier: 'ui',
  live: false,
  description: over.name,
  ...over,
});

describe('ParamField bool — tri-state segmented control', () => {
  const boolMeta = meta({ name: 'disable-copy', kind: 'bool', default: 'false' });

  it('marks the inherited state when the value is empty, with the guacd default visible', () => {
    renderWithProviders(<ParamField meta={boolMeta} value="" onChange={() => {}} />);
    expect(screen.getByRole('button', { name: 'Default (false)' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByRole('button', { name: 'On' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: 'Off' })).toHaveAttribute('aria-pressed', 'false');
  });

  it.each([
    ['On', 'true'],
    ['Off', 'false'],
  ])('clicking %s sends %j (explicit, even when it equals the default)', async (label, sent) => {
    const onChange = vi.fn();
    renderWithProviders(<ParamField meta={boolMeta} value="" onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: label }));
    expect(onChange).toHaveBeenCalledWith(sent);
  });

  it('reflects an explicit value and returns to inherited ("") via the default segment', async () => {
    const onChange = vi.fn();
    renderWithProviders(<ParamField meta={boolMeta} value="true" onChange={onChange} />);
    expect(screen.getByRole('button', { name: 'On' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: 'Default (false)' }));
    expect(onChange).toHaveBeenCalledWith('');
  });

  it('shows a true default distinctly (the ignore-cert case)', () => {
    renderWithProviders(
      <ParamField
        meta={meta({ name: 'ignore-cert', kind: 'bool', default: 'true' })}
        value="false"
        onChange={() => {}}
      />,
    );
    // Explicit false ≠ inherited true: Off is pressed, Default is not.
    expect(screen.getByRole('button', { name: 'Default (true)' })).toHaveAttribute(
      'aria-pressed',
      'false',
    );
    expect(screen.getByRole('button', { name: 'Off' })).toHaveAttribute('aria-pressed', 'true');
  });
});
