// Package model holds the platform's persisted and API-facing entities.
package model

import (
	"time"

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
	// Groups mirrors the Authentik OIDC groups claim: admin-editable
	// until SSO login refreshes it automatically. Drives WorkspacePolicy
	// and WorkspaceImage group matching.
	Groups []string `json:"groups"`
	// Preferences is self-service UI state, editable via PATCH /me.
	Preferences UserPreferences `json:"preferences"`
}

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
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName,omitempty"`
	TemplateRef string    `json:"templateRef"`
	OwnerID     string    `json:"ownerId"`
	Phase       string    `json:"phase"`
	OS          string    `json:"os,omitempty"`
	Protocol    string    `json:"protocol,omitempty"`
	Paused      bool      `json:"paused"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// Protocols the workspace serves, with the user-tunable guacd
	// parameter names per protocol (resolved from the template).
	Protocols []WorkspaceProtocol `json:"protocols,omitempty"`
}

// WorkspaceProtocol is one connection option of a workspace.
type WorkspaceProtocol struct {
	Name    string `json:"name"`
	Port    int32  `json:"port,omitempty"`
	Default bool   `json:"default,omitempty"`
	// UserParams are the guacd parameter names the user may set at
	// connect time (from the template's allow-list).
	UserParams []string `json:"userParams,omitempty"`
}

// WorkspaceTemplate is the API projection of a WorkspaceTemplate CR.
type WorkspaceTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	Description string            `json:"description,omitempty"`
	OS          string            `json:"os"`
	Image       string            `json:"image"`
	Port        int32             `json:"port,omitempty"`
	HomeSize    string            `json:"homeSize,omitempty"`
	Requests    map[string]string `json:"requests,omitempty"`
	Limits      map[string]string `json:"limits,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	// Workload is the workload kind stamping desktops (Deployment,
	// StatefulSet or Pod).
	Workload string `json:"workload,omitempty"`
	// Protocols the template declares (or the OS-derived legacy one).
	Protocols []WorkspaceProtocol `json:"protocols,omitempty"`
	// AllowedOverrides are the template fields plain users may override
	// at instantiation.
	AllowedOverrides []string `json:"allowedOverrides,omitempty"`
}

// CatalogImage is the API projection of a WorkspaceImage CR, already
// filtered down to what the requesting user may deploy.
type CatalogImage struct {
	Name          string            `json:"name"`
	DisplayName   string            `json:"displayName"`
	Description   string            `json:"description,omitempty"`
	Image         string            `json:"image"`
	Protocols     []string          `json:"protocols"`
	Architectures []string          `json:"architectures,omitempty"`
	Enabled       bool              `json:"enabled"`
	AllowedGroups []string          `json:"allowedGroups,omitempty"`
	Defaults      map[string]string `json:"defaults,omitempty"`
	Min           map[string]string `json:"min,omitempty"`
	Max           map[string]string `json:"max,omitempty"`
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
}

// PolicyModel is the API projection of a WorkspacePolicy CR for the
// admin console.
type PolicyModel struct {
	Name      string            `json:"name"`
	Priority  int32             `json:"priority"`
	Subjects  []PolicySubject   `json:"subjects,omitempty"`
	Images    []string          `json:"images,omitempty"`
	Limits    PolicyLimitsModel `json:"limits"`
	Lifecycle map[string]string `json:"lifecycle,omitempty"`
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

// UserUsage is one row of the admin consumption view.
type UserUsage struct {
	UserID     string            `json:"userId"`
	Username   string            `json:"username,omitempty"`
	Groups     []string          `json:"groups,omitempty"`
	Policy     string            `json:"policy,omitempty"`
	Workspaces int               `json:"workspaces"`
	Used       map[string]string `json:"used,omitempty"`
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
