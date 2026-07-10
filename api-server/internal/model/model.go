// Package model holds the platform's persisted and API-facing entities.
package model

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/auth"
)

// UserPreferences is the user-owned UI settings blob (JSON column).
type UserPreferences struct {
	// OpenWorkspaceInNewTab: nil means "never asked" — the portal shows
	// the choice dialog on first open and persists the answer.
	OpenWorkspaceInNewTab *bool `json:"openWorkspaceInNewTab,omitempty"`
	// Language is the preferred UI locale (e.g. "en", "fr").
	Language string `json:"language,omitempty"`
	// Theme is "light" or "dark"; empty follows the system preference.
	Theme string `json:"theme,omitempty"`
	// WorkspaceFolders maps workspace ID → folder name, the user's own
	// portal grouping ("infra", "dev", ...). Purely presentational.
	WorkspaceFolders map[string]string `json:"workspaceFolders,omitempty"`
	// WorkspaceSettings stores per-workspace connection choices; the
	// server still validates them against the template at connect time.
	WorkspaceSettings map[string]WorkspaceConnectionPrefs `json:"workspaceSettings,omitempty"`
}

// WorkspaceConnectionPrefs is the user's saved connection tuning for one
// workspace: preferred protocol and guacd parameter overrides.
type WorkspaceConnectionPrefs struct {
	Protocol string            `json:"protocol,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
}

// User is a platform account (local auth).
type User struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"displayName,omitempty"`
	Email         string     `json:"email,omitempty"`
	PasswordHash  string     `json:"-"`
	Role          auth.Role  `json:"role"`
	Active        bool       `json:"active"`
	MaxWorkspaces int        `json:"maxWorkspaces"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastLoginAt   *time.Time `json:"lastLoginAt,omitempty"`
	// Groups mirrors the IdP OIDC groups claim: admin-editable
	// until SSO login refreshes it automatically. Drives WorkspacePolicy
	// and WorkspaceImage group matching.
	Groups []string `json:"groups,omitempty"`
	// Preferences is self-service UI state, editable via PATCH /me.
	Preferences UserPreferences `json:"preferences"`
}

// SessionKind says what a session's WorkspaceID points at.
const (
	SessionKindWorkspace = "workspace" // provisioned Workspace CR (UID)
	SessionKindRemote    = "remote"    // RemoteWorkspace row (ID)
)

// Session records one desktop connection through the proxy.
type Session struct {
	ID            string     `json:"id"`
	UserID        string     `json:"userId"`
	WorkspaceID   string     `json:"workspaceId"`
	WorkspaceName string     `json:"workspaceName"`
	Protocol      string     `json:"protocol"`
	ClientIP      string     `json:"clientIp,omitempty"`
	StartedAt     time.Time  `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`
	// Params are the user's connect-time guacd parameter overrides,
	// already validated against the template's userParams allow-list.
	Params map[string]string `json:"params,omitempty"`
	// Kind distinguishes provisioned-workspace sessions from remote-
	// workspace sessions (empty = workspace, for pre-migration rows).
	Kind string `json:"kind,omitempty"`
}

// RemoteWorkspace is a user-registered machine OUTSIDE the cluster,
// reachable through guacd. It shares nothing with provisioned
// workspaces: no template, no operator lifecycle, no compute. The
// credentials live in the Kubernetes Secret named SecretName (one per
// row), never in the database or this struct.
// RemoteProtocol is one endpoint a remote machine serves — the same
// shape the frontend already consumes for provisioned workspaces
// (name/port/default), so cards, forms and the protocol switch treat
// both kinds identically.
type RemoteProtocol struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
	// Default marks the protocol used when the user picks none.
	Default bool `json:"default,omitempty"`
	// Params are guacd parameters for THIS protocol (registry-gated).
	Params map[string]string `json:"params,omitempty"`
}

type RemoteWorkspace struct {
	ID       string `json:"id"`
	OwnerID  string `json:"ownerId"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	// Port/Protocol/Params mirror the DEFAULT entry of Protocols — kept
	// for API and storage compatibility with single-protocol clients.
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
	// Protocols are every endpoint the machine serves. Empty on legacy
	// rows: EffectiveProtocols synthesizes the single legacy entry.
	Protocols []RemoteProtocol `json:"protocols,omitempty"`
	// MACAddress enables Wake-on-LAN when set (canonical lower-case,
	// colon-separated). Empty = no WoL.
	MACAddress string `json:"macAddress,omitempty"`
	// Params are guacd connection parameters, validated against the
	// platform registry (non-platform tiers only).
	Params map[string]string `json:"params,omitempty"`
	// SecretName is internal plumbing — never serialized.
	SecretName string `json:"-"`
	// CredentialKeys lists which credential fields are stored
	// (username/password/private-key/passphrase), so the UI can display
	// "credentials stored" without any Secret access.
	CredentialKeys []string  `json:"credentialKeys,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// EffectiveProtocols returns the declared endpoints, or the single
// legacy entry for rows created before multi-protocol support.
func (rw *RemoteWorkspace) EffectiveProtocols() []RemoteProtocol {
	if len(rw.Protocols) > 0 {
		return rw.Protocols
	}
	return []RemoteProtocol{{Name: rw.Protocol, Port: rw.Port, Default: true, Params: rw.Params}}
}

// DefaultProtocol returns the endpoint used when the user picks none.
func (rw *RemoteWorkspace) DefaultProtocol() RemoteProtocol {
	protos := rw.EffectiveProtocols()
	for _, p := range protos {
		if p.Default {
			return p
		}
	}
	return protos[0]
}

// ProtocolNamed returns the endpoint with the given name, or nil.
func (rw *RemoteWorkspace) ProtocolNamed(name string) *RemoteProtocol {
	protos := rw.EffectiveProtocols()
	for i := range protos {
		if protos[i].Name == name {
			return &protos[i]
		}
	}
	return nil
}

// AuditLog is one append-only audit trail entry.
type AuditLog struct {
	ID            string    `json:"id"`
	OccurredAt    time.Time `json:"occurredAt"`
	ActorID       string    `json:"actorId,omitempty"`
	ActorUsername string    `json:"actorUsername,omitempty"`
	Action        string    `json:"action"`
	ResourceType  string    `json:"resourceType"`
	ResourceID    string    `json:"resourceId,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	ClientIP      string    `json:"clientIp,omitempty"`
}

// Workspace is the API projection of a Workspace CR.
type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	TemplateRef string `json:"templateRef"`
	OwnerID     string `json:"ownerId"`
	// OwnerUsername is resolved for admin listings only (the fleet view
	// groups by owner); best-effort — empty when the owner is gone.
	OwnerUsername string    `json:"ownerUsername,omitempty"`
	Phase         string    `json:"phase"`
	OS            string    `json:"os,omitempty"`
	Protocol      string    `json:"protocol,omitempty"`
	Paused        bool      `json:"paused"`
	Message       string    `json:"message,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	// Namespace is where the workloads run (empty = platform namespace)
	// and WorkloadName the frozen Deployment/Service name — display-only.
	Namespace    string `json:"namespace,omitempty"`
	WorkloadName string `json:"workloadName,omitempty"`
	// Protocols the workspace serves, with the user-tunable guacd
	// parameter names per protocol (resolved from the template).
	Protocols []WorkspaceProtocol `json:"protocols,omitempty"`
	// Schedule is the effective uptime/downtime schedule (override or
	// template), so the UI can show and edit it.
	Schedule *waasv1alpha1.WorkspaceSchedule `json:"schedule,omitempty"`
	// NextTransition is the next planned lifecycle change (from status).
	NextTransition *ScheduledTransition `json:"nextTransition,omitempty"`
	// TemplateDrifted: the template changed since this workspace's
	// workload was built; the new shape applies at the next resume
	// (docs/adr/0001) — the card shows a "will restart with updates"
	// notice.
	TemplateDrifted bool `json:"templateDrifted,omitempty"`
	// HomeVolume describes the user-state volume, for the deletion
	// dialog ("volume X (10Gi) will be deleted — keep it?").
	HomeVolume *HomeVolumeInfo `json:"homeVolume,omitempty"`
	// Runtime is the workspace's CURRENT admitted deviations (env,
	// placement, sizing) — what the runtime settings tab edits through
	// PATCH /workspaces/{id}/overrides.
	Runtime *WorkspaceRuntime `json:"runtime,omitempty"`
}

// WorkspaceRuntime mirrors the workspace's runtime-reconfigurable
// overrides. PATCH /workspaces/{id}/overrides replaces each PROVIDED
// field wholesale; the change reaches the live desktop at the next
// scale-up boundary or on manual reload (docs/adr/0001).
type WorkspaceRuntime struct {
	Env          []corev1.EnvVar     `json:"env,omitempty"`
	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
	Tolerations  []corev1.Toleration `json:"tolerations,omitempty"`
	// Resources is the user-chosen sizing ({"cpu","memory"} quantities),
	// empty when the template sizing applies.
	Resources map[string]string `json:"resources,omitempty"`
}

// HomeVolumeInfo is the display projection of a workspace's home volume.
type HomeVolumeInfo struct {
	Name string `json:"name"`
	Size string `json:"size,omitempty"`
}

// RetainedVolume is a home volume kept from a deleted workspace: still
// the user's property, still counted against their storage quota, until
// deleted or reattached to a new workspace.
type RetainedVolume struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Size      string `json:"size"`
	OwnerID   string `json:"ownerId"`
	// OwnerUsername is resolved for admin listings only (the fleet view
	// groups by owner); best-effort — empty when the owner is gone.
	OwnerUsername   string     `json:"ownerUsername,omitempty"`
	OriginWorkspace string     `json:"originWorkspace,omitempty"`
	RetainedAt      *time.Time `json:"retainedAt,omitempty"`
}

// ScheduledTransition is the next planned up/down change of a workspace.
type ScheduledTransition struct {
	Time time.Time `json:"time"`
	Up   bool      `json:"up"`
}

// RemoteWorkspaceAdmin is one row of the admin fleet's remote-workspaces
// tab: metadata + owner + last connection (never credentials).
type RemoteWorkspaceAdmin struct {
	ID              string     `json:"id"`
	OwnerID         string     `json:"ownerId"`
	OwnerUsername   string     `json:"ownerUsername,omitempty"`
	Name            string     `json:"name"`
	Hostname        string     `json:"hostname"`
	Port            int32      `json:"port"`
	Protocol        string     `json:"protocol"`
	MACAddress      string     `json:"macAddress,omitempty"`
	HasCredentials  bool       `json:"hasCredentials"`
	LastConnectedAt *time.Time `json:"lastConnectedAt,omitempty"`
	ActiveNow       bool       `json:"activeNow"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// WorkspaceProtocol is one connection option of a workspace.
type WorkspaceProtocol struct {
	Name    string `json:"name"`
	Port    int32  `json:"port,omitempty"`
	Default bool   `json:"default,omitempty"`
	// Params are the template's locked guacd parameters (template views
	// only; workspace listings omit them).
	Params map[string]string `json:"params,omitempty"`
	// UserParams is the template's connect-time delegation list AS
	// CONFIGURED: exact parameter names and/or cat: category selectors
	// (cat:audio). The template editor edits this raw list — cat: intact,
	// so it can tell a category delegated wholesale from names picked
	// one by one.
	UserParams []string `json:"userParams,omitempty"`
	// ResolvedUserParams is UserParams expanded server-side against the
	// parameter registry into the flat set of names actually overridable
	// at connect time (cat: selectors resolved, platform tier excluded).
	// Connect-time forms consume THIS list — the frontend never parses
	// cat: syntax itself.
	ResolvedUserParams []string `json:"resolvedUserParams,omitempty"`
	// CredentialsSecretRef names the credentials Secret (reference only,
	// never its content).
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`
	// ExposeAudioPort: the template opens the workspace's PulseAudio
	// port (4713) on the pod and Service, so guacd's enable-audio
	// parameter actually reaches an audio server (vnc only).
	ExposeAudioPort bool `json:"exposeAudioPort,omitempty"`
}

// WorkspaceTemplate is the API projection of a WorkspaceTemplate CR.
type WorkspaceTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	OS          string `json:"os"`
	Image       string `json:"image"`
	Port        int32  `json:"port,omitempty"`
	HomeSize    string `json:"homeSize,omitempty"`
	// HomeMountPath is where the home volume is mounted (default
	// /home/user; kasmweb images expect /home/kasm-user).
	HomeMountPath string `json:"homeMountPath,omitempty"`
	// KasmVNCConfig is the admin's ~/.vnc/kasmvnc.yaml content, merged
	// key-by-key over the image's own defaults by KasmVNC (unspecified
	// keys inherit the image default) and mounted read-only in the
	// workspace. The clipboard DLP keys are policy-owned and rejected
	// here — see https://kasmweb.com/kasmvnc/docs/latest/configuration.html
	KasmVNCConfig string            `json:"kasmvncConfig,omitempty"`
	Requests      map[string]string `json:"requests,omitempty"`
	Limits        map[string]string `json:"limits,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	// Workload is the workload kind stamping desktops (Deployment,
	// StatefulSet or Pod).
	Workload string `json:"workload,omitempty"`
	// WorkloadSpec is the CR's workload block verbatim (pod-spec
	// passthrough), for the template editor's advanced section.
	WorkloadSpec *waasv1alpha1.WorkspaceWorkload `json:"workloadSpec,omitempty"`
	// Env is the CR's env verbatim (valueFrom included) so the editor can
	// round-trip it. Secret VALUES never appear here — only references.
	Env []corev1.EnvVar `json:"env,omitempty"`
	// StorageClassName of the home volume, when pinned.
	StorageClassName string `json:"storageClassName,omitempty"`
	// Protocols the template declares (or the OS-derived legacy one).
	Protocols []WorkspaceProtocol `json:"protocols,omitempty"`
	// AllowedOverrides are the template fields plain users may override
	// at instantiation.
	AllowedOverrides []string `json:"allowedOverrides,omitempty"`
	// OverridesOwner is the username owning this template (may override
	// everything on workspaces stamped from it).
	OverridesOwner string `json:"overridesOwner,omitempty"`
	// Schedule is the CR's uptime/downtime schedule verbatim.
	Schedule *waasv1alpha1.WorkspaceSchedule `json:"schedule,omitempty"`
	// Placement is the CR's placement block verbatim (target-namespace
	// pattern, namespace metadata, cleanup policy).
	Placement *waasv1alpha1.WorkspacePlacement `json:"placement,omitempty"`
}

// CatalogImage is the API projection of a WorkspaceImage CR, already
// filtered down to what the requesting user may deploy.
type CatalogImage struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	// Registry approves every image under this prefix (exclusive with Image).
	Registry string `json:"registry,omitempty"`
	// TagPolicy: digest | tag | any (empty = tag).
	TagPolicy string `json:"tagPolicy,omitempty"`
	// ImagePullSecretRef names the registry pull-credentials Secret.
	ImagePullSecretRef string            `json:"imagePullSecretRef,omitempty"`
	Protocols          []string          `json:"protocols,omitempty"`
	Architectures      []string          `json:"architectures,omitempty"`
	Enabled            bool              `json:"enabled"`
	AllowedGroups      []string          `json:"allowedGroups,omitempty"`
	Defaults           map[string]string `json:"defaults,omitempty"`
	Min                map[string]string `json:"min,omitempty"`
	Max                map[string]string `json:"max,omitempty"`
	// Templates using this image, so the portal can go straight from
	// catalog card to "create workspace".
	Templates []string `json:"templates,omitempty"`
}

// QuotaStatus is "where do I stand" for one user: applied policy, hard
// limits, and current consumption — everything the portal needs to render
// "2/3 workspaces, 6 Gi RAM left".
type QuotaStatus struct {
	Policy         string            `json:"policy"`
	PolicyPriority int32             `json:"policyPriority"`
	MaxWorkspaces  *int32            `json:"maxWorkspaces,omitempty"`
	UsedWorkspaces int               `json:"usedWorkspaces"`
	Limits         map[string]string `json:"limits,omitempty"` // aggregate caps (cpu/memory/storage)
	Used           map[string]string `json:"used,omitempty"`   // current aggregates
	PerWorkspace   map[string]string `json:"perWorkspace,omitempty"`
	Defaults       map[string]string `json:"defaults,omitempty"`  // policy-proposed sizing (image defaults win)
	Lifecycle      map[string]string `json:"lifecycle,omitempty"` // idleSuspendAfter / maxLifetime
	// Features flags what the resolved policy opts the user into (e.g.
	// "remoteWorkspaces"); the UI hides gated tabs from it.
	Features map[string]bool `json:"features,omitempty"`
	// AllowedOverrides is the policy-level override allow-list (nil = the
	// template's own list applies alone).
	AllowedOverrides []string `json:"allowedOverrides,omitempty"`
	// RetainedVolumes/RetainedStorage break down how much of the storage
	// usage comes from volumes kept after workspace deletion (already
	// included in Used["storage"] — display detail, same server-side
	// computation as the enforcement).
	RetainedVolumes int    `json:"retainedVolumes,omitempty"`
	RetainedStorage string `json:"retainedStorage,omitempty"`
}

// PolicyModel is the API projection of a WorkspacePolicy CR for the
// admin console.
type PolicyModel struct {
	Name      string                `json:"name"`
	Priority  int32                 `json:"priority"`
	Subjects  []PolicySubject       `json:"subjects,omitempty"`
	Images    []string              `json:"images,omitempty"`
	Limits    PolicyLimitsModel     `json:"limits"`
	Lifecycle map[string]string     `json:"lifecycle,omitempty"`
	Clipboard *ClipboardPolicyModel `json:"clipboard,omitempty"`
	// Overrides is the policy-level restriction on template overrides
	// (nil = no restriction; empty list = all overrides forbidden).
	Overrides *PolicyOverridesModel `json:"overrides,omitempty"`
	// RemoteWorkspaces opts governed users into the remote workspaces
	// feature.
	RemoteWorkspaces bool `json:"remoteWorkspaces,omitempty"`
}

// PolicyOverridesModel mirrors the CRD overrides block.
type PolicyOverridesModel struct {
	// AllowedFields: the EMPTY list is semantic (every override
	// forbidden) and must serialize as [] — non-nil guaranteed by
	// policyToModel.
	AllowedFields []string `json:"allowedFields"`
}

// ClipboardPolicyModel mirrors the CRD clipboard block (nil = allowed).
type ClipboardPolicyModel struct {
	CopyFromWorkspace *bool `json:"copyFromWorkspace,omitempty"`
	PasteToWorkspace  *bool `json:"pasteToWorkspace,omitempty"`
}

// PolicySubject mirrors the CRD subject.
type PolicySubject struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// PolicyLimitsModel mirrors the CRD limits in string form.
type PolicyLimitsModel struct {
	MaxWorkspaces *int32            `json:"maxWorkspaces,omitempty"`
	PerWorkspace  map[string]string `json:"perWorkspace,omitempty"`
	Aggregate     map[string]string `json:"aggregate,omitempty"`
	Defaults      map[string]string `json:"defaults,omitempty"`
}

// EffectivePolicy is the admin debug view answering "which policy governs
// this user, and why": the resolved identity, every policy with its match
// outcome, and the winner — computed by the same pkg/policy code the
// admission webhook runs.
type EffectivePolicy struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	// Groups: non-nil guaranteed by EffectivePolicyOf's construction.
	Groups []string `json:"groups"`
	// Evaluated lists every policy in resolution order (priority desc).
	// non-nil guaranteed at construction (the report page iterates it
	// unguarded).
	Evaluated []EvaluatedPolicy `json:"evaluated"`
	// Effective is the winning policy, absent when nothing matches
	// (fail-closed: the user cannot create workspaces at all).
	Effective *PolicyModel `json:"effective,omitempty"`
	Warnings  []string     `json:"warnings,omitempty"`
	Denial    string       `json:"denial,omitempty"`
}

// EvaluatedPolicy is one policy's outcome during resolution.
type EvaluatedPolicy struct {
	Name     string `json:"name"`
	Priority int32  `json:"priority"`
	Matched  bool   `json:"matched"`
	// Via is the matching subject ("Group:nymphe:dev", "User:marc") or "*"
	// for a subjects-less catch-all.
	Via      string `json:"via,omitempty"`
	Selected bool   `json:"selected"`
}

// UserUsage is one row of the admin consumption view.
type UserUsage struct {
	UserID     string            `json:"userId"`
	Username   string            `json:"username,omitempty"`
	Groups     []string          `json:"groups,omitempty"`
	Policy     string            `json:"policy,omitempty"`
	Workspaces int               `json:"workspaces"`
	Used       map[string]string `json:"used,omitempty"`
}

// SessionCapabilities is what the user's policy allows in-session; the
// overlay reflects it, the proxy enforces it.
type SessionCapabilities struct {
	ClipboardCopy  bool `json:"clipboardCopy"`
	ClipboardPaste bool `json:"clipboardPaste"`
}

// WorkspaceEvent is one aggregated Kubernetes Event of a workspace (the
// CR itself or any managed child resource), pre-authorized server-side.
type WorkspaceEvent struct {
	// Type is Normal or Warning.
	Type       string    `json:"type"`
	Reason     string    `json:"reason"`
	Message    string    `json:"message"`
	ObjectKind string    `json:"objectKind"`
	ObjectName string    `json:"objectName"`
	Count      int32     `json:"count"`
	FirstSeen  time.Time `json:"firstSeen"`
	LastSeen   time.Time `json:"lastSeen"`
}

// ConnectionInfo is what the WebSocket proxy needs to reach a desktop. It is
// only ever served on the internal service-to-service endpoint.
type ConnectionInfo struct {
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname"`
	Port     int32  `json:"port"`
	Password string `json:"password,omitempty"`
	Username string `json:"username,omitempty"`
	// Params are extra guacd connection parameters: the template's locked
	// params merged with the session's user overrides (user wins only on
	// allow-listed names).
	Params map[string]string `json:"params,omitempty"`
}
