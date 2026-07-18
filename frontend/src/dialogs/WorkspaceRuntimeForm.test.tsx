// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { UpdateOverridesInput } from '@/hooks/useApi';
import type { Workspace } from '@/types';
import { WorkspaceRuntimeForm } from './WorkspaceRuntimeForm';

const template = (allowedOverrides: string[]) => ({
  name: 'xfce',
  displayName: 'XFCE',
  os: 'linux',
  allowedOverrides,
  schedule: { timezone: 'Europe/Paris', uptime: ['0 8 * * 1-5'] },
});

const apiMock = createApiMock({
  '/api/v1/catalog': [],
  '/api/v1/me/quota': { policy: 'default' },
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const workspace = (runtime?: Workspace['runtime']): Workspace =>
  ({
    id: 'w1',
    name: 'marc-box',
    templateRef: 'xfce',
    ownerId: 'u1',
    phase: 'Running',
    paused: false,
    createdAt: '2026-01-01T00:00:00Z',
    // The model's EFFECTIVE schedule (override ?? template).
    schedule: runtime?.schedule ?? { timezone: 'Europe/Paris', uptime: ['0 8 * * 1-5'] },
    runtime,
  }) as Workspace;

const renderForm = (ws: Workspace) => {
  const onApply = vi.fn();
  renderWithProviders(
    <>
      <WorkspaceRuntimeForm workspace={ws} formId="rt" onApply={onApply} />
      <button type="submit" form="rt">
        Apply
      </button>
    </>,
  );
  return onApply;
};

const submit = async () => userEvent.click(screen.getByRole('button', { name: 'Apply' }));

describe('WorkspaceRuntimeForm', () => {
  beforeEach(() => {
    signIn({ username: 'marc' });
    apiMock.route('/api/v1/workspace-templates', [
      template(['env', 'nodeSelector', 'tolerations', 'resources', 'metadata', 'schedule']),
    ]);
  });

  it('locks every group when the template delegates nothing', async () => {
    apiMock.route('/api/v1/workspace-templates', [template([])]);
    const onApply = renderForm(workspace({ labels: { team: 'blue' } }));

    expect(await screen.findAllByText(/not allowed by this template/)).toHaveLength(6);
    // Locked metadata still SHOWS the stored override, read-only.
    expect(screen.getByDisplayValue('team')).toBeDisabled();

    await submit();
    expect(onApply).toHaveBeenCalledWith(null);
  });

  it('untouched form sends nothing (null payload)', async () => {
    const onApply = renderForm(
      workspace({ labels: { team: 'blue' }, schedule: { timezone: 'Europe/Paris' } }),
    );
    await screen.findByDisplayValue('team');
    await submit();
    expect(onApply).toHaveBeenCalledWith(null);
  });

  it('metadata edits travel alone — only the changed fields', async () => {
    const onApply = renderForm(workspace({ labels: { team: 'blue' } }));
    // The rights arrive with the templates query: editable once loaded.
    await waitFor(() => expect(screen.getByDisplayValue('blue')).toBeEnabled());

    await userEvent.type(screen.getByDisplayValue('blue'), 's');
    await submit();
    const input = onApply.mock.calls[0][0] as UpdateOverridesInput;
    // env/schedule untouched: absent. Annotations untouched: absent.
    expect(input).toEqual({ labels: { team: 'blues' } });
  });

  it('clearing the schedule sends the empty struct (back to template)', async () => {
    const onApply = renderForm(
      workspace({ schedule: { timezone: 'Europe/Paris', uptime: ['0 9 * * *'] } }),
    );
    const tz = await screen.findByDisplayValue('Europe/Paris');
    await waitFor(() => expect(tz).toBeEnabled());
    await userEvent.clear(screen.getByDisplayValue('0 9 * * *'));
    await userEvent.clear(tz);
    await submit();
    expect(onApply).toHaveBeenCalledWith({ schedule: {} });
  });

  it('editing the schedule sends the full schedule', async () => {
    const onApply = renderForm(workspace(undefined));
    // Seeded from the effective (template) schedule.
    const uptime = await screen.findByDisplayValue('0 8 * * 1-5');
    await waitFor(() => expect(uptime).toBeEnabled());
    await userEvent.clear(uptime);
    await userEvent.type(uptime, '0 9 * * 1-5');
    await submit();
    expect(onApply).toHaveBeenCalledWith({
      schedule: { timezone: 'Europe/Paris', uptime: ['0 9 * * 1-5'] },
    });
  });
});
