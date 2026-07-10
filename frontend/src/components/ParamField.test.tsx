// @vitest-environment jsdom
import { useState } from 'react';
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

/** Stateful harness: ParamField is controlled, and the custom-input flow
 * (type digit by digit) only makes sense when the value round-trips. */
function Controlled({
  meta: pm,
  initial = '',
  onChange,
}: {
  meta: ParamMeta;
  initial?: string;
  onChange: (value: string) => void;
}) {
  const [value, setValue] = useState(initial);
  return (
    <ParamField
      meta={pm}
      value={value}
      onChange={(v) => {
        setValue(v);
        onChange(v);
      }}
    />
  );
}

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

describe('ParamField int/string with a default — hybrid dropdown', () => {
  const fontSize = meta({ name: 'font-size', kind: 'int', min: 6, max: 48, default: '12' });
  const termType = meta({ name: 'terminal-type', kind: 'string', default: 'linux' });

  it('selects the "(default: X)" option when the value is empty, with no free input shown', () => {
    renderWithProviders(<ParamField meta={fontSize} value="" onChange={() => {}} />);
    const select = screen.getByRole('combobox');
    expect(select).toHaveValue('');
    expect(screen.getByRole('option', { name: '(default: 12)' })).toBeInTheDocument();
    expect(screen.queryByRole('spinbutton')).toBeNull();
  });

  it('sends a suggested value directly', async () => {
    const onChange = vi.fn();
    renderWithProviders(<Controlled meta={fontSize} onChange={onChange} />);
    await userEvent.selectOptions(screen.getByRole('combobox'), '14');
    expect(onChange).toHaveBeenLastCalledWith('14');
  });

  it('Custom… reveals the bounded number input and typing flows to the parent', async () => {
    const onChange = vi.fn();
    renderWithProviders(<Controlled meta={fontSize} onChange={onChange} />);
    await userEvent.selectOptions(screen.getByRole('combobox'), 'Custom…');
    const input = screen.getByRole('spinbutton');
    expect(input).toHaveAttribute('min', '6');
    expect(input).toHaveAttribute('max', '48');
    await userEvent.type(input, '37');
    expect(onChange).toHaveBeenLastCalledWith('37');
  });

  it('an initial value outside the suggestions opens in custom mode', () => {
    renderWithProviders(<ParamField meta={fontSize} value="37" onChange={() => {}} />);
    expect(screen.getByRole('combobox')).toHaveValue('__custom__');
    expect(screen.getByRole('spinbutton')).toHaveValue(37);
  });

  it('re-selecting the default option clears the value and hides the input', async () => {
    const onChange = vi.fn();
    renderWithProviders(<Controlled meta={fontSize} initial="37" onChange={onChange} />);
    await userEvent.selectOptions(screen.getByRole('combobox'), '(default: 12)');
    expect(onChange).toHaveBeenLastCalledWith('');
    expect(screen.queryByRole('spinbutton')).toBeNull();
  });

  it('offers the usual TERM values for terminal-type, without duplicating the default', async () => {
    const onChange = vi.fn();
    renderWithProviders(<Controlled meta={termType} onChange={onChange} />);
    const labels = screen
      .getAllByRole('option')
      .map((o) => o.textContent)
      .filter((label) => label !== 'Custom…');
    // 'linux' only appears as the default option, not as a second entry.
    expect(labels).toEqual(['(default: linux)', 'xterm', 'xterm-256color', 'vt100', 'screen']);
    await userEvent.selectOptions(screen.getByRole('combobox'), 'xterm-256color');
    expect(onChange).toHaveBeenLastCalledWith('xterm-256color');
  });
});

describe('ParamField without a default — unchanged native inputs', () => {
  it('int stays a plain number input', () => {
    renderWithProviders(
      <ParamField
        meta={meta({ name: 'autoretry', kind: 'int', min: 0, max: 10 })}
        value=""
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole('spinbutton')).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('string stays a plain text input', () => {
    renderWithProviders(
      <ParamField
        meta={meta({ name: 'font-name', kind: 'string' })}
        value=""
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole('textbox')).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('enum keeps its select with the default as first option', () => {
    renderWithProviders(
      <ParamField
        meta={meta({
          name: 'color-scheme',
          kind: 'enum',
          enum: ['black-white', 'gray-black'],
          default: 'gray-black',
        })}
        value=""
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole('option', { name: '(gray-black)' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'black-white' })).toBeInTheDocument();
  });
});
