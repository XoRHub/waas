import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';
import type {
  AuditLog,
  CatalogImage,
  ConnectResult,
  LoginResult,
  PolicyModel,
  Session,
  QuotaStatus,
  User,
  UserPreferences,
  UserUsage,
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
    mutationFn: (input: {
      templateRef: string;
      name?: string;
      displayName?: string;
      resources?: { cpu: string; memory: string };
    }) => api.post<Workspace>('/api/v1/workspaces', input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] });
      void queryClient.invalidateQueries({ queryKey: ['quota'] });
    },
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

export interface UpdateProfileInput {
  displayName?: string;
  email?: string;
  preferences?: UserPreferences;
  currentPassword?: string;
  newPassword?: string;
}

// Updates the caller's own profile and refreshes the persisted auth user.
export function useUpdateProfile() {
  const setUser = useAuthStore((s) => s.setUser);
  return useMutation({
    mutationFn: (input: UpdateProfileInput) => api.patch<User>('/api/v1/me', input),
    onSuccess: (res) => setUser(res.data),
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

// ---- Governance ----

export function useCatalog() {
  return useQuery({
    queryKey: ['catalog'],
    queryFn: () => api.get<CatalogImage[]>('/api/v1/catalog'),
  });
}

export function useQuota() {
  return useQuery({
    queryKey: ['quota'],
    queryFn: () => api.get<QuotaStatus>('/api/v1/me/quota'),
    // Quota moves with every create/pause/delete; keep it fresh.
    refetchInterval: 15000,
  });
}

export function useAdminImages() {
  return useQuery({
    queryKey: ['admin-images'],
    queryFn: () => api.get<CatalogImage[]>('/api/v1/admin/images'),
  });
}

export function useToggleImage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      api.post<CatalogImage>(`/api/v1/admin/images/${name}/${enabled ? 'enable' : 'disable'}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['admin-images'] });
      void queryClient.invalidateQueries({ queryKey: ['catalog'] });
    },
  });
}

export function useAdminPolicies() {
  return useQuery({
    queryKey: ['admin-policies'],
    queryFn: () => api.get<PolicyModel[]>('/api/v1/admin/policies'),
  });
}

export function useUpsertPolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, body }: { name: string; body: unknown }) =>
      api.put<PolicyModel>(`/api/v1/admin/policies/${name}`, body),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['admin-policies'] }),
  });
}

export function useAdminUsage() {
  return useQuery({
    queryKey: ['admin-usage'],
    queryFn: () => api.get<UserUsage[]>('/api/v1/admin/usage'),
  });
}
