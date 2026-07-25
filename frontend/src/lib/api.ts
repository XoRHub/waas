import { useAuthStore } from '@/stores/authStore';
import type { ListMeta } from '@/types';

// RFC 7807 problem details, as returned by the API server.
export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
}

export class ApiError extends Error {
  readonly problem: Problem;

  constructor(problem: Problem) {
    super(problem.detail ?? problem.title);
    this.problem = problem;
  }
}

interface Envelope<T> {
  data: T;
  meta?: ListMeta;
}

async function request<T>(path: string, init?: RequestInit): Promise<Envelope<T>> {
  const headers = new Headers(init?.headers);
  headers.set('Content-Type', 'application/json');

  // The session travels in the httpOnly `waas_session` cookie, which this
  // code cannot read and does not need to: `same-origin` is what attaches
  // it. No Authorization header — that transport is for clients that are
  // not this browser.
  // Which session this request is about. Captured BEFORE it leaves, so a
  // late answer can be told apart from one about the session we hold now.
  const epoch = useAuthStore.getState().epoch;

  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' });

  if (response.status === 401) {
    const state = useAuthStore.getState();
    // Expired, revoked, or minted for a role that has since changed: the
    // server has already rejected this session, so drop our view of it
    // locally only. Calling logout() would revoke every session of the
    // account — turning one rejected request into a fleet-wide sign-out.
    //
    // The epoch check is what keeps a stale answer from doing that: the
    // boot probe leaves before anyone is signed in and is guaranteed to
    // 401, so on a cold server it can land AFTER the user has signed in
    // and would otherwise sign them straight back out.
    if (state.user && state.epoch === epoch) {
      state.clearLocal('rejected');
    }
  }
  if (!response.ok) {
    let problem: Problem = {
      type: 'about:blank',
      title: response.statusText,
      status: response.status,
    };
    try {
      problem = (await response.json()) as Problem;
    } catch {
      // non-JSON error body; keep the fallback problem
    }
    throw new ApiError(problem);
  }
  if (response.status === 204) {
    return { data: undefined as T };
  }
  return (await response.json()) as Envelope<T>;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, {
      method: 'POST',
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  put: <T>(path: string, body: unknown) =>
    request<T>(path, { method: 'PUT', body: JSON.stringify(body) }),
  patch: <T>(path: string, body: unknown) =>
    request<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
};
