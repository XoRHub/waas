// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import en from '@/i18n/locales/en.json';
import { GovernancePage } from './GovernancePage';

// Server-generated schema scaffold (every field present): the editor
// seeds from it and deep-merges the edited object over it.
const policyScaffold = [
  'priority: 0',
  'subjects: []',
  'images: []',
  'limits:',
  '  maxWorkspaces: 0',
  'overrides:',
  '  allowedFields: []',
  'remoteWorkspaces: false',
].join('\n');

const apiMock = createApiMock({
  '/api/v1/admin/images': [
    {
      name: 'ubuntu',
      displayName: 'Ubuntu Desktop',
      image: 'registry.example/ubuntu:24.04',
      protocols: ['vnc', 'rdp'],
      allowedGroups: ['devs'],
      enabled: true,
    },
    {
      name: 'windows',
      displayName: 'Windows 11',
      image: 'registry.example/win:11',
      protocols: ['rdp'],
      enabled: false,
    },
    {
      name: 'xorhub-registry',
      displayName: 'XorHub images',
      registry: 'docker.io/xorhub',
      protocols: ['vnc'],
      enabled: true,
      catalog: { source: 'Fetched', lastSyncTime: '2026-07-01T10:00:00Z' },
      catalogSource: { from: { url: 'https://example.com/catalog.yaml' } },
      discovered: [{ image: 'docker.io/xorhub/firefox:1@sha256:def' }],
    },
    {
      name: 'broken-registry',
      displayName: 'Broken registry',
      registry: 'docker.io/broken',
      protocols: ['vnc'],
      enabled: true,
      catalog: { lastSyncError: 'fetching catalog: HTTP 500' },
    },
  ],
  '/api/v1/admin/policies': [
    {
      name: 'devs-policy',
      priority: 10,
      subjects: [{ kind: 'Group', name: 'devs' }],
      images: ['ubuntu'],
      limits: { maxWorkspaces: 3, defaults: { cpu: '2' } },
    },
  ],
  '/api/v1/admin/usage': [
    {
      userId: 'u1',
      username: 'alice',
      groups: ['devs'],
      policy: 'devs-policy',
      workspaces: 2,
      used: { cpu: '2', memory: '4Gi', storage: '10Gi' },
    },
  ],
  '/api/v1/meta/override-fields': [
    { name: 'requests.cpu', description: 'CPU request' },
    { name: 'homeSize', description: 'Home volume size' },
  ],
  '/api/v1/meta/scaffold/workspacepolicy': { scaffold: policyScaffold },
  '/api/v1/meta/scaffold/workspaceimage': { scaffold: 'displayName: ""\nimage: ""\nenabled: true' },
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

beforeEach(() => {
  apiMock.api.post.mockClear();
  apiMock.api.put.mockClear();
  apiMock.api.delete.mockClear();
});

const renderPage = () => {
  signIn({ username: 'root', role: 'admin' });
  renderWithProviders(<GovernancePage />);
};

describe('GovernancePage sections', () => {
  it('renders catalog, policies and usage from the API', async () => {
    renderPage();
    // Catalog row.
    expect(await screen.findByText('Ubuntu Desktop')).toBeInTheDocument();
    expect(screen.getByText('vnc, rdp')).toBeInTheDocument();
    expect(screen.getAllByText(en.governance.enabled).length).toBeGreaterThan(0);
    expect(screen.getByText(en.governance.disabled)).toBeInTheDocument();
    // Policy card ('devs-policy' also shows in the usage table: scope
    // to the card heading).
    expect(
      await screen.findByRole('heading', { level: 3, name: 'devs-policy' }),
    ).toBeInTheDocument();
    expect(screen.getByText('Group:devs')).toBeInTheDocument();
    expect(screen.getByText('cpu=2')).toBeInTheDocument();
    // Usage row.
    expect(await screen.findByText('alice')).toBeInTheDocument();
    expect(screen.getByText('4Gi')).toBeInTheDocument();
  });
});

describe('catalog kill-switch', () => {
  it('disables an enabled image through the right endpoint', async () => {
    renderPage();
    const row = (await screen.findByText('Ubuntu Desktop')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: en.governance.disable }));
    await waitFor(() =>
      expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/admin/images/ubuntu/disable'),
    );
  });

  it('enables a disabled image through the right endpoint', async () => {
    renderPage();
    const row = (await screen.findByText('Windows 11')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: en.governance.enable }));
    await waitFor(() =>
      expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/admin/images/windows/enable'),
    );
  });
});

describe('catalog sync', () => {
  it('offers Sync now only on catalog-backed images', async () => {
    renderPage();
    const catalogRow = (await screen.findByText('XorHub images')).closest('tr')!;
    expect(
      within(catalogRow).getByRole('button', { name: en.governance.syncNow }),
    ).toBeInTheDocument();
    const plainRow = screen.getByText('Ubuntu Desktop').closest('tr')!;
    expect(
      within(plainRow).queryByRole('button', { name: en.governance.syncNow }),
    ).not.toBeInTheDocument();
  });

  it('forces a re-fetch through the right endpoint', async () => {
    renderPage();
    const row = (await screen.findByText('XorHub images')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: en.governance.syncNow }));
    await waitFor(() =>
      expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/admin/images/xorhub-registry/sync'),
    );
  });

  it('shows the last sync time, entry count and sync error', async () => {
    renderPage();
    const syncedRow = (await screen.findByText('XorHub images')).closest('tr')!;
    // formatDateTime uses toLocaleString: assert via the same helper.
    expect(
      within(syncedRow).getByText(
        (text) =>
          text.includes(new Date('2026-07-01T10:00:00Z').toLocaleString()) &&
          text.includes('1 entry'),
      ),
    ).toBeInTheDocument();
    const brokenRow = screen.getByText('Broken registry').closest('tr')!;
    expect(within(brokenRow).getByText(en.governance.syncNever)).toBeInTheDocument();
    expect(within(brokenRow).getByText(/fetching catalog: HTTP 500/)).toBeInTheDocument();
  });
});

describe('image editor round-trip', () => {
  it('keeps the catalog sync source in the submitted body (no silent wipe)', async () => {
    renderPage();
    const row = (await screen.findByText('XorHub images')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: en.app.edit }));
    const editor = (await screen.findByRole('textbox')) as HTMLTextAreaElement;
    // The spec source (echoed as catalogSource) seeds the editor under
    // the payload key `catalog`.
    await waitFor(() => expect(editor.value).toContain('https://example.com/catalog.yaml'));

    await userEvent.click(screen.getByRole('button', { name: en.app.save }));
    await waitFor(() => expect(apiMock.api.put).toHaveBeenCalled());
    const [path, body] = apiMock.api.put.mock.calls[0] as unknown as [
      string,
      Record<string, unknown>,
    ];
    expect(path).toBe('/api/v1/admin/images/xorhub-registry');
    expect(body.catalog).toEqual({ from: { url: 'https://example.com/catalog.yaml' } });
    // The echo key and the read-only projections never reach the payload.
    expect(body.catalogSource).toBeUndefined();
    expect(body.name).toBeUndefined();
    expect(body.discovered).toBeUndefined();
  });
});

describe('catalog delete', () => {
  it('deletes an entry after confirmation through the right endpoint', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderPage();
    const row = (await screen.findByText('Ubuntu Desktop')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: en.app.delete }));
    expect(confirm).toHaveBeenCalledWith(en.governance.deleteImageConfirm);
    await waitFor(() =>
      expect(apiMock.api.delete).toHaveBeenCalledWith('/api/v1/admin/images/ubuntu'),
    );
    confirm.mockRestore();
  });

  it('does nothing when the confirmation is declined', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    renderPage();
    const row = (await screen.findByText('Ubuntu Desktop')).closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: en.app.delete }));
    expect(apiMock.api.delete).not.toHaveBeenCalled();
    confirm.mockRestore();
  });
});

describe('policy delete', () => {
  it('deletes a policy after confirmation through the right endpoint', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderPage();
    const heading = await screen.findByRole('heading', { level: 3, name: 'devs-policy' });
    const card = heading.closest('div')!.parentElement!;
    await userEvent.click(within(card).getByRole('button', { name: en.app.delete }));
    expect(confirm).toHaveBeenCalledWith(en.governance.deletePolicyConfirm);
    await waitFor(() =>
      expect(apiMock.api.delete).toHaveBeenCalledWith('/api/v1/admin/policies/devs-policy'),
    );
    confirm.mockRestore();
  });
});

describe('policy editor', () => {
  const openEditor = async () => {
    renderPage();
    const heading = await screen.findByRole('heading', { level: 3, name: 'devs-policy' });
    const card = heading.closest('div')!.parentElement!;
    await userEvent.click(within(card).getByRole('button', { name: en.app.edit }));
    // Seeded once the scaffold arrives: scaffold deep-merged with the
    // policy, so the edited object's values win over placeholders.
    const editor = (await screen.findByRole('textbox')) as HTMLTextAreaElement;
    await waitFor(() => expect(editor.value).toContain('priority: 10'));
    return editor;
  };

  it('seeds the YAML from the scaffold merged with the policy, and submits it', async () => {
    const editor = await openEditor();
    // Scaffold-only fields surface too (the "never read the docs" merge).
    expect(editor.value).toContain('remoteWorkspaces: false');

    await userEvent.click(screen.getByRole('button', { name: en.app.save }));
    await waitFor(() => expect(apiMock.api.put).toHaveBeenCalled());
    const [path, body] = apiMock.api.put.mock.calls[0] as unknown as [
      string,
      Record<string, unknown>,
    ];
    expect(path).toBe('/api/v1/admin/policies/devs-policy');
    expect(body.priority).toBe(10);
    expect(body.name).toBeUndefined(); // projection field stripped from the payload
  });

  it('refuses an invalid YAML without calling the API', async () => {
    const editor = await openEditor();
    await userEvent.clear(editor);
    // A sequence parses as YAML but violates "must be a mapping".
    await userEvent.type(editor, '- item');
    await userEvent.click(screen.getByRole('button', { name: en.app.save }));

    expect(await screen.findByText(en.governance.invalidYaml)).toBeInTheDocument();
    expect(apiMock.api.put).not.toHaveBeenCalled();
  });

  it('flags a semantic issue (non-numeric priority) live before submit', async () => {
    const editor = await openEditor();
    await userEvent.clear(editor);
    await userEvent.type(editor, 'priority: high');
    // The validator's message is rendered by the editor's issue panel.
    expect(await screen.findByText(/priority: must be a number/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: en.app.save }));
    expect(apiMock.api.put).not.toHaveBeenCalled();
  });

  it('keeps the allowedFields chips and the YAML in sync (registry-driven)', async () => {
    const editor = await openEditor();
    // Chips come from GET /meta/override-fields, not a local list.
    const chip = screen.getByRole('button', { name: 'requests.cpu' });
    await userEvent.click(chip);
    expect(editor.value).toMatch(/allowedFields:\n\s+- requests.cpu/);
  });
});
