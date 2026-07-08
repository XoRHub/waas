package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Protocol is a desktop protocol a workspace image can serve.
// +kubebuilder:validation:Enum=vnc;rdp;ssh;kasmvnc
type Protocol string

const (
	ProtocolVNC Protocol = "vnc"
	ProtocolRDP Protocol = "rdp"
	ProtocolSSH Protocol = "ssh"
	// ProtocolKasmVNC is the web-native KasmVNC endpoint of kasmweb/*
	// images: browser-only, reverse-proxied by wwt instead of guacd.
	ProtocolKasmVNC Protocol = "kasmvnc"
)

// ImageTagPolicy is the pinning discipline for images matched by a
// catalog entry.
// +kubebuilder:validation:Enum=digest;tag;any
type ImageTagPolicy string

const (
	// TagPolicyDigest requires an @sha256:… digest on the reference.
	TagPolicyDigest ImageTagPolicy = "digest"
	// TagPolicyTag requires a fixed tag: :latest and tag-less references
	// are rejected. The default.
	TagPolicyTag ImageTagPolicy = "tag"
	// TagPolicyAny allows anything, :latest included — an explicit
	// opt-in, never a default.
	TagPolicyAny ImageTagPolicy = "any"
)

// WorkspaceImageSpec is one admin-approved catalog entry. Only images
// present in the catalog AND enabled can be referenced (through a
// WorkspaceTemplate) by a Workspace; everything else is rejected at
// admission. The catalog is deliberately separate from WorkspaceTemplate:
// the template says HOW to deploy, this object records WHAT is approved
// and for WHOM, and disabling it must not tear the template down.
// +kubebuilder:validation:XValidation:rule="has(self.image) != has(self.registry)",message="exactly one of image or registry must be set"
type WorkspaceImageSpec struct {
	// DisplayName is the human-facing name shown in the portal catalog.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Description is shown to users when picking an image.
	// +optional
	Description string `json:"description,omitempty"`

	// Image is the exact reference approved by the admin. Templates must
	// match it verbatim; pin the digest for immutability (the waas-images
	// pipeline publishes immutable tags precisely for this). Exactly one
	// of image/registry must be set.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image,omitempty"`

	// Registry approves every image UNDER this prefix instead of one
	// exact reference — e.g. "docker.io/kasmweb" approves
	// docker.io/kasmweb/terminal:1.19.0 (path-boundary match: it never
	// approves docker.io/kasmweb-evil/*). An exact image entry always
	// beats a registry entry; among registry entries the longest prefix
	// wins. Combine with tagPolicy: a whole-registry approval with
	// moving tags allowed is the loosest possible gate.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Registry string `json:"registry,omitempty"`

	// TagPolicy is the pinning discipline the matched template reference
	// must satisfy: digest (must carry @sha256:…), tag (fixed tag
	// required — :latest and tag-less references rejected), any
	// (everything allowed, latest included). Unset defaults to "any" on
	// exact image entries (the approval is verbatim) and to "tag" on
	// registry entries (broad approvals stay pinned unless explicitly
	// loosened).
	// +optional
	TagPolicy ImageTagPolicy `json:"tagPolicy,omitempty"`

	// Protocols the image can serve. A template using this image must
	// pick a port whose protocol is listed here.
	// +kubebuilder:validation:MinItems=1
	Protocols []Protocol `json:"protocols"`

	// Architectures the image is published for. The operator turns this
	// into node affinity (ARM64 control-plane vs AMD64 workers). Empty
	// means any node.
	// +optional
	// +kubebuilder:validation:items:Enum=amd64;arm64
	Architectures []string `json:"architectures,omitempty"`

	// Enabled is the admin kill-switch: false blocks NEW workspaces
	// immediately (existing ones keep running, see grandfathering) while
	// keeping the entry, its history and its group bindings in place.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// AllowedGroups restricts this image to members of at least one of
	// these IdP (OIDC) groups. Empty = every authenticated user (still
	// subject to the policy's image subset).
	// +optional
	AllowedGroups []string `json:"allowedGroups,omitempty"`

	// ImagePullSecretRef names an existing kubernetes.io/dockerconfigjson
	// Secret (in the platform workspace namespace) holding the pull
	// credentials for this entry's registry. The operator copies it into
	// each workspace's target namespace (imagePullSecrets are read by the
	// kubelet in the POD's namespace) and wires it into the PodSpec. A
	// missing or unreadable source is FAIL-CLOSED: the workspace does not
	// start and its Ready condition says PullSecretMissing.
	// +optional
	ImagePullSecretRef string `json:"imagePullSecretRef,omitempty"`

	// Resources are the per-workspace sizing hints and hard bounds for
	// this image.
	// +optional
	Resources *ImageResources `json:"resources,omitempty"`
}

// ImageResources bounds what a single workspace of this image may request.
type ImageResources struct {
	// Default is applied by the portal when the user does not choose.
	// +optional
	Default *ComputeSize `json:"default,omitempty"`

	// Min rejects undersized workspaces (an IDE image needs real memory).
	// +optional
	Min *ComputeSize `json:"min,omitempty"`

	// Max caps a single workspace of this image regardless of policy.
	// The effective per-workspace cap is min(image.max, policy.perWorkspace).
	// +optional
	Max *ComputeSize `json:"max,omitempty"`
}

// ComputeSize is a cpu/memory pair; either side may be omitted.
type ComputeSize struct {
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=wsi
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Registry",type=string,JSONPath=`.spec.registry`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspaceImage is an admin-approved catalog entry.
type WorkspaceImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkspaceImageSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// WorkspaceImageList contains a list of WorkspaceImage.
type WorkspaceImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkspaceImage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkspaceImage{}, &WorkspaceImageList{})
}
