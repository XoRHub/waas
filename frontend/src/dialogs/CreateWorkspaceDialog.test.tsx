// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
      description: 'Full XFCE desktop, VNC + RDP + SSH.',
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
  '/api/v1/workspaces': [],
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

  it('a template with a description gets the "?" tooltip affordance on its card', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<CreateWorkspaceDialog onClose={() => {}} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    await screen.findByRole('option', { name: /XFCE Desktop/ });
    const hint = screen.getByText('?');
    expect(hint).toHaveAttribute('title', 'Full XFCE desktop, VNC + RDP + SSH.');
    // "blocked" has no description — exactly one "?" in the list.
    expect(screen.getAllByText('?')).toHaveLength(1);
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

  it('the metadata tab is gated on the right and lands in the overrides', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<CreateWorkspaceDialog onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    await userEvent.click(await screen.findByRole('option', { name: /XFCE Desktop/ }));
    // 'resources' only: a Workspace section exists (Resources tab) but
    // no Metadata tab inside it.
    await userEvent.click(screen.getByRole('button', { name: 'Workspace' }));
    expect(screen.getByRole('button', { name: 'Resources' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Metadata' })).toBeNull();

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

    // metadata only: the Workspace section opens straight on its single
    // Metadata tab.
    await userEvent.click(screen.getByRole('button', { name: 'Workspace' }));
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

describe('running workspace limit', () => {
  const baseQuota = {
    policy: 'default',
    perWorkspace: { cpu: '2', memory: '4Gi' },
    defaults: { cpu: '1', memory: '2Gi' },
  };

  beforeEach(() => {
    apiMock.api.post.mockClear();
    apiMock.route('/api/v1/me/quota', {
      ...baseQuota,
      maxRunningWorkspaces: 1,
      runningWorkspaces: 1,
    });
    apiMock.route('/api/v1/workspaces', [
      { id: 'w-busy', name: 'busy', displayName: 'Busy', phase: 'Running', paused: false },
    ]);
    apiMock.route('/api/v1/workspace-templates', [
      {
        name: 'xfce',
        displayName: 'XFCE Desktop',
        os: 'linux',
        allowedOverrides: ['resources'],
        protocols: [{ name: 'kasmvnc', port: 6901, default: true }],
      },
    ]);
  });

  afterEach(() => {
    apiMock.route('/api/v1/me/quota', baseQuota);
    apiMock.route('/api/v1/workspaces', []);
    cleanup();
  });

  const openAndSubmit = async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<CreateWorkspaceDialog onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Template' }));
    await userEvent.click(await screen.findByRole('option', { name: /XFCE Desktop/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));
  };

  it('detours through the choice dialog instead of submitting', async () => {
    await openAndSubmit();

    expect(await screen.findByText('Running workspace limit reached')).toBeInTheDocument();
    expect(apiMock.api.post).not.toHaveBeenCalled();

    // Default choice: create paused — the payload says so.
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalled());
    const [path, input] = apiMock.api.post.mock.calls[0] as unknown as [
      string,
      { templateRef: string; paused?: boolean },
    ];
    expect(path).toBe('/api/v1/workspaces');
    expect(input.paused).toBe(true);
  });

  it('pause-first chains the sibling pause, then creates running', async () => {
    await openAndSubmit();

    await screen.findByText('Running workspace limit reached');
    await userEvent.click(screen.getByRole('radio', { name: /Pause another workspace first/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalledTimes(2));
    expect(apiMock.api.post.mock.calls[0][0]).toBe('/api/v1/workspaces/w-busy/pause');
    const [path, input] = apiMock.api.post.mock.calls[1] as unknown as [
      string,
      { templateRef: string; paused?: boolean },
    ];
    expect(path).toBe('/api/v1/workspaces');
    expect(input.paused).toBeUndefined();
  });
});
