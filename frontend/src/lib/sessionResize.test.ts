import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApiMock } from '@/test/apiMock';
import { createSessionResizer } from './sessionResize';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  apiMock.api.post.mockClear();
});

const resizer = (over: Partial<Parameters<typeof createSessionResizer>[0]> = {}) =>
  createSessionResizer({ workspaceId: 'w1', kind: 'workspace', protocol: 'vnc', ...over });

describe('createSessionResizer', () => {
  it('debounces: a burst of reports produces ONE request with the last size', () => {
    const r = resizer();
    for (let w = 800; w <= 1200; w += 10) r.report(w, 900);
    expect(apiMock.api.post).not.toHaveBeenCalled();

    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).toHaveBeenCalledTimes(1);
    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/resize', {
      width: 1200,
      height: 900,
    });
  });

  it('never fires for kasmvnc, ssh or remote targets', () => {
    for (const r of [
      resizer({ protocol: 'kasmvnc' }),
      resizer({ protocol: 'ssh' }),
      resizer({ kind: 'remote', protocol: 'vnc' }),
    ]) {
      r.report(1920, 1080);
    }
    vi.advanceTimersByTime(2000);
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });

  it('rdp sessions fire like vnc ones', () => {
    resizer({ protocol: 'rdp' }).report(1024, 768);
    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/resize', {
      width: 1024,
      height: 768,
    });
  });

  it('does not repeat an already-sent size, but follows a new one', () => {
    const r = resizer();
    r.report(1920, 1080);
    vi.advanceTimersByTime(600);
    r.report(1920, 1080); // same: no new timer
    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).toHaveBeenCalledTimes(1);

    r.report(2560, 1440);
    vi.advanceTimersByTime(600);
    expect(apiMock.api.post).toHaveBeenCalledTimes(2);
  });

  it('cancel drops the pending request (unmount path)', () => {
    const r = resizer();
    r.report(1920, 1080);
    r.cancel();
    vi.advanceTimersByTime(2000);
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });

  it('ignores degenerate sizes', () => {
    const r = resizer();
    r.report(0, 0);
    r.report(-5, 400);
    vi.advanceTimersByTime(2000);
    expect(apiMock.api.post).not.toHaveBeenCalled();
  });
});
