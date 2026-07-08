import { vi } from 'vitest';

/**
 * Test double for `@/lib/api` — the single funnel every hook's request
 * goes through. Use with vi.mock's factory (hoisting-safe):
 *
 *   const apiMock = createApiMock();
 *   vi.mock('@/lib/api', () => ({ get api() { return apiMock.api; } }));
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
  const mutate = () => vi.fn(() => Promise.resolve({ data: {} }));
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
