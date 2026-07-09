// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { SessionTarget } from '@/lib/target';
import { SessionCard } from './SessionCard';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

afterEach(() => {
  vi.restoreAllMocks();
  apiMock.api.post.mockClear();
});

const target = (over: Partial<SessionTarget> = {}): SessionTarget => ({
  id: 'w1',
  kind: 'workspace',
  displayName: 'Marc box',
  subtitle: 'xfce',
  connectUrl: '/workspaces/w1/connect',
  protocols: [],
  defaultProtocol: 'vnc',
  templateDrifted: true,
  capabilities: {
    pause: true,
    wake: false,
    splitView: true,
    connectionSettings: true,
    editEndpoint: false,
    hasPhase: true,
    reload: true,
  },
  ...over,
});

const renderCard = (t: SessionTarget, phase: 'Running' | 'Paused' = 'Running') => {
  signIn({ username: 'marc' });
  renderWithProviders(
    <SessionCard
      target={t}
      phase={phase}
      buttons={<></>}
      onDelete={() => {}}
      deletePending={false}
    />,
  );
};

describe('SessionCard drift badge', () => {
  it('reloads a running reload-capable target after confirmation', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderCard(target());

    await userEvent.click(screen.getByRole('button', { name: /configuration changed/i }));

    expect(window.confirm).toHaveBeenCalled();
    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/reload');
  });

  it('does nothing when the confirmation is declined', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    renderCard(target());

    await userEvent.click(screen.getByRole('button', { name: /configuration changed/i }));

    expect(apiMock.api.post).not.toHaveBeenCalled();
  });

  it('stays informative-only while the workspace is not running', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderCard(target(), 'Paused');

    // The badge and its tooltip stay (the pending update is real), but a
    // down workspace converges at its next start — no reload to trigger.
    await userEvent.click(screen.getByRole('button', { name: /configuration changed/i }));

    expect(confirm).not.toHaveBeenCalled();
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });

  it('never offers the reload on targets without the capability', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderCard(
      target({ capabilities: { ...target().capabilities, reload: false, hasPhase: false } }),
    );

    await userEvent.click(screen.getByRole('button', { name: /configuration changed/i }));

    expect(confirm).not.toHaveBeenCalled();
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });

  it('shows no badge without pending drift', () => {
    renderCard(target({ templateDrifted: false }));
    expect(screen.queryByRole('button', { name: /configuration changed/i })).toBeNull();
  });
});
