// Facade over the API types. The app imports from '@/types' only:
//  - types.gen.ts   — GENERATED from api-server/internal/model (tygo,
//                     `make generate-types`, drift-checked in CI);
//  - types.manual.ts — hand-curated k8s passthrough shapes + Role;
//  - below          — types not yet migrated to generation (see the
//                     migration plan in the T8 commit).
import type {
  EnvVar,
  Role,
  Toleration,
  WorkspacePlacement,
  WorkspaceSchedule,
  WorkspaceWorkload,
} from './types.manual';
import type {
  AuditLog,
  CatalogImage,
  EffectivePolicy,
  EvaluatedPolicy,
  PolicyModel,
  PolicySubject,
  QuotaStatus,
  RemoteProtocol,
  RemoteWorkspace,
  RemoteWorkspaceAdmin,
  RetainedVolume,
  ScheduledTransition,
  Session,
  SessionCapabilities,
  UserUsage,
  Workspace,
  WorkspaceEvent,
  WorkspaceProtocol,
  WorkspaceRuntime,
  WorkspaceTemplate,
} from './types.gen';
export type {
  EnvVar,
  Role,
  Toleration,
  WorkspacePlacement,
  WorkspaceSchedule,
  WorkspaceWorkload,
  AuditLog,
  CatalogImage,
  EffectivePolicy,
  EvaluatedPolicy,
  PolicyModel,
  PolicySubject,
  QuotaStatus,
  RemoteProtocol,
  RemoteWorkspace,
  RemoteWorkspaceAdmin,
  RetainedVolume,
  ScheduledTransition,
  Session,
  SessionCapabilities,
  UserUsage,
  Workspace,
  WorkspaceEvent,
  WorkspaceProtocol,
  WorkspaceRuntime,
  WorkspaceTemplate,
};

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
  /** IdP (OIDC) group mirror: synced at every SSO login, admin-editable. */
  groups?: string[];
  preferences?: UserPreferences;
}

export type WorkspacePhase =
  'Pending' | 'Provisioning' | 'Running' | 'Paused' | 'Stopped' | 'Failed' | 'Terminating';

// ---- Platform metadata (GET /api/v1/meta/protocols) ----
// The guacd parameter registry, served verbatim from the operator's
// single source of truth; every form derives from it.

export type ParamTier = 'ui' | 'advanced' | 'platform';
export type ParamKind = 'string' | 'bool' | 'int' | 'enum';
export type ParamCategory =
  | 'display'
  | 'audio'
  | 'input'
  | 'clipboard'
  | 'session'
  | 'security'
  | 'connection';

export interface ParamMeta {
  name: string;
  protocols: string[];
  kind: ParamKind;
  enum?: string[];
  min?: number;
  max?: number;
  default?: string;
  tier: ParamTier;
  /** Thematic form section; the payload arrives already sorted by it. */
  category: ParamCategory;
  /** Toggleable mid-session; everything else needs a reconnect. */
  live: boolean;
  description: string;
}

export interface ProtocolMeta {
  name: string;
  params: ParamMeta[];
}

/** One env var of a template — alias of the k8s EnvVar passthrough
 * (references only, never secret values). */
export type TemplateEnvVar = EnvVar;

export interface LoginResult {
  accessToken: string;
  expiresAt: string;
  user: User;
}

export interface WorkspaceEventsPayload {
  events: WorkspaceEvent[];
  /** Server-driven refresh cadence for the panel. */
  pollIntervalSeconds: number;
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

// ---- Remote workspaces (out-of-cluster machines via guacd) ----

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

/** Which login methods the login page should offer. */
export interface AuthProviders {
  local: boolean;
  oidc: {
    enabled: boolean;
    name?: string;
    startUrl?: string;
  };
}

