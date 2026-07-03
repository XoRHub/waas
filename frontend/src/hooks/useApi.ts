import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type {
  AuditLog,
  ConnectResult,
  LoginResult,
  Session,
  User,
  Workspace,
  WorkspaceTemplate,
} from '@/types';

// Workspace phases move fast while provisioning; poll while any workspace
// is in a transient state.
const TRANSIENT_PHASES = new Set(['Pending', 'Provisioning', 'Terminating']);

export function useWorkspaces() {
  return useQuery({
    queryKey: ['workspaces'],
    queryFn: () => api.get<Workspace[]>('/api/v1/workspaces'),
    refetchInterval: (query) => {
      const items = query.state.data?.data ?? [];
      return items.some((ws) => TRANSIENT_PHASES.has(ws.phase)) ? 3000 : 15000;
    },
  });
}

export function useTemplates() {
  return useQuery({
    queryKey: ['workspace-templates'],
    queryFn: () => api.get<WorkspaceTemplate[]>('/api/v1/workspace-templates'),
  });
}

export function useUsers() {
  return useQuery({
    queryKey: ['users'],
    queryFn: () => api.get<User[]>('/api/v1/users?page_size=200'),
  });
}

export function useAuditLogs() {
  return useQuery({
    queryKey: ['audit-logs'],
    queryFn: () => api.get<AuditLog[]>('/api/v1/audit-logs?page_size=100'),
  });
}

export function useSessions() {
  return useQuery({
    queryKey: ['sessions'],
    queryFn: () => api.get<Session[]>('/api/v1/sessions?page_size=100'),
  });
}

export function useLogin() {
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      api.post<LoginResult>('/api/v1/auth/login', input),
  });
}

export function useCreateWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { templateRef: string; name?: string; displayName?: string }) =>
      api.post<Workspace>('/api/v1/workspaces', input),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['workspaces'] }),
  });
}

export function useWorkspaceAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'pause' | 'resume' }) =>
      api.post<Workspace>(`/api/v1/workspaces/${id}/${action}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['workspaces'] }),
  });
}

export function useDeleteWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.delete<void>(`/api/v1/workspaces/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['workspaces'] }),
  });
}

export function useConnectWorkspace() {
  return useMutation({
    mutationFn: (id: string) => api.post<ConnectResult>(`/api/v1/workspaces/${id}/connect`),
  });
}

export interface TemplateInput {
  name: string;
  displayName: string;
  description?: string;
  os: string;
  image: string;
  homeSize?: string;
}

export function useSaveTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ isNew, input }: { isNew: boolean; input: TemplateInput }) =>
      isNew
        ? api.post<WorkspaceTemplate>('/api/v1/workspace-templates', input)
        : api.put<WorkspaceTemplate>(`/api/v1/workspace-templates/${input.name}`, input),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['workspace-templates'] }),
  });
}

export function useDeleteTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.delete<void>(`/api/v1/workspace-templates/${name}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['workspace-templates'] }),
  });
}

export interface CreateUserInput {
  username: string;
  email?: string;
  password: string;
  role: string;
  maxWorkspaces?: number;
}

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateUserInput) => api.post<User>('/api/v1/users', input),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['users'] }),
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.delete<void>(`/api/v1/users/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['users'] }),
  });
}
