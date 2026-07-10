// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
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
