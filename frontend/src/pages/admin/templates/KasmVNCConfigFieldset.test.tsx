// @vitest-environment jsdom
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import en from '@/i18n/locales/en.json';
import type { CatalogImage } from '@/types';
import { KasmVNCConfigFieldset } from './KasmVNCConfigFieldset';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

// Excerpt of kasmweb/terminal:1.19.0's kasmvnc_defaults.yaml — every
// essential's keys but pointer.game_mode (hidden-when-absent case).
const defaults = `---
desktop:
  resolution:
    width: 1024
    height: 768
  allow_resize: true

user_session:
  idle_timeout: never

keyboard:
  ignore_numlock: false

runtime_configuration:
  allow_client_to_override_kasm_server_settings: true

data_loss_prevention:
  clipboard:
    server_to_client:
      enabled: true
      size: unlimited
    client_to_server:
      enabled: true
      size: unlimited

encoding:
  max_frame_rate: 60
  rect_encoding_mode:
    min_quality: 7
    max_quality: 8
`;

const TERMINAL = 'docker.io/kasmweb/terminal:1.19.0';
const catalogs: CatalogImage[] = [
  {
    name: 'kasm',
    displayName: 'Kasm',
    registry: 'docker.io/kasmweb',
    enabled: true,
    protocols: ['kasmvnc'],
    discovered: [{ image: TERMINAL, kasmvnc: { version: '1.5.1', defaults } }],
  },
];

beforeEach(() => {
  apiMock.route('/api/v1/admin/images', catalogs);
  apiMock.route('/api/v1/meta/kasmvnc-managed-keys', [
    'data_loss_prevention.clipboard.server_to_client.enabled',
    'data_loss_prevention.clipboard.client_to_server.enabled',
    'runtime_configuration.allow_client_to_override_kasm_server_settings',
  ]);
});

// Stateful host: the fieldset is controlled by the dialog in the app,
// so an edit only shows when the value round-trips. `last` exposes the
// text handed to the dialog.
const last = { value: '' };
function Host({ initial = '', image = TERMINAL }: { initial?: string; image?: string }) {
  const [value, setValue] = useState(initial);
  const onChange = (text: string) => {
    setValue(text);
    last.value = text;
  };
  return <KasmVNCConfigFieldset value={value} onChange={onChange} image={image} />;
}

const tab = (name: string) => userEvent.click(screen.getByRole('button', { name }));
const row = (label: string) => screen.getByText(label).parentElement!;
const segment = (label: string, name: string) => within(row(label)).getByRole('button', { name });

describe('KasmVNCConfigFieldset — essentials', () => {
  it('reads the schema from the catalog entry of the template image', async () => {
    renderWithProviders(<Host />);
    expect(
      await screen.findByText('KasmVNC 1.5.1 · 13 options read from the image'),
    ).toBeInTheDocument();
    // The image default is named on each control.
    expect(screen.getByRole('option', { name: 'Image default (1024 × 768)' })).toBeInTheDocument();
    expect(segment(en.admin.templatesPage.kasmvncAutoResize, 'Default (true)')).toBeInTheDocument();
  });

  it('hides an essential whose key the image lacks, and offers everything without defaults when the image is unknown', async () => {
    const { unmount } = renderWithProviders(<Host />);
    expect(
      await screen.findByText(
        'Not offered: pointer.game_mode does not exist in this image version.',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /pointer\.game_mode/ })).toBeNull();
    unmount();

    renderWithProviders(<Host image="docker.io/kasmweb/unknown:0.0.0" />);
    expect(await screen.findAllByText(en.admin.templatesPage.kasmvncSchemaUnknown)).toHaveLength(2);
    expect(segment(en.admin.templatesPage.kasmvncGameMode, 'Default (—)')).toBeInTheDocument();
    expect(screen.getAllByRole('option', { name: 'Image default' }).length).toBeGreaterThan(0);
  });

  it('writes a typed bool for On/Off and removes the key on Default', async () => {
    renderWithProviders(<Host />);
    await screen.findByText(/options read from the image/);
    await userEvent.click(segment(en.admin.templatesPage.kasmvncAutoResize, 'Off'));
    expect(last.value).toBe('desktop:\n  allow_resize: false\n');
    await userEvent.click(segment(en.admin.templatesPage.kasmvncAutoResize, 'On'));
    expect(last.value).toBe('desktop:\n  allow_resize: true\n');
    await userEvent.click(segment(en.admin.templatesPage.kasmvncAutoResize, 'Default (true)'));
    expect(last.value).toBe('');
  });

  it('writes both quality keys per preset, and both resolution keys as numbers', async () => {
    renderWithProviders(<Host />);
    await screen.findByText(/options read from the image/);
    await userEvent.click(segment(en.admin.templatesPage.kasmvncQuality, 'Economy'));
    expect(last.value).toBe(
      'encoding:\n  rect_encoding_mode:\n    min_quality: 4\n    max_quality: 6\n',
    );
    await userEvent.selectOptions(
      screen.getByRole('combobox', { name: en.admin.templatesPage.kasmvncResolution }),
      '1920×1080',
    );
    expect(last.value).toContain('desktop:\n  resolution:\n    width: 1920\n    height: 1080\n');
    // Idle timeout is seconds in KasmVNC; the control writes the number.
    await userEvent.selectOptions(
      screen.getByRole('combobox', { name: en.admin.templatesPage.kasmvncIdleTimeout }),
      en.admin.templatesPage.kasmvncIdle30,
    );
    expect(last.value).toContain('user_session:\n  idle_timeout: 1800\n');
    await userEvent.click(segment(en.admin.templatesPage.kasmvncQuality, 'Image default (7 × 8)'));
    expect(last.value).not.toContain('rect_encoding_mode');
    expect(last.value).toContain('width: 1920');
  });
});

describe('KasmVNCConfigFieldset — advanced, orphans, YAML', () => {
  it('locks the policy-managed keys and highlights modified rows', async () => {
    renderWithProviders(<Host initial={'encoding:\n  max_frame_rate: 30\n'} />);
    await screen.findByText(/options read from the image/);
    await tab(en.admin.templatesPage.kasmvncTabAdvanced);
    const rowOf = (key: string) => document.querySelector<HTMLElement>(`[data-key="${key}"]`)!;
    const managed = rowOf('runtime_configuration.allow_client_to_override_kasm_server_settings');
    expect(within(managed).getByText(/managed by policy/)).toBeInTheDocument();
    for (const button of within(managed).getAllByRole('button')) expect(button).toBeDisabled();
    // An unmanaged bool stays editable and writes through the tree.
    await userEvent.click(
      within(rowOf('keyboard.ignore_numlock')).getByRole('button', { name: 'On' }),
    );
    expect(last.value).toContain('keyboard:\n  ignore_numlock: true\n');
    expect(last.value).toContain('max_frame_rate: 30');
    expect(rowOf('encoding.max_frame_rate')).toHaveClass('bg-blue-50');
    expect(rowOf('desktop.allow_resize')).not.toHaveClass('bg-blue-50');
  });

  it('filters the advanced list by key or hint', async () => {
    renderWithProviders(<Host />);
    await screen.findByText(/options read from the image/);
    await tab(en.admin.templatesPage.kasmvncTabAdvanced);
    await userEvent.type(screen.getByRole('searchbox'), 'numlock');
    expect(screen.getByText('ignore_numlock')).toBeInTheDocument();
    expect(screen.queryByText('max_frame_rate')).toBeNull();
    await userEvent.type(screen.getByRole('searchbox'), 'zzz');
    expect(screen.getByText('No option matches "numlockzzz".')).toBeInTheDocument();
  });

  it('warns about overrides unknown to the image and removes one on demand, keeping the rest', async () => {
    renderWithProviders(
      <Host initial={'desktop:\n  allow_resize: false\nlegacy:\n  option: 1\n'} />,
    );
    expect(await screen.findByText(en.admin.templatesPage.kasmvncOrphans)).toBeInTheDocument();
    expect(screen.getByText('legacy.option: 1')).toBeInTheDocument();
    expect(screen.getByText('2 options changed from the image defaults')).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.kasmvncRemove }),
    );
    expect(last.value).toBe('desktop:\n  allow_resize: false\n');
    expect(screen.queryByText(en.admin.templatesPage.kasmvncOrphans)).toBeNull();
    expect(screen.getByText('1 option changed from the image defaults')).toBeInTheDocument();
  });

  it('round-trips an override the structured tabs cannot represent across tab switches and edits', async () => {
    const nested = 'network:\n  udp:\n    peers:\n      - host: a\n        port: 1\n';
    renderWithProviders(<Host initial={nested} />);
    await screen.findByText(/options read from the image/);
    await tab(en.admin.templatesPage.kasmvncTabYaml);
    expect(screen.getByDisplayValue(/host: a/)).toBeInTheDocument();
    await tab(en.admin.templatesPage.kasmvncTabEssentials);
    await userEvent.click(segment(en.admin.templatesPage.kasmvncIgnoreNumlock, 'On'));
    expect(last.value).toContain(nested);
    expect(last.value).toContain('ignore_numlock: true');
    // Known defaults: the nested key is an orphan, never silently dropped.
    expect(screen.getByText(en.admin.templatesPage.kasmvncOrphans)).toBeInTheDocument();
  });

  it('resets every override at once', async () => {
    renderWithProviders(<Host initial={'desktop:\n  allow_resize: false\n'} />);
    await screen.findByText(/options read from the image/);
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.kasmvncResetAll }),
    );
    expect(last.value).toBe('');
    expect(screen.getByText(en.admin.templatesPage.kasmvncUnchanged)).toBeInTheDocument();
  });

  it('keeps the YAML tab as the raw editor, flagging a non-mapping live and blocking the structured tabs', async () => {
    renderWithProviders(<Host />);
    expect(
      screen.getByRole('link', { name: en.admin.templatesPage.kasmvncConfigDocLink }),
    ).toHaveAttribute('href', 'https://kasmweb.com/kasmvnc/docs/latest/configuration.html');
    await tab(en.admin.templatesPage.kasmvncTabYaml);
    await userEvent.type(screen.getByPlaceholderText(/resolution/), '- item');
    expect(
      await screen.findByText(en.admin.templatesPage.kasmvncConfigMustBeMapping),
    ).toBeInTheDocument();
    await tab(en.admin.templatesPage.kasmvncTabEssentials);
    expect(screen.getAllByText(en.admin.templatesPage.kasmvncFixYaml).length).toBeGreaterThan(0);
    expect(screen.queryByText(en.admin.templatesPage.kasmvncAutoResize)).toBeNull();
  });
});
