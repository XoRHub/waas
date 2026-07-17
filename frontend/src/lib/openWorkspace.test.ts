// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { NavigateFunction } from 'react-router';
import { openWorkspace } from './openWorkspace';

// jsdom implements neither startViewTransition nor matchMedia — each
// case stubs exactly what it needs.
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (document as { startViewTransition?: unknown }).startViewTransition;
});

const mediaQuery = (matches: boolean) =>
  vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches } as MediaQueryList));

describe('openWorkspace', () => {
  it('opens a new tab without navigating', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const navigate = vi.fn() as unknown as NavigateFunction;
    openWorkspace('/workspaces/w1/connect', true, navigate);
    expect(open).toHaveBeenCalledWith('/workspaces/w1/connect', '_blank');
    expect(navigate).not.toHaveBeenCalled();
  });

  it('navigates without a view transition when the browser has no support', () => {
    mediaQuery(false);
    const navigate = vi.fn() as unknown as NavigateFunction;
    openWorkspace('/workspaces/w1/connect', false, navigate);
    expect(navigate).toHaveBeenCalledWith('/workspaces/w1/connect', { viewTransition: false });
  });

  it('asks for a view transition when supported and motion is allowed', () => {
    mediaQuery(false);
    (document as { startViewTransition?: unknown }).startViewTransition = vi.fn();
    const navigate = vi.fn() as unknown as NavigateFunction;
    openWorkspace('/workspaces/w1/connect', false, navigate);
    expect(navigate).toHaveBeenCalledWith('/workspaces/w1/connect', { viewTransition: true });
  });

  it('stays instant under prefers-reduced-motion', () => {
    mediaQuery(true);
    (document as { startViewTransition?: unknown }).startViewTransition = vi.fn();
    const navigate = vi.fn() as unknown as NavigateFunction;
    openWorkspace('/workspaces/w1/connect', false, navigate);
    expect(navigate).toHaveBeenCalledWith('/workspaces/w1/connect', { viewTransition: false });
  });
});
