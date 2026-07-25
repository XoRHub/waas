// @vitest-environment jsdom
import { createElement, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createApiMock } from '@/test/apiMock';
import { useAuthStore } from '@/stores/authStore';
import { useEvents } from './useEvents';
import type { User } from '@/types';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

/** jsdom has no EventSource: record the URLs, fire the callbacks by hand. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }
  close() {
    this.closed = true;
  }
}
vi.stubGlobal('EventSource', FakeEventSource);

// Distinctive on purpose: every URL is asserted NOT to contain it.
// The session is an httpOnly cookie now, so there is no bearer to leak
// into a URL — but the stream token must still be the ONLY credential
// that ever appears there.
const SIGNED_IN = { id: 'u1', username: 'marc', role: 'user' } as User;

// Hoisted so its identity is stable across renders — a fresh client per
// render would re-run the effect and reconnect on its own.
const queryClient = new QueryClient();
const wrapper = ({ children }: { children: ReactNode }) =>
  createElement(QueryClientProvider, { client: queryClient }, children);

const mount = () => renderHook(() => useEvents(), { wrapper });

/** URLs of the EventSources opened so far, in order. */
const openedUrls = () => FakeEventSource.instances.map((s) => s.url);

beforeEach(() => {
  vi.useFakeTimers();
  useAuthStore.setState({ user: SIGNED_IN, ready: true });
  let minted = 0;
  apiMock.api.post.mockImplementation(() =>
    Promise.resolve({ data: { streamToken: `stream-token-${++minted}` } }),
  );
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  apiMock.api.post.mockReset();
  FakeEventSource.instances = [];
  useAuthStore.setState({ user: null, ready: false });
});

describe('useEvents connect', () => {
  it('opens the stream with the minted token, never with the API bearer', async () => {
    mount();
    await vi.advanceTimersByTimeAsync(0);

    expect(apiMock.api.post).toHaveBeenCalledTimes(1);
    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/auth/stream-token');
    expect(openedUrls()).toEqual(['/api/v1/events?access_token=stream-token-1']);
    expect(openedUrls()[0]).toMatch(/access_token=stream-token-/);
  });

  it('stays offline while signed out', async () => {
    useAuthStore.setState({ user: null, ready: true });
    mount();
    await vi.advanceTimersByTimeAsync(0);

    expect(apiMock.api.post).not.toHaveBeenCalled();
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it('mints a fresh token on reconnect instead of replaying the URL', async () => {
    mount();
    await vi.advanceTimersByTimeAsync(0);
    FakeEventSource.instances[0].onerror?.();
    expect(FakeEventSource.instances[0].closed).toBe(true);

    await vi.advanceTimersByTimeAsync(1_000);

    expect(apiMock.api.post).toHaveBeenCalledTimes(2);
    expect(openedUrls()).toEqual([
      '/api/v1/events?access_token=stream-token-1',
      '/api/v1/events?access_token=stream-token-2',
    ]);
    for (const url of openedUrls()) {
      expect(url).toMatch(/access_token=stream-token-/);
    }
  });
});

describe('useEvents backoff', () => {
  it('keeps growing when a connection opens then drops right away', async () => {
    mount();
    await vi.advanceTimersByTimeAsync(0);

    // First cycle: accepted, dropped before it proved itself.
    FakeEventSource.instances[0].onopen?.();
    FakeEventSource.instances[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(FakeEventSource.instances).toHaveLength(2);

    // Second cycle: same, so the next retry must wait 2s, not 1s.
    FakeEventSource.instances[1].onopen?.();
    FakeEventSource.instances[1].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(FakeEventSource.instances).toHaveLength(2);

    await vi.advanceTimersByTimeAsync(1_000);
    expect(FakeEventSource.instances).toHaveLength(3);
  });

  it('resets only once a connection has held for 10s', async () => {
    mount();
    await vi.advanceTimersByTimeAsync(0);

    // Grow the backoff to 2s.
    FakeEventSource.instances[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(FakeEventSource.instances).toHaveLength(2);

    // This one holds long enough to count as healthy.
    FakeEventSource.instances[1].onopen?.();
    await vi.advanceTimersByTimeAsync(10_000);
    FakeEventSource.instances[1].onerror?.();

    await vi.advanceTimersByTimeAsync(999);
    expect(FakeEventSource.instances).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeEventSource.instances).toHaveLength(3);
  });
});

describe('useEvents cleanup', () => {
  it('closes the stream on unmount and never reconnects', async () => {
    const { unmount } = mount();
    await vi.advanceTimersByTimeAsync(0);

    unmount();
    expect(FakeEventSource.instances[0].closed).toBe(true);

    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(apiMock.api.post).toHaveBeenCalledTimes(1);
  });

  it('drops a token minted after unmount instead of opening a stream', async () => {
    let release!: (value: { data: { streamToken: string } }) => void;
    apiMock.api.post.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          release = resolve;
        }),
    );

    const { unmount } = mount();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeEventSource.instances).toHaveLength(0);

    unmount();
    release({ data: { streamToken: 'too-late' } });
    await vi.advanceTimersByTimeAsync(60_000);

    expect(FakeEventSource.instances).toHaveLength(0);
  });
});
