// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import { clampRange, CreateWorkspaceDialog } from './CreateWorkspaceDialog';

const apiMock = createApiMock({
  '/api/v1/workspace-templates': [
    {
      name: 'xfce',
      displayName: 'XFCE Desktop',
      os: 'linux',
      allowedOverrides: ['resources'],
      protocols: [{ name: 'kasmvnc', port: 6901, default: true }],
    },
    // Catalog approves xfce only: "blocked" must be visible but disabled.
    { name: 'blocked', displayName: 'No image', os: 'linux' },
  ],
  '/api/v1/catalog': [{ name: 'xfce-img', templates: ['xfce'] }],
  '/api/v1/me/quota': {
    policy: 'default',
    perWorkspace: { cpu: '2', memory: '4Gi' },
    defaults: { cpu: '1', memory: '2Gi' },
  },
  '/api/v1/meta/protocols': [{ name: 'kasmvnc', params: [] }],
  '/api/v1/volumes': [],
});
// The namespace preview carries query parameters: prefix-match it.
const tableGet = apiMock.api.get.getMockImplementation()!;
apiMock.api.get.mockImplementation((path: string) => {
  if (path.startsWith('/api/v1/workspaces/namespace-preview')) {
    return Promise.resolve({ data: { namespace: 'waas-marc' } });
  }
  return tableGet(path);
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

describe('clampRange', () => {
  it('caps the max at the tightest of image/policy/remaining', () => {
    const b = clampRange({ min: 500, maxes: [4000, 2000, 3000], defaults: [] }, 250, 250);
    expect(b).toEqual({ min: 500, max: 2000, initial: 500 });
  });

  it('never lets the max fall below the min (over-consumed quota)', () => {
    const b = clampRange({ min: 1000, maxes: [500], defaults: [] }, 250, 250);
    expect(b.max).toBe(1000);
  });

  it('first defined default wins, rounded to the step and clamped', () => {
    const b = clampRange({ maxes: [2000], defaults: [undefined, 1100] }, 250, 250);
    expect(b.initial).toBe(1000); // 1100 → nearest 250-step within bounds
  });

  it('falls back to floor*16 when nothing caps the resource', () => {
    expect(clampRange({ maxes: [], defaults: [] }, 250, 250).max).toBe(4000);
  });
});

describe('CreateWorkspaceDialog', () => {
  beforeEach(() => {
    apiMock.api.post.mockClear();
  });

  it('a template without catalog image is listed but not selectable', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<CreateWorkspaceDialog onClose={() => {}} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    const blocked = await screen.findByRole('option', { name: /No image/ });
    expect(blocked).toBeDisabled();
    // The reason rides on the card itself — never silently dropped.
    expect(blocked.textContent).toContain('unavailable');
    expect(await screen.findByRole('option', { name: /XFCE Desktop/ })).toBeEnabled();
  });

  it('submits the template with sized resources when the right is granted', async () => {
    signIn({ username: 'marc' });
    const onClose = vi.fn();
    renderWithProviders(<CreateWorkspaceDialog onClose={onClose} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    await userEvent.click(await screen.findByRole('option', { name: /XFCE Desktop/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const [path, input] = apiMock.api.post.mock.calls[0] as unknown as [
      string,
      { templateRef: string; resources?: { cpu: string; memory: string }; overrides?: unknown },
    ];
    expect(path).toBe('/api/v1/workspaces');
    expect(input.templateRef).toBe('xfce');
    // Policy defaults (1 vCPU / 2Gi) seed the sliders; the payload
    // carries them because the template grants the resources right.
    expect(input.resources).toEqual({ cpu: '1', memory: '2Gi' });
    // No protocol/env/schedule touched: no overrides block at all.
    expect(input.overrides).toBeUndefined();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('the metadata block is gated on the right and lands in the overrides', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<CreateWorkspaceDialog onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    await userEvent.click(await screen.findByRole('option', { name: /XFCE Desktop/ }));
    // 'resources' only: no advanced overrides panel at all.
    expect(screen.queryByRole('button', { name: /Advanced/ })).toBeNull();

    apiMock.route('/api/v1/workspace-templates', [
      {
        name: 'xfce',
        displayName: 'XFCE Desktop',
        os: 'linux',
        allowedOverrides: ['metadata'],
        protocols: [{ name: 'kasmvnc', port: 6901, default: true }],
      },
    ]);
    cleanup();
    renderWithProviders(<CreateWorkspaceDialog onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    await userEvent.click(await screen.findByRole('option', { name: /XFCE Desktop/ }));

    await userEvent.click(screen.getByRole('button', { name: /Advanced/ }));
    await userEvent.click(screen.getByRole('button', { name: '+ Add label' }));
    await userEvent.type(screen.getByPlaceholderText('key'), 'team');
    await userEvent.type(screen.getByPlaceholderText('value'), 'blue');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const [, input] = apiMock.api.post.mock.calls[0] as unknown as [
      string,
      { overrides?: { labels?: Record<string, string> } },
    ];
    expect(input.overrides).toEqual({ labels: { team: 'blue' } });
  });
});
