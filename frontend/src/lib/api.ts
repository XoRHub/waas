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
  const token = useAuthStore.getState().accessToken;
  const headers = new Headers(init?.headers);
  headers.set('Content-Type', 'application/json');
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(path, { ...init, headers });

  if (response.status === 401 && useAuthStore.getState().accessToken) {
    // Token expired or revoked: drop local auth state.
    useAuthStore.getState().logout();
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
