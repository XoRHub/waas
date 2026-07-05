export type Role = 'admin' | 'user';

export interface UserPreferences {
  /** null/undefined = never asked: the portal shows the choice dialog. */
  openWorkspaceInNewTab?: boolean | null;
  language?: string;
}

export interface User {
  id: string;
  username: string;
  displayName?: string;
  email?: string;
  role: Role;
  active: boolean;
  maxWorkspaces: number;
  createdAt: string;
  updatedAt: string;
  lastLoginAt?: string;
  preferences?: UserPreferences;
}

export type WorkspacePhase =
  | 'Pending'
  | 'Provisioning'
  | 'Running'
  | 'Stopped'
  | 'Failed'
  | 'Terminating';

export interface Workspace {
  id: string;
  name: string;
  displayName?: string;
  templateRef: string;
  ownerId: string;
  phase: WorkspacePhase;
  os?: string;
  protocol?: string;
  paused: boolean;
  message?: string;
  createdAt: string;
}

export interface WorkspaceTemplate {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  os: string;
  image: string;
  port?: number;
  homeSize?: string;
  requests?: Record<string, string>;
  limits?: Record<string, string>;
  createdAt: string;
}

export interface AuditLog {
  id: string;
  occurredAt: string;
  actorId?: string;
  actorUsername?: string;
  action: string;
  resourceType: string;
  resourceId?: string;
  detail?: string;
  clientIp?: string;
}

export interface Session {
  id: string;
  userId: string;
  workspaceId: string;
  workspaceName: string;
  protocol: string;
  clientIp?: string;
  startedAt: string;
  endedAt?: string;
}

export interface LoginResult {
  accessToken: string;
  expiresAt: string;
  user: User;
}

export interface ConnectResult {
  sessionId: string;
  connectionToken: string;
  protocol: string;
}

export interface ListMeta {
  total: number;
  page: number;
  page_size: number;
}

// ---- Governance (catalog / quotas / policies) ----

export interface CatalogImage {
  name: string;
  displayName: string;
  description?: string;
  image: string;
  protocols: string[];
  architectures?: string[];
  enabled: boolean;
  allowedGroups?: string[];
  defaults?: Record<string, string>;
  min?: Record<string, string>;
  max?: Record<string, string>;
  templates?: string[];
}

export interface QuotaStatus {
  policy: string;
  policyPriority: number;
  maxWorkspaces?: number | null;
  usedWorkspaces: number;
  limits?: Record<string, string>;
  used?: Record<string, string>;
  perWorkspace?: Record<string, string>;
  defaults?: Record<string, string>;
  lifecycle?: Record<string, string>;
}

export interface PolicySubject {
  kind: 'User' | 'Group';
  name: string;
}

export interface PolicyModel {
  name: string;
  priority: number;
  subjects?: PolicySubject[];
  images?: string[];
  limits: {
    maxWorkspaces?: number | null;
    perWorkspace?: Record<string, string>;
    aggregate?: Record<string, string>;
    defaults?: Record<string, string>;
  };
  lifecycle?: Record<string, string>;
}

export interface UserUsage {
  userId: string;
  username?: string;
  groups?: string[];
  policy?: string;
  workspaces: number;
  used?: Record<string, string>;
}
