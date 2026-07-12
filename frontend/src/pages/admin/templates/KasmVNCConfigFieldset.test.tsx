// @vitest-environment jsdom
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import { KasmVNCConfigFieldset } from './KasmVNCConfigFieldset';

// Minimal stateful host: the fieldset is controlled by the dialog in the
// app, so typed input only accumulates when the value round-trips.
function Host({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial);
  return <KasmVNCConfigFieldset value={value} onChange={setValue} />;
}

describe('KasmVNCConfigFieldset', () => {
  it('links the upstream configuration reference', () => {
    renderWithProviders(<KasmVNCConfigFieldset value="" onChange={() => {}} />);
    expect(
      screen.getByRole('link', { name: en.admin.templatesPage.kasmvncConfigDocLink }),
    ).toHaveAttribute('href', 'https://kasmweb.com/kasmvnc/docs/latest/configuration.html');
  });

  it('relays edits to the dialog state', async () => {
    const onChange = vi.fn();
    renderWithProviders(<KasmVNCConfigFieldset value="" onChange={onChange} />);
    await userEvent.type(screen.getByPlaceholderText(/resolution/), 'a');
    expect(onChange).toHaveBeenCalledWith('a');
  });

  it('flags a non-mapping config live (webhook guard mirrored client-side)', async () => {
    renderWithProviders(<Host />);
    // A sequence parses fine as YAML but is not a mapping.
    await userEvent.type(screen.getByPlaceholderText(/resolution/), '- item');
    expect(
      await screen.findByText(en.admin.templatesPage.kasmvncConfigMustBeMapping),
    ).toBeInTheDocument();
  });

  it('accepts a mapping without complaint', async () => {
    renderWithProviders(<Host initial={'logging:\n  level: info'} />);
    await userEvent.type(screen.getByDisplayValue(/level: info/), ' ');
    expect(screen.queryByText(en.admin.templatesPage.kasmvncConfigMustBeMapping)).toBeNull();
  });
});
