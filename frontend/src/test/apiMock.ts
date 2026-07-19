import { vi } from 'vitest';

/**
 * Test double for `@/lib/api` — the single funnel every hook's request
 * goes through. Use with vi.mock's factory (hoisting-safe):
 *
 *   const apiMock = createApiMock();
 *   vi.mock('@/lib/api', async (importOriginal) => ({
 *     ...(await importOriginal<typeof import('@/lib/api')>()),
 *     get api() { return apiMock.api; },
 *   }));
 *
 * Spread the original module so the other real exports (ApiError, …)
 * survive — components under test may `instanceof` them.
 *
 * GET routes are declared as `path -> data`; mutations resolve with
 * `{ data: {} }` by default and record their calls for payload
 * assertions (`apiMock.api.post.mock.calls`).
 */
export function createApiMock(routes: Record<string, unknown> = {}) {
  const table = { ...routes };
  const get = vi.fn((path: string) => {
    if (path in table) {
      return Promise.resolve({ data: table[path] });
    }
    return Promise.reject(new Error(`unmocked GET ${path}`));
  });
  // Same (path, body) shape as the real api, so tests can replace the
  // implementation with a handler that reads its arguments.
  const mutate = () =>
    vi.fn<(path: string, body?: unknown) => Promise<{ data: unknown }>>(() =>
      Promise.resolve({ data: {} }),
    );
  return {
    /** Add or replace a GET route after creation. */
    route(path: string, data: unknown) {
      table[path] = data;
    },
    api: {
      get,
      post: mutate(),
      put: mutate(),
      patch: mutate(),
      delete: mutate(),
    },
  };
}
