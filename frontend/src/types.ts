export type Role = 'admin' | 'user';

export type Theme = 'light' | 'dark' | 'system';

/** Saved connection tuning for one workspace (validated server-side). */
export interface WorkspaceConnectionPrefs {
  protocol?: string;
  /** Params of the CHOSEN protocol — what the connect call sends. */
  params?: Record<string, string>;
  /**
   * Params kept per protocol, so the settings tabs round-trip values for
   * protocols other than the chosen one (quick-switching restores them).
   */
  paramsByProtocol?: Record<string, Record<string, string>>;
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
  | 'Paused'
  | 'Stopped'
  | 'Failed'
  | 'Terminating';

/** Uptime/downtime schedule (cron), timezone-explicit. */
export interface WorkspaceSchedule {
  timezone?: string;
  uptime?: string[];
  downtime?: string[];
}

/** Next planned lifecycle change of a workspace. */
export interface ScheduledTransition {
  /** RFC3339 instant. */
  time: string;
  /** true = start (uptime) edge, false = stop (downtime) edge. */
  up: boolean;
}

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
  schedule?: WorkspaceSchedule;
  nextTransition?: ScheduledTransition;
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
  schedule?: WorkspaceSchedule;
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
  /** Policy-gated feature flags (e.g. remoteWorkspaces). */
  features?: Record<string, boolean>;
  /** Policy-level override allow-list (undefined = template list alone). */
  allowedOverrides?: string[];
}

// ---- Remote workspaces (out-of-cluster machines via guacd) ----

/** One endpoint a remote machine serves — same shape as WorkspaceProtocol
 * for the fields the UI shares (name/port/default). */
export interface RemoteProtocol {
  name: string;
  port: number;
  default?: boolean;
  params?: Record<string, string>;
}

export interface RemoteWorkspace {
  id: string;
  ownerId: string;
  name: string;
  hostname: string;
  /** Legacy mirror of the default endpoint (older API shape). */
  port: number;
  protocol: string;
  /** Every endpoint the machine serves (empty on legacy rows). */
  protocols?: RemoteProtocol[];
  /** MAC for Wake-on-LAN (empty = no WoL). */
  macAddress?: string;
  params?: Record<string, string>;
  /** Which credential fields are stored (never their values). */
  credentialKeys?: string[];
  createdAt: string;
  updatedAt: string;
}

/** Admin fleet row for a remote workspace (metadata only, no credentials). */
export interface RemoteWorkspaceAdmin {
  id: string;
  ownerId: string;
  ownerUsername?: string;
  name: string;
  hostname: string;
  port: number;
  protocol: string;
  macAddress?: string;
  hasCredentials: boolean;
  lastConnectedAt?: string;
  activeNow: boolean;
  createdAt: string;
}

/** Write-only credential payload; omitted field = keep, '' = delete. */
export interface RemoteCredentialsInput {
  username?: string;
  password?: string;
  privateKey?: string;
  passphrase?: string;
}

export interface RemoteWorkspaceInput {
  name: string;
  hostname: string;
  /** Multi-endpoint shape; the legacy port/protocol/params fields stay
   * accepted server-side when this is empty. */
  protocols?: RemoteProtocol[];
  port?: number;
  protocol?: string;
  macAddress?: string;
  params?: Record<string, string>;
  credentials?: RemoteCredentialsInput;
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
  clipboard?: { copyFromWorkspace?: boolean; pasteToWorkspace?: boolean };
  /** Policy-level override restriction (empty list = none allowed). */
  overrides?: { allowedFields: string[] };
  /** Opt-in to the Remote Workspaces feature. */
  remoteWorkspaces?: boolean;
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
