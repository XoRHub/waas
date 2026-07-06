export type Role = 'admin' | 'user';

export type Theme = 'light' | 'dark' | 'system';

/** Saved connection tuning for one workspace (validated server-side). */
export interface WorkspaceConnectionPrefs {
  protocol?: string;
  params?: Record<string, string>;
}

export interface UserPreferences {
  /** null/undefined = never asked: the portal shows the choice dialog. */
  openWorkspaceInNewTab?: boolean | null;
  language?: string;
  /** 'light' | 'dark'; empty/undefined follows the system. */
  theme?: string;
  /** workspace ID → folder name (the portal's user-defined grouping). */
  workspaceFolders?: Record<string, string>;
  /** workspace ID → saved protocol/params choices. */
  workspaceSettings?: Record<string, WorkspaceConnectionPrefs>;
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
  /** Authentik group mirror: synced at every SSO login, admin-editable. */
  groups?: string[];
  preferences?: UserPreferences;
}

export type WorkspacePhase =
  | 'Pending'
  | 'Provisioning'
  | 'Running'
  | 'Stopped'
  | 'Failed'
  | 'Terminating';

/** One connection option of a workspace, with its user-tunable guacd params. */
export interface WorkspaceProtocol {
  name: string;
  port?: number;
  default?: boolean;
  /** Template-locked guacd params (template views only). */
  params?: Record<string, string>;
  userParams?: string[];
  /** Name of the credentials Secret (reference only). */
  credentialsSecretRef?: string;
}

// ---- Platform metadata (GET /api/v1/meta/protocols) ----
// The guacd parameter registry, served verbatim from the operator's
// single source of truth; every form derives from it.

export type ParamTier = 'ui' | 'advanced' | 'platform';
export type ParamKind = 'string' | 'bool' | 'int' | 'enum';

export interface ParamMeta {
  name: string;
  protocols: string[];
  kind: ParamKind;
  enum?: string[];
  min?: number;
  max?: number;
  default?: string;
  tier: ParamTier;
  /** Toggleable mid-session; everything else needs a reconnect. */
  live: boolean;
  description: string;
}

export interface ProtocolMeta {
  name: string;
  params: ParamMeta[];
}

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
  protocols?: WorkspaceProtocol[];
}

/** One env var of a template — the CR type verbatim (references only,
 * never secret values). */
export interface TemplateEnvVar {
  name: string;
  value?: string;
  valueFrom?: {
    secretKeyRef?: { name: string; key: string };
    configMapKeyRef?: { name: string; key: string };
  };
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
  storageClassName?: string;
  requests?: Record<string, string>;
  limits?: Record<string, string>;
  createdAt: string;
  workload?: string;
  workloadSpec?: Record<string, unknown>;
  env?: TemplateEnvVar[];
  protocols?: WorkspaceProtocol[];
  allowedOverrides?: string[];
  overridesOwner?: string;
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

/** What the user's policy allows in-session — the overlay reflects it,
 * the proxy enforces it. */
export interface SessionCapabilities {
  clipboardCopy: boolean;
  clipboardPaste: boolean;
}

export interface ConnectResult {
  sessionId: string;
  connectionToken: string;
  protocol: string;
  capabilities?: SessionCapabilities;
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

/** Which login methods the login page should offer. */
export interface AuthProviders {
  local: boolean;
  oidc: {
    enabled: boolean;
    name?: string;
    startUrl?: string;
  };
}

/** One policy's outcome in the effective-policy debug view. */
export interface EvaluatedPolicy {
  name: string;
  priority: number;
  matched: boolean;
  via?: string;
  selected: boolean;
}

/** Admin debug view: which policy governs a user, and why. */
export interface EffectivePolicy {
  userId: string;
  username: string;
  groups?: string[];
  evaluated: EvaluatedPolicy[];
  effective?: PolicyModel;
  warnings?: string[];
  denial?: string;
}

export interface UserUsage {
  userId: string;
  username?: string;
  groups?: string[];
  policy?: string;
  workspaces: number;
  used?: Record<string, string>;
}
