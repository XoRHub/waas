// @vitest-environment jsdom
import { useEffect } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders, signIn } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import type { ConnectionState } from '@/components/DesktopPane';
import type { Workspace } from '@/types';
import { ConnectPage } from './ConnectPage';

const workspace: Workspace = {
  id: 'w1',
  name: 'w1',
  templateRef: 'xfce',
  ownerId: 'u1',
  phase: 'Running',
  paused: false,
  createdAt: '2026-07-10T00:00:00Z',
  protocol: 'vnc',
  protocols: [{ name: 'vnc', default: true }],
};

const apiMock = createApiMock({
  '/api/v1/workspaces': [workspace],
  '/api/v1/meta/protocols': [],
});
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

// The page reads :id from the route; everything else from react-router
// (MemoryRouter, useNavigate) stays real.
vi.mock('react-router', async (importOriginal) => {
  const mod = await importOriginal<typeof import('react-router')>();
  return { ...mod, useParams: () => ({ id: 'w1' }) };
});

// Stub the Guacamole canvas: report "connected" so the leave bar renders.
vi.mock('@/components/DesktopPane', () => ({
  DesktopPane: ({ onStateChange }: { onStateChange: (s: ConnectionState) => void }) => {
    useEffect(() => {
      onStateChange('connected');
    }, [onStateChange]);
    return <div data-testid="desktop-pane" />;
  },
}));

describe('leave bar hit-box', () => {
  // Regression guard: the full-width wrapper used to default to
  // pointer-events auto and swallowed every click on the top band of the
  // remote desktop (e.g. the XFCE Applications menu, top-left), because
  // the label's translateY(-100%) hides it visually without shrinking
  // the wrapper's hit-test box. Clicks must pass through everywhere
  // except the pull-tab and the leave label itself.
  it('lets clicks through the wrapper, keeps the tab and button interactive', async () => {
    signIn({ username: 'marc' });
    renderWithProviders(<ConnectPage />);

    const button = await screen.findByRole('button', { name: 'Leave session' });

    const label = button.parentElement!;
    const wrapper = label.parentElement!;
    const pullTab = wrapper.firstElementChild!;

    expect(wrapper.className).toContain('pointer-events-none');
    expect(wrapper.className).toContain('inset-x-0');
    expect(pullTab.className).toContain('pointer-events-auto');
    expect(label.className).toContain('pointer-events-auto');
  });
});
