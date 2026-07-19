import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { isTransient } from '@/lib/lifecycle';
import { useAuthStore } from '@/stores/authStore';
import type {
  WorkspaceEventsPayload,
  AuditLog,
  AuthProviders,
  CatalogImage,
  EffectivePolicy,
  EnvVar,
  LoginResult,
  ProtocolMeta,
  RemoteWorkspace,
  RemoteWorkspaceAdmin,
  RemoteWorkspaceInput,
  RetainedVolume,
  TemplateEnvVar,
  Toleration,
  PolicyModel,
  QuotaStatus,
  User,
  UserPreferences,
  UserUsage,
  Workspace,
  WorkspaceSchedule,
  WorkspaceTemplate,
  WorkspaceWorkload,
} from '@/types';

// Poll fast while any workspace is converging (provisioning, terminating,
// or a lifecycle action whose spec intent the operator has not reconciled
// yet — see lib/lifecycle), slow otherwise.
export function useWorkspaces() {
  return useQuery({
    queryKey: ['workspaces'],
    queryFn: () => api.get<Workspace[]>('/api/v1/workspaces'),
    refetchInterval: (query) => {
      const items = query.state.data?.data ?? [];
      return items.some(isTransient) ? 3000 : 15000;
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

export interface AuditQuery {
  page: number;
  pageSize?: number;
  actor?: string;
  action?: string;
  from?: string;
  to?: string;
}

// Server-side pagination: the filters travel with the query so switching
// pages keeps them, and the query key makes each (filters, page) cacheable.
export function useAuditLogs(q: AuditQuery) {
  const params = new URLSearchParams({
    page: String(q.page),
    page_size: String(q.pageSize ?? 25),
  });
  if (q.actor) params.set('actor', q.actor);
  if (q.action) params.set('action', q.action);
  if (q.from) params.set('from', q.from);
  if (q.to) params.set('to', q.to);
  return useQuery({
    queryKey: ['audit-logs', q],
    queryFn: () => api.get<AuditLog[]>(`/api/v1/audit-logs?${params.toString()}`),
    placeholderData: (prev) => prev,
  });
}

export function useLogin() {
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      api.post<LoginResult>('/api/v1/auth/login', input),
  });
}

export function useAuthProviders() {
  return useQuery({
    queryKey: ['auth-providers'],
    queryFn: () => api.get<AuthProviders>('/api/v1/auth/providers'),
    staleTime: Infinity,
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
      // Template deviations (protocol, env, schedule…); the admission
      // webhook is the single judge of what this creator may override.
      overrides?: {
        protocol?: string;
        env?: TemplateEnvVar[];
        schedule?: { timezone?: string; uptime?: string[]; downtime?: string[] };
        labels?: Record<string, string>;
        annotations?: Record<string, string>;
      };
      /** Reattach a retained volume as home (webhook-vetted). */
      homeVolumeName?: string;
      /** Create without starting: takes no running-quota slot. */
      paused?: boolean;
    }) => api.post<Workspace>('/api/v1/workspaces', input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] });
      void queryClient.invalidateQueries({ queryKey: ['quota'] });
    },
  });
}

// ---- Remote workspaces (policy-gated, see quota.features) ----

export function useRemoteWorkspaces(enabled: boolean) {
  return useQuery({
    queryKey: ['remote-workspaces'],
    queryFn: () => api.get<RemoteWorkspace[]>('/api/v1/remote-workspaces'),
    enabled,
    // Remote entries carry no lifecycle phase, but edits from another
    // tab/device should still land without a manual reload.
    refetchInterval: 30000,
  });
}

export function useSaveRemoteWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: RemoteWorkspaceInput }) =>
      id
        ? api.put<RemoteWorkspace>(`/api/v1/remote-workspaces/${id}`, input)
        : api.post<RemoteWorkspace>('/api/v1/remote-workspaces', input),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['remote-workspaces'] }),
  });
}

export function useDeleteRemoteWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.delete<void>(`/api/v1/remote-workspaces/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['remote-workspaces'] }),
  });
}

// Wake-on-LAN a remote workspace via the platform relay.
export function useWakeRemoteWorkspace() {
  return useMutation({
    mutationFn: (id: string) => api.post<void>(`/api/v1/remote-workspaces/${id}/wake`),
  });
}

export function useWorkspaceAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'pause' | 'resume' }) =>
      api.post<Workspace>(`/api/v1/workspaces/${id}/${action}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] });
      // Pause/resume moves the running-quota needle.
      void queryClient.invalidateQueries({ queryKey: ['quota'] });
    },
  });
}

// Manual reload: ONE immediate convergence boundary — the desktop
// restarts now on its pending configuration (template edit or override
// change). Deliberately not a pause/resume: it never touches the pause
// intent or the schedule conflict resolution (docs/adr/0001).
export function useReloadWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.post<Workspace>(`/api/v1/workspaces/${id}/reload`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['workspaces'] }),
  });
}

/** Runtime-reconfiguration payload: a PROVIDED field replaces the stored
 * override wholesale (empty clears it), absent fields stay untouched. */
export interface UpdateOverridesInput {
  env?: EnvVar[];
  nodeSelector?: Record<string, string>;
  tolerations?: Toleration[];
  resources?: Record<string, string>;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  /** An empty struct clears the override (back to the template's). */
  schedule?: WorkspaceSchedule;
}

// Reconfigures an instantiated workspace (env, node placement, sizing).
// The admission webhook is the judge of what the caller may override;
// the change reaches the desktop at the next boundary or manual reload.
export function useUpdateWorkspaceOverrides() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateOverridesInput }) =>
      api.patch<Workspace>(`/api/v1/workspaces/${id}/overrides`, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] });
      void queryClient.invalidateQueries({ queryKey: ['quota'] });
    },
  });
}

export function useDeleteWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    // keepVolume is EXPLICIT: the dialog always decides; the server keeps
    // the volume unless told otherwise (never a silent deletion).
    mutationFn: ({ id, keepVolume }: { id: string; keepVolume: boolean }) =>
      api.delete<void>(`/api/v1/workspaces/${id}?keepVolume=${keepVolume}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] });
      void queryClient.invalidateQueries({ queryKey: ['volumes'] });
      void queryClient.invalidateQueries({ queryKey: ['quota'] });
    },
  });
}

// ---- Admin fleet (fleet-wide list + delete ONLY; the live-session
// actions — connect, pause, overrides… — are strictly owner-only and
// deliberately have no admin route) ----

export function useAdminWorkspaces() {
  return useQuery({
    queryKey: ['admin-workspaces'],
    queryFn: () => api.get<Workspace[]>('/api/v1/admin/workspaces'),
    refetchInterval: (query) => {
      const items = query.state.data?.data ?? [];
      return items.some(isTransient) ? 3000 : 15000;
    },
  });
}

export function useAdminDeleteWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, keepVolume }: { id: string; keepVolume: boolean }) =>
      api.delete<void>(`/api/v1/admin/workspaces/${id}?keepVolume=${keepVolume}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['admin-workspaces'] });
      // Fleet deletes retain the home volume: it reappears on the
      // volumes tab right away.
      void queryClient.invalidateQueries({ queryKey: ['admin-volumes'] });
    },
  });
}

// ---- Retained volumes (home volumes kept after workspace deletion) ----

export function useVolumes() {
  return useQuery({
    queryKey: ['volumes'],
    queryFn: () => api.get<RetainedVolume[]>('/api/v1/volumes'),
  });
}

export function useDeleteVolume() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ namespace, name }: { namespace: string; name: string }) =>
      api.delete<void>(`/api/v1/volumes/${namespace}/${name}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['volumes'] });
      void queryClient.invalidateQueries({ queryKey: ['quota'] });
    },
  });
}

export function useAdminVolumes() {
  return useQuery({
    queryKey: ['admin-volumes'],
    queryFn: () => api.get<RetainedVolume[]>('/api/v1/admin/volumes'),
  });
}

export function useAdminDeleteVolume() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ namespace, name }: { namespace: string; name: string }) =>
      api.delete<void>(`/api/v1/admin/volumes/${namespace}/${name}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['admin-volumes'] }),
  });
}

// The effective kasmvnc.yaml the operator materialized for a workspace
// (admin template config + policy clipboard layer) — read-only display
// data; editing stays admin-only on the template. 404 (non-kasmvnc, or
// not reconciled yet) is a normal answer, not a retryable failure.
export function useWorkspaceKasmVNCConfig(workspaceId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['workspace-kasmvnc-config', workspaceId],
    queryFn: () => api.get<{ config: string }>(`/api/v1/workspaces/${workspaceId}/kasmvnc-config`),
    enabled,
    retry: false,
  });
}

export interface NamespacePlaceholder {
  token: string;
  source: string;
  description: string;
}

// Namespace pattern placeholders (contextual help of the pattern
// editor) — served from the naming engine, cached hard.
export function usePlaceholders() {
  return useQuery({
    queryKey: ['placeholders'],
    queryFn: () => api.get<NamespacePlaceholder[]>('/api/v1/meta/placeholders'),
    staleTime: Infinity,
  });
}

// The namespace a creation WOULD land in for the caller, resolved
// SERVER-SIDE (precedence template > global env > built-in) — the UI
// displays it, never computes it.
export function useNamespacePreview(templateRef: string, displayName: string) {
  const params = new URLSearchParams({ template: templateRef, displayName });
  return useQuery({
    queryKey: ['namespace-preview', templateRef, displayName],
    queryFn: () =>
      api.get<{ namespace: string }>(`/api/v1/workspaces/namespace-preview?${params.toString()}`),
    enabled: templateRef !== '',
    placeholderData: (prev) => prev,
  });
}

export interface OverrideFieldMeta {
  name: string;
  description: string;
}

// The governable override fields (policy/template allowedFields) with
// their semantics — served from the operator's single registry so the
// admin editors can never drift from what the server enforces.
export function useOverrideFields() {
  return useQuery({
    queryKey: ['override-fields'],
    queryFn: () => api.get<OverrideFieldMeta[]>('/api/v1/meta/override-fields'),
    staleTime: Infinity,
  });
}

// The guacd parameter registry — cached hard: it only changes with a
// platform deployment.
export function useProtocolMeta() {
  return useQuery({
    queryKey: ['protocol-meta'],
    queryFn: () => api.get<ProtocolMeta[]>('/api/v1/meta/protocols'),
    staleTime: Infinity,
  });
}

export interface TemplateProtocolInput {
  name: string;
  port: number;
  default?: boolean;
  params?: Record<string, string>;
  /** Raw delegation list as stored on the CR: exact names and/or cat:
   * category selectors (resolved server-side, never in the browser). */
  userParams?: string[];
  credentialsSecretRef?: string;
  /** vnc only: expose the workspace's PulseAudio port (4713). */
  exposeAudioPort?: boolean;
}

export interface TemplateInput {
  name: string;
  displayName: string;
  description?: string;
  /** Icon reference: https URL, file:<path>, or dashboard-icons slug.
   * Empty falls back to the catalog icon, then the OS icon. */
  logo?: string;
  os: string;
  image: string;
  homeSize?: string;
  /** Where the home volume mounts in the pod; empty = the platform
   * default (/home/waas_user). Applies to pods deployed AFTER the save. */
  homeMountPath?: string;
  // Raw ~/.vnc/kasmvnc.yaml override; only meaningful with a kasmvnc
  // protocol (the webhook enforces that). Clipboard DLP keys are
  // policy-owned and rejected server-side.
  kasmvncConfig?: string;
  storageClassName?: string;
  requests?: Record<string, string>;
  limits?: Record<string, string>;
  env?: TemplateEnvVar[];
  /** Parsed YAML mapping from the editor, or the CR block round-tripped
   * verbatim when the admin left it untouched. */
  workload?: WorkspaceWorkload | Record<string, unknown>;
  protocols?: TemplateProtocolInput[];
  overrides?: { allowedFields?: string[]; owner?: string };
  schedule?: { timezone?: string; uptime?: string[]; downtime?: string[] };
  placement?: {
    namespace?: string;
    namespaceLabels?: Record<string, string>;
    namespaceAnnotations?: Record<string, string>;
    cleanup?: string;
  };
  /** Home PVC metadata, e.g. Longhorn recurring-job enrollment; synced
   * in place by the operator (removals included). */
  homeVolume?: {
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
  };
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
  /** IdP (OIDC) groups seeded at creation (overwritten at first SSO login). */
  groups?: string[];
}

// Groups the platform already knows (policy Group subjects + existing
// users' groups) — the suggestions when assigning groups.
export function useKnownGroups() {
  return useQuery({
    queryKey: ['known-groups'],
    queryFn: () => api.get<string[]>('/api/v1/admin/groups'),
  });
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

export interface UpdateUserInput {
  email?: string;
  password?: string;
  role?: string;
  active?: boolean;
  maxWorkspaces?: number;
  groups?: string[];
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateUserInput }) =>
      api.patch<User>(`/api/v1/users/${id}`, input),
    onSuccess: (_res, { id }) => {
      void queryClient.invalidateQueries({ queryKey: ['users'] });
      void queryClient.invalidateQueries({ queryKey: ['effective-policy', id] });
    },
  });
}

// Admin debug: which policy governs this user, and why.
export function useEffectivePolicy(userId: string | null) {
  return useQuery({
    queryKey: ['effective-policy', userId],
    queryFn: () => api.get<EffectivePolicy>(`/api/v1/admin/users/${userId}/effective-policy`),
    enabled: userId !== null,
  });
}

// ---- Governance ----

export function useCatalog() {
  return useQuery({
    queryKey: ['catalog'],
    queryFn: () => api.get<CatalogImage[]>('/api/v1/catalog'),
  });
}

/**
 * Aggregated Kubernetes events of one workspace (CR + children),
 * authorized and sorted server-side. The refetch cadence is
 * server-driven: the response carries pollIntervalSeconds
 * (WAAS_EVENTS_POLL_INTERVAL), so operators tune it without a rebuild.
 */
export function useWorkspaceEvents(workspaceId: string) {
  return useQuery({
    queryKey: ['workspace-events', workspaceId],
    queryFn: () => api.get<WorkspaceEventsPayload>(`/api/v1/workspaces/${workspaceId}/events`),
    refetchInterval: (query) => (query.state.data?.data.pollIntervalSeconds ?? 10) * 1000,
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

// YAML scaffold (all schema fields) for a governance object, generated
// server-side from the PUT payload types — never a hand-maintained
// template. Cached hard: it only changes with a deployment.
export function useScaffold(kind: 'workspacepolicy' | 'workspaceimage') {
  return useQuery({
    queryKey: ['scaffold', kind],
    queryFn: () => api.get<{ scaffold: string }>(`/api/v1/meta/scaffold/${kind}`),
    staleTime: Infinity,
  });
}

export function useUpsertImage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, body }: { name: string; body: unknown }) =>
      api.put<CatalogImage>(`/api/v1/admin/images/${name}`, body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['admin-images'] });
      void queryClient.invalidateQueries({ queryKey: ['catalog'] });
    },
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

// useSyncImage forces an immediate catalog re-fetch of one entry instead
// of waiting for the server's periodic sync ticker (admin-only).
export function useSyncImage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.post<CatalogImage>(`/api/v1/admin/images/${name}/sync`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['admin-images'] });
      void queryClient.invalidateQueries({ queryKey: ['catalog'] });
    },
  });
}

export function useDeleteImage() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.delete<void>(`/api/v1/admin/images/${name}`),
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

export function useDeletePolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.delete<void>(`/api/v1/admin/policies/${name}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['admin-policies'] }),
  });
}

export function useAdminRemoteWorkspaces() {
  return useQuery({
    queryKey: ['admin-remote-workspaces'],
    queryFn: () => api.get<RemoteWorkspaceAdmin[]>('/api/v1/admin/remote-workspaces'),
    refetchInterval: 15000,
  });
}

export function useAdminUsage() {
  return useQuery({
    queryKey: ['admin-usage'],
    queryFn: () => api.get<UserUsage[]>('/api/v1/admin/usage'),
  });
}
