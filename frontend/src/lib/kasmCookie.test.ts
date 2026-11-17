// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApiMock } from '@/test/apiMock';
import { refreshKasmCookie, tokenTTL } from './kasmCookie';

const apiMock = createApiMock();
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  get api() {
    return apiMock.api;
  },
}));

/** An unsigned JWT whose payload carries exp (seconds). */
const jwt = (exp: number) => `h.${btoa(JSON.stringify({ exp })).replace(/=+$/, '')}.s`;

afterEach(() => vi.unstubAllGlobals());

describe('tokenTTL', () => {
  it('reads the remaining life from exp, 0 when unreadable', () => {
    const in5min = Math.floor(Date.now() / 1000) + 300;
    expect(tokenTTL(jwt(in5min))).toBeGreaterThan(290_000);
    expect(tokenTTL(jwt(in5min))).toBeLessThanOrEqual(300_000);
    expect(tokenTTL(jwt(Math.floor(Date.now() / 1000) - 10))).toBe(0);
    expect(tokenTTL('garbage')).toBe(0);
  });
});

describe('refreshKasmCookie', () => {
  it('mints a token for the open session and hands it to wwt', async () => {
    const token = jwt(Math.floor(Date.now() / 1000) + 300);
    apiMock.api.post.mockResolvedValue({
      data: { sessionId: 's1', connectionToken: token, protocol: 'kasmvnc' },
    });
    const fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    vi.stubGlobal('fetch', fetchMock);

    const ttl = await refreshKasmCookie('w1', 's1');

    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/sessions/s1/token');
    expect(fetchMock).toHaveBeenCalledWith(
      `/kasm/s1/waas/session?token=${encodeURIComponent(token)}`,
      expect.objectContaining({ credentials: 'same-origin' }),
    );
    expect(ttl).toBeGreaterThan(290_000);
  });

  it('fails when wwt refuses the token', async () => {
    apiMock.api.post.mockResolvedValue({
      data: { sessionId: 's1', connectionToken: 'tok', protocol: 'kasmvnc' },
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('no', { status: 401 }))),
    );

    await expect(refreshKasmCookie('w1', 's1')).rejects.toBe(401);
  });
});
