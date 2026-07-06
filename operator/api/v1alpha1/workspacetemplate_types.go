package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OSType is the operating-system family of a workspace.
// +kubebuilder:validation:Enum=linux;windows
type OSType string

const (
	OSLinux   OSType = "linux"
	OSWindows OSType = "windows"
)

// WorkloadKind selects which Kubernetes workload carries the desktop pod.
// +kubebuilder:validation:Enum=Deployment;StatefulSet;Pod
type WorkloadKind string

const (
	WorkloadDeployment  WorkloadKind = "Deployment"
	WorkloadStatefulSet WorkloadKind = "StatefulSet"
	WorkloadPod         WorkloadKind = "Pod"
)

// OverridableField names one template facet that workspace creators may be
// allowed to override at instantiation time.
// +kubebuilder:validation:Enum=env;securityContext;podSecurityContext;volumes;nodeSelector;tolerations;resources;protocol;protocolParams;schedule
type OverridableField string

const (
	FieldEnv                OverridableField = "env"
	FieldSecurityContext    OverridableField = "securityContext"
	FieldPodSecurityContext OverridableField = "podSecurityContext"
	FieldVolumes            OverridableField = "volumes"
	FieldNodeSelector       OverridableField = "nodeSelector"
	FieldTolerations        OverridableField = "tolerations"
	FieldResources          OverridableField = "resources"
	FieldProtocol           OverridableField = "protocol"
	FieldProtocolParams     OverridableField = "protocolParams"
	FieldSchedule           OverridableField = "schedule"
)

// WorkspaceSchedule declares planned uptime/downtime by cron. Downtime
// scales the workspace to 0 (same mechanism as pause); a manual action
// wins until the next opposite scheduled edge (conflict rule B). Empty
// lists mean "no schedule". See docs/workspace-lifecycle.md.
type WorkspaceSchedule struct {
	// Timezone is an IANA name (e.g. "Europe/Paris"). REQUIRED when any
	// cron is set: the controller never falls back to its own timezone.
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// Uptime cron expressions (standard 5-field). Each fires a start edge
	// (bring the workspace up / scale to 1).
	// +optional
	Uptime []string `json:"uptime,omitempty"`

	// Downtime cron expressions (standard 5-field). Each fires a stop edge
	// (scale the workspace to 0, phase Stopped).
	// +optional
	Downtime []string `json:"downtime,omitempty"`
}

// WorkspaceWorkload shapes the workload wrapping the desktop container,
// beyond image and resources: how it is deployed and with which pod spec.
type WorkspaceWorkload struct {
	// Kind is the workload type stamping the desktop pod. Defaults to
	// Deployment; Pod keeps the legacy bare-pod behavior, StatefulSet
	// gives stable identity for stateful desktop stacks.
	// +optional
	Kind WorkloadKind `json:"kind,omitempty"`

	// SecurityContext is the desktop container's security context.
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// PodSecurityContext is the pod-level security context.
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`

	// Volumes are extra volumes added to the pod (the home PVC is always
	// mounted independently of this list).
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts are extra mounts on the desktop container, matching
	// entries in Volumes.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// NodeSelector constrains scheduling of the desktop pod.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations of the desktop pod.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// ServiceAccountName runs the desktop pod under a specific SA.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// WorkspaceProtocol declares one way to reach the desktop, described in
// guacd terms: a protocol name, the port the workspace serves it on, and
// the guacd connection parameters to use.
type WorkspaceProtocol struct {
	// Name is the guacamole protocol identifier.
	// +kubebuilder:validation:Enum=vnc;rdp;ssh
	Name string `json:"name"`

	// Port the workspace serves this protocol on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Default marks the protocol used when the user does not choose one.
	// The first entry wins when no entry is marked.
	// +optional
	Default bool `json:"default,omitempty"`

	// Params are guacd connection parameters for this protocol (e.g.
	// color-depth, security). Keys are the guacd wire names, validated
	// against the platform registry (operator/pkg/params) by the
	// admission webhook: unknown, malformed or platform-owned parameters
	// (credentials, gateways, repeaters) are rejected. hostname/port/
	// width/height/dpi are always managed by the platform.
	// +optional
	Params map[string]string `json:"params,omitempty"`

	// UserParams lists the guacd parameter names users may set or
	// override when connecting. Anything not listed is locked to Params.
	// +optional
	UserParams []string `json:"userParams,omitempty"`

	// CredentialsSecretRef names a Secret (in the workspace namespace)
	// holding the desktop credentials for this protocol, under the keys
	// username, password, private-key and passphrase (all optional).
	// Resolved server-side at connect time and handed to guacd by the
	// proxy: credentials never appear in a CR and never reach the
	// browser. Ship the Secret via External Secrets/Vault.
	// +optional
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`
}

// TemplateOverrides controls what workspace creators may deviate from the
// template. Platform admins may always override everything.
type TemplateOverrides struct {
	// AllowedFields users may override when instantiating a workspace.
	// +optional
	AllowedFields []OverridableField `json:"allowedFields,omitempty"`

	// Owner is the platform username owning this template: that user may
	// override any field on workspaces stamped from it, like an admin.
	// +optional
	Owner string `json:"owner,omitempty"`
}

// WorkspaceTemplateSpec defines the desired shape of workspaces stamped from
// this template. Templates are cluster-side configuration ("workspaces as
// code"): admins manage them via kubectl/GitOps or through the API server,
// which itself only manipulates this CRD.
type WorkspaceTemplateSpec struct {
	// DisplayName is the human-facing name shown in the portal.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Description is shown to admins when picking a template.
	// +optional
	Description string `json:"description,omitempty"`

	// OS selects the workspace kind: linux (pod + VNC) or windows
	// (KubeVirt VM + RDP). Windows requires KubeVirt in the cluster and is
	// rejected by the validating webhook otherwise.
	OS OSType `json:"os"`

	// Image is the container image (linux) or containerdisk image (windows)
	// providing the desktop environment.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Port is the port the in-workspace desktop server listens on.
	// Defaults to 5901 (VNC) for linux and 3389 (RDP) for windows.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// Resources are the compute resources of the workspace pod/VM.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// HomeSize is the size of the persistent home volume. The home PVC is
	// decoupled from the pod lifecycle: destroying a workspace keeps it.
	// +optional
	HomeSize *resource.Quantity `json:"homeSize,omitempty"`

	// StorageClassName selects the storage class for home volumes.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Env is extra environment injected into linux workspace pods.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Workload shapes how the desktop is deployed (Deployment by default)
	// and the pod spec passthrough (security contexts, volumes, ...).
	// +optional
	Workload *WorkspaceWorkload `json:"workload,omitempty"`

	// Protocols are the connection protocols this workspace serves. When
	// empty, one protocol is synthesized from OS and Port (linux → vnc,
	// windows → rdp) to keep older templates working unchanged.
	// +optional
	Protocols []WorkspaceProtocol `json:"protocols,omitempty"`

	// Overrides declares which fields workspace creators may override.
	// Absent means nothing is overridable except by admins.
	// +optional
	Overrides *TemplateOverrides `json:"overrides,omitempty"`

	// Schedule plans uptime/downtime by cron to cap resource use. Empty =
	// always available (subject to manual pause and lifecycle).
	// +optional
	Schedule *WorkspaceSchedule `json:"schedule,omitempty"`
}

// DesktopPort returns the effective default desktop port for this template.
func (s *WorkspaceTemplateSpec) DesktopPort() int32 {
	return s.DefaultProtocol().Port
}

// Protocol returns the default guacamole protocol of this template.
func (s *WorkspaceTemplateSpec) Protocol() string {
	return s.DefaultProtocol().Name
}

// EffectiveProtocols returns the declared protocols, or the single
// OS-derived legacy protocol when the template declares none.
func (s *WorkspaceTemplateSpec) EffectiveProtocols() []WorkspaceProtocol {
	if len(s.Protocols) > 0 {
		return s.Protocols
	}
	port := s.Port
	name := "vnc"
	if s.OS == OSWindows {
		name = "rdp"
	}
	if port == 0 {
		port = 5901
		if s.OS == OSWindows {
			port = 3389
		}
	}
	return []WorkspaceProtocol{{Name: name, Port: port, Default: true}}
}

// DefaultProtocol returns the protocol used when the user picks none:
// the first entry marked default, else the first entry.
func (s *WorkspaceTemplateSpec) DefaultProtocol() WorkspaceProtocol {
	protos := s.EffectiveProtocols()
	for _, p := range protos {
		if p.Default {
			return p
		}
	}
	return protos[0]
}

// ProtocolNamed returns the protocol entry with the given name, or nil.
func (s *WorkspaceTemplateSpec) ProtocolNamed(name string) *WorkspaceProtocol {
	protos := s.EffectiveProtocols()
	for i := range protos {
		if protos[i].Name == name {
			return &protos[i]
		}
	}
	return nil
}

// WorkloadKindOrDefault returns the workload kind, defaulting to Deployment.
func (s *WorkspaceTemplateSpec) WorkloadKindOrDefault() WorkloadKind {
	if s.Workload != nil && s.Workload.Kind != "" {
		return s.Workload.Kind
	}
	return WorkloadDeployment
}

// FieldOverridable reports whether the template lets plain users override
// the given field.
func (s *WorkspaceTemplateSpec) FieldOverridable(field OverridableField) bool {
	if s.Overrides == nil {
		return false
	}
	for _, f := range s.Overrides.AllowedFields {
		if f == field {
			return true
		}
	}
	return false
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=wst
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="OS",type=string,JSONPath=`.spec.os`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspaceTemplate is the blueprint a Workspace references.
type WorkspaceTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkspaceTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// WorkspaceTemplateList contains a list of WorkspaceTemplate.
type WorkspaceTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkspaceTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkspaceTemplate{}, &WorkspaceTemplateList{})
}
