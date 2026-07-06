package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubjectKind says what a policy subject name refers to.
// +kubebuilder:validation:Enum=User;Group
type SubjectKind string

const (
	SubjectUser  SubjectKind = "User"
	SubjectGroup SubjectKind = "Group"
)

// PolicySubject binds a policy to an identity. Groups are Authentik
// group names carried by the OIDC claims; users match the workspace
// owner identity (see the webhook's trusted-writer model).
type PolicySubject struct {
	Kind SubjectKind `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// WorkspacePolicySpec is the admin-defined envelope for self-service.
//
// Resolution rule (deliberate, keep in sync with pkg/policy): among all
// policies whose subjects match the user, the HIGHEST spec.priority wins
// and applies as a whole — no field merging between policies. Ties break
// on the lexicographically smallest name and surface a warning, because
// two same-priority matches are a configuration smell. A policy with no
// subjects matches every authenticated user: name one "default" with
// priority 0 as the restrictive fallback. No matching policy at all
// means DENY (fail closed).
type WorkspacePolicySpec struct {
	// Priority orders competing policies; higher wins. Convention:
	// 0 = default fallback, 100–999 = group policies, 1000+ = per-user
	// exceptions.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// Subjects this policy applies to (OR). Empty = every authenticated
	// user.
	// +optional
	Subjects []PolicySubject `json:"subjects,omitempty"`

	// Images is the subset of catalog entries (WorkspaceImage names)
	// this policy allows. Empty = the whole enabled catalog (each image's
	// own allowedGroups still applies).
	// +optional
	Images []string `json:"images,omitempty"`

	// Limits caps what the matched user can consume.
	// +optional
	Limits PolicyLimits `json:"limits,omitempty"`

	// Lifecycle bounds workspace longevity to fight accumulation.
	// +optional
	Lifecycle *PolicyLifecycle `json:"lifecycle,omitempty"`

	// Clipboard controls the clipboard bridge between the user's machine
	// and their workspace sessions. Absent = both directions allowed.
	// Enforced by the WebSocket proxy (instruction filtering, rights
	// stamped into the connection token) — the portal only reflects it.
	// +optional
	Clipboard *ClipboardPolicy `json:"clipboard,omitempty"`

	// Overrides bounds instantiation-time template overrides for the
	// governed users, on top of each template's own allow-list.
	// +optional
	Overrides *PolicyOverrides `json:"overrides,omitempty"`

	// RemoteWorkspaces opts the governed users into the Remote Workspaces
	// feature: registering out-of-cluster machines (host/port/protocol +
	// credentials Secret) and connecting to them through guacd. Absent or
	// false = feature hidden and refused (fail closed).
	// +optional
	RemoteWorkspaces bool `json:"remoteWorkspaces,omitempty"`
}

// PolicyOverrides restricts template overrides per policy. The effective
// allow-list of a creator is the INTERSECTION of the template's
// overrides.allowedFields and AllowedFields here. nil = no policy
// restriction (the template list applies alone); non-nil with an empty
// list = this policy forbids every override. Platform admins bypass both
// lists; a template owner bypasses the template list but stays subject
// to the policy list.
type PolicyOverrides struct {
	// AllowedFields the governed users may override when the template
	// also allows them.
	// +optional
	AllowedFields []OverridableField `json:"allowedFields,omitempty"`
}

// ClipboardPolicy gates the two clipboard directions independently.
// Absent booleans default to true (allowed).
type ClipboardPolicy struct {
	// CopyFromWorkspace permits copying FROM the workspace to the local
	// clipboard (data exfiltration direction).
	// +optional
	CopyFromWorkspace *bool `json:"copyFromWorkspace,omitempty"`
	// PasteToWorkspace permits pasting the local clipboard INTO the
	// workspace (data injection direction).
	// +optional
	PasteToWorkspace *bool `json:"pasteToWorkspace,omitempty"`
}

// PolicyLimits — absent fields mean "unlimited" so the restrictive
// default policy must set them all explicitly.
type PolicyLimits struct {
	// MaxWorkspaces is the max simultaneous workspaces per user. Paused
	// workspaces count: their home PVC still holds storage.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxWorkspaces *int32 `json:"maxWorkspaces,omitempty"`

	// PerWorkspace caps a single workspace. The effective cap is
	// min(these, image.resources.max).
	// +optional
	PerWorkspace *PerWorkspaceCaps `json:"perWorkspace,omitempty"`

	// Aggregate caps the SUM across all of the user's workspaces
	// (paused included for storage; compute of paused workspaces is
	// released and therefore not counted).
	// +optional
	Aggregate *AggregateCaps `json:"aggregate,omitempty"`

	// Defaults is the sizing the portal proposes when the user does not
	// choose. Display-only: a per-image default (image.resources.default)
	// takes precedence, and enforcement is unaffected either way.
	// +optional
	Defaults *ComputeSize `json:"defaults,omitempty"`
}

// PerWorkspaceCaps bounds one workspace.
type PerWorkspaceCaps struct {
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Home caps the persistent home volume size.
	// +optional
	Home *resource.Quantity `json:"home,omitempty"`
}

// AggregateCaps bounds the per-user sum.
type AggregateCaps struct {
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Storage caps the sum of home volume sizes.
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
}

// PolicyLifecycle — enforcement is split by data ownership: the
// api-server (which owns session data) pauses idle workspaces, the
// operator (which owns the CR lifecycle) deletes expired ones.
type PolicyLifecycle struct {
	// IdleSuspendAfter pauses a workspace with no active session for
	// this long (compute freed, home kept). Zero/absent = never.
	// +optional
	IdleSuspendAfter *metav1.Duration `json:"idleSuspendAfter,omitempty"`

	// MaxLifetime deletes the workspace (home included) this long after
	// creation. Zero/absent = never. Deletion is announced through the
	// workspace's Events and status conditions.
	// +optional
	MaxLifetime *metav1.Duration `json:"maxLifetime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=wsp
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Max WS",type=integer,JSONPath=`.spec.limits.maxWorkspaces`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspacePolicy is the self-service envelope for a user or group.
type WorkspacePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkspacePolicySpec `json:"spec"`
}

// +kubebuilder:object:root=true

// WorkspacePolicyList contains a list of WorkspacePolicy.
type WorkspacePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkspacePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkspacePolicy{}, &WorkspacePolicyList{})
}
