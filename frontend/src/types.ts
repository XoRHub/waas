export type Role = 'admin' | 'user';

export interface User {
  id: string;
  username: string;
  email?: string;
  role: Role;
  active: boolean;
  maxWorkspaces: number;
  createdAt: string;
  updatedAt: string;
  lastLoginAt?: string;
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
