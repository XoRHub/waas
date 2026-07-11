// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { TemplateInput } from '@/hooks/useApi';
import en from '@/i18n/locales/en.json';
import fr from '@/i18n/locales/fr.json';
import { TemplateDialog } from './TemplatesPage';

const apiMock = createApiMock({
  '/api/v1/meta/protocols': [],
  '/api/v1/meta/override-fields': [],
  '/api/v1/meta/placeholders': [],
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const base = (protocol: string, kasmvncConfig?: string): TemplateInput => ({
  name: 'tpl1',
  displayName: 'Tpl 1',
  os: 'linux',
  image: 'img:1',
  homeSize: '10Gi',
  kasmvncConfig,
  protocols: [{ name: protocol, port: protocol === 'kasmvnc' ? 6901 : 5901, default: true }],
});

const renderDialog = (input: TemplateInput) => {
  signIn({ username: 'admin', role: 'admin' });
  renderWithProviders(<TemplateDialog isNew initial={input} onClose={() => {}} />);
};

describe('TemplateDialog kasmvncConfig field', () => {
  it('hides the textarea when no kasmvnc protocol is present', () => {
    // The field is gated on the protocol list (synchronous), so a query
    // right after render is enough — no async settle needed.
    renderDialog(base('vnc'));
    expect(screen.queryByText('KasmVNC configuration')).toBeNull();
  });

  it('renders the textarea when kasmvnc is in the protocol list', async () => {
    renderDialog(base('kasmvnc'));
    expect(await screen.findByText('KasmVNC configuration')).toBeInTheDocument();
    // The doc reference link is present.
    const link = screen.getByRole('link', { name: en.admin.templatesPage.kasmvncConfigDocLink });
    expect(link).toHaveAttribute(
      'href',
      'https://kasmweb.com/kasmvnc/docs/latest/configuration.html',
    );
  });

  it('round-trips the value and submits it', async () => {
    renderDialog(base('kasmvnc', 'desktop:\n  resolution:\n    width: 800'));
    const area = (await screen.findByPlaceholderText(/resolution/)) as HTMLTextAreaElement;
    // Reloads the saved value.
    expect(area.value).toContain('width: 800');

    await userEvent.clear(area);
    await userEvent.type(area, 'logging:\n  level: info');
    await userEvent.click(screen.getByRole('button', { name: en.app.save }));

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const calls = apiMock.api.post.mock.calls as unknown as Array<[string, TemplateInput]>;
    const payload = calls[calls.length - 1][1];
    expect(payload.kasmvncConfig).toContain('level: info');
  });

  it('flags a non-mapping config live in the YAML editor', async () => {
    renderDialog(base('kasmvnc'));
    const area = await screen.findByPlaceholderText(/resolution/);
    // A sequence parses fine as YAML but is not a mapping.
    await userEvent.type(area, '- item');
    expect(
      await screen.findByText(en.admin.templatesPage.kasmvncConfigMustBeMapping),
    ).toBeInTheDocument();
  });
});

describe('TemplateDialog protocol picker kasmvnc exclusivity', () => {
  // These tests need a populated registry; the route is restored to the
  // file-wide empty default afterwards so the other describes keep
  // their behavior.
  beforeEach(() => {
    apiMock.route('/api/v1/meta/protocols', [
      { name: 'vnc', params: [] },
      { name: 'rdp', params: [] },
      { name: 'ssh', params: [] },
      { name: 'kasmvnc', params: [] },
    ]);
  });
  afterEach(() => {
    apiMock.route('/api/v1/meta/protocols', []);
  });

  const withProtocols = (...names: string[]): TemplateInput => ({
    name: 'tpl1',
    displayName: 'Tpl 1',
    os: 'linux',
    image: 'img:1',
    homeSize: '10Gi',
    protocols: names.map((name, i) => ({
      name,
      port: name === 'kasmvnc' ? 6901 : 5901 + i,
      default: i === 0,
    })),
  });

  it('offers rdp/ssh but not kasmvnc once vnc is configured', async () => {
    renderDialog(withProtocols('vnc'));
    await userEvent.click(await screen.findByRole('button', { name: `+ ${en.protocolTabs.add}` }));
    expect(screen.getByRole('button', { name: 'rdp' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'ssh' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'kasmvnc' })).toBeNull();
  });

  it('offers no other protocol once kasmvnc is configured', async () => {
    renderDialog(withProtocols('kasmvnc'));
    // The registry query resolves async: the "+" menu must stay absent
    // even after it settles, so wait out a findBy instead of asserting
    // on the initial render only.
    await expect(
      screen.findByRole('button', { name: `+ ${en.protocolTabs.add}` }, { timeout: 250 }),
    ).rejects.toThrow();
  });

  it('removes the last protocol back to the empty (legacy fallback) state', async () => {
    // kasmvnc-only is the worst case: exclusivity blocks any addition,
    // so removing the last protocol is the ONLY way to change course.
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderDialog({ ...withProtocols('kasmvnc'), kasmvncConfig: 'logging:\n  level: info' });

    await userEvent.click(
      await screen.findByTitle(en.protocolTabs.remove.replace('{{protocol}}', 'KASMVNC')),
    );
    expect(confirm).toHaveBeenCalled();
    expect(screen.getByText(en.admin.templatesPage.noProtocolsYet)).toBeInTheDocument();

    // The empty template is fully editable again: every registry
    // protocol is addable, kasmvnc included.
    await userEvent.click(await screen.findByRole('button', { name: `+ ${en.protocolTabs.add}` }));
    for (const p of ['vnc', 'rdp', 'ssh', 'kasmvnc']) {
      expect(screen.getByRole('button', { name: p })).toBeInTheDocument();
    }
    await userEvent.keyboard('{Escape}');

    // Saving the empty state must not smuggle the dead kasmvncConfig
    // along (the server would reject it, its editor is hidden).
    await userEvent.click(screen.getByRole('button', { name: en.app.save }));
    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const calls = apiMock.api.post.mock.calls as unknown as Array<[string, TemplateInput]>;
    const payload = calls[calls.length - 1][1];
    expect(payload.protocols).toEqual([]);
    expect(payload.kasmvncConfig).toBe('');
    confirm.mockRestore();
  });
});

describe('kasmvncConfig i18n', () => {
  it('has the keys in both locales', () => {
    for (const loc of [en, fr]) {
      const p = loc.admin.templatesPage;
      expect(p.kasmvncConfig).toBeTruthy();
      expect(p.kasmvncConfigHint).toBeTruthy();
      expect(p.kasmvncConfigDocLink).toBeTruthy();
      expect(p.kasmvncConfigPlaceholder).toBeTruthy();
      expect(p.kasmvncConfigMustBeMapping).toBeTruthy();
    }
  });
});
