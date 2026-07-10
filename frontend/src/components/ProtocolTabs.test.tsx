// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import { KasmVNCConfigView, protocolRemovalBlock } from './ProtocolTabs';

describe('protocolRemovalBlock', () => {
  it('allows removing a protocol when others remain', () => {
    expect(protocolRemovalBlock({ count: 2 })).toBeNull();
  });

  it('blocks removing the last protocol', () => {
    expect(protocolRemovalBlock({ count: 1 })).toBe('last');
  });

  it('blocks removing a template-locked protocol even among several', () => {
    expect(protocolRemovalBlock({ count: 3, locked: true })).toBe('locked');
  });

  it('locked wins over last (the message must say why)', () => {
    expect(protocolRemovalBlock({ count: 1, locked: true })).toBe('locked');
  });
});

describe('KasmVNCConfigView', () => {
  it('renders a non-empty config through the read-only YamlEditor', async () => {
    const config = 'logging:\n  log_writer_name: all\n  level: info';
    renderWithProviders(<KasmVNCConfigView config={config} variant="effective" />);
    const area = screen.getByRole<HTMLTextAreaElement>('textbox');
    expect(area).toHaveAttribute('readonly');
    expect(area.value).toBe(config);

    // Typing must be a no-op: the value is admin/operator-owned.
    await userEvent.type(area, 'x');
    expect(area.value).toBe(config);
  });

  it('sizes the frame to the content, capped at 7 rows', () => {
    renderWithProviders(<KasmVNCConfigView config={'a: 1\nb: 2'} variant="template" />);
    expect(screen.getByRole('textbox')).toHaveAttribute('rows', '2');
  });

  it('keeps the italic message, not an editor, for an empty config', () => {
    renderWithProviders(<KasmVNCConfigView config={'  \n'} variant="template" />);
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.getByText(en.portal.kasmvncManagedConfigEmpty)).toBeInTheDocument();
  });
});
