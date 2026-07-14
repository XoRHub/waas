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

	// Catalog configures the periodic fetch of a published catalog
	// manifest (format below) listing the images currently under this
	// entry's registry, with display metadata (os/app/version/icon) for
	// the portal catalog picker. Only meaningful when spec.registry is
	// set (ignored on exact spec.image entries — ENFORCEMENT never reads
	// this field, it is purely cosmetic). Grouped under one struct
	// (rather than flat fields) so a future split into its own CRD, if a
	// real need for K8s-RBAC-level separation ever appears, is a
	// mechanical lift instead of a field-by-field migration — no such
	// split is planned today, application-level authorization (the same
	// model WorkspacePolicy already uses) covers any future need for a
	// narrower "catalog editor" role. Absent = no automatic catalog; the
	// registry approval itself still works.
	// +optional
	Catalog *ImageCatalogSpec `json:"catalog,omitempty"`
}

// ImageCatalogSpec points at exactly one catalog manifest source.
// +kubebuilder:validation:XValidation:rule="!has(self.auth) || size(self.from.url) > 0",message="auth is only meaningful when from.url is set"
type ImageCatalogSpec struct {
	// From is the catalog manifest source (format below, § 1) —
	// exactly one of URL/ConfigMapKeyRef/SecretKeyRef, mutually
	// exclusive (enforced on ImageCatalogSource below; never more than
	// one set). URL is fetched live over HTTP(S); ConfigMapKeyRef/SecretKeyRef
	// are read directly, no HTTP involved. Both are re-checked on the
	// SAME periodic cadence (operator.catalogSyncInterval) — no
	// dedicated watch on the referenced ConfigMap/Secret (the operator
	// deliberately reads both uncached, without the watch verb) — a
	// static, GitOps-managed catalog for an admin who prefers not to
	// depend on a live registry endpoint. This is a first-class,
	// permanent choice, not a stopgap-until-network-works: an admin
	// picks ONE of the three and stays on it.
	// +kubebuilder:validation:Required
	From ImageCatalogSource `json:"from"`

	// Auth configures how the live fetch authenticates — only
	// meaningful when From.URL is set (ignored, and rejected at
	// admission if From points at ConfigMapKeyRef/SecretKeyRef instead,
	// see the XValidation on ImageCatalogSpec above). Nested by method
	// (one field per auth kind) instead of a flat credential reference,
	// so a future method (basic auth, mTLS...) is a pure ADDITION — a
	// new sibling field on ImageCatalogAuth — never a rename or a
	// reinterpretation of what an existing field means. Absent =
	// unauthenticated GET, the only mode the two known public catalogs
	// (docker.io/xorhub, docker.io/kasmweb) need.
	// +optional
	Auth *ImageCatalogAuth `json:"auth,omitempty"`
}

// ImageCatalogSource names the catalog manifest source — exactly one
// of URL/ConfigMapKeyRef/SecretKeyRef must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.url) ? 1 : 0) + (has(self.configMapKeyRef) ? 1 : 0) + (has(self.secretKeyRef) ? 1 : 0) == 1",message="exactly one of url, configMapKeyRef, or secretKeyRef must be set"
type ImageCatalogSource struct {
	// URL is the catalog manifest location, fetched live and
	// periodically (operator.catalogSyncInterval).
	// +optional
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url,omitempty"`

	// ConfigMapKeyRef reads the manifest from a ConfigMap key in the
	// platform workspace namespace instead of fetching it over HTTP —
	// the common case, since the content isn't secret, just a static
	// admin-provided catalog. Key defaults to "catalog.yaml" when
	// empty. Re-read periodically (operator.catalogSyncInterval), not
	// just once — no dedicated watch on the ConfigMap (uncached reads,
	// no watch verb, by existing design). Not a corev1
	// ConfigMapKeySelector: that type marks key as REQUIRED in the
	// generated schema, which would kill the default.
	// +optional
	ConfigMapKeyRef *CatalogConfigMapSource `json:"configMapKeyRef,omitempty"`

	// SecretKeyRef reads the manifest from a Secret key instead, in the
	// platform workspace namespace — for an admin who wants the
	// manifest content itself access-controlled. Key is REQUIRED (no
	// default, unlike ConfigMapKeyRef): no naming convention is assumed
	// for a Secret. Distinct from ImageCatalogAuth.BearerToken below:
	// that one is a fetch CREDENTIAL for a URL source, this one IS the
	// manifest content itself; the two are never the same Secret in
	// practice but nothing in the schema prevents it, and they cannot
	// both apply at once since URL/SecretKeyRef are mutually exclusive.
	// +optional
	SecretKeyRef *CatalogSecretSource `json:"secretKeyRef,omitempty"`
}

// CatalogConfigMapSource names a ConfigMap key holding a catalog
// manifest, in the platform workspace namespace.
type CatalogConfigMapSource struct {
	// Name of the ConfigMap.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Key inside it; empty reads "catalog.yaml".
	// +optional
	Key string `json:"key,omitempty"`
}

// CatalogSecretSource names a Secret key holding a catalog manifest,
// in the platform workspace namespace.
type CatalogSecretSource struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Key inside it — required: no naming convention is assumed for a
	// Secret.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// ImageCatalogAuth holds one authentication method for the catalog
// fetch. Only BearerToken exists today — deliberately no
// mutual-exclusion XValidation yet (a single optional field has
// nothing to conflict with; that CEL rule is dead code until a second
// method exists). Add the "exactly one of" rule THE DAY a second
// method is introduced, not before (YAGNI).
type ImageCatalogAuth struct {
	// BearerToken sends "Authorization: Bearer <token>" on the fetch.
	// +optional
	BearerToken *BearerTokenAuth `json:"bearerToken,omitempty"`
}

// BearerTokenAuth names the Secret holding the bearer token used to
// authenticate the catalog fetch.
type BearerTokenAuth struct {
	// SecretRef names an existing Opaque Secret (in the platform
	// workspace namespace, same convention as
	// WorkspaceImageSpec.ImagePullSecretRef) holding the token under
	// the key "token". A missing/unreadable Secret, or one without this
	// key, is a sync failure (status.catalog.lastSyncError), never a
	// crash — same fail-soft doctrine as the rest of the reconciler.
	// +kubebuilder:validation:MinLength=1
	SecretRef string `json:"secretRef"`
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

// ImageCatalogStatus is the last known result of the catalog fetch
// configured by spec.catalog. The discovered entries themselves live
// in the api-server's catalog_entries table (Postgres), not here: this
// status only keeps the small, purely-informational bookkeeping useful
// for `kubectl get workspaceimage` — see docs/image-catalog.md.
type ImageCatalogStatus struct {
	// Source says which From variant last produced entries: "Fetched"
	// (From.URL, a live sync succeeded at least once) or "Static"
	// (From.ConfigMapKeyRef/SecretKeyRef was read successfully at least
	// once). Empty = never synced yet.
	// +optional
	Source string `json:"source,omitempty"`
	// LastSyncTime is when the catalog was last synced successfully:
	// the real fetch time for "Fetched", the time the ConfigMap/Secret
	// was last read for "Static".
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// LastSyncError is the most recent fetch/read failure, kept even
	// after a later sync succeeds, so admins can see WHY it once
	// failed.
	// +optional
	LastSyncError string `json:"lastSyncError,omitempty"`
}

// WorkspaceImageStatus is the observed state of a WorkspaceImage.
type WorkspaceImageStatus struct {
	// Catalog is nil until the first sync attempt of a spec.catalog-configured
	// entry.
	// +optional
	Catalog *ImageCatalogStatus `json:"catalog,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wsi
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Registry",type=string,JSONPath=`.spec.registry`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Catalog",type=string,JSONPath=`.status.catalog.source`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspaceImage is an admin-approved catalog entry.
type WorkspaceImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceImageSpec   `json:"spec"`
	Status WorkspaceImageStatus `json:"status,omitempty"`
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
