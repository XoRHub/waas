// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, renderHook } from '@testing-library/react';
import { createApiMock } from '@/test/apiMock';
import { useSessionResize } from './useSessionResize';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

/** jsdom has no ResizeObserver: capture callbacks, fire them by hand. */
class FakeResizeObserver {
  static instances: FakeResizeObserver[] = [];
  observed: Element[] = [];
  disconnected = false;
  constructor(private cb: ResizeObserverCallback) {
    FakeResizeObserver.instances.push(this);
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  unobserve() {}
  disconnect() {
    this.disconnected = true;
  }
  fire() {
    this.cb([], this as unknown as ResizeObserver);
  }
}
vi.stubGlobal('ResizeObserver', FakeResizeObserver);

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  apiMock.api.post.mockClear();
  FakeResizeObserver.instances = [];
});

function sizedContainer(width = 1280, height = 720) {
  const el = document.createElement('div');
  Object.defineProperty(el, 'clientWidth', { value: width, configurable: true });
  Object.defineProperty(el, 'clientHeight', { value: height, configurable: true });
  return el;
}

const attachOpts = { workspaceId: 'w1', kind: 'workspace' as const, protocol: 'vnc' };

describe('useSessionResize', () => {
  it('observes the container; resize events rescale immediately and POST once debounced', () => {
    const { result } = renderHook(() => useSessionResize());
    const container = sizedContainer();
    const onResize = vi.fn();
    result.current.attach(container, { ...attachOpts, onResize });

    const observer = FakeResizeObserver.instances[0];
    expect(observer.observed).toContain(container);

    observer.fire();
    observer.fire();
    expect(onResize).toHaveBeenCalledTimes(2);
    expect(apiMock.api.post).not.toHaveBeenCalled();

    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).toHaveBeenCalledTimes(1);
    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/resize', {
      width: 1280,
      height: 720,
    });
  });

  it('detach stops observing and drops the pending POST', () => {
    const { result } = renderHook(() => useSessionResize());
    result.current.attach(sizedContainer(), attachOpts);

    const observer = FakeResizeObserver.instances[0];
    observer.fire();
    result.current.detach();
    expect(observer.disconnected).toBe(true);

    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });

  it('re-attach (reconnect) replaces the previous observer', () => {
    const { result } = renderHook(() => useSessionResize());
    result.current.attach(sizedContainer(), attachOpts);
    result.current.attach(sizedContainer(), attachOpts);

    expect(FakeResizeObserver.instances).toHaveLength(2);
    expect(FakeResizeObserver.instances[0].disconnected).toBe(true);
    expect(FakeResizeObserver.instances[1].disconnected).toBe(false);
  });

  it('unmount cleans up like detach', () => {
    const { result, unmount } = renderHook(() => useSessionResize());
    result.current.attach(sizedContainer(), attachOpts);

    const observer = FakeResizeObserver.instances[0];
    observer.fire();
    unmount();
    expect(observer.disconnected).toBe(true);

    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });
});
