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
  User,
  UserPreferences,
  UserUsage,
  Workspace,
  WorkspaceConnectionPrefs,
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
  User,
  UserPreferences,
  UserUsage,
  Workspace,
  WorkspaceConnectionPrefs,
  WorkspaceEvent,
  WorkspaceProtocol,
  WorkspaceRuntime,
  WorkspaceTemplate,
};
// Value re-exports (generated constants, not types).
export { ClipboardLockParams, ClipboardLockPolicy } from './types.gen';

export type Theme = 'light' | 'dark' | 'system';

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

